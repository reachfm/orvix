package bulkprovision

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
	if _, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_bulk_import_jobs (
		id `+autoInc+`,
		tenant_id INTEGER NOT NULL,
		domain_id INTEGER NOT NULL,
		status TEXT NOT NULL,
		strategy TEXT NOT NULL,
		idempotency_key TEXT NOT NULL DEFAULT '',
		total_rows INTEGER NOT NULL DEFAULT 0,
		valid_rows INTEGER NOT NULL DEFAULT 0,
		invalid_rows INTEGER NOT NULL DEFAULT 0,
		created_count INTEGER NOT NULL DEFAULT 0,
		failed_count INTEGER NOT NULL DEFAULT 0,
		version INTEGER NOT NULL DEFAULT 1,
		created_by INTEGER NOT NULL DEFAULT 0,
		created_at `+ts+` NOT NULL,
		updated_at `+ts+` NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_bulk_import_jobs_tenant_idem
		ON platform_bulk_import_jobs (tenant_id, idempotency_key) WHERE idempotency_key <> ''`); err != nil {
		// SQLite/older Postgres both support partial indexes with this
		// syntax; if a dialect ever doesn't, this is still additive
		// and safe to fail non-fatally in that edge case rather than
		// block schema setup entirely.
		_ = err
	}
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_bulk_import_rows (
		id `+autoInc+`,
		job_id INTEGER NOT NULL,
		row_number INTEGER NOT NULL,
		email TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		quota_mb INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL,
		error_code TEXT NOT NULL DEFAULT '',
		error_detail TEXT NOT NULL DEFAULT '',
		mailbox_id INTEGER NOT NULL DEFAULT 0,
		setup_token_hash TEXT NOT NULL DEFAULT '',
		created_at `+ts+` NOT NULL,
		updated_at `+ts+` NOT NULL
	)`)
	return err
}

func (r *Repository) CreateJob(ctx context.Context, j *Job, rows []Row) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := j.CreatedAt
	res, err := tx.ExecContext(ctx,
		`INSERT INTO platform_bulk_import_jobs (tenant_id, domain_id, status, strategy, idempotency_key, total_rows, valid_rows, invalid_rows, version, created_by, created_at, updated_at) VALUES (`+r.dialect.Placeholders(12)+`)`,
		j.TenantID, j.DomainID, j.Status, j.Strategy, j.IdempotencyKey, j.TotalRows, j.ValidRows, j.InvalidRows, 1, j.CreatedBy, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	j.ID = uint(id)
	j.Version = 1

	for i := range rows {
		rows[i].JobID = j.ID
		if err := r.insertRow(ctx, tx, &rows[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) insertRow(ctx context.Context, tx *sql.Tx, row *Row) error {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO platform_bulk_import_rows (job_id, row_number, email, name, quota_mb, status, error_code, error_detail, created_at, updated_at) VALUES (`+r.dialect.Placeholders(10)+`)`,
		row.JobID, row.RowNumber, row.Email, row.Name, row.QuotaMB, row.Status, row.ErrorCode, row.ErrorDetail, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	row.ID = uint(id)
	return nil
}

func (r *Repository) GetJob(ctx context.Context, id, tenantID uint) (*Job, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, domain_id, status, strategy, idempotency_key, total_rows, valid_rows, invalid_rows, created_count, failed_count, version, created_by, created_at, updated_at
		 FROM platform_bulk_import_jobs WHERE id=`+r.dialect.Placeholder(1)+` AND tenant_id=`+r.dialect.Placeholder(2), id, tenantID)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

func (r *Repository) GetJobByIdempotencyKey(ctx context.Context, tenantID uint, key string) (*Job, error) {
	if key == "" {
		return nil, nil
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, domain_id, status, strategy, idempotency_key, total_rows, valid_rows, invalid_rows, created_count, failed_count, version, created_by, created_at, updated_at
		 FROM platform_bulk_import_jobs WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND idempotency_key=`+r.dialect.Placeholder(2), tenantID, key)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

func (r *Repository) ListRows(ctx context.Context, jobID uint, statuses []RowStatus) ([]Row, error) {
	q := `SELECT id, job_id, row_number, email, name, quota_mb, status, error_code, error_detail, mailbox_id, created_at, updated_at FROM platform_bulk_import_rows WHERE job_id=` + r.dialect.Placeholder(1)
	args := []any{jobID}
	if len(statuses) > 0 {
		placeholders := ""
		for i, s := range statuses {
			if i > 0 {
				placeholders += ","
			}
			placeholders += r.dialect.Placeholder(len(args) + 1)
			args = append(args, s)
		}
		q += " AND status IN (" + placeholders + ")"
	}
	q += " ORDER BY row_number ASC"
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.ID, &row.JobID, &row.RowNumber, &row.Email, &row.Name, &row.QuotaMB, &row.Status, &row.ErrorCode, &row.ErrorDetail, &row.MailboxID, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// UpdateRowResult sets a row's post-attempt outcome, optionally
// storing the setup token's hash (never the raw token).
func (r *Repository) UpdateRowResult(ctx context.Context, id uint, status RowStatus, errCode ErrorCode, errDetail string, mailboxID uint, setupTokenHash string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE platform_bulk_import_rows SET status=`+r.dialect.Placeholder(1)+`, error_code=`+r.dialect.Placeholder(2)+`, error_detail=`+r.dialect.Placeholder(3)+`, mailbox_id=`+r.dialect.Placeholder(4)+`, setup_token_hash=`+r.dialect.Placeholder(5)+`, updated_at=`+r.dialect.Placeholder(6)+` WHERE id=`+r.dialect.Placeholder(7),
		status, errCode, errDetail, mailboxID, setupTokenHash, now, id)
	return err
}

// TransitionJobIfVersion is the same atomic optimistic-concurrency
// pattern used in internal/platform/domainlifecycle: the expected
// state AND version are both in the WHERE clause of a single UPDATE,
// so two concurrent callers (e.g. a cancel racing an execute) cannot
// both succeed.
func (r *Repository) TransitionJobIfVersion(ctx context.Context, id uint, expected, next JobStatus, expectedVersion int, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_bulk_import_jobs SET status=`+r.dialect.Placeholder(1)+`, version=version+1, updated_at=`+r.dialect.Placeholder(2)+`
		 WHERE id=`+r.dialect.Placeholder(3)+` AND status=`+r.dialect.Placeholder(4)+` AND version=`+r.dialect.Placeholder(5),
		next, now, id, expected, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *Repository) UpdateJobCounters(ctx context.Context, id uint, status JobStatus, createdCount, failedCount int, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE platform_bulk_import_jobs SET status=`+r.dialect.Placeholder(1)+`, created_count=`+r.dialect.Placeholder(2)+`, failed_count=`+r.dialect.Placeholder(3)+`, version=version+1, updated_at=`+r.dialect.Placeholder(4)+` WHERE id=`+r.dialect.Placeholder(5),
		status, createdCount, failedCount, now, id)
	return err
}

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.TenantID, &j.DomainID, &j.Status, &j.Strategy, &j.IdempotencyKey, &j.TotalRows, &j.ValidRows, &j.InvalidRows, &j.CreatedCount, &j.FailedCount, &j.Version, &j.CreatedBy, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}
