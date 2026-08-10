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
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_dr_operations (
		id `+autoInc+`,
		op_type TEXT NOT NULL,
		ref_id TEXT NOT NULL,
		status TEXT NOT NULL,
		idempotency_key TEXT NOT NULL DEFAULT '',
		actor_id INTEGER NOT NULL DEFAULT 0,
		created_at `+ts+` NOT NULL
	)`)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_dr_operations_idem ON platform_dr_operations (op_type, idempotency_key)`)
	return err
}

// RecordOperation inserts a historical DR operation row.
func (r *Repository) RecordOperation(ctx context.Context, op *Operation) error {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_dr_operations (op_type, ref_id, status, idempotency_key, actor_id, created_at) VALUES (`+r.dialect.Placeholders(6)+`)`,
		string(op.Type), op.RefID, op.Status, op.IdempotencyKey, op.ActorID, op.CreatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	op.ID = uint(id)
	return nil
}

// FindOperationByIdempotencyKey returns a previously recorded
// operation of the given type with the same idempotency key, if any
// — used to make CoordinatedBackup idempotent on retry the same way
// the retention and billing services already are.
func (r *Repository) FindOperationByIdempotencyKey(ctx context.Context, opType OperationType, idempotencyKey string) (*Operation, error) {
	if idempotencyKey == "" {
		return nil, nil
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, op_type, ref_id, status, idempotency_key, actor_id, created_at FROM platform_dr_operations WHERE op_type=`+r.dialect.Placeholder(1)+` AND idempotency_key=`+r.dialect.Placeholder(2)+` ORDER BY id ASC LIMIT 1`,
		string(opType), idempotencyKey)
	var op Operation
	var typ string
	if err := row.Scan(&op.ID, &typ, &op.RefID, &op.Status, &op.IdempotencyKey, &op.ActorID, &op.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	op.Type = OperationType(typ)
	return &op, nil
}

// ListOperations returns past DR operations newest-first with
// pagination, plus the total count for the caller to compute total
// pages.
func (r *Repository) ListOperations(ctx context.Context, limit, offset int) ([]Operation, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_dr_operations`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, op_type, ref_id, status, idempotency_key, actor_id, created_at FROM platform_dr_operations ORDER BY id DESC LIMIT `+r.dialect.Placeholder(1)+` OFFSET `+r.dialect.Placeholder(2),
		limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Operation
	for rows.Next() {
		var op Operation
		var typ string
		if err := rows.Scan(&op.ID, &typ, &op.RefID, &op.Status, &op.IdempotencyKey, &op.ActorID, &op.CreatedAt); err != nil {
			return nil, 0, err
		}
		op.Type = OperationType(typ)
		out = append(out, op)
	}
	return out, total, rows.Err()
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
