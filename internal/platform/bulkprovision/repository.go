package bulkprovision

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/dbdialect"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
	// domainRepo is constructed ONCE here, at Repository construction
	// time — never inside CreateJob after a transaction is already
	// open. domain.NewDomainAdminRepo runs its own dialect-detection
	// query against the raw *sql.DB; doing that while a tx already
	// holds the pool's only connection (SQLite test pools are
	// frequently capped at 1) deadlocks forever. This exact bug was
	// found and fixed once already in the alias-creation handler
	// (Phase 8 C2) — same root cause, same fix: construct before any
	// transaction, then only ever call .WithTx() (no query) on it.
	domainRepo *domain.DomainAdminRepo
}

func NewRepository(db *sql.DB) *Repository {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d, domainRepo: domain.NewDomainAdminRepo(db)}
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
		conflict_policy TEXT NOT NULL DEFAULT 'fail',
		idempotency_key TEXT NOT NULL DEFAULT '',
		source_hash TEXT NOT NULL DEFAULT '',
		schema_version INTEGER NOT NULL DEFAULT 1,
		total_rows INTEGER NOT NULL DEFAULT 0,
		valid_rows INTEGER NOT NULL DEFAULT 0,
		invalid_rows INTEGER NOT NULL DEFAULT 0,
		created_count INTEGER NOT NULL DEFAULT 0,
		failed_count INTEGER NOT NULL DEFAULT 0,
		skipped_count INTEGER NOT NULL DEFAULT 0,
		next_row_number INTEGER NOT NULL DEFAULT 0,
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
	if _, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_bulk_import_rows (
		id `+autoInc+`,
		job_id INTEGER NOT NULL,
		row_number INTEGER NOT NULL,
		external_ref TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		quota_mb INTEGER NOT NULL DEFAULT 0,
		access_mode TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		error_code TEXT NOT NULL DEFAULT '',
		error_detail TEXT NOT NULL DEFAULT '',
		mailbox_id INTEGER NOT NULL DEFAULT 0,
		setup_token_hash TEXT NOT NULL DEFAULT '',
		created_at `+ts+` NOT NULL,
		updated_at `+ts+` NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_bulk_import_rows_job_row ON platform_bulk_import_rows (job_id, row_number)`); err != nil {
		return err
	}
	// Additive migrations for databases created before these columns
	// existed (mirrors the ensureColumn convention used elsewhere in
	// this codebase: ADD COLUMN, tolerate an "already exists" error so
	// re-running is idempotent).
	migrations := []struct{ table, column, ddl string }{
		{"platform_bulk_import_jobs", "conflict_policy", "TEXT NOT NULL DEFAULT 'fail'"},
		{"platform_bulk_import_jobs", "source_hash", "TEXT NOT NULL DEFAULT ''"},
		{"platform_bulk_import_jobs", "schema_version", "INTEGER NOT NULL DEFAULT 1"},
		{"platform_bulk_import_jobs", "skipped_count", "INTEGER NOT NULL DEFAULT 0"},
		{"platform_bulk_import_jobs", "next_row_number", "INTEGER NOT NULL DEFAULT 0"},
		{"platform_bulk_import_rows", "external_ref", "TEXT NOT NULL DEFAULT ''"},
		{"platform_bulk_import_rows", "access_mode", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, m := range migrations {
		if err := r.ensureColumn(ctx, m.table, m.column, m.ddl); err != nil {
			return err
		}
	}
	return nil
}

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

// insertReturningID executes an INSERT and returns the real generated
// id portably: PostgreSQL has no LastInsertId, so it uses
// INSERT ... RETURNING id; SQLite uses LastInsertId.
func (r *Repository) insertReturningID(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any) (int64, error) {
	if r.dialect.IsPostgres() {
		var id int64
		err := q.QueryRowContext(ctx, query+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) CreateJob(ctx context.Context, j *Job, rows []Row) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Canonical domain operability guard (Phase 8 C2-R1), inside this
	// same transaction, FOR UPDATE-locked on Postgres. Validation
	// success does not authorize job creation on its own — a domain
	// disabled between validation and this call must still be
	// rejected, and rejection here means the job/row insert below
	// never runs and the deferred Rollback discards everything.
	if opOut := r.domainRepo.WithTx(tx).CheckOperabilityByIDTx(ctx, j.DomainID, j.TenantID, true); !opOut.Operational() {
		return opOut.Err
	}

	now := j.CreatedAt
	id, err := r.insertReturningID(ctx, tx,
		`INSERT INTO platform_bulk_import_jobs (tenant_id, domain_id, status, strategy, conflict_policy, idempotency_key, source_hash, schema_version, total_rows, valid_rows, invalid_rows, next_row_number, version, created_by, created_at, updated_at) VALUES (`+r.dialect.Placeholders(16)+`)`,
		j.TenantID, j.DomainID, j.Status, j.Strategy, j.ConflictPolicy, j.IdempotencyKey, j.SourceHash, j.SchemaVersion, j.TotalRows, j.ValidRows, j.InvalidRows, 1, 1, j.CreatedBy, now, now)
	if err != nil {
		return err
	}
	j.ID = uint(id)
	j.Version = 1
	j.NextRowNumber = 1

	for i := range rows {
		rows[i].JobID = j.ID
		if err := r.insertRow(ctx, tx, &rows[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) insertRow(ctx context.Context, tx *sql.Tx, row *Row) error {
	id, err := r.insertReturningID(ctx, tx,
		`INSERT INTO platform_bulk_import_rows (job_id, row_number, external_ref, email, name, quota_mb, access_mode, status, error_code, error_detail, created_at, updated_at) VALUES (`+r.dialect.Placeholders(12)+`)`,
		row.JobID, row.RowNumber, row.ExternalRef, row.Email, row.Name, row.QuotaMB, row.AccessMode, row.Status, row.ErrorCode, row.ErrorDetail, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return err
	}
	row.ID = uint(id)
	return nil
}

const jobCols = `id, tenant_id, domain_id, status, strategy, conflict_policy, idempotency_key, source_hash, schema_version, total_rows, valid_rows, invalid_rows, created_count, failed_count, skipped_count, next_row_number, version, created_by, created_at, updated_at`

func (r *Repository) GetJob(ctx context.Context, id, tenantID uint) (*Job, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+jobCols+` FROM platform_bulk_import_jobs WHERE id=`+r.dialect.Placeholder(1)+` AND tenant_id=`+r.dialect.Placeholder(2), id, tenantID)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

// getJobTx reads the job by ID (no tenant filter — the caller already
// holds an authoritative tenant-scoped job) within the given
// transaction, so a lifecycle-finalize read-after-write sees its own
// uncommitted write.
func (r *Repository) getJobTx(ctx context.Context, tx *sql.Tx, id uint) (*Job, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+jobCols+` FROM platform_bulk_import_jobs WHERE id=`+r.dialect.Placeholder(1), id)
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
		`SELECT `+jobCols+` FROM platform_bulk_import_jobs WHERE tenant_id=`+r.dialect.Placeholder(1)+` AND idempotency_key=`+r.dialect.Placeholder(2), tenantID, key)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

// ListJobs returns a tenant's import jobs, newest first, bounded by
// limit/offset (the handler layer enforces sane pagination bounds).
func (r *Repository) ListJobs(ctx context.Context, tenantID uint, limit, offset int) ([]Job, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_bulk_import_jobs WHERE tenant_id=`+r.dialect.Placeholder(1), tenantID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM platform_bulk_import_jobs WHERE tenant_id=`+r.dialect.Placeholder(1)+` ORDER BY id DESC LIMIT `+r.dialect.Placeholder(2)+` OFFSET `+r.dialect.Placeholder(3),
		tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *j)
	}
	return out, total, rows.Err()
}

func (r *Repository) ListRows(ctx context.Context, jobID uint, statuses []RowStatus) ([]Row, error) {
	return r.listRowsPage(ctx, jobID, statuses, 0, 0)
}

// ListRowsPage is the bounded, paginated row-result read used by the
// report endpoint. limit<=0 means "no limit" (used internally by
// Execute, which must see every eligible row).
func (r *Repository) ListRowsPage(ctx context.Context, jobID uint, statuses []RowStatus, limit, offset int) ([]Row, int, error) {
	rows, err := r.listRowsPage(ctx, jobID, statuses, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var total int
	q := `SELECT COUNT(*) FROM platform_bulk_import_rows WHERE job_id=` + r.dialect.Placeholder(1)
	args := []any{jobID}
	if len(statuses) > 0 {
		ph := ""
		for i, s := range statuses {
			if i > 0 {
				ph += ","
			}
			ph += r.dialect.Placeholder(len(args) + 1)
			args = append(args, s)
		}
		q += " AND status IN (" + ph + ")"
	}
	if err := r.db.QueryRowContext(ctx, q, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *Repository) listRowsPage(ctx context.Context, jobID uint, statuses []RowStatus, limit, offset int) ([]Row, error) {
	q := `SELECT id, job_id, row_number, external_ref, email, name, quota_mb, access_mode, status, error_code, error_detail, mailbox_id, created_at, updated_at FROM platform_bulk_import_rows WHERE job_id=` + r.dialect.Placeholder(1)
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
	if limit > 0 {
		q += " LIMIT " + r.dialect.Placeholder(len(args)+1)
		args = append(args, limit)
		q += " OFFSET " + r.dialect.Placeholder(len(args)+1)
		args = append(args, offset)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.ID, &row.JobID, &row.RowNumber, &row.ExternalRef, &row.Email, &row.Name, &row.QuotaMB, &row.AccessMode, &row.Status, &row.ErrorCode, &row.ErrorDetail, &row.MailboxID, &row.CreatedAt, &row.UpdatedAt); err != nil {
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

// CheckpointBatch advances the durable execution checkpoint after one
// bounded batch: it records the running created/failed/skipped totals
// and the next unattempted row number in a single guarded UPDATE keyed
// on the job's CURRENT version, so a stale worker whose lease was lost
// and reclaimed by another worker cannot silently overwrite a newer
// checkpoint (its UPDATE affects zero rows and the caller must stop).
func (r *Repository) CheckpointBatch(ctx context.Context, id uint, expectedVersion int, createdCount, failedCount, skippedCount, nextRowNumber int, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_bulk_import_jobs SET created_count=`+r.dialect.Placeholder(1)+`, failed_count=`+r.dialect.Placeholder(2)+`, skipped_count=`+r.dialect.Placeholder(3)+`, next_row_number=`+r.dialect.Placeholder(4)+`, version=version+1, updated_at=`+r.dialect.Placeholder(5)+`
		 WHERE id=`+r.dialect.Placeholder(6)+` AND version=`+r.dialect.Placeholder(7),
		createdCount, failedCount, skippedCount, nextRowNumber, now, id, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *Repository) UpdateJobCounters(ctx context.Context, id uint, status JobStatus, createdCount, failedCount, skippedCount int, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE platform_bulk_import_jobs SET status=`+r.dialect.Placeholder(1)+`, created_count=`+r.dialect.Placeholder(2)+`, failed_count=`+r.dialect.Placeholder(3)+`, skipped_count=`+r.dialect.Placeholder(4)+`, version=version+1, updated_at=`+r.dialect.Placeholder(5)+` WHERE id=`+r.dialect.Placeholder(6),
		status, createdCount, failedCount, skippedCount, now, id)
	return err
}

// UpdateJobCountersTx is UpdateJobCounters run inside the caller's
// transaction, so the terminal job-status write and its lifecycle
// outbox/audit evidence commit or roll back together — never one
// without the other.
func (r *Repository) UpdateJobCountersTx(ctx context.Context, tx *sql.Tx, id uint, status JobStatus, createdCount, failedCount, skippedCount int, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE platform_bulk_import_jobs SET status=`+r.dialect.Placeholder(1)+`, created_count=`+r.dialect.Placeholder(2)+`, failed_count=`+r.dialect.Placeholder(3)+`, skipped_count=`+r.dialect.Placeholder(4)+`, version=version+1, updated_at=`+r.dialect.Placeholder(5)+` WHERE id=`+r.dialect.Placeholder(6),
		status, createdCount, failedCount, skippedCount, now, id)
	return err
}

// TransitionJobIfVersionTx is TransitionJobIfVersion run inside the
// caller's transaction (used by Cancel so the state transition and its
// lifecycle outbox/audit evidence are coherent).
func (r *Repository) TransitionJobIfVersionTx(ctx context.Context, tx *sql.Tx, id uint, expected, next JobStatus, expectedVersion int, now time.Time) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE platform_bulk_import_jobs SET status=`+r.dialect.Placeholder(1)+`, version=version+1, updated_at=`+r.dialect.Placeholder(2)+`
		 WHERE id=`+r.dialect.Placeholder(3)+` AND status=`+r.dialect.Placeholder(4)+` AND version=`+r.dialect.Placeholder(5),
		next, now, id, expected, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.TenantID, &j.DomainID, &j.Status, &j.Strategy, &j.ConflictPolicy, &j.IdempotencyKey, &j.SourceHash, &j.SchemaVersion, &j.TotalRows, &j.ValidRows, &j.InvalidRows, &j.CreatedCount, &j.FailedCount, &j.SkippedCount, &j.NextRowNumber, &j.Version, &j.CreatedBy, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}
