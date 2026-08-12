package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

const jobColumns = `id,tenant_id,scope,actor,type,payload_version,payload,status,progress,attempt_count,max_attempts,run_after,lease_owner,lease_token,lease_version,lease_expires_at,heartbeat_at,cancellation_requested_at,created_at,started_at,completed_at,cancelled_at,result,error_code,error_message,idempotency_key,idempotency_scope,request_hash,manual_retry_key,correlation_id,version,updated_at`

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewJobRepository(db *sql.DB) *Repository {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: dialect}
}

func (r *Repository) q(query string) string { return r.dialect.Rewrite(query) }

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts, auto := r.dialect.TimestampType(), r.dialect.AutoIncrement()
	ddl := `CREATE TABLE IF NOT EXISTS platform_jobs (
		id ` + auto + `, tenant_id INTEGER NOT NULL DEFAULT 0, scope TEXT NOT NULL DEFAULT 'tenant',
		actor TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, payload_version INTEGER NOT NULL DEFAULT 1,
		payload TEXT NOT NULL DEFAULT '{}', status TEXT NOT NULL DEFAULT 'queued', progress INTEGER NOT NULL DEFAULT 0,
		attempt_count INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 3, run_after ` + ts + ` NOT NULL,
		lease_owner TEXT NOT NULL DEFAULT '', lease_token TEXT NOT NULL DEFAULT '', lease_version INTEGER NOT NULL DEFAULT 0,
		lease_expires_at ` + ts + `, heartbeat_at ` + ts + `, cancellation_requested_at ` + ts + `,
		created_at ` + ts + ` NOT NULL, started_at ` + ts + `, completed_at ` + ts + `, cancelled_at ` + ts + `,
		result TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL DEFAULT '', idempotency_scope TEXT NOT NULL DEFAULT '', request_hash TEXT NOT NULL DEFAULT '',
		manual_retry_key TEXT NOT NULL DEFAULT '', correlation_id TEXT NOT NULL DEFAULT '', version INTEGER NOT NULL DEFAULT 1,
		updated_at ` + ts + ` NOT NULL)`
	if _, err := r.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ensure automation jobs table: %w", err)
	}
	columns := []struct{ name, definition string }{
		{"scope", "TEXT NOT NULL DEFAULT 'tenant'"}, {"actor", "TEXT NOT NULL DEFAULT ''"},
		{"payload_version", "INTEGER NOT NULL DEFAULT 1"}, {"run_after", ts},
		{"lease_owner", "TEXT NOT NULL DEFAULT ''"}, {"lease_token", "TEXT NOT NULL DEFAULT ''"},
		{"lease_version", "INTEGER NOT NULL DEFAULT 0"}, {"lease_expires_at", ts}, {"heartbeat_at", ts},
		{"cancellation_requested_at", ts}, {"cancelled_at", ts}, {"error_code", "TEXT NOT NULL DEFAULT ''"},
		{"error_message", "TEXT NOT NULL DEFAULT ''"}, {"idempotency_scope", "TEXT NOT NULL DEFAULT ''"},
		{"request_hash", "TEXT NOT NULL DEFAULT ''"}, {"manual_retry_key", "TEXT NOT NULL DEFAULT ''"},
		{"correlation_id", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		if _, err := r.ensureColumn(ctx, column.name, column.definition); err != nil {
			return err
		}
	}
	// Legacy rows used next_run_at and worker_leased_to. Preserve their scheduling data.
	if exists, _ := r.columnExists(ctx, "next_run_at"); exists {
		_, _ = r.db.ExecContext(ctx, `UPDATE platform_jobs SET run_after=COALESCE(run_after,next_run_at,created_at) WHERE run_after IS NULL`)
	} else {
		_, _ = r.db.ExecContext(ctx, `UPDATE platform_jobs SET run_after=COALESCE(run_after,created_at) WHERE run_after IS NULL`)
	}
	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_jobs_idempotency ON platform_jobs(idempotency_scope,idempotency_key) WHERE idempotency_key<>''`,
		`CREATE INDEX IF NOT EXISTS idx_platform_jobs_claim ON platform_jobs(status,run_after,lease_expires_at,id)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_jobs_tenant ON platform_jobs(tenant_id,status,created_at)`,
	}
	for _, statement := range indexes {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure automation jobs index: %w", err)
		}
	}
	return nil
}

func (r *Repository) columnExists(ctx context.Context, column string) (bool, error) {
	if r.dialect.IsPostgres() {
		var exists bool
		err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='platform_jobs' AND column_name=$1)`, column).Scan(&exists)
		return exists, err
	}
	rows, err := r.db.QueryContext(ctx, `PRAGMA table_info(platform_jobs)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (r *Repository) ensureColumn(ctx context.Context, name, definition string) (bool, error) {
	exists, err := r.columnExists(ctx, name)
	if err != nil || exists {
		return false, err
	}
	if _, err := r.db.ExecContext(ctx, `ALTER TABLE platform_jobs ADD COLUMN `+name+` `+definition); err != nil {
		return false, fmt.Errorf("add automation jobs column %s: %w", name, err)
	}
	return true, nil
}

func (r *Repository) SubmitIdempotent(ctx context.Context, submission Submission, requestHash, idempotencyScope string, now time.Time) (*Job, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	runAfter := submission.RunAfter.UTC()
	if runAfter.IsZero() {
		runAfter = now
	}
	maxAttempts := submission.MaxAttempts
	if maxAttempts <= 0 || maxAttempts > 20 {
		maxAttempts = 3
	}
	query := r.q(`INSERT INTO platform_jobs (tenant_id,scope,actor,type,payload_version,payload,status,progress,attempt_count,max_attempts,run_after,lease_owner,lease_token,lease_version,created_at,result,error_code,error_message,idempotency_key,idempotency_scope,request_hash,manual_retry_key,correlation_id,version,updated_at) VALUES (?,?,?,?,?,?,?,0,0,?,?,?,?,0,?,'','','',?,?,?,'',?,1,?) ON CONFLICT(idempotency_scope,idempotency_key) WHERE idempotency_key<>'' DO NOTHING`)
	args := []any{submission.TenantID, submission.Scope, submission.Actor, submission.Type, submission.PayloadVersion, string(submission.Payload), StatusQueued, maxAttempts, runAfter, "", "", now, submission.IdempotencyKey, idempotencyScope, requestHash, submission.CorrelationID, now}
	var id uint
	if r.dialect.IsPostgres() {
		err = tx.QueryRowContext(ctx, query+` RETURNING id`, args...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, query, args...)
		if err == nil {
			if n, _ := result.RowsAffected(); n > 0 {
				inserted, idErr := result.LastInsertId()
				err, id = idErr, uint(inserted)
			}
		}
	}
	if err != nil {
		return nil, false, err
	}
	if id == 0 {
		var existingHash string
		err = tx.QueryRowContext(ctx, r.q(`SELECT id,request_hash FROM platform_jobs WHERE idempotency_scope=? AND idempotency_key=?`), idempotencyScope, submission.IdempotencyKey).Scan(&id, &existingHash)
		if err != nil {
			return nil, false, err
		}
		if existingHash != requestHash {
			return nil, false, ErrIdempotencyReuse
		}
		job, err := r.getWith(ctx, tx, id, submission.TenantID, submission.Scope)
		if err != nil {
			return nil, false, err
		}
		if err = tx.Commit(); err != nil {
			return nil, false, err
		}
		return job, true, nil
	}
	job, err := r.getWith(ctx, tx, id, submission.TenantID, submission.Scope)
	if err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, false, nil
}

type dbQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *Repository) getWith(ctx context.Context, q dbQuerier, id, tenantID uint, scope Scope) (*Job, error) {
	query := `SELECT ` + jobColumns + ` FROM platform_jobs WHERE id=?`
	args := []any{id}
	if scope == ScopeTenant {
		query += ` AND tenant_id=? AND scope='tenant'`
		args = append(args, tenantID)
	} else if scope == ScopePlatform {
		query += ` AND scope='platform'`
	}
	job, err := scanJob(q.QueryRowContext(ctx, r.q(query), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return job, err
}

func (r *Repository) Get(ctx context.Context, id uint) (*Job, error) {
	return r.getWith(ctx, r.db, id, 0, "")
}

func (r *Repository) GetForScope(ctx context.Context, id, tenantID uint, scope Scope) (*Job, error) {
	return r.getWith(ctx, r.db, id, tenantID, scope)
}

func scanJob(row *sql.Row) (*Job, error) {
	var job Job
	var payload, result string
	var leaseExpiry, heartbeat, cancelAsked, started, completed, cancelled sql.NullTime
	err := row.Scan(&job.ID, &job.TenantID, &job.Scope, &job.Actor, &job.Type, &job.PayloadVersion, &payload, &job.Status, &job.Progress, &job.Attempt, &job.MaxAttempts, &job.RunAfter, &job.LeaseOwner, &job.LeaseToken, &job.LeaseVersion, &leaseExpiry, &heartbeat, &cancelAsked, &job.CreatedAt, &started, &completed, &cancelled, &result, &job.ErrorCode, &job.ErrorMessage, &job.IdempotencyKey, &job.IdempotencyScope, &job.RequestHash, &job.ManualRetryKey, &job.CorrelationID, &job.Version, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	job.Payload = json.RawMessage(payload)
	if result != "" {
		job.Result = json.RawMessage(result)
	}
	assignTime := func(value sql.NullTime, target **time.Time) {
		if value.Valid {
			t := value.Time.UTC()
			*target = &t
		}
	}
	assignTime(leaseExpiry, &job.LeaseExpiresAt)
	assignTime(heartbeat, &job.HeartbeatAt)
	assignTime(cancelAsked, &job.CancellationAskedAt)
	assignTime(started, &job.StartedAt)
	assignTime(completed, &job.CompletedAt)
	assignTime(cancelled, &job.CancelledAt)
	return &job, nil
}

func (r *Repository) List(ctx context.Context, filter ListFilter) (kernel.PageResponse[Job], error) {
	page := filter.Page.Normalize()
	where, args := []string{"1=1"}, []any{}
	if filter.Scope == ScopeTenant {
		where = append(where, "tenant_id=?", "scope='tenant'")
		args = append(args, filter.TenantID)
	} else if filter.Scope == ScopePlatform {
		where = append(where, "scope='platform'")
	}
	if filter.Status != "" {
		where = append(where, "status=?")
		args = append(args, filter.Status)
	}
	if filter.Type != "" {
		where = append(where, "type=?")
		args = append(args, filter.Type)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM platform_jobs WHERE `+whereSQL), args...).Scan(&total); err != nil {
		return kernel.PageResponse[Job]{}, err
	}
	queryArgs := append(append([]any{}, args...), page.Limit(), page.Offset())
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT `+jobColumns+` FROM platform_jobs WHERE `+whereSQL+` ORDER BY id DESC LIMIT ? OFFSET ?`), queryArgs...)
	if err != nil {
		return kernel.PageResponse[Job]{}, err
	}
	defer rows.Close()
	items := []Job{}
	for rows.Next() {
		job, err := scanJobRows(rows)
		if err != nil {
			return kernel.PageResponse[Job]{}, err
		}
		items = append(items, *job)
	}
	if err := rows.Err(); err != nil {
		return kernel.PageResponse[Job]{}, err
	}
	return kernel.NewPageResponse(items, page, total), nil
}

func scanJobRows(rows *sql.Rows) (*Job, error) {
	var job Job
	var payload, result string
	var leaseExpiry, heartbeat, cancelAsked, started, completed, cancelled sql.NullTime
	err := rows.Scan(&job.ID, &job.TenantID, &job.Scope, &job.Actor, &job.Type, &job.PayloadVersion, &payload, &job.Status, &job.Progress, &job.Attempt, &job.MaxAttempts, &job.RunAfter, &job.LeaseOwner, &job.LeaseToken, &job.LeaseVersion, &leaseExpiry, &heartbeat, &cancelAsked, &job.CreatedAt, &started, &completed, &cancelled, &result, &job.ErrorCode, &job.ErrorMessage, &job.IdempotencyKey, &job.IdempotencyScope, &job.RequestHash, &job.ManualRetryKey, &job.CorrelationID, &job.Version, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	job.Payload = json.RawMessage(payload)
	if result != "" {
		job.Result = json.RawMessage(result)
	}
	assign := func(value sql.NullTime, target **time.Time) {
		if value.Valid {
			t := value.Time.UTC()
			*target = &t
		}
	}
	assign(leaseExpiry, &job.LeaseExpiresAt)
	assign(heartbeat, &job.HeartbeatAt)
	assign(cancelAsked, &job.CancellationAskedAt)
	assign(started, &job.StartedAt)
	assign(completed, &job.CompletedAt)
	assign(cancelled, &job.CancelledAt)
	return &job, nil
}

func (r *Repository) ClaimOne(ctx context.Context, owner, token string, now time.Time, leaseDuration time.Duration) (*Job, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(token) == "" {
		return nil, kernel.ValidationError(map[string]string{"worker": "lease owner and token are required"})
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := r.q(`SELECT id,version FROM platform_jobs WHERE status='queued' AND run_after<=? AND cancellation_requested_at IS NULL ORDER BY run_after,id LIMIT 1`)
	if r.dialect.IsPostgres() {
		query += ` FOR UPDATE SKIP LOCKED`
	}
	var id uint
	var version int
	if err = tx.QueryRowContext(ctx, query, now).Scan(&id, &version); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	leaseExpiry := now.Add(leaseDuration)
	res, err := tx.ExecContext(ctx, r.q(`UPDATE platform_jobs SET status='running',lease_owner=?,lease_token=?,lease_version=lease_version+1,lease_expires_at=?,heartbeat_at=?,attempt_count=attempt_count+1,started_at=COALESCE(started_at,?),version=version+1,updated_at=? WHERE id=? AND version=? AND status='queued' AND cancellation_requested_at IS NULL`), owner, token, leaseExpiry, now, now, now, id, version)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
	job, err := r.getWith(ctx, tx, id, 0, "")
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *Repository) Heartbeat(ctx context.Context, lease Lease, now time.Time, extension time.Duration) error {
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_jobs SET heartbeat_at=?,lease_expires_at=?,version=version+1,updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_token=? AND lease_version=? AND cancellation_requested_at IS NULL`), now, now.Add(extension), now, lease.JobID, lease.Owner, lease.Token, lease.LeaseVersion)
	return r.requireLeaseResult(ctx, lease, res, err)
}

