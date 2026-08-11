package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info

	// failMarkRunningAndLink is an unexported test-only failpoint used by
	// the submission/link failure-injection tests.
	failMarkRunningAndLink func() error

	// failIdempotencyComplete is an unexported test-only failpoint used to
	// prove idempotency abandonment on final-response persistence failure.
	failIdempotencyComplete func() error
}

func NewRepository(db *sql.DB) *Repository {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: dialect}
}

// SetTestFailpoint installs the MarkRunningAndLink failure-injection hook.
// Intended only for tests.
func (r *Repository) SetTestFailpoint(fn func() error) {
	r.failMarkRunningAndLink = fn
}

// SetIdempotencyCompleteFailpoint installs a failure-injection hook for
// IdempotencyComplete. Intended only for tests.
func (r *Repository) SetIdempotencyCompleteFailpoint(fn func() error) {
	r.failIdempotencyComplete = fn
}

func (r *Repository) q(query string) string { return r.dialect.Rewrite(query) }

// ── EntityLookup (used by the planner/validator dry-run) ──────────────

func (r *Repository) OrgExists(ctx context.Context, domain string, tenantID uint) (bool, error) {
	var c int
	err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM tenants WHERE domain=? AND deleted_at IS NULL`), domain).Scan(&c)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func (r *Repository) UserExists(ctx context.Context, email string) (bool, error) {
	var c int
	err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM users WHERE email=? AND deleted_at IS NULL`), email).Scan(&c)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func (r *Repository) DomainExists(ctx context.Context, name string) (bool, uint, error) {
	var id, tenantID uint
	err := r.db.QueryRowContext(ctx, r.q(`SELECT id, tenant_id FROM coremail_domains WHERE name=? AND deleted_at IS NULL AND status='active'`), name).Scan(&id, &tenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, tenantID, nil
}

func (r *Repository) MailboxExists(ctx context.Context, email string) (bool, error) {
	var c int
	err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM coremail_mailboxes WHERE email=? AND deleted_at IS NULL`), email).Scan(&c)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

const importColumns = `id,tenant_id,scope,actor,source_type,conflict_policy,schema_version,status,source_hash,source_name,staging_id,stored_size,total_rows,processed_rows,succeeded_rows,skipped_rows,failed_rows,current_checkpoint,checkpoint_entity,checkpoint_row,last_error,job_id,validation_report,created_at,updated_at,version`

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts, auto := r.dialect.TimestampType(), r.dialect.AutoIncrement()
	ddl := `CREATE TABLE IF NOT EXISTS platform_imports (
		id ` + auto + `, tenant_id INTEGER NOT NULL DEFAULT 0, scope TEXT NOT NULL DEFAULT 'tenant',
		actor TEXT NOT NULL DEFAULT '', source_type TEXT NOT NULL, conflict_policy TEXT NOT NULL DEFAULT 'fail',
		schema_version INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'uploaded',
		source_hash TEXT NOT NULL DEFAULT '', source_name TEXT NOT NULL DEFAULT '',
		staging_id TEXT NOT NULL DEFAULT '', stored_size BIGINT NOT NULL DEFAULT 0,
		total_rows INTEGER NOT NULL DEFAULT 0, processed_rows INTEGER NOT NULL DEFAULT 0,
		succeeded_rows INTEGER NOT NULL DEFAULT 0, skipped_rows INTEGER NOT NULL DEFAULT 0,
		failed_rows INTEGER NOT NULL DEFAULT 0, current_checkpoint INTEGER NOT NULL DEFAULT 0,
		checkpoint_entity TEXT NOT NULL DEFAULT '', checkpoint_row INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '', job_id INTEGER NOT NULL DEFAULT 0,
		validation_report TEXT NOT NULL DEFAULT '', created_at ` + ts + ` NOT NULL,
		updated_at ` + ts + ` NOT NULL, version INTEGER NOT NULL DEFAULT 1)`
	if _, err := r.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ensure platform_imports table: %w", err)
	}
	// checkpoint table
	cddl := `CREATE TABLE IF NOT EXISTS platform_import_checkpoints (
		id ` + auto + `, import_id INTEGER NOT NULL, entity TEXT NOT NULL,
		row_index INTEGER NOT NULL, succeeded_ids TEXT NOT NULL DEFAULT '[]',
		failed_rows TEXT NOT NULL DEFAULT '[]', processed_count INTEGER NOT NULL DEFAULT 0,
		committed_at ` + ts + ` NOT NULL)`
	if _, err := r.db.ExecContext(ctx, cddl); err != nil {
		return fmt.Errorf("ensure platform_import_checkpoints table: %w", err)
	}
	// compensation table
	comddl := `CREATE TABLE IF NOT EXISTS platform_import_compensations (
		id ` + auto + `, import_id INTEGER NOT NULL, resource_id INTEGER NOT NULL,
		entity_type TEXT NOT NULL, row_key TEXT NOT NULL DEFAULT '', row_index INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'pending', compensated_at ` + ts + `,
		error TEXT NOT NULL DEFAULT '', created_at ` + ts + ` NOT NULL)`
	if _, err := r.db.ExecContext(ctx, comddl); err != nil {
		return fmt.Errorf("ensure platform_import_compensations table: %w", err)
	}
	// idempotency table for execute/resume/compensate actions
	iddl := `CREATE TABLE IF NOT EXISTS platform_import_idempotency (
		id ` + auto + `, scope TEXT NOT NULL, actor TEXT NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 0, import_id INTEGER NOT NULL DEFAULT 0,
		idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0, response_body TEXT NOT NULL DEFAULT '',
		created_at ` + ts + ` NOT NULL, completed_at ` + ts + `)`
	if _, err := r.db.ExecContext(ctx, iddl); err != nil {
		return fmt.Errorf("ensure platform_import_idempotency table: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_import_idempotency_key ON platform_import_idempotency(scope,actor,tenant_id,idempotency_key)`); err != nil {
		return fmt.Errorf("ensure import idempotency index: %w", err)
	}
	// pending cleanup table: recoverable staged-file cleanup records so a
	// file that could not be removed immediately is retried, never silently
	// orphaned.
	pclddl := `CREATE TABLE IF NOT EXISTS platform_import_cleanup (
		id ` + auto + `, import_id INTEGER NOT NULL DEFAULT 0, staging_id TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '', created_at ` + ts + ` NOT NULL,
		updated_at ` + ts + ` NOT NULL, resolved_at ` + ts + `)`
	if _, err := r.db.ExecContext(ctx, pclddl); err != nil {
		return fmt.Errorf("ensure platform_import_cleanup table: %w", err)
	}
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_platform_imports_tenant ON platform_imports(tenant_id,scope,status)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_imports_hash ON platform_imports(source_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_import_checkpoints_import ON platform_import_checkpoints(import_id)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_import_cmp_import ON platform_import_compensations(import_id)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_import_cleanup_unresolved ON platform_import_cleanup(import_id) WHERE resolved_at IS NULL`,
	}
	for _, idx := range indexes {
		if _, err := r.db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("ensure import index: %w", err)
		}
	}
	return nil
}

func (r *Repository) Create(ctx context.Context, job *ImportJob) error {
	query := r.q(`INSERT INTO platform_imports (tenant_id,scope,actor,source_type,conflict_policy,schema_version,status,source_hash,source_name,staging_id,stored_size,total_rows,processed_rows,succeeded_rows,skipped_rows,failed_rows,current_checkpoint,checkpoint_entity,checkpoint_row,last_error,job_id,validation_report,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	args := []any{
		job.TenantID, job.Scope, job.Actor, job.SourceType, job.ConflictPolicy,
		job.SchemaVersion, job.Status, job.SourceHash, job.SourceName,
		job.StagingID, job.StoredSize,
		job.TotalRows, job.ProcessedRows, job.SucceededRows, job.SkippedRows, job.FailedRows,
		job.CurrentCheckpoint, job.CheckpointEntity, job.CheckpointRow,
		job.LastError, job.JobID, job.ValidationReportRaw,
		job.CreatedAt, job.UpdatedAt, job.Version,
	}
	if r.dialect.IsPostgres() {
		return r.db.QueryRowContext(ctx, query+` RETURNING id`, args...).Scan(&job.ID)
	}
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	job.ID = uint(id)
	return nil
}

func (r *Repository) Get(ctx context.Context, id uint) (*ImportJob, error) {
	row := r.db.QueryRowContext(ctx, r.q(`SELECT `+importColumns+` FROM platform_imports WHERE id=?`), id)
	return scanImportJob(row)
}

func (r *Repository) GetForScope(ctx context.Context, id, tenantID uint, scope string) (*ImportJob, error) {
	query := `SELECT ` + importColumns + ` FROM platform_imports WHERE id=?`
	args := []any{id}
	if scope == "tenant" {
		query += ` AND tenant_id=? AND scope='tenant'`
		args = append(args, tenantID)
	} else if scope == "platform" {
		query += ` AND scope='platform'`
	}
	job, err := scanImportJob(r.db.QueryRowContext(ctx, r.q(query), args...))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return job, nil
}

func (r *Repository) List(ctx context.Context, filter ImportFilter) ([]ImportJob, int, error) {
	page := filter.Page.Normalize()
	where, args := []string{"1=1"}, []any{}
	if filter.Scope == "tenant" {
		where = append(where, "tenant_id=?", "scope='tenant'")
		args = append(args, filter.TenantID)
	} else if filter.Scope == "platform" {
		where = append(where, "scope='platform'")
	}
	if filter.Status != "" {
		where = append(where, "status=?")
		args = append(args, filter.Status)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM platform_imports WHERE `+whereSQL), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	qArgs := append(append([]any{}, args...), page.Limit(), page.Offset())
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT `+importColumns+` FROM platform_imports WHERE `+whereSQL+` ORDER BY id DESC LIMIT ? OFFSET ?`), qArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var items []ImportJob
	for rows.Next() {
		job, err := scanImportRows(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *job)
	}
	return items, total, rows.Err()
}

func (r *Repository) UpdateStatus(ctx context.Context, id uint, fromStatus, toStatus ImportStatus, version int) error {
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_imports SET status=?,version=version+1,updated_at=? WHERE id=? AND status=? AND version=?`), toStatus, time.Now().UTC(), id, fromStatus, version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return kernel.InvalidStateTransition(string(fromStatus), string(toStatus))
	}
	return nil
}

// MarkRunningAndLink atomically transitions the import to running and
// records the durable job ID in a single transaction, so a worker can never
// observe a running import without its durable job relationship. It is
// idempotent: if the import is already running and linked to the same
// durable job, it returns success so an idempotent retry recovers the same
// job instead of creating a duplicate.
func (r *Repository) MarkRunningAndLink(ctx context.Context, id uint, fromStatus ImportStatus, version int, jobID uint, now time.Time) error {
	if r.failMarkRunningAndLink != nil {
		if err := r.failMarkRunningAndLink(); err != nil {
			return err
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, r.q(`UPDATE platform_imports SET status=?,job_id=?,version=version+1,updated_at=? WHERE id=? AND status=? AND version=?`),
		StatusRunning, jobID, now, id, fromStatus, version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Idempotent retry: already running and linked to the same job?
		var currentStatus ImportStatus
		var currentJobID uint
		if err := tx.QueryRowContext(ctx, r.q(`SELECT status,job_id FROM platform_imports WHERE id=?`), id).Scan(&currentStatus, &currentJobID); err != nil {
			return err
		}
		if currentStatus == StatusRunning && currentJobID == jobID {
			if err := tx.Commit(); err != nil {
				return err
			}
			return nil
		}
		return kernel.InvalidStateTransition(string(fromStatus), string(StatusRunning))
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (r *Repository) UpdateProgress(ctx context.Context, id uint, processed, succeeded, skipped, failed int, checkpoint int, entity ImportEntityType, rowIndex int) error {
	_, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_imports SET processed_rows=?,succeeded_rows=?,skipped_rows=?,failed_rows=?,current_checkpoint=?,checkpoint_entity=?,checkpoint_row=?,updated_at=? WHERE id=?`), processed, succeeded, skipped, failed, checkpoint, entity, rowIndex, time.Now().UTC(), id)
	return err
}

