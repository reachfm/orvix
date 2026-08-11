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
}

func NewRepository(db *sql.DB) *Repository {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: dialect}
}

func (r *Repository) q(query string) string { return r.dialect.Rewrite(query) }

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
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_platform_imports_tenant ON platform_imports(tenant_id,scope,status)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_imports_hash ON platform_imports(source_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_import_checkpoints_import ON platform_import_checkpoints(import_id)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_import_cmp_import ON platform_import_compensations(import_id)`,
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
	raw, _ := json.Marshal(report)
	_, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_imports SET validation_report=?,source_hash=?,total_rows=?,processed_rows=0,succeeded_rows=0,skipped_rows=0,failed_rows=0,updated_at=? WHERE id=?`), string(raw), hash, report.Total, time.Now().UTC(), id)
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
	ids, _ := json.Marshal(cp.SucceededIDs)
	failed, _ := json.Marshal(cp.FailedRows)
	_, err := r.db.ExecContext(ctx, r.q(`INSERT INTO platform_import_checkpoints (import_id,entity,row_index,succeeded_ids,failed_rows,processed_count,committed_at) VALUES (?,?,?,?,?,?,?)`), cp.ImportID, cp.Entity, cp.RowIndex, string(ids), string(failed), cp.ProcessedCount, cp.CommittedAt)
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
	json.Unmarshal([]byte(idsStr), &cp.SucceededIDs)
	json.Unmarshal([]byte(failedStr), &cp.FailedRows)
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

func (r *Repository) UpdateStagingID(ctx context.Context, id uint, stagingID string) error {
	_, err := r.db.ExecContext(ctx, r.q(`UPDATE platform_imports SET staging_id=?,updated_at=? WHERE id=?`), stagingID, time.Now().UTC(), id)
	return err
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