// Activate makes a previously-held queued durable job claimable now by
// moving its run_after into the past. Held jobs (submitted with a far-future
// run_after) are never picked up by ClaimOne until this is called, which is
// the queued-activation handoff the importer uses so a worker can never
// claim a job whose import is not already linked and running. It is
// idempotent: activating an already-claimable queued job succeeds.
func (r *Repository) Activate(ctx context.Context, id, tenantID uint, scope Scope, now time.Time) error {
	query := `UPDATE platform_jobs SET run_after=?,updated_at=? WHERE id=? AND status='queued' AND run_after > ?`
	args := []any{now, now, id, now}
	if scope == ScopeTenant {
		query += ` AND tenant_id=? AND scope='tenant'`
		args = append(args, tenantID)
	} else {
		query += ` AND scope='platform'`
	}
	res, err := r.db.ExecContext(ctx, r.q(query), args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	// Already claimable (run_after <= now) or terminal — idempotent success.
	// Just confirm the job still exists in the given scope.
	_, getErr := r.GetForScope(ctx, id, tenantID, scope)
	return getErr
}

func (r *Repository) UpdateProgress(ctx context.Context, lease Lease, progress int, now time.Time) error {
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_jobs SET progress=?,version=version+1,updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_token=? AND lease_version=? AND cancellation_requested_at IS NULL`), progress, now, lease.JobID, lease.Owner, lease.Token, lease.LeaseVersion)
	return r.requireLeaseResult(ctx, lease, res, err)
}

func (r *Repository) requireLeaseResult(ctx context.Context, lease Lease, result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		return nil
	}
	job, getErr := r.Get(ctx, lease.JobID)
	if getErr != nil {
		return getErr
	}
	if job.CancellationAskedAt != nil {
		return ErrCancellationAsked
	}
	return ErrLeaseLost
}

func (r *Repository) Complete(ctx context.Context, lease Lease, result json.RawMessage, now time.Time) error {
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_jobs SET status='succeeded',progress=100,result=?,error_code='',error_message='',completed_at=?,lease_owner='',lease_token='',lease_expires_at=NULL,heartbeat_at=?,version=version+1,updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_token=? AND lease_version=? AND cancellation_requested_at IS NULL`), string(result), now, now, now, lease.JobID, lease.Owner, lease.Token, lease.LeaseVersion)
	return r.requireLeaseResult(ctx, lease, res, err)
}

