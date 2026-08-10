package updates

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
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_update_records (
		id `+autoInc+`,
		version TEXT NOT NULL,
		platform TEXT NOT NULL,
		arch TEXT NOT NULL,
		artifact_hash TEXT NOT NULL,
		artifact_path TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL,
		prev_version TEXT NOT NULL DEFAULT '',
		prev_hash TEXT NOT NULL DEFAULT '',
		failure_note TEXT NOT NULL DEFAULT '',
		actor_id INTEGER NOT NULL DEFAULT 0,
		created_at `+ts+` NOT NULL,
		updated_at `+ts+` NOT NULL
	)`)
	return err
}

func (r *Repository) Insert(ctx context.Context, rec *Record) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_update_records
			(version, platform, arch, artifact_hash, artifact_path, state, prev_version, prev_hash, failure_note, actor_id, created_at, updated_at)
		VALUES (`+r.dialect.Placeholders(12)+`)`,
		rec.Version, rec.Platform, rec.Arch, rec.ArtifactHash, rec.ArtifactPath, rec.State,
		rec.PrevVersion, rec.PrevHash, rec.FailureNote, rec.ActorID, rec.CreatedAt, rec.UpdatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	rec.ID = uint(id)
	return nil
}

func (r *Repository) UpdateState(ctx context.Context, id uint, state State, failureNote string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE platform_update_records SET state=`+r.dialect.Placeholder(1)+`, failure_note=`+r.dialect.Placeholder(2)+`, updated_at=`+r.dialect.Placeholder(3)+` WHERE id=`+r.dialect.Placeholder(4),
		state, failureNote, now, id)
	return err
}

func (r *Repository) Get(ctx context.Context, id uint) (*Record, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, version, platform, arch, artifact_hash, artifact_path, state, prev_version, prev_hash, failure_note, actor_id, created_at, updated_at
		FROM platform_update_records WHERE id=`+r.dialect.Placeholder(1), id)
	return scanRecord(row)
}

func (r *Repository) Latest(ctx context.Context) (*Record, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, version, platform, arch, artifact_hash, artifact_path, state, prev_version, prev_hash, failure_note, actor_id, created_at, updated_at
		FROM platform_update_records ORDER BY id DESC LIMIT 1`)
	rec, err := scanRecord(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return rec, err
}

func (r *Repository) List(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, version, platform, arch, artifact_hash, artifact_path, state, prev_version, prev_hash, failure_note, actor_id, created_at, updated_at
		FROM platform_update_records ORDER BY id DESC LIMIT `+r.dialect.Placeholder(1), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var rec Record
		if err := rows.Scan(&rec.ID, &rec.Version, &rec.Platform, &rec.Arch, &rec.ArtifactHash, &rec.ArtifactPath, &rec.State, &rec.PrevVersion, &rec.PrevHash, &rec.FailureNote, &rec.ActorID, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanRecord(row *sql.Row) (*Record, error) {
	var rec Record
	err := row.Scan(&rec.ID, &rec.Version, &rec.Platform, &rec.Arch, &rec.ArtifactHash, &rec.ArtifactPath, &rec.State, &rec.PrevVersion, &rec.PrevHash, &rec.FailureNote, &rec.ActorID, &rec.CreatedAt, &rec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}
