package deliverability

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
		`CREATE INDEX IF NOT EXISTS idx_deliv_signals_tenant_time ON platform_deliverability_signals (tenant_id, recorded_at)`,
		`CREATE TABLE IF NOT EXISTS platform_deliverability_suppressions (
			id ` + autoInc + `,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			address TEXT NOT NULL,
			reason TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			actor_id INTEGER NOT NULL DEFAULT 0,
			notes TEXT NOT NULL DEFAULT '',
			expires_at ` + ts + `,
			state TEXT NOT NULL DEFAULT 'active',
			released_at ` + ts + `,
			released_by INTEGER NOT NULL DEFAULT 0,
			released_reason TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_deliv_suppressions_tenant_addr ON platform_deliverability_suppressions (tenant_id, address)`,
		`CREATE INDEX IF NOT EXISTS idx_deliv_suppressions_tenant_state ON platform_deliverability_suppressions (tenant_id, state, id)`,
		`CREATE TABLE IF NOT EXISTS platform_deliverability_suppression_events (
			id ` + autoInc + `,
			suppression_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			event TEXT NOT NULL,
			actor_id INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			at ` + ts + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deliv_suppression_events_sup ON platform_deliverability_suppression_events (suppression_id, id)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	// Additive migrations for databases created before the lifecycle
	// columns existed. CREATE TABLE IF NOT EXISTS cannot add columns to
	// an existing table, so each column is added idempotently.
	for _, col := range []struct{ name, ddl string }{
		{"state", "TEXT NOT NULL DEFAULT 'active'"},
		{"released_at", ts},
		{"released_by", "INTEGER NOT NULL DEFAULT 0"},
		{"released_reason", "TEXT NOT NULL DEFAULT ''"},
		{"version", "INTEGER NOT NULL DEFAULT 1"},
		{"updated_at", ts + " NOT NULL DEFAULT " + r.dialect.NowExpr()},
	} {
		if err := r.ensureColumn(ctx, "platform_deliverability_suppressions", col.name, col.ddl); err != nil {
			return err
		}
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
	// H-5: every placeholder is dialect-rewritten. Raw `?` here made this
	// aggregation 500 on PostgreSQL (which needs $1..$n), returning a 500
	// from the Deliverability API. The argument ORDER is unchanged; only the
	// placeholder token is dialect-aware.
	ph := func(n int) string { return r.dialect.Placeholder(n) }
	row := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN type=`+ph(1)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(2)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(3)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(4)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(5)+` THEN 1 ELSE 0 END), 0),
			COALESCE(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END), 0)
		FROM platform_deliverability_signals
		WHERE dimension=`+ph(6)+` AND dimension_value=`+ph(7)+` AND recorded_at >= `+ph(8)+` AND recorded_at < `+ph(9),
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

// ── Events (signals) and aggregation ───────────────────────────────

// EventFilter bounds and filters the tenant's delivery-event list.
type EventFilter struct {
	TenantID uint
	Domain   string // matches sending or recipient domain dimension values
	Type     SignalType
	Provider string // matches relay_provider dimension values
	Start    *time.Time
	End      *time.Time
	Limit    int
	Offset   int
}

// ListEvents returns the tenant's delivery events, newest first, with
// bounded pagination. The projection carries only safe fields — no
// message body, no credentials, no raw headers; recipient evidence is
// the domain-level dimension value only.
func (r *Repository) ListEvents(ctx context.Context, f EventFilter) ([]Signal, int64, error) {
	where := []string{"tenant_id=" + r.dialect.Placeholder(1)}
	args := []any{f.TenantID}
	add := func(cond string, val any) {
		where = append(where, cond)
		args = append(args, val)
	}
	if f.Domain != "" {
		add("(dimension_value="+r.dialect.Placeholder(len(args)+1)+" AND dimension IN ('sending_domain','recipient_domain'))", strings.ToLower(f.Domain))
	}
	if f.Type != "" {
		add("type="+r.dialect.Placeholder(len(args)+1), string(f.Type))
	}
	if f.Provider != "" {
		add("(dimension='relay_provider' AND dimension_value="+r.dialect.Placeholder(len(args)+1)+")", f.Provider)
	}
	if f.Start != nil {
		add("recorded_at>="+r.dialect.Placeholder(len(args)+1), *f.Start)
	}
	if f.End != nil {
		add("recorded_at<"+r.dialect.Placeholder(len(args)+1), *f.End)
	}
	whereClause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_deliverability_signals`+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := r.db.QueryContext(ctx, `SELECT id, event_key, tenant_id, dimension, dimension_value, type, latency_ms, recorded_at
		FROM platform_deliverability_signals`+whereClause+
		` ORDER BY id DESC LIMIT `+r.dialect.Placeholder(len(args)-1)+` OFFSET `+r.dialect.Placeholder(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Signal
	for rows.Next() {
		var s Signal
		if err := rows.Scan(&s.ID, &s.EventKey, &s.TenantID, &s.Dimension, &s.DimensionValue, &s.Type, &s.LatencyMS, &s.RecordedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// GetSignal returns one event strictly scoped to its tenant.
func (r *Repository) GetSignal(ctx context.Context, id, tenantID uint) (*Signal, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, event_key, tenant_id, dimension, dimension_value, type, latency_ms, recorded_at
		 FROM platform_deliverability_signals WHERE id=`+r.dialect.Placeholder(1)+` AND tenant_id=`+r.dialect.Placeholder(2), id, tenantID)
	var s Signal
	err := row.Scan(&s.ID, &s.EventKey, &s.TenantID, &s.Dimension, &s.DimensionValue, &s.Type, &s.LatencyMS, &s.RecordedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CategoryTotals is the single-window aggregate over the tenant's real
// signals. Every category counts rows that were actually recorded by
// the delivery path.
type CategoryTotals struct {
	Volume      int64
	Delivered   int64
	TempFail    int64
	PermFail    int64
	Bounced     int64
	Complaints  int64
	SpamReject  int64
	PolicyDeny  int64
	Throttled   int64
	TLSFailure  int64
	AuthFailure int64
	Suppressed  int64
}

// AggregateTenant computes the tenant-wide category totals over
// [start, end). A single query; every rate is derived from the same
// COUNT(CASE...) aggregation (portable: SQLite lacks FILTER).
func (r *Repository) AggregateTenant(ctx context.Context, tenantID uint, start, end time.Time) (*CategoryTotals, error) {
	// H-5: dialect-rewritten placeholders (was raw `?`, which 500'd on
	// PostgreSQL). Argument order unchanged.
	ph := func(n int) string { return r.dialect.Placeholder(n) }
	row := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN type=`+ph(1)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(2)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(3)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(4)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(5)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(6)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(7)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(8)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(9)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(10)+` THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type=`+ph(11)+` THEN 1 ELSE 0 END), 0)
		FROM platform_deliverability_signals
		WHERE tenant_id=`+ph(12)+` AND recorded_at >= `+ph(13)+` AND recorded_at < `+ph(14),
		SignalDelivered, SignalTempFail, SignalPermFail, SignalBounce, SignalComplaint,
		SignalSpamReject, SignalPolicyReject, SignalThrottled, SignalTLSFailure, SignalAuthFailure,
		SignalSuppressed, tenantID, start, end)
	t := &CategoryTotals{}
	if err := row.Scan(&t.Volume, &t.Delivered, &t.TempFail, &t.PermFail, &t.Bounced, &t.Complaints,
		&t.SpamReject, &t.PolicyDeny, &t.Throttled, &t.TLSFailure, &t.AuthFailure, &t.Suppressed); err != nil {
		return nil, err
	}
	return t, nil
}

// BreakdownRow is one GROUP BY result with bounded cardinality.
type BreakdownRow struct {
	Key   string
	Count int64
}

// DimensionBreakdown groups the tenant's signals over [start, end) by
// one dimension's value, highest volume first, bounded to maxRows.
func (r *Repository) DimensionBreakdown(ctx context.Context, tenantID uint, dim Dimension, start, end time.Time, maxRows int) ([]BreakdownRow, error) {
	if maxRows <= 0 || maxRows > 100 {
		maxRows = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT dimension_value, COUNT(*) FROM platform_deliverability_signals
		 WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND dimension=`+r.dialect.Placeholder(2)+` AND recorded_at>=`+r.dialect.Placeholder(3)+` AND recorded_at<`+r.dialect.Placeholder(4)+`
		 GROUP BY dimension_value ORDER BY COUNT(*) DESC, dimension_value ASC LIMIT `+r.dialect.Placeholder(5),
		tenantID, string(dim), start, end, maxRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BreakdownRow
	for rows.Next() {
		var b BreakdownRow
		if err := rows.Scan(&b.Key, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CategoryBreakdown groups the tenant's signals over [start, end) by
// signal type, mapped to canonical categories.
func (r *Repository) CategoryBreakdown(ctx context.Context, tenantID uint, start, end time.Time) ([]BreakdownRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT type, COUNT(*) FROM platform_deliverability_signals
		 WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND recorded_at>=`+r.dialect.Placeholder(2)+` AND recorded_at<`+r.dialect.Placeholder(3)+`
		 GROUP BY type ORDER BY COUNT(*) DESC, type ASC`,
		tenantID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BreakdownRow
	for rows.Next() {
		var b BreakdownRow
		if err := rows.Scan(&b.Key, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// bucketExpr returns the dialect-appropriate timestamp-truncation
// expression for the requested granularity.
//
// SQLite: the modernc driver persists time.Time values as TEXT in its
// own "2006-01-02 15:04:05.999999999 +0000 UTC" format, which
// SQLite's strftime() time parser rejects (returns NULL). The bucket
// expression therefore truncates the ISO prefix with substr() instead
// of parsing — "2026-08-12 16:44:20 +0000 UTC" → "2026-08-12 16:00:00".
// The 'T' separator is normalized to a space so rows stored in either
// ISO form bucket identically. All values are UTC by construction
// (kernel.Clock contract).
func (r *Repository) bucketExpr(granularity string) string {
	if r.dialect.IsPostgres() {
		return "date_trunc('" + granularity + "', recorded_at)"
	}
	if granularity == "day" {
		return "substr(replace(recorded_at, 'T', ' '), 1, 10) || ' 00:00:00'"
	}
	return "substr(replace(recorded_at, 'T', ' '), 1, 13) || ':00:00'"
}

// TimeBuckets returns per-bucket totals over [start, end), bounded to
// maxBuckets rows. The bucket expression is dialect-portable and UTC;
// bucket keys are normalized to RFC3339 UTC via parseBucketKey.
func (r *Repository) TimeBuckets(ctx context.Context, tenantID uint, start, end time.Time, granularity string, maxBuckets int) ([]BreakdownRow, error) {
	if maxBuckets <= 0 || maxBuckets > 200 {
		maxBuckets = 200
	}
	expr := r.bucketExpr(granularity)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+expr+` AS bucket, COUNT(*) FROM platform_deliverability_signals
		 WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND recorded_at>=`+r.dialect.Placeholder(2)+` AND recorded_at<`+r.dialect.Placeholder(3)+`
		 GROUP BY bucket ORDER BY bucket ASC LIMIT `+r.dialect.Placeholder(4),
		tenantID, start, end, maxBuckets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BreakdownRow
	for rows.Next() {
		var key string
		var b BreakdownRow
		if err := rows.Scan(&key, &b.Count); err != nil {
			return nil, err
		}
		// The bucket key format differs by driver (SQLite substr
		// "2006-01-02 15:04:05", Postgres date_trunc driver-formatted);
		// normalize to RFC3339 UTC so responses are deterministic.
		t, perr := parseBucketKey(key)
		if perr != nil {
			return nil, perr
		}
		b.Key = t.UTC().Format(time.RFC3339)
		out = append(out, b)
	}
	return out, rows.Err()
}

// parseBucketKey tolerantly parses the driver-specific bucket key
// formats and returns the UTC instant.
func parseBucketKey(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable bucket key %q", s)
}

// PurgeSignalsBefore applies the retention policy (bounded retention —
// events older than the cutoff are removed). Called by the scheduler.
func (r *Repository) PurgeSignalsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM platform_deliverability_signals WHERE recorded_at < `+r.dialect.Placeholder(1), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const suppressionCols = `id, tenant_id, address, reason, source, actor_id, notes, expires_at, state, released_at, released_by, released_reason, version, created_at, updated_at`

// AddSuppression is an atomic create/upsert scoped to
// (tenant_id, address). Re-suppressing an address whose row is
// ACTIVE updates the reason/expiry/source in place (idempotent
// duplicate request → the same logical suppression); a row in
// released/expired state is reactivated (cleared release fields).
// The unique index makes concurrent duplicates race safely: exactly
// one logical row always results. Concurrency-safe by constraint, not
// by check-then-write.
func (r *Repository) AddSuppression(ctx context.Context, s *Suppression) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_deliverability_suppressions
			(tenant_id, address, reason, source, actor_id, notes, expires_at, state, version, created_at, updated_at)
		VALUES (`+r.dialect.Placeholders(11)+`)
		ON CONFLICT (tenant_id, address) DO UPDATE SET
			reason=excluded.reason, source=excluded.source, actor_id=excluded.actor_id,
			notes=excluded.notes, expires_at=excluded.expires_at,
			state='active',
			released_at=NULL, released_by=0, released_reason='',
			version=platform_deliverability_suppressions.version+1,
			updated_at=excluded.updated_at`,
		s.TenantID, s.Address, s.Reason, s.Source, s.ActorID, s.Notes, s.ExpiresAt,
		string(SuppressionActive), 1, s.CreatedAt, s.UpdatedAt)
	return err
}

// GetSuppression returns one suppression strictly scoped to its
// tenant — a cross-tenant id yields (nil, nil), never another
// tenant's row.
func (r *Repository) GetSuppression(ctx context.Context, id, tenantID uint) (*Suppression, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+suppressionCols+` FROM platform_deliverability_suppressions WHERE id=`+r.dialect.Placeholder(1)+` AND tenant_id=`+r.dialect.Placeholder(2), id, tenantID)
	s, err := scanSuppression(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// GetSuppressionByAddress returns the single row for a tenant +
// normalized address regardless of state (used by release semantics to
// find the row for history recording).
func (r *Repository) GetSuppressionByAddress(ctx context.Context, tenantID uint, address string) (*Suppression, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+suppressionCols+` FROM platform_deliverability_suppressions WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND address=`+r.dialect.Placeholder(2), tenantID, address)
	s, err := scanSuppression(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// ReleaseSuppression is the guarded active→released transition. Only
// an ACTIVE row can be released; a released/expired row is left
// untouched (one valid terminal state). RowsAffected distinguishes
// "not found / wrong tenant" from "already terminal".
func (r *Repository) ReleaseSuppression(ctx context.Context, id, tenantID, actorID uint, reason string, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_deliverability_suppressions SET
			state=`+r.dialect.Placeholder(1)+`, released_at=`+r.dialect.Placeholder(2)+`, released_by=`+r.dialect.Placeholder(3)+`,
			released_reason=`+r.dialect.Placeholder(4)+`, version=version+1, updated_at=`+r.dialect.Placeholder(5)+`
		 WHERE id=`+r.dialect.Placeholder(6)+` AND tenant_id=`+r.dialect.Placeholder(7)+` AND state=`+r.dialect.Placeholder(8),
		string(SuppressionReleased), now, actorID, reason, now, id, tenantID, string(SuppressionActive))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReactivateSuppression is the guarded terminal→active transition
// (policy: an operator may reactivate a released or expired
// suppression). Returns affected=false when the row is missing, wrong
// tenant, or already active.
func (r *Repository) ReactivateSuppression(ctx context.Context, id, tenantID, actorID uint, reason SuppressionReason, source string, notes string, expiresAt *time.Time, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_deliverability_suppressions SET
			state=`+r.dialect.Placeholder(1)+`, reason=`+r.dialect.Placeholder(2)+`, source=`+r.dialect.Placeholder(3)+`,
			notes=`+r.dialect.Placeholder(4)+`, expires_at=`+r.dialect.Placeholder(5)+`, actor_id=`+r.dialect.Placeholder(6)+`,
			released_at=NULL, released_by=0, released_reason='',
			version=version+1, updated_at=`+r.dialect.Placeholder(7)+`
		 WHERE id=`+r.dialect.Placeholder(8)+` AND tenant_id=`+r.dialect.Placeholder(9)+` AND state IN (`+r.dialect.Placeholder(10)+`,`+r.dialect.Placeholder(11)+`)`,
		string(SuppressionActive), string(reason), source, notes, expiresAt, actorID, now,
		id, tenantID, string(SuppressionReleased), string(SuppressionExpired))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReconcileExpired marks active suppressions whose expiry has passed
// as expired. Called ONLY by the background scheduler — never on the
// delivery or request path. Bounded by the state+tenant index.
func (r *Repository) ReconcileExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_deliverability_suppressions SET state=`+r.dialect.Placeholder(1)+`, version=version+1, updated_at=`+r.dialect.Placeholder(2)+`
		 WHERE state=`+r.dialect.Placeholder(3)+` AND expires_at IS NOT NULL AND expires_at <= `+r.dialect.Placeholder(4),
		string(SuppressionExpired), now, string(SuppressionActive), now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RecordSuppressionEvent appends a lifecycle evidence row.
func (r *Repository) RecordSuppressionEvent(ctx context.Context, suppressionID, tenantID, actorID uint, event, reason string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_deliverability_suppression_events (suppression_id, tenant_id, event, actor_id, reason, at) VALUES (`+r.dialect.Placeholders(6)+`)`,
		suppressionID, tenantID, event, actorID, reason, at)
	return err
}

// ListSuppressionEvents returns the append-only lifecycle evidence for
// one suppression, tenant-scoped, most recent first.
func (r *Repository) ListSuppressionEvents(ctx context.Context, suppressionID, tenantID uint, limit int) ([]SuppressionEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, suppression_id, tenant_id, event, actor_id, reason, at FROM platform_deliverability_suppression_events
		 WHERE suppression_id=`+r.dialect.Placeholder(1)+` AND tenant_id=`+r.dialect.Placeholder(2)+`
		 ORDER BY id DESC LIMIT `+r.dialect.Placeholder(3), suppressionID, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SuppressionEvent
	for rows.Next() {
		var e SuppressionEvent
		if err := rows.Scan(&e.ID, &e.SuppressionID, &e.TenantID, &e.Event, &e.ActorID, &e.Reason, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RemoveSuppression releases an active suppression by normalized
// address (release semantics — history is preserved, the row is never
// hard-deleted). Returns whether an ACTIVE row was released.
func (r *Repository) RemoveSuppression(ctx context.Context, tenantID uint, address string, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_deliverability_suppressions SET
			state=`+r.dialect.Placeholder(1)+`, released_at=`+r.dialect.Placeholder(2)+`, released_reason=`+r.dialect.Placeholder(3)+`,
			version=version+1, updated_at=`+r.dialect.Placeholder(4)+`
		 WHERE tenant_id=`+r.dialect.Placeholder(5)+` AND address=`+r.dialect.Placeholder(6)+` AND state=`+r.dialect.Placeholder(7),
		string(SuppressionReleased), now, "operator release", now, tenantID, address, string(SuppressionActive))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// IsSuppressed is the real-time enforcement check — called from the
// actual outbound delivery path. A single indexed point lookup
// (tenant_id, address) — never a scan. Expired/released rows are not
// suppressed (the state predicate handles release; the Go-side expiry
// comparison below handles expiry portably, independent of the
// scheduler having reconciled yet).
func (r *Repository) IsSuppressed(ctx context.Context, tenantID uint, address string, now time.Time) (bool, *Suppression, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+suppressionCols+`
		 FROM platform_deliverability_suppressions WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND address=`+r.dialect.Placeholder(2)+` AND state=`+r.dialect.Placeholder(3),
		tenantID, address, string(SuppressionActive))
	s, err := scanSuppression(row)
	if err == sql.ErrNoRows {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if s.ExpiresAt != nil && !s.ExpiresAt.After(now) {
		return false, nil, nil
	}
	return true, s, nil
}

// ListSuppressions returns the paginated, filterable suppression list
// for one tenant with deterministic ordering (id DESC) and bounded
// pagination. State filtering is exact; domain filtering matches the
// address's domain suffix; ranges are inclusive/exclusive-safe via the
// caller's UTC-normalized bounds.
func (r *Repository) ListSuppressions(ctx context.Context, f SuppressionFilter) ([]Suppression, int64, error) {
	where := []string{"tenant_id=" + r.dialect.Placeholder(1)}
	args := []any{f.TenantID}
	add := func(cond string, val any) {
		where = append(where, cond)
		args = append(args, val)
	}
	if f.Domain != "" {
		add("address LIKE "+r.dialect.Placeholder(len(args)+1), "%@"+strings.ToLower(strings.TrimPrefix(f.Domain, "@")))
	}
	if f.Search != "" {
		add("address LIKE "+r.dialect.Placeholder(len(args)+1), "%"+strings.ToLower(f.Search)+"%")
	}
	if f.Reason != "" {
		add("reason="+r.dialect.Placeholder(len(args)+1), f.Reason)
	}
	if f.Source != "" {
		add("source="+r.dialect.Placeholder(len(args)+1), f.Source)
	}
	// State filter: empty defaults to ACTIVE only (backward-compatible
	// list contract — released/expired rows are historical, surfaced
	// via state=released/expired or state=all); "all" removes the
	// predicate; any other value is an exact state match.
	switch f.State {
	case "", SuppressionActive:
		add("state="+r.dialect.Placeholder(len(args)+1), string(SuppressionActive))
	case SuppressionState("all"):
		// no predicate
	default:
		add("state="+r.dialect.Placeholder(len(args)+1), string(f.State))
	}
	if f.CreatedFrom != nil {
		add("created_at>="+r.dialect.Placeholder(len(args)+1), *f.CreatedFrom)
	}
	if f.CreatedTo != nil {
		add("created_at<="+r.dialect.Placeholder(len(args)+1), *f.CreatedTo)
	}
	if f.ExpiryFrom != nil {
		add("expires_at>="+r.dialect.Placeholder(len(args)+1), *f.ExpiryFrom)
	}
	if f.ExpiryTo != nil {
		add("expires_at<="+r.dialect.Placeholder(len(args)+1), *f.ExpiryTo)
	}
	whereClause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_deliverability_suppressions`+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := r.db.QueryContext(ctx, `SELECT `+suppressionCols+` FROM platform_deliverability_suppressions`+whereClause+
		` ORDER BY id DESC LIMIT `+r.dialect.Placeholder(len(args)-1)+` OFFSET `+r.dialect.Placeholder(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Suppression
	for rows.Next() {
		s, err := scanSuppression(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *s)
	}
	return out, total, rows.Err()
}

func scanSuppression(row interface{ Scan(...any) error }) (*Suppression, error) {
	var s Suppression
	err := row.Scan(&s.ID, &s.TenantID, &s.Address, &s.Reason, &s.Source, &s.ActorID, &s.Notes,
		&s.ExpiresAt, &s.State, &s.ReleasedAt, &s.ReleasedBy, &s.ReleasedReason, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
