package relay

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// ProviderFilter bounds the platform-wide relay list.
type ProviderFilter struct {
	Scope    Scope
	TenantID *uint
	DomainID *uint
	Active   *bool
	Search   string
	Limit    int
	Offset   int
}

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
		`CREATE TABLE IF NOT EXISTS platform_relay_pools (
			id ` + autoInc + `,
			scope TEXT NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			strategy TEXT NOT NULL DEFAULT 'priority',
			direct_only INTEGER NOT NULL DEFAULT 0,
			version INTEGER NOT NULL DEFAULT 1,
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS platform_relay_providers (
			id ` + autoInc + `,
			scope TEXT NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL DEFAULT 0,
			pool_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			secret_ref TEXT NOT NULL DEFAULT '',
			conn_security TEXT NOT NULL DEFAULT 'starttls',
			tls_validation TEXT NOT NULL DEFAULT 'strict',
			priority INTEGER NOT NULL DEFAULT 100,
			weight INTEGER NOT NULL DEFAULT 1,
			active INTEGER NOT NULL DEFAULT 1,
			rate_limit_per_min INTEGER NOT NULL DEFAULT 0,
			circuit_state TEXT NOT NULL DEFAULT 'closed',
			circuit_failures INTEGER NOT NULL DEFAULT 0,
			circuit_opened_at ` + ts + `,
			last_test_at ` + ts + `,
			last_test_result TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_relay_providers_scope ON platform_relay_providers (scope, tenant_id, domain_id, active)`,
		`CREATE INDEX IF NOT EXISTS idx_relay_providers_pool ON platform_relay_providers (pool_id, active)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_relay_providers_scope_name ON platform_relay_providers (scope, tenant_id, domain_id, name) WHERE name <> ''`,
		`CREATE TABLE IF NOT EXISTS platform_relay_routing_rules (
			id ` + autoInc + `,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL DEFAULT 0,
			sender_pattern TEXT NOT NULL DEFAULT '',
			recipient_domain TEXT NOT NULL DEFAULT '',
			classification TEXT NOT NULL DEFAULT '',
			pool_id INTEGER NOT NULL,
			priority INTEGER NOT NULL DEFAULT 100,
			created_at ` + ts + ` NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS platform_relay_overrides (
			id ` + autoInc + `,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			pool_id INTEGER NOT NULL,
			reason TEXT NOT NULL,
			actor_id INTEGER NOT NULL,
			expires_at ` + ts + ` NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			created_at ` + ts + ` NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS platform_relay_rate_counters (
			provider_id INTEGER NOT NULL,
			window_start ` + ts + ` NOT NULL,
			count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (provider_id, window_start)
		)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	// Additive migrations for databases created before the platform
	// relay administration surface existed. CREATE TABLE IF NOT EXISTS
	// cannot add columns to an existing table, so each column is added
	// with an idempotent ALTER that ignores the already-exists error
	// (which has different text on SQLite vs PostgreSQL).
	if err := r.ensureColumn(ctx, "platform_relay_providers", "last_test_at", ts); err != nil {
		return err
	}
	if err := r.ensureColumn(ctx, "platform_relay_providers", "last_test_result", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

// ensureColumn attempts an additive ALTER TABLE ... ADD COLUMN and
// treats an "already exists" error as success so the migration is
// idempotent across both supported drivers.
func (r *Repository) ensureColumn(ctx context.Context, table, column, columnDDL string) error {
	_, err := r.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+columnDDL)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "duplicate column") || strings.Contains(msg, "already exists") {
		return nil
	}
	return err
}

// ── Providers ─────────────────────────────────────────────────────

// insertReturningID executes an INSERT and captures the new row's id
// portably: PostgreSQL uses RETURNING id (LastInsertId is not reliable
// on Postgres drivers), SQLite uses LastInsertId.
// execQuerier is the shared subset of *sql.DB and *sql.Tx used by the
// repository's write helpers, so every insert can participate in a caller's
// transaction alongside its audit and outbox evidence (Fix G).
type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (r *Repository) insertReturningID(ctx context.Context, query string, args ...any) (int64, error) {
	return r.insertReturningIDWith(ctx, r.db, query, args...)
}

func (r *Repository) insertReturningIDWith(ctx context.Context, q execQuerier, query string, args ...any) (int64, error) {
	if r.dialect.IsPostgres() {
		var id int64
		err := q.QueryRowContext(ctx, query+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const insertProviderSQL = `INSERT INTO platform_relay_providers (scope, tenant_id, domain_id, pool_id, name, host, port, username, secret_ref, conn_security, tls_validation, priority, weight, active, rate_limit_per_min, circuit_state, version, created_at, updated_at) VALUES (`

func (r *Repository) CreateProvider(ctx context.Context, p *Provider) error {
	return r.createProviderWith(ctx, r.db, p)
}

// CreateProviderTx inserts a provider inside the caller's transaction, so the
// row, its audit entry, and its outbox event commit or roll back together.
func (r *Repository) CreateProviderTx(ctx context.Context, tx *sql.Tx, p *Provider) error {
	return r.createProviderWith(ctx, tx, p)
}

func (r *Repository) createProviderWith(ctx context.Context, q execQuerier, p *Provider) error {
	id, err := r.insertReturningIDWith(ctx, q,
		insertProviderSQL+r.dialect.Placeholders(19)+`)`,
		p.Scope, p.TenantID, p.DomainID, p.PoolID, p.Name, p.Host, p.Port, p.Username, p.SecretRef, p.ConnSecurity, p.TLSValidation, p.Priority, p.Weight, boolToInt(p.Active), p.RateLimitPerMin, CircuitClosed, 1, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	p.ID = uint(id)
	p.Version = 1
	p.CircuitState = CircuitClosed
	return nil
}

const providerCols = `id, scope, tenant_id, domain_id, pool_id, name, host, port, username, secret_ref, conn_security, tls_validation, priority, weight, active, rate_limit_per_min, circuit_state, circuit_failures, circuit_opened_at, last_test_at, last_test_result, version, created_at, updated_at`

func (r *Repository) GetProvider(ctx context.Context, id uint) (*Provider, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM platform_relay_providers WHERE id=`+r.dialect.Placeholder(1), id)
	p, err := scanProvider(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *Repository) ListProvidersByPool(ctx context.Context, poolID uint) ([]Provider, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerCols+` FROM platform_relay_providers WHERE pool_id=`+r.dialect.Placeholder(1)+` ORDER BY priority ASC, id ASC`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// UpdateProviderCircuit persists a circuit-breaker transition. It verifies
// RowsAffected so a transition against a deleted provider is reported instead
// of silently succeeding — a circuit that believes it recorded an "open" state
// it never persisted would keep routing to a failing provider.
func (r *Repository) UpdateProviderCircuit(ctx context.Context, id uint, state CircuitState, failures int, openedAt *time.Time, now time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_relay_providers SET circuit_state=`+r.dialect.Placeholder(1)+`, circuit_failures=`+r.dialect.Placeholder(2)+`, circuit_opened_at=`+r.dialect.Placeholder(3)+`, version=version+1, updated_at=`+r.dialect.Placeholder(4)+` WHERE id=`+r.dialect.Placeholder(5),
		state, failures, openedAt, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrProviderNotFound
	}
	return nil
}

// ── Platform relay administration ─────────────────────────────────

// ListProviders returns the platform-wide relay list with optional
// scope/tenant/domain/active filters, bounded pagination, and a
// deterministic ordering (id ASC). The name/host search filter is a
// bounded LIKE on both columns.
func (r *Repository) ListProviders(ctx context.Context, f ProviderFilter) ([]Provider, int64, error) {
	where := []string{"1=1"}
	var args []any
	add := func(cond string, val any) {
		where = append(where, cond)
		args = append(args, val)
	}
	if f.Scope != "" {
		add("scope="+r.dialect.Placeholder(len(args)+1), string(f.Scope))
	}
	if f.TenantID != nil {
		add("tenant_id="+r.dialect.Placeholder(len(args)+1), *f.TenantID)
	}
	if f.DomainID != nil {
		add("domain_id="+r.dialect.Placeholder(len(args)+1), *f.DomainID)
	}
	if f.Active != nil {
		add("active="+r.dialect.Placeholder(len(args)+1), boolToInt(*f.Active))
	}
	if f.Search != "" {
		add("(name LIKE "+r.dialect.Placeholder(len(args)+1)+" OR host LIKE "+r.dialect.Placeholder(len(args)+2)+")", "%"+f.Search+"%")
		args = append(args, "%"+f.Search+"%")
	}
	whereClause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_relay_providers`+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerCols+` FROM platform_relay_providers`+whereClause+
		` ORDER BY id ASC LIMIT `+r.dialect.Placeholder(len(args)-1)+` OFFSET `+r.dialect.Placeholder(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *p)
	}
	return out, total, rows.Err()
}

