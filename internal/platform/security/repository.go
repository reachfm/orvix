package security

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
		`CREATE TABLE IF NOT EXISTS platform_security_events (
			id ` + autoInc + `,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			category TEXT NOT NULL,
			severity TEXT NOT NULL DEFAULT 'info',
			source_system TEXT NOT NULL DEFAULT '',
			actor TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at ` + ts + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_tenant_created ON platform_security_events (tenant_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_category ON platform_security_events (category)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) Record(ctx context.Context, e *Event) error {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_security_events (tenant_id, category, severity, source_system, actor, detail, created_at) VALUES (`+r.dialect.Placeholders(7)+`)`,
		e.TenantID, e.Category, e.Severity, e.SourceSystem, e.Actor, e.Detail, e.CreatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	e.ID = uint(id)
	return nil
}

// ListFilter narrows List. Zero values mean "no filter" on that
// dimension. AfterID is the cursor.
type ListFilter struct {
	TenantID uint
	Category Category
	Severity Severity
	AfterID  uint
	Limit    int
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]Event, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, tenant_id, category, severity, source_system, actor, detail, created_at FROM platform_security_events WHERE id > ` + r.dialect.Placeholder(1)
	args := []any{f.AfterID}
	if f.TenantID != 0 {
		q += ` AND tenant_id = ` + r.dialect.Placeholder(len(args)+1)
		args = append(args, f.TenantID)
	}
	if f.Category != "" {
		q += ` AND category = ` + r.dialect.Placeholder(len(args)+1)
		args = append(args, f.Category)
	}
	if f.Severity != "" {
		q += ` AND severity = ` + r.dialect.Placeholder(len(args)+1)
		args = append(args, f.Severity)
	}
	q += ` ORDER BY id ASC LIMIT ` + r.dialect.Placeholder(len(args)+1)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Category, &e.Severity, &e.SourceSystem, &e.Actor, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PurgeOlderThan deletes events older than cutoff, in bounded batches
// (LIMIT), so a large backlog never becomes one unbounded DELETE —
// callers loop until 0 rows are affected.
func (r *Repository) PurgeOlderThan(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 || batchSize > 5000 {
		batchSize = 1000
	}
	// SQLite and PostgreSQL both support DELETE ... WHERE id IN
	// (SELECT id ... LIMIT n) for bounded-batch deletes without a
	// vendor-specific DELETE ... LIMIT extension.
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM platform_security_events WHERE id IN (SELECT id FROM platform_security_events WHERE created_at < `+r.dialect.Placeholder(1)+` LIMIT `+r.dialect.Placeholder(2)+`)`,
		cutoff, batchSize)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