func (r *Repository) UpdateStatusAndHash(ctx context.Context, id uint, status ImportStatus, hash string, version int) error {
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_imports SET status=?,source_hash=?,version=version+1,updated_at=? WHERE id=? AND version=?`), status, hash, time.Now().UTC(), id, version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return kernel.NewError(kernel.ErrCodePreconditionFail, "concurrent modification detected")
	}
	return nil
}

func (r *Repository) SaveValidationReport(ctx context.Context, id uint, report *ValidationReport, hash string) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "encode validation report", err)
	}
	_, err = r.db.ExecContext(ctx, r.q(`UPDATE platform_imports SET validation_report=?,source_hash=?,total_rows=?,processed_rows=0,succeeded_rows=0,skipped_rows=0,failed_rows=0,updated_at=? WHERE id=?`), string(raw), hash, report.Total, time.Now().UTC(), id)
	return err
}

func (r *Repository) GetActiveForSource(ctx context.Context, sourceHash string, tenantID uint) (*ImportJob, error) {
	job, err := scanImportJob(r.db.QueryRowContext(ctx, r.q(`SELECT `+importColumns+` FROM platform_imports WHERE source_hash=? AND tenant_id=? AND status NOT IN ('completed','cancelled','compensated','compensation_failed') ORDER BY id DESC LIMIT 1`), sourceHash, tenantID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return job, nil
}

func (r *Repository) SaveCheckpoint(ctx context.Context, cp *Checkpoint) error {
	ids, err := json.Marshal(cp.SucceededIDs)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "encode checkpoint succeeded ids", err)
	}
	failed, err := json.Marshal(cp.FailedRows)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "encode checkpoint failed rows", err)
	}
	_, err = r.db.ExecContext(ctx, r.q(`INSERT INTO platform_import_checkpoints (import_id,entity,row_index,succeeded_ids,failed_rows,processed_count,committed_at) VALUES (?,?,?,?,?,?,?)`), cp.ImportID, cp.Entity, cp.RowIndex, string(ids), string(failed), cp.ProcessedCount, cp.CommittedAt)
	return err
}

func (r *Repository) LastCheckpoint(ctx context.Context, importID uint) (*Checkpoint, error) {
	var cp Checkpoint
	var idsStr, failedStr string
	err := r.db.QueryRowContext(ctx, r.q(`SELECT import_id,entity,row_index,succeeded_ids,failed_rows,processed_count,committed_at FROM platform_import_checkpoints WHERE import_id=? ORDER BY id DESC LIMIT 1`), importID).Scan(&cp.ImportID, &cp.Entity, &cp.RowIndex, &idsStr, &failedStr, &cp.ProcessedCount, &cp.CommittedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(idsStr), &cp.SucceededIDs); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "decode checkpoint succeeded ids", err)
	}
	if err := json.Unmarshal([]byte(failedStr), &cp.FailedRows); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "decode checkpoint failed rows", err)
	}
	return &cp, nil
}

func (r *Repository) SaveCompensationRecord(ctx context.Context, rec *CompensationRecord) error {
	_, err := r.db.ExecContext(ctx, r.q(`INSERT INTO platform_import_compensations (import_id,resource_id,entity_type,row_key,row_index,status,compensated_at,error,created_at) VALUES (?,?,?,?,?,?,?,?,?)`), rec.ImportID, rec.ResourceID, rec.EntityType, rec.RowKey, rec.RowIndex, rec.Status, rec.CompensatedAt, rec.Error, rec.CreatedAt)
	return err
}

func (r *Repository) GetCompensationRecords(ctx context.Context, importID uint) ([]CompensationRecord, error) {
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT import_id,resource_id,entity_type,row_key,row_index,status,compensated_at,error,created_at FROM platform_import_compensations WHERE import_id=? ORDER BY id DESC`), importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []CompensationRecord
	for rows.Next() {
		var rec CompensationRecord
		var compAt sql.NullTime
		if err := rows.Scan(&rec.ImportID, &rec.ResourceID, &rec.EntityType, &rec.RowKey, &rec.RowIndex, &rec.Status, &compAt, &rec.Error, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if compAt.Valid {
			rec.CompensatedAt = &compAt.Time
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (r *Repository) UpdateCompensationStatus(ctx context.Context, importID, resourceID uint, status string, errMsg string) error {
	var compAt *time.Time
	if status == "compensated" {
		now := time.Now().UTC()
		compAt = &now
	}
	_, execErr := r.db.ExecContext(ctx, r.q(`UPDATE platform_import_compensations SET status=?,compensated_at=?,error=? WHERE import_id=? AND resource_id=?`), status, compAt, errMsg, importID, resourceID)
	return execErr
}

// CompensationExistsForRow reports whether a compensation record has already
// been recorded for the given source row index. The executor uses this to
// resume a crashed import without duplicating entities whose creation had
// already persisted a compensation record before the crash.
func (r *Repository) CompensationExistsForRow(ctx context.Context, importID uint, rowIndex int) (bool, error) {
	var c int
	err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM platform_import_compensations WHERE import_id=? AND row_index=?`), importID, rowIndex).Scan(&c)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

// PendingCleanup records a staged file that could not be removed. It is
// persisted so a later reconciliation pass can retry the removal instead of
// silently leaving an orphaned file.
type PendingCleanup struct {
	ID        uint
	ImportID  uint
	StagingID string
	Reason    string
	Attempts  int
	LastError string
	CreatedAt time.Time
}

// RecordPendingCleanup persists a recoverable staged-file cleanup record.
func (r *Repository) RecordPendingCleanup(ctx context.Context, importID uint, stagingID, reason string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, r.q(`INSERT INTO platform_import_cleanup (import_id, staging_id, reason, attempts, last_error, created_at, updated_at) VALUES (?,?,?,0,'',?,?)`),
		importID, stagingID, reason, now, now)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "persist pending staged-file cleanup", err)
	}
	return nil
}

// PendingCleanups returns all unresolved cleanup records for an import.
func (r *Repository) PendingCleanups(ctx context.Context, importID uint) ([]PendingCleanup, error) {
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT id,import_id,staging_id,reason,attempts,last_error,created_at FROM platform_import_cleanup WHERE import_id=? AND resolved_at IS NULL ORDER BY id`), importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingCleanup
	for rows.Next() {
		var pc PendingCleanup
		if err := rows.Scan(&pc.ID, &pc.ImportID, &pc.StagingID, &pc.Reason, &pc.Attempts, &pc.LastError, &pc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pc)
	}
	return out, rows.Err()
}

// BumpCleanupAttempt records a failed removal attempt for a pending cleanup.
func (r *Repository) BumpCleanupAttempt(ctx context.Context, id uint, lastError string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_import_cleanup SET attempts=attempts+1,last_error=?,updated_at=? WHERE id=? AND resolved_at IS NULL`), lastError, now, id)
	return err
}

// ResolveCleanup marks a pending cleanup as resolved.
func (r *Repository) ResolveCleanup(ctx context.Context, id uint, now time.Time) error {
	_, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_import_cleanup SET resolved_at=?,updated_at=? WHERE id=?`), now, now, id)
	return err
}