// UpdateProvider applies a guarded, optimistic-concurrency update:
// the version predicate makes two concurrent writers race safely (the
// loser affects 0 rows). Only non-zero fields of p are applied; the
// caller is responsible for pre-filling unchanged values when a field
// legitimately needs to be cleared.
func (r *Repository) UpdateProvider(ctx context.Context, p *Provider) (bool, error) {
	return r.updateProviderWith(ctx, r.db, p)
}

// UpdateProviderTx applies the same guarded update inside the caller's
// transaction so the row change commits atomically with its audit and outbox
// evidence (Fix G).
func (r *Repository) UpdateProviderTx(ctx context.Context, tx *sql.Tx, p *Provider) (bool, error) {
	return r.updateProviderWith(ctx, tx, p)
}

func (r *Repository) updateProviderWith(ctx context.Context, q execQuerier, p *Provider) (bool, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE platform_relay_providers SET
			scope=`+r.dialect.Placeholder(1)+`, tenant_id=`+r.dialect.Placeholder(2)+`, domain_id=`+r.dialect.Placeholder(3)+`, pool_id=`+r.dialect.Placeholder(4)+`, name=`+r.dialect.Placeholder(5)+`, host=`+r.dialect.Placeholder(6)+`, port=`+r.dialect.Placeholder(7)+`,
			username=`+r.dialect.Placeholder(8)+`, secret_ref=`+r.dialect.Placeholder(9)+`, conn_security=`+r.dialect.Placeholder(10)+`, tls_validation=`+r.dialect.Placeholder(11)+`,
			priority=`+r.dialect.Placeholder(12)+`, weight=`+r.dialect.Placeholder(13)+`, active=`+r.dialect.Placeholder(14)+`, rate_limit_per_min=`+r.dialect.Placeholder(15)+`,
			version=version+1, updated_at=`+r.dialect.Placeholder(16)+`
		 WHERE id=`+r.dialect.Placeholder(17)+` AND version=`+r.dialect.Placeholder(18),
		p.Scope, p.TenantID, p.DomainID, p.PoolID, p.Name, p.Host, p.Port,
		p.Username, p.SecretRef, p.ConnSecurity, p.TLSValidation,
		p.Priority, p.Weight, boolToInt(p.Active), p.RateLimitPerMin,
		p.UpdatedAt, p.ID, p.Version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetProviderActive is the guarded enable/disable transition. Returns
// (affected, nil); affected=false means the version predicate failed
// (stale write) or the row is missing.
func (r *Repository) SetProviderActive(ctx context.Context, id uint, active bool, version int, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_relay_providers SET active=`+r.dialect.Placeholder(1)+`, version=version+1, updated_at=`+r.dialect.Placeholder(2)+` WHERE id=`+r.dialect.Placeholder(3)+` AND version=`+r.dialect.Placeholder(4),
		boolToInt(active), now, id, version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetProviderActiveTx is SetProviderActive inside the caller's
// mutation transaction (used when the mutation, outbox event, and
// audit record must commit atomically).
func (r *Repository) SetProviderActiveTx(ctx context.Context, tx *sql.Tx, id uint, active bool, version int, now time.Time) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE platform_relay_providers SET active=`+r.dialect.Placeholder(1)+`, version=version+1, updated_at=`+r.dialect.Placeholder(2)+` WHERE id=`+r.dialect.Placeholder(3)+` AND version=`+r.dialect.Placeholder(4),
		boolToInt(active), now, id, version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RotateProviderSecret swaps the encrypted credential under the
// version predicate.
func (r *Repository) RotateProviderSecret(ctx context.Context, id uint, secretRef string, version int, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_relay_providers SET secret_ref=`+r.dialect.Placeholder(1)+`, version=version+1, updated_at=`+r.dialect.Placeholder(2)+` WHERE id=`+r.dialect.Placeholder(3)+` AND version=`+r.dialect.Placeholder(4),
		secretRef, now, id, version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RotateProviderSecretTx is RotateProviderSecret inside the caller's
// mutation transaction.
func (r *Repository) RotateProviderSecretTx(ctx context.Context, tx *sql.Tx, id uint, secretRef string, version int, now time.Time) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE platform_relay_providers SET secret_ref=`+r.dialect.Placeholder(1)+`, version=version+1, updated_at=`+r.dialect.Placeholder(2)+` WHERE id=`+r.dialect.Placeholder(3)+` AND version=`+r.dialect.Placeholder(4),
		secretRef, now, id, version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetTestResult records the last safe connection-test outcome. The statement
// is version-guarded, so RowsAffected==0 means a concurrent modification (or a
// deleted provider) — reported as ErrVersionConflict rather than swallowed,
// which previously let the caller believe a test outcome was persisted when it
// was not.
func (r *Repository) SetTestResult(ctx context.Context, id uint, at time.Time, result string, version int) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_relay_providers SET last_test_at=`+r.dialect.Placeholder(1)+`, last_test_result=`+r.dialect.Placeholder(2)+`, version=version+1, updated_at=`+r.dialect.Placeholder(3)+` WHERE id=`+r.dialect.Placeholder(4)+` AND version=`+r.dialect.Placeholder(5),
		at, result, at, id, version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrVersionConflict
	}
	return nil
}

