package billing

// UsageCounterStore implements concurrency-safe quota reservation: "no
// negative counters" and "concurrency-safe usage reservation" from
// Feature 3. Reserve is a SINGLE atomic UPDATE whose WHERE clause
// includes the capacity check itself (used + delta <= limit) — there is
// no read-then-write race window, because the database engine (SQLite or
// PostgreSQL) evaluates and applies the WHERE+SET atomically per row.
// Two concurrent Reserve calls racing for the last unit of capacity will
// have exactly one succeed (1 row affected) and one fail (0 rows
// affected, or the row doesn't exist yet) — never both succeeding and
// pushing the counter over the limit.

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

type UsageDimension string

const (
	UsageDomains   UsageDimension = "domains"
	UsageMailboxes UsageDimension = "mailboxes"
	UsageAliases   UsageDimension = "aliases"
	UsageGroups    UsageDimension = "groups"
	UsageStorageMB UsageDimension = "storage_mb"
	UsageSendDay   UsageDimension = "send_day"
	UsageSendHour  UsageDimension = "send_hour"
)

type UsageCounterStore struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewUsageCounterStore(db *sql.DB) *UsageCounterStore {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &UsageCounterStore{db: db, dialect: dialect}
}

func (s *UsageCounterStore) EnsureSchema(ctx context.Context) error {
	ts := s.dialect.TimestampType()
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS billing_usage_counters (
		tenant_id INTEGER NOT NULL,
		dimension TEXT NOT NULL,
		used INTEGER NOT NULL DEFAULT 0,
		limit_value INTEGER NOT NULL,
		updated_at `+ts+` NOT NULL,
		PRIMARY KEY (tenant_id, dimension)
	)`)
	return err
}

// EnsureCounter creates the (tenant, dimension) row if absent, seeding
// used=initialUsed and the current limit. Safe to call repeatedly — it
// no-ops if the row already exists, it never resets an existing counter's
// `used` value (that would erase real reservations already made).
func (s *UsageCounterStore) EnsureCounter(ctx context.Context, tenantID uint, dim UsageDimension, limit, initialUsed int, now time.Time) error {
	setConflict := "excluded"
	if s.dialect.IsPostgres() {
		setConflict = "EXCLUDED"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO billing_usage_counters (tenant_id, dimension, used, limit_value, updated_at) VALUES (`+s.dialect.Placeholders(5)+`)
		 ON CONFLICT (tenant_id, dimension) DO UPDATE SET limit_value = `+setConflict+`.limit_value`,
		tenantID, string(dim), initialUsed, limit, now,
	)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "ensure usage counter", err)
	}
	return nil
}

// Reserve atomically increments used by delta if, and only if,
// used+delta <= limit_value AND used+delta >= 0 — the second condition is
// what makes "no negative counters" structurally true even for a
// negative delta (a release), not just a convention callers must
// remember. Returns kernel.ErrCodeQuotaExceeded if the reservation would
// exceed the limit, kernel.ErrCodeNotFound if EnsureCounter was never
// called for this (tenant, dimension).
func (s *UsageCounterStore) Reserve(ctx context.Context, tenantID uint, dim UsageDimension, delta int, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE billing_usage_counters SET used = used + `+s.dialect.Placeholder(1)+`, updated_at = `+s.dialect.Placeholder(2)+`
		 WHERE tenant_id = `+s.dialect.Placeholder(3)+` AND dimension = `+s.dialect.Placeholder(4)+`
		   AND used + `+s.dialect.Placeholder(5)+` <= limit_value AND used + `+s.dialect.Placeholder(6)+` >= 0`,
		delta, now, tenantID, string(dim), delta, delta,
	)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "reserve usage", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	// Zero rows: either the counter doesn't exist, or it exists but the
	// reservation would violate a bound. Distinguish them so a caller
	// sees NOT_FOUND (a real setup bug) rather than a misleading
	// QUOTA_EXCEEDED for a dimension that was never initialized.
	exists, existsErr := s.counterExists(ctx, tenantID, dim)
	if existsErr != nil {
		return existsErr
	}
	if !exists {
		return kernel.NotFound(fmt.Sprintf("usage counter for tenant=%d dimension=%s", tenantID, dim))
	}
	return kernel.QuotaExceeded(fmt.Sprintf("%s quota exceeded", dim))
}

// Release is Reserve's inverse — a negative-delta convenience wrapper
// used when a resource is deleted and its reserved capacity must be
// given back. It goes through the exact same atomic, no-negative-counter
// path as Reserve.
func (s *UsageCounterStore) Release(ctx context.Context, tenantID uint, dim UsageDimension, amount int, now time.Time) error {
	return s.Reserve(ctx, tenantID, dim, -amount, now)
}

func (s *UsageCounterStore) counterExists(ctx context.Context, tenantID uint, dim UsageDimension) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM billing_usage_counters WHERE tenant_id = `+s.dialect.Placeholder(1)+` AND dimension = `+s.dialect.Placeholder(2),
		tenantID, string(dim),
	).Scan(&n)
	if err != nil {
		return false, kernel.Wrap(kernel.ErrCodeInternal, "check counter existence", err)
	}
	return n > 0, nil
}

type UsageCounter struct {
	TenantID  uint
	Dimension UsageDimension
	Used      int
	Limit     int
	UpdatedAt time.Time
}

func (s *UsageCounterStore) Get(ctx context.Context, tenantID uint, dim UsageDimension) (*UsageCounter, error) {
	var c UsageCounter
	var dimension string
	err := s.db.QueryRowContext(ctx,
		`SELECT tenant_id, dimension, used, limit_value, updated_at FROM billing_usage_counters WHERE tenant_id = `+s.dialect.Placeholder(1)+` AND dimension = `+s.dialect.Placeholder(2),
		tenantID, string(dim),
	).Scan(&c.TenantID, &dimension, &c.Used, &c.Limit, &c.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, kernel.NotFound(fmt.Sprintf("usage counter for tenant=%d dimension=%s", tenantID, dim))
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get usage counter", err)
	}
	c.Dimension = UsageDimension(dimension)
	return &c, nil
}

// Reconcile resets a counter's `used` value to actualCount — the ground
// truth from an authoritative real-data count (e.g. "SELECT COUNT(*)
// FROM mailboxes WHERE tenant_id = ? AND deleted_at IS NULL"), which the
// bounded context that owns that data supplies. This is what "no
// negative counters" and "reconciliation jobs that compare counters with
// real data" resolve to in practice: a periodic job calls Reconcile with
// the true count for every (tenant, dimension) pair, correcting any
// drift Reserve/Release calls may have accumulated (e.g. from a crashed
// request that reserved but never actually created the resource, or vice
// versa via an out-of-band deletion that bypassed Release).
func (s *UsageCounterStore) Reconcile(ctx context.Context, tenantID uint, dim UsageDimension, actualCount int, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE billing_usage_counters SET used = `+s.dialect.Placeholder(1)+`, updated_at = `+s.dialect.Placeholder(2)+`
		 WHERE tenant_id = `+s.dialect.Placeholder(3)+` AND dimension = `+s.dialect.Placeholder(4),
		actualCount, now, tenantID, string(dim),
	)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "reconcile usage counter", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return kernel.NotFound(fmt.Sprintf("usage counter for tenant=%d dimension=%s", tenantID, dim))
	}
	return nil
}
