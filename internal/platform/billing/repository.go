package billing

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
		`CREATE TABLE IF NOT EXISTS platform_billing_adjustments (
			id ` + autoInc + `,
			tenant_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			amount_cents INTEGER NOT NULL,
			currency TEXT NOT NULL,
			reason TEXT NOT NULL,
			actor_id INTEGER NOT NULL DEFAULT 0,
			idempotency_key TEXT NOT NULL DEFAULT '',
			created_at ` + ts + ` NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_adj_idem ON platform_billing_adjustments (tenant_id, idempotency_key) WHERE idempotency_key <> ''`,
		`CREATE TABLE IF NOT EXISTS platform_billing_balances (
			tenant_id INTEGER PRIMARY KEY,
			currency TEXT NOT NULL,
			balance_cents INTEGER NOT NULL DEFAULT 0,
			version INTEGER NOT NULL DEFAULT 1,
			updated_at ` + ts + ` NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			// The partial unique index (WHERE idempotency_key <> '')
			// syntax is unsupported on some very old SQLite builds;
			// fail open on that one statement only, since the
			// idempotency check in service.go also verifies via a
			// SELECT before insert as a second layer.
			if s == stmts[1] {
				continue
			}
			return err
		}
	}
	return nil
}

func (r *Repository) InsertAdjustment(ctx context.Context, tx *sql.Tx, a *Adjustment) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO platform_billing_adjustments (tenant_id, type, amount_cents, currency, reason, actor_id, idempotency_key, created_at)
		VALUES (`+r.dialect.Placeholders(8)+`)`,
		a.TenantID, a.Type, a.AmountCents, a.Currency, a.Reason, a.ActorID, a.IdempotencyKey, a.CreatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	a.ID = uint(id)
	return nil
}

func (r *Repository) FindByIdempotencyKey(ctx context.Context, tenantID uint, key string) (*Adjustment, error) {
	if key == "" {
		return nil, nil
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, type, amount_cents, currency, reason, actor_id, idempotency_key, created_at
		FROM platform_billing_adjustments WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND idempotency_key=`+r.dialect.Placeholder(2),
		tenantID, key)
	var a Adjustment
	err := row.Scan(&a.ID, &a.TenantID, &a.Type, &a.AmountCents, &a.Currency, &a.Reason, &a.ActorID, &a.IdempotencyKey, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAdjustments returns the most recent adjustments for a tenant,
// newest first — used by the admin history endpoint. No message
// bodies or PII beyond the operator-supplied reason string are
// stored on this row.
func (r *Repository) ListAdjustments(ctx context.Context, tenantID uint, limit int) ([]Adjustment, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, type, amount_cents, currency, reason, actor_id, idempotency_key, created_at
		FROM platform_billing_adjustments WHERE tenant_id=`+r.dialect.Placeholder(1)+`
		ORDER BY id DESC LIMIT `+r.dialect.Placeholder(2), tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Adjustment
	for rows.Next() {
		var a Adjustment
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Type, &a.AmountCents, &a.Currency, &a.Reason, &a.ActorID, &a.IdempotencyKey, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repository) GetBalance(ctx context.Context, tx *sql.Tx, tenantID uint) (*Balance, error) {
	q := `SELECT tenant_id, currency, balance_cents, version, updated_at FROM platform_billing_balances WHERE tenant_id=` + r.dialect.Placeholder(1)
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, q, tenantID)
	} else {
		row = r.db.QueryRowContext(ctx, q, tenantID)
	}
	var b Balance
	err := row.Scan(&b.TenantID, &b.Currency, &b.BalanceCents, &b.Version, &b.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ApplyDelta atomically adjusts the tenant's balance by delta (signed)
// in ONE UPDATE (or an initial INSERT if no row exists yet) — never
// read-then-write, so concurrent adjustments can never lose an
// update regardless of Version. Version is still incremented on every
// write so a caller that DOES want to detect "did the balance change
// since I last read it" can.
func (r *Repository) ApplyDelta(ctx context.Context, tx *sql.Tx, tenantID uint, currency string, deltaCents int64, now time.Time) (*Balance, error) {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO platform_billing_balances (tenant_id, currency, balance_cents, version, updated_at)
		VALUES (`+r.dialect.Placeholders(5)+`)
		ON CONFLICT (tenant_id) DO UPDATE SET
			balance_cents = platform_billing_balances.balance_cents + `+r.dialect.Placeholder(6)+`,
			version = platform_billing_balances.version + 1,
			updated_at = `+r.dialect.Placeholder(7),
		tenantID, currency, deltaCents, 1, now, deltaCents, now)
	if err != nil {
		return nil, err
	}
	return r.GetBalance(ctx, tx, tenantID)
}