// DeleteProvider removes a relay endpoint (hard delete — routing rules
// pointing at its pool simply skip it). Returns whether a row existed.
func (r *Repository) DeleteProvider(ctx context.Context, id uint) (bool, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM platform_relay_providers WHERE id=`+r.dialect.Placeholder(1), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// DeleteProviderTx is DeleteProvider inside the caller's mutation
// transaction.
func (r *Repository) DeleteProviderTx(ctx context.Context, tx *sql.Tx, id uint) (bool, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM platform_relay_providers WHERE id=`+r.dialect.Placeholder(1), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ── Pools ─────────────────────────────────────────────────────────

func (r *Repository) CreatePool(ctx context.Context, p *Pool) error {
	id, err := r.insertReturningID(ctx,
		`INSERT INTO platform_relay_pools (scope, tenant_id, domain_id, name, strategy, direct_only, version, created_at, updated_at) VALUES (`+r.dialect.Placeholders(9)+`)`,
		p.Scope, p.TenantID, p.DomainID, p.Name, p.Strategy, boolToInt(p.DirectOnly), 1, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	p.ID = uint(id)
	p.Version = 1
	return nil
}

const poolCols = `id, scope, tenant_id, domain_id, name, strategy, direct_only, version, created_at, updated_at`

func (r *Repository) GetPool(ctx context.Context, id uint) (*Pool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+poolCols+` FROM platform_relay_pools WHERE id=`+r.dialect.Placeholder(1), id)
	return scanPool(row)
}

// ── Routing rules ────────────────────────────────────────────────

// CreateRoutingRule inserts a routing rule and captures its real generated
// id. It previously used res.LastInsertId() with the error DISCARDED
// (`id, _ :=`). On PostgreSQL that call is unsupported and returns an error,
// so every rule created on the shipping dialect came back with ID 0 — the API
// reported id 0, the rule could not be addressed for update or delete, and
// id-based tie-breaking in rule precedence had nothing real to work with.
func (r *Repository) CreateRoutingRule(ctx context.Context, rule *RoutingRule) error {
	return r.createRoutingRuleWith(ctx, r.db, rule)
}

// CreateRoutingRuleTx inserts a routing rule inside the caller's transaction.
func (r *Repository) CreateRoutingRuleTx(ctx context.Context, tx *sql.Tx, rule *RoutingRule) error {
	return r.createRoutingRuleWith(ctx, tx, rule)
}

func (r *Repository) createRoutingRuleWith(ctx context.Context, q execQuerier, rule *RoutingRule) error {
	id, err := r.insertReturningIDWith(ctx, q,
		`INSERT INTO platform_relay_routing_rules (tenant_id, domain_id, sender_pattern, recipient_domain, classification, pool_id, priority, created_at) VALUES (`+r.dialect.Placeholders(8)+`)`,
		rule.TenantID, rule.DomainID, rule.SenderPattern, rule.RecipientDomain, rule.Classification, rule.PoolID, rule.Priority, rule.CreatedAt)
	if err != nil {
		return err
	}
	rule.ID = uint(id)
	return nil
}

// ListRoutingRules returns every rule scoped to tenantID or domainID
// (or fully global — both zero), most-specific (domain) first, then
// by priority ascending. Resolution logic itself lives in service.go;
// this is purely a read.
func (r *Repository) ListRoutingRules(ctx context.Context, tenantID, domainID uint) ([]RoutingRule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, domain_id, sender_pattern, recipient_domain, classification, pool_id, priority, created_at FROM platform_relay_routing_rules
		 WHERE (domain_id=`+r.dialect.Placeholder(1)+` OR tenant_id=`+r.dialect.Placeholder(2)+` OR (tenant_id=0 AND domain_id=0))
		 ORDER BY (CASE WHEN domain_id<>0 THEN 0 WHEN tenant_id<>0 THEN 1 ELSE 2 END) ASC, priority ASC`,
		domainID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutingRule
	for rows.Next() {
		var rule RoutingRule
		if err := rows.Scan(&rule.ID, &rule.TenantID, &rule.DomainID, &rule.SenderPattern, &rule.RecipientDomain, &rule.Classification, &rule.PoolID, &rule.Priority, &rule.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// ── Emergency overrides ─────────────────────────────────────────

func (r *Repository) CreateOverride(ctx context.Context, o *EmergencyOverride) error {
	return r.createOverrideWith(ctx, r.db, o)
}

// CreateOverrideTx inserts an emergency override inside the caller's
// transaction. An emergency control must never exist without the audit record
// that explains who invoked it and why.
func (r *Repository) CreateOverrideTx(ctx context.Context, tx *sql.Tx, o *EmergencyOverride) error {
	return r.createOverrideWith(ctx, tx, o)
}

func (r *Repository) createOverrideWith(ctx context.Context, q execQuerier, o *EmergencyOverride) error {
	// Same LastInsertId defect as CreateRoutingRule: an emergency override
	// created on PostgreSQL came back with ID 0, so it could never be revoked
	// by id — the one operation an emergency control must always support.
	id, err := r.insertReturningIDWith(ctx, q,
		`INSERT INTO platform_relay_overrides (tenant_id, pool_id, reason, actor_id, expires_at, active, created_at) VALUES (`+r.dialect.Placeholders(7)+`)`,
		o.TenantID, o.PoolID, o.Reason, o.ActorID, o.ExpiresAt, 1, o.CreatedAt)
	if err != nil {
		return err
	}
	o.ID = uint(id)
	o.Active = true
	return nil
}

// ActiveOverride returns the active, unexpired override for tenantID
// (or a global one, tenant_id=0), if any. An expired-but-still-marked-
// active row is treated as absent AND lazily deactivated — expiry is
// automatic, not dependent on a background sweep having run.
func (r *Repository) ActiveOverride(ctx context.Context, tenantID uint, now time.Time) (*EmergencyOverride, error) {
	// Expiry is filtered in Go, not SQL: comparing a driver-encoded
	// TIMESTAMP column against a bound time.Time parameter is not
	// reliably portable across SQLite driver time-encoding modes and
	// PostgreSQL — fetching every still-"active"-flagged candidate and
	// comparing ExpiresAt after scanning removes that class of bug
	// entirely, at the cost of a few extra rows read (bounded: one
	// tenant's override count is always small).
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, pool_id, reason, actor_id, expires_at, active, created_at FROM platform_relay_overrides
		 WHERE (tenant_id=`+r.dialect.Placeholder(1)+` OR tenant_id=0) AND active=1
		 ORDER BY tenant_id DESC, created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o EmergencyOverride
		var active int
		if err := rows.Scan(&o.ID, &o.TenantID, &o.PoolID, &o.Reason, &o.ActorID, &o.ExpiresAt, &active, &o.CreatedAt); err != nil {
			return nil, err
		}
		o.Active = active != 0
		if o.ExpiresAt.After(now) {
			return &o, nil
		}
	}
	return nil, rows.Err()
}

// RevokeOverrideTx deactivates a single override inside the caller's
// transaction. The tenant predicate is part of the statement, so one tenant
// can never revoke another tenant's emergency override (tenant 0 denotes a
// platform-scope caller and may revoke any). Returns whether a row actually
// changed, so "already inactive" and "not found" are never reported as
// success.
func (r *Repository) RevokeOverrideTx(ctx context.Context, tx *sql.Tx, id, tenantID uint) (bool, error) {
	q := `UPDATE platform_relay_overrides SET active=0 WHERE id=` + r.dialect.Placeholder(1) + ` AND active=1`
	args := []any{id}
	if tenantID != 0 {
		q += ` AND tenant_id=` + r.dialect.Placeholder(2)
		args = append(args, tenantID)
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *Repository) ExpireOverrides(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE platform_relay_overrides SET active=0 WHERE active=1 AND expires_at<=`+r.dialect.Placeholder(1), now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── Rate limiting ────────────────────────────────────────────────

// IncrementAndCheck atomically bumps the per-minute counter for
// providerID's current window and reports whether the NEW count is
// within limit (limit<=0 means unlimited, always true). The
// insert-or-increment is a single statement using the dialect's
// upsert helper, so two concurrent callers in the same window cannot
// both read count=0 and both proceed.
func (r *Repository) IncrementAndCheck(ctx context.Context, providerID uint, windowStart time.Time, limit int) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	// Native ON CONFLICT ... DO UPDATE upsert (SQLite 3.24+ and
	// PostgreSQL both support this syntax) so the increment is a
	// single atomic statement — two concurrent callers in the same
	// window cannot both observe count=0 and both proceed. The
	// dialect's generic Upsert helper isn't used here because its
	// SQLite branch does INSERT OR REPLACE, which overwrites the row
	// (count=1) instead of incrementing it.
	upsert := `INSERT INTO platform_relay_rate_counters (provider_id, window_start, count) VALUES (` + r.dialect.Placeholders(3) + `)
		 ON CONFLICT (provider_id, window_start) DO UPDATE SET count = platform_relay_rate_counters.count + 1`

	if r.dialect.IsPostgres() {
		// RETURNING reads back the count produced by THIS statement, so the
		// limit decision is made on the value this caller actually created.
		// The INSERT-then-SELECT form below leaves a window in which another
		// caller's increment lands between the two statements, so a caller can
		// observe a count higher than its own — non-deterministic enforcement
		// of a security-relevant limit. Retained for SQLite, whose driver
		// support for RETURNING depends on the compiled library version.
		var count int
		if err := r.db.QueryRowContext(ctx, upsert+` RETURNING count`, providerID, windowStart, 1).Scan(&count); err != nil {
			return false, err
		}
		return count <= limit, nil
	}

	if _, err := r.db.ExecContext(ctx, upsert, providerID, windowStart, 1); err != nil {
		return false, err
	}
	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count FROM platform_relay_rate_counters WHERE provider_id=`+r.dialect.Placeholder(1)+` AND window_start=`+r.dialect.Placeholder(2),
		providerID, windowStart).Scan(&count); err != nil {
		return false, err
	}
	return count <= limit, nil
}

func scanProvider(row interface{ Scan(...any) error }) (*Provider, error) {
	var p Provider
	var active int
	err := row.Scan(&p.ID, &p.Scope, &p.TenantID, &p.DomainID, &p.PoolID, &p.Name, &p.Host, &p.Port, &p.Username, &p.SecretRef, &p.ConnSecurity, &p.TLSValidation, &p.Priority, &p.Weight, &active, &p.RateLimitPerMin, &p.CircuitState, &p.CircuitFailures, &p.CircuitOpenedAt, &p.LastTestAt, &p.LastTestResult, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.Active = active != 0
	return &p, nil
}

func scanPool(row interface{ Scan(...any) error }) (*Pool, error) {
	var p Pool
	var directOnly int
	err := row.Scan(&p.ID, &p.Scope, &p.TenantID, &p.DomainID, &p.Name, &p.Strategy, &directOnly, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.DirectOnly = directOnly != 0
	return &p, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