func (r *Repository) Fail(ctx context.Context, lease Lease, code, message string, retryable bool, now time.Time) error {
	job, err := r.Get(ctx, lease.JobID)
	if err != nil {
		return err
	}
	status, runAfter, completed := StatusFailed, now, any(now)
	if retryable && job.Attempt < job.MaxAttempts {
		status, runAfter, completed = StatusQueued, now.Add(backoff(job.Attempt)), nil
	}
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_jobs SET status=?,run_after=?,error_code=?,error_message=?,completed_at=?,lease_owner='',lease_token='',lease_expires_at=NULL,heartbeat_at=?,version=version+1,updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_token=? AND lease_version=? AND cancellation_requested_at IS NULL`), status, runAfter, code, message, completed, now, now, lease.JobID, lease.Owner, lease.Token, lease.LeaseVersion)
	return r.requireLeaseResult(ctx, lease, res, err)
}

func (r *Repository) RequestCancellation(ctx context.Context, id, tenantID uint, scope Scope, now time.Time) (*Job, error) {
	query := `UPDATE platform_jobs SET status=CASE WHEN status='queued' THEN 'cancelled' ELSE status END,cancellation_requested_at=?,cancelled_at=CASE WHEN status='queued' THEN ? ELSE cancelled_at END,completed_at=CASE WHEN status='queued' THEN ? ELSE completed_at END,version=version+1,updated_at=? WHERE id=? AND status IN ('queued','running')`
	args := []any{now, now, now, now, id}
	if scope == ScopeTenant {
		query += ` AND tenant_id=? AND scope='tenant'`
		args = append(args, tenantID)
	} else {
		query += ` AND scope='platform'`
	}
	res, err := r.db.ExecContext(ctx, r.q(query), args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		job, getErr := r.GetForScope(ctx, id, tenantID, scope)
		if getErr != nil {
			return nil, getErr
		}
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusCancelled))
	}
	return r.GetForScope(ctx, id, tenantID, scope)
}

func (r *Repository) CancellationRequested(ctx context.Context, lease Lease) (bool, error) {
	var requested sql.NullTime
	err := r.db.QueryRowContext(ctx, r.q(`SELECT cancellation_requested_at FROM platform_jobs WHERE id=? AND status='running' AND lease_owner=? AND lease_token=? AND lease_version=?`), lease.JobID, lease.Owner, lease.Token, lease.LeaseVersion).Scan(&requested)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrLeaseLost
	}
	return requested.Valid, err
}

func (r *Repository) FinishCancellation(ctx context.Context, lease Lease, now time.Time) error {
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_jobs SET status='cancelled',cancelled_at=?,completed_at=?,lease_owner='',lease_token='',lease_expires_at=NULL,version=version+1,updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_token=? AND lease_version=? AND cancellation_requested_at IS NOT NULL`), now, now, now, lease.JobID, lease.Owner, lease.Token, lease.LeaseVersion)
	return r.requireLeaseResult(ctx, lease, res, err)
}