func (r *Repository) UpdateStagingID(ctx context.Context, id uint, stagingID string) error {
	_, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_imports SET staging_id=?,updated_at=? WHERE id=?`), stagingID, time.Now().UTC(), id)
	return err
}

// IdempotencyBegin registers an attempt to process an idempotent import
// action (execute/resume/compensate). The key is scoped by
// scope|actor|tenantID, and the request hash covers the action, import ID
// and import identity, so:
//   - (nil, false, nil): the key is new and the caller should proceed.
//   - (stored, true, nil): a completed result exists for the same key and
//     request hash; the caller must replay it without re-executing.
//   - (nil, false, err): the key was reused with a different request hash
//     (ErrCodeIdempotencyReuse), the key is currently in flight
//     (ErrIdempotencyInFlight), or a persistence error occurred.
//
// A row that is in flight but older than the stale threshold is treated as
// abandoned (the claiming process crashed before Complete/Abandon) so a
// retry is a fresh attempt rather than a permanently stuck "in flight".
func (r *Repository) IdempotencyBegin(ctx context.Context, scope, actor string, tenantID uint, key, requestHash string, now time.Time) (*StoredResult, bool, error) {
	var existingHash, responseBody string
	var statusCode int
	var completedAt sql.NullTime
	row := r.db.QueryRowContext(ctx, r.q(`SELECT request_hash,status_code,response_body,completed_at FROM platform_import_idempotency WHERE scope=? AND actor=? AND tenant_id=? AND idempotency_key=?`),
		scope, actor, tenantID, key)
	err := row.Scan(&existingHash, &statusCode, &responseBody, &completedAt)
	if err == sql.ErrNoRows {
		if _, insertErr := r.db.ExecContext(ctx, r.q(`INSERT INTO platform_import_idempotency (scope,actor,tenant_id,idempotency_key,request_hash,status_code,response_body,created_at) VALUES (?,?,?,?,?,0,'',?)`),
			scope, actor, tenantID, key, requestHash, now); insertErr != nil {
			if kernel.IsUniqueViolation(insertErr) {
				// A concurrent request claimed the key first; recurse once.
				return r.IdempotencyBegin(ctx, scope, actor, tenantID, key, requestHash, now)
			}
			return nil, false, kernel.Wrap(kernel.ErrCodeInternal, "register import idempotency key", insertErr)
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, kernel.Wrap(kernel.ErrCodeInternal, "lookup import idempotency key", err)
	}
	if existingHash != requestHash {
		return nil, false, kernel.NewError(kernel.ErrCodeIdempotencyReuse, "idempotency key was already used for a different request")
	}
	if !completedAt.Valid {
		// In flight. If the claim is stale (older than the stale window), the
		// owning process crashed before completing or abandoning; reclaim it
		// so the retry is a fresh attempt and cannot be stuck forever.
		var createdAt time.Time
		if scanErr := r.db.QueryRowContext(ctx, r.q(`SELECT created_at FROM platform_import_idempotency WHERE scope=? AND actor=? AND tenant_id=? AND idempotency_key=?`),
			scope, actor, tenantID, key).Scan(&createdAt); scanErr != nil {
			return nil, false, kernel.Wrap(kernel.ErrCodeInternal, "lookup in-flight import idempotency claim", scanErr)
		}
		if now.Sub(createdAt) > StaleIdempotencyWindow {
			if _, abandonErr := r.db.ExecContext(ctx, r.q(`DELETE FROM platform_import_idempotency WHERE scope=? AND actor=? AND tenant_id=? AND idempotency_key=? AND completed_at IS NULL`),
				scope, actor, tenantID, key); abandonErr != nil {
				return nil, false, kernel.Wrap(kernel.ErrCodeInternal, "reclaim stale import idempotency claim", abandonErr)
			}
			return r.IdempotencyBegin(ctx, scope, actor, tenantID, key, requestHash, now)
		}
		return nil, false, kernel.ErrIdempotencyInFlight
	}
	return &StoredResult{StatusCode: statusCode, ResponseBody: responseBody}, true, nil
}

// StaleIdempotencyWindow is how long an in-flight idempotency claim may
// remain before a retry treats it as abandoned (the owner crashed).
const StaleIdempotencyWindow = 10 * time.Minute

// IdempotencyComplete records a successful outcome for an idempotency key.
func (r *Repository) IdempotencyComplete(ctx context.Context, scope, actor string, tenantID uint, key string, statusCode int, response any, now time.Time) error {
	if r.failIdempotencyComplete != nil {
		if err := r.failIdempotencyComplete(); err != nil {
			return err
		}
	}
	body, err := json.Marshal(response)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "encode idempotent import result", err)
	}
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_import_idempotency SET status_code=?,response_body=?,completed_at=? WHERE scope=? AND actor=? AND tenant_id=? AND idempotency_key=? AND completed_at IS NULL`),
		statusCode, string(body), now, scope, actor, tenantID, key)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "complete import idempotency record", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return kernel.NewError(kernel.ErrCodeInternal, "no in-flight import idempotency row to complete")
	}
	return nil
}

