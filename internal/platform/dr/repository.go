package dr

import (
	"context"
	"database/sql"

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
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_dr_drills (
		id `+autoInc+`,
		backup_id TEXT NOT NULL,
		outcome TEXT NOT NULL,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		failure_reason TEXT NOT NULL DEFAULT '',
		actor_id INTEGER NOT NULL DEFAULT 0,
		started_at `+ts+` NOT NULL
	)`)
	return err
}

func (r *Repository) RecordDrill(ctx context.Context, d *Drill) error {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_dr_drills (backup_id, outcome, duration_ms, failure_reason, actor_id, started_at) VALUES (`+r.dialect.Placeholders(6)+`)`,
		d.BackupID, d.Outcome, d.DurationMS, d.FailureReason, d.ActorID, d.StartedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	d.ID = uint(id)
	return nil
}

func (r *Repository) ListDrills(ctx context.Context, limit int) ([]Drill, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, backup_id, outcome, duration_ms, failure_reason, actor_id, started_at FROM platform_dr_drills ORDER BY id DESC LIMIT `+r.dialect.Placeholder(1), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Drill
	for rows.Next() {
		var d Drill
		if err := rows.Scan(&d.ID, &d.BackupID, &d.Outcome, &d.DurationMS, &d.FailureReason, &d.ActorID, &d.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repository) LastSuccessfulDrill(ctx context.Context) (*Drill, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, backup_id, outcome, duration_ms, failure_reason, actor_id, started_at FROM platform_dr_drills WHERE outcome=`+r.dialect.Placeholder(1)+` ORDER BY id DESC LIMIT 1`,
		string(DrillSucceeded))
	var d Drill
	err := row.Scan(&d.ID, &d.BackupID, &d.Outcome, &d.DurationMS, &d.FailureReason, &d.ActorID, &d.StartedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
