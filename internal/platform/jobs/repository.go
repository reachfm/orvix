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