func (r *Repository) RecoverExpired(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT id,lease_version,attempt_count,max_attempts FROM platform_jobs WHERE status='running' AND lease_expires_at<? ORDER BY lease_expires_at,id LIMIT ?`), now, limit)
	if err != nil {
		return 0, err
	}
	type expired struct {
		id                         uint
		leaseVersion, attempt, max int
	}
	var jobs []expired
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.leaseVersion, &item.attempt, &item.max); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, item)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	recovered := 0
	for _, item := range jobs {
		status, runAfter, completed := StatusFailed, now, any(now)
		if item.attempt < item.max {
			status, runAfter, completed = StatusQueued, now.Add(backoff(item.attempt)), nil
		}
		res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_jobs SET status=?,run_after=?,error_code='LEASE_EXPIRED',error_message='worker lease expired',completed_at=?,lease_owner='',lease_token='',lease_expires_at=NULL,lease_version=lease_version+1,version=version+1,updated_at=? WHERE id=? AND status='running' AND lease_version=? AND lease_expires_at<?`), status, runAfter, completed, now, item.id, item.leaseVersion, now)
		if err != nil {
			return recovered, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			recovered++
		}
	}
	return recovered, nil
}

func (r *Repository) ManualRetry(ctx context.Context, id, tenantID uint, scope Scope, key string, now time.Time) (*Job, bool, error) {
	job, err := r.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, false, err
	}
	if job.Status == StatusQueued && job.ManualRetryKey == key {
		return job, true, nil
	}
	if job.Status != StatusFailed {
		return nil, false, kernel.InvalidStateTransition(string(job.Status), string(StatusQueued))
	}
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_jobs SET status='queued',run_after=?,completed_at=NULL,error_code='',error_message='',manual_retry_key=?,version=version+1,updated_at=? WHERE id=? AND status='failed' AND version=?`), now, key, now, id, job.Version)
	if err != nil {
		return nil, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		current, getErr := r.GetForScope(ctx, id, tenantID, scope)
		if getErr == nil && current.Status == StatusQueued && current.ManualRetryKey == key {
			return current, true, nil
		}
		return nil, false, kernel.NewError(kernel.ErrCodePreconditionFail, "automation job changed concurrently")
	}
	updated, err := r.GetForScope(ctx, id, tenantID, scope)
	return updated, false, err
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(1<<min(attempt, 8)) * time.Second
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}