// IdempotencyAbandon removes an in-flight (never completed) idempotency
// record so a failed attempt can be retried safely with the same key.
func (r *Repository) IdempotencyAbandon(ctx context.Context, scope, actor string, tenantID uint, key string) error {
	_, err := r.db.ExecContext(ctx, r.q(`DELETE FROM platform_import_idempotency WHERE scope=? AND actor=? AND tenant_id=? AND idempotency_key=? AND completed_at IS NULL`),
		scope, actor, tenantID, key)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "abandon import idempotency record", err)
	}
	return nil
}

// StoredResult mirrors kernel.StoredResult so callers do not need to import
// the kernel idempotency package.
type StoredResult struct {
	StatusCode   int
	ResponseBody string
}

func (r *Repository) LinkJobID(ctx context.Context, importID, durableJobID uint) error {
	_, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_imports SET job_id=?,updated_at=? WHERE id=?`), durableJobID, time.Now().UTC(), importID)
	return err
}

func scanImportJob(row *sql.Row) (*ImportJob, error) {
	var job ImportJob
	var report string
	var sourceType, conflictPolicy, status, stagingID string
	var storedSize int64
	err := row.Scan(&job.ID, &job.TenantID, &job.Scope, &job.Actor,
		&sourceType, &conflictPolicy, &job.SchemaVersion, &status,
		&job.SourceHash, &job.SourceName, &stagingID, &storedSize,
		&job.TotalRows, &job.ProcessedRows,
		&job.SucceededRows, &job.SkippedRows, &job.FailedRows,
		&job.CurrentCheckpoint, &job.CheckpointEntity, &job.CheckpointRow,
		&job.LastError, &job.JobID, &report, &job.CreatedAt, &job.UpdatedAt, &job.Version)
	if err != nil {
		return nil, err
	}
	job.SourceType = ImportSourceType(sourceType)
	job.ConflictPolicy = ConflictPolicy(conflictPolicy)
	job.Status = ImportStatus(status)
	job.StagingID = stagingID
	job.StoredSize = storedSize
	job.ValidationReportRaw = report
	return &job, nil
}

func scanImportRows(rows *sql.Rows) (*ImportJob, error) {
	var job ImportJob
	var report string
	var sourceType, conflictPolicy, status, stagingID string
	var storedSize int64
	err := rows.Scan(&job.ID, &job.TenantID, &job.Scope, &job.Actor,
		&sourceType, &conflictPolicy, &job.SchemaVersion, &status,
		&job.SourceHash, &job.SourceName, &stagingID, &storedSize,
		&job.TotalRows, &job.ProcessedRows,
		&job.SucceededRows, &job.SkippedRows, &job.FailedRows,
		&job.CurrentCheckpoint, &job.CheckpointEntity, &job.CheckpointRow,
		&job.LastError, &job.JobID, &report, &job.CreatedAt, &job.UpdatedAt, &job.Version)
	if err != nil {
		return nil, err
	}
	job.SourceType = ImportSourceType(sourceType)
	job.ConflictPolicy = ConflictPolicy(conflictPolicy)
	job.Status = ImportStatus(status)
	job.StagingID = stagingID
	job.StoredSize = storedSize
	job.ValidationReportRaw = report
	return &job, nil
}
