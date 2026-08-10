package relay

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
			version INTEGER NOT NULL DEFAULT 1,
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL
		)`,
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
	return nil
}

// ── Providers ─────────────────────────────────────────────────────

func (r *Repository) CreateProvider(ctx context.Context, p *Provider) error {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_relay_providers (scope, tenant_id, domain_id, pool_id, name, host, port, username, secret_ref, conn_security, tls_validation, priority, weight, active, rate_limit_per_min, circuit_state, version, created_at, updated_at) VALUES (`+r.dialect.Placeholders(19)+`)`,
		p.Scope, p.TenantID, p.DomainID, p.PoolID, p.Name, p.Host, p.Port, p.Username, p.SecretRef, p.ConnSecurity, p.TLSValidation, p.Priority, p.Weight, boolToInt(p.Active), p.RateLimitPerMin, CircuitClosed, 1, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	p.ID = uint(id)
	p.Version = 1
	p.CircuitState = CircuitClosed
	return nil
}

const providerCols = `id, scope, tenant_id, domain_id, pool_id, name, host, port, username, secret_ref, conn_security, tls_validation, priority, weight, active, rate_limit_per_min, circuit_state, circuit_failures, circuit_opened_at, version, created_at, updated_at`

func (r *Repository) GetProvider(ctx context.Context, id uint) (*Provider, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM platform_relay_providers WHERE id=`+r.dialect.Placeholder(1), id)
	return scanProvider(row)
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

func (r *Repository) UpdateProviderCircuit(ctx context.Context, id uint, state CircuitState, failures int, openedAt *time.Time, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE platform_relay_providers SET circuit_state=`+r.dialect.Placeholder(1)+`, circuit_failures=`+r.dialect.Placeholder(2)+`, circuit_opened_at=`+r.dialect.Placeholder(3)+`, version=version+1, updated_at=`+r.dialect.Placeholder(4)+` WHERE id=`+r.dialect.Placeholder(5),
		state, failures, openedAt, now, id)
	return err
}

// ── Pools ─────────────────────────────────────────────────────────

func (r *Repository) CreatePool(ctx context.Context, p *Pool) error {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_relay_pools (scope, tenant_id, domain_id, name, strategy, direct_only, version, created_at, updated_at) VALUES (`+r.dialect.Placeholders(9)+`)`,
		p.Scope, p.TenantID, p.DomainID, p.Name, p.Strategy, boolToInt(p.DirectOnly), 1, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
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

func (r *Repository) CreateRoutingRule(ctx context.Context, rule *RoutingRule) error {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_relay_routing_rules (tenant_id, domain_id, sender_pattern, recipient_domain, classification, pool_id, priority, created_at) VALUES (`+r.dialect.Placeholders(8)+`)`,
		rule.TenantID, rule.DomainID, rule.SenderPattern, rule.RecipientDomain, rule.Classification, rule.PoolID, rule.Priority, rule.CreatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
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
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_relay_overrides (tenant_id, pool_id, reason, actor_id, expires_at, active, created_at) VALUES (`+r.dialect.Placeholders(7)+`)`,
		o.TenantID, o.PoolID, o.Reason, o.ActorID, o.ExpiresAt, 1, o.CreatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
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
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_relay_rate_counters (provider_id, window_start, count) VALUES (`+r.dialect.Placeholders(3)+`)
		 ON CONFLICT (provider_id, window_start) DO UPDATE SET count = platform_relay_rate_counters.count + 1`,
		providerID, windowStart, 1); err != nil {
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
	err := row.Scan(&p.ID, &p.Scope, &p.TenantID, &p.DomainID, &p.PoolID, &p.Name, &p.Host, &p.Port, &p.Username, &p.SecretRef, &p.ConnSecurity, &p.TLSValidation, &p.Priority, &p.Weight, &active, &p.RateLimitPerMin, &p.CircuitState, &p.CircuitFailures, &p.CircuitOpenedAt, &p.Version, &p.CreatedAt, &p.UpdatedAt)
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
