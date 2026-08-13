package observability

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
		`CREATE TABLE IF NOT EXISTS platform_alert_rules (
			id ` + autoInc + `,
			name TEXT NOT NULL,
			metric_name TEXT NOT NULL,
			comparator TEXT NOT NULL,
			threshold REAL NOT NULL,
			duration_seconds INTEGER NOT NULL DEFAULT 0,
			severity TEXT NOT NULL DEFAULT 'warning',
			scope TEXT NOT NULL DEFAULT 'global',
			enabled INTEGER NOT NULL DEFAULT 1,
			cooldown_seconds INTEGER NOT NULL DEFAULT 300,
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS platform_alerts (
			id ` + autoInc + `,
			rule_id INTEGER NOT NULL,
			scope TEXT NOT NULL,
			state TEXT NOT NULL,
			value REAL NOT NULL DEFAULT 0,
			first_observed_at ` + ts + ` NOT NULL,
			fired_at ` + ts + `,
			resolved_at ` + ts + `,
			acknowledged_at ` + ts + `,
			acknowledged_by INTEGER NOT NULL DEFAULT 0,
			silenced_until ` + ts + `,
			last_notified_at ` + ts + `,
			version INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_alerts_rule_scope_active ON platform_alerts (rule_id, scope)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) CreateRule(ctx context.Context, rule *Rule) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_alert_rules (name, metric_name, comparator, threshold, duration_seconds, severity, scope, enabled, cooldown_seconds, created_at, updated_at)
		VALUES (`+r.dialect.Placeholders(11)+`)`,
		rule.Name, rule.MetricName, rule.Comparator, rule.Threshold, int(rule.Duration.Seconds()),
		rule.Severity, rule.Scope, boolToInt(rule.Enabled), rule.CooldownSeconds, rule.CreatedAt, rule.UpdatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	rule.ID = uint(id)
	return nil
}

const ruleCols = `id, name, metric_name, comparator, threshold, duration_seconds, severity, scope, enabled, cooldown_seconds, created_at, updated_at`

func (r *Repository) ListEnabledRules(ctx context.Context) ([]Rule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+ruleCols+` FROM platform_alert_rules WHERE enabled=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

func (r *Repository) GetRule(ctx context.Context, id uint) (*Rule, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM platform_alert_rules WHERE id=`+r.dialect.Placeholder(1), id)
	return scanRule(row)
}

// ── Alerts ───────────────────────────────────────────────────────

const alertCols = `id, rule_id, scope, state, value, first_observed_at, fired_at, resolved_at, acknowledged_at, acknowledged_by, silenced_until, last_notified_at, version`

// UpsertObservation is the evaluation-loop write path: one atomic
// upsert per (rule, scope) per tick. On first observation it creates
// a Pending alert; on repeated observation it updates Value and
// leaves State to the caller's subsequent logic (state transitions
// are decided in Go, in service.go, then persisted via
// TransitionState — this method ONLY tracks "is the condition
// currently true and since when").
func (r *Repository) GetOrCreateAlert(ctx context.Context, ruleID uint, scope string, value float64, now time.Time) (*Alert, bool, error) {
	existing, err := r.getAlertByRuleScope(ctx, ruleID, scope)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_alerts (rule_id, scope, state, value, first_observed_at, version)
		VALUES (`+r.dialect.Placeholders(6)+`)`,
		ruleID, scope, string(AlertPending), value, now, 1)
	if err != nil {
		return nil, false, err
	}
	id, _ := res.LastInsertId()
	a, err := r.GetAlert(ctx, uint(id))
	return a, true, err
}

func (r *Repository) getAlertByRuleScope(ctx context.Context, ruleID uint, scope string) (*Alert, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+alertCols+` FROM platform_alerts WHERE rule_id=`+r.dialect.Placeholder(1)+` AND scope=`+r.dialect.Placeholder(2), ruleID, scope)
	a, err := scanAlert(row)
	if err == ErrAlertNotFound {
		return nil, nil
	}
	return a, err
}

func (r *Repository) GetAlert(ctx context.Context, id uint) (*Alert, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+alertCols+` FROM platform_alerts WHERE id=`+r.dialect.Placeholder(1), id)
	return scanAlert(row)
}

func (r *Repository) ListAlerts(ctx context.Context, state AlertState, afterID uint, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + alertCols + ` FROM platform_alerts WHERE id > ` + r.dialect.Placeholder(1)
	args := []any{afterID}
	if state != "" {
		q += ` AND state = ` + r.dialect.Placeholder(2)
		args = append(args, state)
	}
	q += ` ORDER BY id ASC LIMIT ` + r.dialect.Placeholder(len(args)+1)
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// UpdateValue refreshes an alert's observed value without changing state.
func (r *Repository) UpdateValue(ctx context.Context, id uint, value float64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE platform_alerts SET value=`+r.dialect.Placeholder(1)+`, version=version+1 WHERE id=`+r.dialect.Placeholder(2), value, id)
	return err
}

// TransitionState is the same atomic expected-state+version-guarded
// pattern used throughout this codebase (domainlifecycle,
// bulkprovision, cluster).
func (r *Repository) TransitionState(ctx context.Context, id uint, expected, next AlertState, expectedVersion int, now time.Time, setters map[string]any) (bool, error) {
	setClauses := "state=" + r.dialect.Placeholder(1) + ", version=version+1"
	args := []any{next}
	i := 2
	for col, val := range setters {
		setClauses += ", " + col + "=" + r.dialect.Placeholder(i)
		args = append(args, val)
		i++
	}
	args = append(args, id, expected, expectedVersion)
	q := `UPDATE platform_alerts SET ` + setClauses + ` WHERE id=` + r.dialect.Placeholder(i) + ` AND state=` + r.dialect.Placeholder(i+1) + ` AND version=` + r.dialect.Placeholder(i+2)
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func scanRule(row interface{ Scan(...any) error }) (*Rule, error) {
	var rule Rule
	var enabled, durationSec int
	err := row.Scan(&rule.ID, &rule.Name, &rule.MetricName, &rule.Comparator, &rule.Threshold, &durationSec,
		&rule.Severity, &rule.Scope, &enabled, &rule.CooldownSeconds, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrRuleNotFound
		}
		return nil, err
	}
	rule.Duration = time.Duration(durationSec) * time.Second
	rule.Enabled = enabled != 0
	return &rule, nil
}

func scanAlert(row interface{ Scan(...any) error }) (*Alert, error) {
	var a Alert
	err := row.Scan(&a.ID, &a.RuleID, &a.Scope, &a.State, &a.Value, &a.FirstObservedAt, &a.FiredAt, &a.ResolvedAt,
		&a.AcknowledgedAt, &a.AcknowledgedBy, &a.SilencedUntil, &a.LastNotifiedAt, &a.Version)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrAlertNotFound
		}
		return nil, err
	}
	return &a, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
