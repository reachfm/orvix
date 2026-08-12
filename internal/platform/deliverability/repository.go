package deliverability

import (
	"context"
	"database/sql"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
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
		`CREATE TABLE IF NOT EXISTS platform_deliverability_signals (
			id ` + autoInc + `,
			event_key TEXT NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			dimension TEXT NOT NULL,
			dimension_value TEXT NOT NULL,
			type TEXT NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			recorded_at ` + ts + ` NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_deliv_signals_event_dim ON platform_deliverability_signals (event_key, dimension)`,
		`CREATE INDEX IF NOT EXISTS idx_deliv_signals_window ON platform_deliverability_signals (dimension, dimension_value, recorded_at)`,
		`CREATE TABLE IF NOT EXISTS platform_deliverability_suppressions (
			id ` + autoInc + `,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			address TEXT NOT NULL,
			reason TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			actor_id INTEGER NOT NULL DEFAULT 0,
			notes TEXT NOT NULL DEFAULT '',
			expires_at ` + ts + `,
			created_at ` + ts + ` NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_deliv_suppressions_tenant_addr ON platform_deliverability_suppressions (tenant_id, address)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// RecordSignal inserts a signal, idempotent on (event_key, dimension).
// Concurrency safety comes from the unique index, not a check-then-
// insert: a duplicate/retried event for the same dimension hits the
// unique constraint and is reported as inserted=false (not an error)
// via kernel.IsUniqueViolation — this holds even when two callers race
// on the exact same event, unlike a SELECT-then-INSERT guard.
func (r *Repository) RecordSignal(ctx context.Context, s *Signal) (inserted bool, err error) {
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO platform_deliverability_signals (event_key, tenant_id, dimension, dimension_value, type, latency_ms, recorded_at) VALUES (`+r.dialect.Placeholders(7)+`)`,
		s.EventKey, s.TenantID, s.Dimension, s.DimensionValue, s.Type, s.LatencyMS, s.RecordedAt)
	if err != nil {
		if kernel.IsUniqueViolation(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Aggregate computes WindowMetrics for one dimension value over
// [start, end). A single query, not N+1 — every rate is derived from
// the same COUNT(*) FILTER-style aggregation (implemented portably
// via SUM(CASE...) since SQLite lacks FILTER).
func (r *Repository) Aggregate(ctx context.Context, dim Dimension, dimValue string, start, end time.Time) (*WindowMetrics, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN type=? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=? THEN 1 ELSE 0 END), 0),
			COALESCE(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END), 0)
		FROM platform_deliverability_signals
		WHERE dimension=? AND dimension_value=? AND recorded_at >= ? AND recorded_at < ?`,
		SignalDelivered, SignalTempFail, SignalPermFail, SignalBounce, SignalComplaint,
		dim, dimValue, start, end)

	m := &WindowMetrics{Dimension: dim, DimensionValue: dimValue, WindowStart: start, WindowEnd: end}
	if err := row.Scan(&m.Volume, &m.Delivered, &m.TempFail, &m.PermFail, &m.Bounced, &m.Complaints, &m.AvgLatencyMS); err != nil {
		return nil, err
	}
	if m.Volume > 0 {
		m.DeliveryRate = float64(m.Delivered) / float64(m.Volume)
		m.BounceRate = float64(m.Bounced) / float64(m.Volume)
		m.ComplaintRate = float64(m.Complaints) / float64(m.Volume)
		m.TempFailRate = float64(m.TempFail) / float64(m.Volume)
		m.PermFailRate = float64(m.PermFail) / float64(m.Volume)
	}
	return m, nil
}

// ── Suppressions ─────────────────────────────────────────────────

// AddSuppression is an upsert: re-suppressing an already-suppressed
// address (e.g. a second hard bounce) updates the reason/expiry rather
// than erroring, so the enforcement check never has to reason about
// duplicate rows.
func (r *Repository) AddSuppression(ctx context.Context, s *Suppression) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_deliverability_suppressions (tenant_id, address, reason, source, actor_id, notes, expires_at, created_at)
		VALUES (`+r.dialect.Placeholders(8)+`)
		ON CONFLICT (tenant_id, address) DO UPDATE SET
			reason=excluded.reason, source=excluded.source, actor_id=excluded.actor_id,
			notes=excluded.notes, expires_at=excluded.expires_at, created_at=excluded.created_at`,
		s.TenantID, s.Address, s.Reason, s.Source, s.ActorID, s.Notes, s.ExpiresAt, s.CreatedAt)
	return err
}

func (r *Repository) RemoveSuppression(ctx context.Context, tenantID uint, address string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM platform_deliverability_suppressions WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND address=`+r.dialect.Placeholder(2),
		tenantID, address)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// IsSuppressed is the real-time enforcement check — called from the
// actual outbound delivery path. An expired suppression (expires_at
// in the past) is treated as not-suppressed without requiring a
// background sweep to have deleted it first.
func (r *Repository) IsSuppressed(ctx context.Context, tenantID uint, address string, now time.Time) (bool, *Suppression, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, address, reason, source, actor_id, notes, expires_at, created_at
		 FROM platform_deliverability_suppressions WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND address=`+r.dialect.Placeholder(2), tenantID, address)
	var s Suppression
	err := row.Scan(&s.ID, &s.TenantID, &s.Address, &s.Reason, &s.Source, &s.ActorID, &s.Notes, &s.ExpiresAt, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if s.ExpiresAt != nil && !s.ExpiresAt.After(now) {
		return false, nil, nil
	}
	return true, &s, nil
}

func (r *Repository) ListSuppressions(ctx context.Context, tenantID uint, limit int, afterID uint) ([]Suppression, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, tenant_id, address, reason, source, actor_id, notes, expires_at, created_at
		 FROM platform_deliverability_suppressions WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND id > `+r.dialect.Placeholder(2)+`
		 ORDER BY id ASC LIMIT `+r.dialect.Placeholder(3), tenantID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suppression
	for rows.Next() {
		var s Suppression
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Address, &s.Reason, &s.Source, &s.ActorID, &s.Notes, &s.ExpiresAt, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
