package retention

import (
	"context"
	"database/sql"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRepository(db *sql.DB) *Repository {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS platform_retention_policies (
			id ` + autoInc + `,
			level TEXT NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL DEFAULT 0,
			mailbox_id INTEGER NOT NULL DEFAULT 0,
			category TEXT NOT NULL DEFAULT '',
			retention_days INTEGER NOT NULL DEFAULT 0,
			recovery_days INTEGER NOT NULL DEFAULT 0,
			archive_eligible INTEGER NOT NULL DEFAULT 0,
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS platform_legal_holds (
			id ` + autoInc + `,
			scope_kind TEXT NOT NULL,
			scope_id INTEGER NOT NULL,
			case_ref TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL,
			actor_id INTEGER NOT NULL DEFAULT 0,
			started_at ` + ts + ` NOT NULL,
			ends_at ` + ts + `,
			released INTEGER NOT NULL DEFAULT 0,
			created_at ` + ts + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_legal_holds_scope ON platform_legal_holds (scope_kind, scope_id, released)`,
		`CREATE TABLE IF NOT EXISTS platform_chain_of_custody (
			id ` + autoInc + `,
			operation TEXT NOT NULL,
			scope_kind TEXT NOT NULL,
			scope_id INTEGER NOT NULL,
			actor_id INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT NOT NULL DEFAULT '',
			record_count INTEGER NOT NULL DEFAULT 0,
			created_at ` + ts + ` NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS platform_purge_executions (
			idempotency_key TEXT PRIMARY KEY,
			scope_kind TEXT NOT NULL,
			scope_id INTEGER NOT NULL,
			purged_count INTEGER NOT NULL DEFAULT 0,
			completed_at ` + ts + ` NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// ── Policies ─────────────────────────────────────────────────────

func (r *Repository) CreatePolicy(ctx context.Context, p *Policy) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_retention_policies (level, tenant_id, domain_id, mailbox_id, category, retention_days, recovery_days, archive_eligible, created_at, updated_at)
		VALUES (`+r.dialect.Placeholders(10)+`)`,
		p.Level, p.TenantID, p.DomainID, p.MailboxID, p.Category, p.RetentionDays, p.RecoveryDays, boolToInt(p.ArchiveEligible), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	p.ID = uint(id)
	return nil
}

// ListApplicable returns every policy that COULD apply to
// (tenantID, domainID, mailboxID, category) — i.e. platform-wide, this
// tenant, this domain, this mailbox, each optionally category-scoped.
// Resolution (picking the single most specific one) happens in
// service.go, in Go, not SQL — keeping the specificity rule in one
// place and unit-testable without a database.
func (r *Repository) ListApplicable(ctx context.Context, tenantID, domainID, mailboxID uint, category string) ([]Policy, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, level, tenant_id, domain_id, mailbox_id, category, retention_days, recovery_days, archive_eligible, created_at, updated_at
		FROM platform_retention_policies
		WHERE (level='platform')
		   OR (level='tenant' AND tenant_id=`+r.dialect.Placeholder(1)+`)
		   OR (level='domain' AND domain_id=`+r.dialect.Placeholder(2)+`)
		   OR (level='mailbox' AND mailbox_id=`+r.dialect.Placeholder(3)+`)`,
		tenantID, domainID, mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Policy
	for rows.Next() {
		var p Policy
		var archiveEligible int
		if err := rows.Scan(&p.ID, &p.Level, &p.TenantID, &p.DomainID, &p.MailboxID, &p.Category, &p.RetentionDays, &p.RecoveryDays, &archiveEligible, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.ArchiveEligible = archiveEligible != 0
		if p.Category == "" || p.Category == category {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// ── Legal holds ──────────────────────────────────────────────────

func (r *Repository) CreateHold(ctx context.Context, h *LegalHold) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_legal_holds (scope_kind, scope_id, case_ref, reason, actor_id, started_at, ends_at, released, created_at)
		VALUES (`+r.dialect.Placeholders(9)+`)`,
		h.ScopeKind, h.ScopeID, h.CaseRef, h.Reason, h.ActorID, h.StartedAt, h.EndsAt, 0, h.CreatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	h.ID = uint(id)
	return nil
}

func (r *Repository) ReleaseHold(ctx context.Context, id uint) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE platform_legal_holds SET released=1 WHERE id=`+r.dialect.Placeholder(1)+` AND released=0`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ActiveHoldsForScope returns unreleased holds for the exact scope —
// callers combine this with any broader scope (e.g. tenant-level
// holds also block a mailbox within that tenant) themselves, since
// the caller is the one who knows the scope hierarchy for a given
// purge target.
func (r *Repository) ActiveHoldsForScope(ctx context.Context, scopeKind string, scopeID uint, now time.Time) ([]LegalHold, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, scope_kind, scope_id, case_ref, reason, actor_id, started_at, ends_at, released, created_at
		FROM platform_legal_holds WHERE scope_kind=`+r.dialect.Placeholder(1)+` AND scope_id=`+r.dialect.Placeholder(2)+` AND released=0`,
		scopeKind, scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LegalHold
	for rows.Next() {
		var h LegalHold
		var released int
		if err := rows.Scan(&h.ID, &h.ScopeKind, &h.ScopeID, &h.CaseRef, &h.Reason, &h.ActorID, &h.StartedAt, &h.EndsAt, &released, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.Released = released != 0
		if h.IsActive(now) {
			out = append(out, h)
		}
	}
	return out, rows.Err()
}

// ── Chain of custody ─────────────────────────────────────────────

func (r *Repository) RecordCustody(ctx context.Context, e *ChainOfCustodyEvent) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_chain_of_custody (operation, scope_kind, scope_id, actor_id, content_hash, record_count, created_at)
		VALUES (`+r.dialect.Placeholders(7)+`)`,
		e.Operation, e.ScopeKind, e.ScopeID, e.ActorID, e.ContentHash, e.RecordCount, e.CreatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	e.ID = uint(id)
	return nil
}

// ── Idempotent purge execution tracking ──────────────────────────

// RecordPurgeExecution is the idempotency guard for ExecutePurge: a
// retried call with the same key is a no-op (0 rows affected via the
// primary key conflict, translated to "already executed" by the
// caller checking kernel.IsUniqueViolation).
func (r *Repository) RecordPurgeExecution(ctx context.Context, idempotencyKey, scopeKind string, scopeID uint, purgedCount int, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_purge_executions (idempotency_key, scope_kind, scope_id, purged_count, completed_at)
		VALUES (`+r.dialect.Placeholders(5)+`)`,
		idempotencyKey, scopeKind, scopeID, purgedCount, now)
	return err
}

func (r *Repository) GetPurgeExecution(ctx context.Context, idempotencyKey string) (purgedCount int, found bool, err error) {
	row := r.db.QueryRowContext(ctx, `SELECT purged_count FROM platform_purge_executions WHERE idempotency_key=`+r.dialect.Placeholder(1), idempotencyKey)
	err = row.Scan(&purgedCount)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return purgedCount, true, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
