package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Job struct {
	ID          uint            `json:"id"`
	Type        string          `json:"type"`
	Status      Status          `json:"status"`
	Progress    int             `json:"progress"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	Idempotency string          `json:"idempotency_key,omitempty"`
	WorkerID    string          `json:"worker_leased_to,omitempty"`
	Attempt     int             `json:"attempt_count"`
	MaxAttempts int             `json:"max_attempts"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	TenantID    uint            `json:"tenant_id"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	NextRunAt   *time.Time      `json:"next_run_at,omitempty"`
	Version     int             `json:"-"`
}

func (s Status) CanTransition(to Status) bool {
	switch s {
	case StatusQueued:
		return to == StatusRunning || to == StatusCancelled
	case StatusRunning:
		return to == StatusSucceeded || to == StatusFailed || to == StatusQueued || to == StatusCancelled
	case StatusFailed:
		return to == StatusQueued || to == StatusCancelled
	}
	return false
}

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewJobRepository(db *sql.DB) *Repository {
	d, _ := dbdialect.Detect(db)
	if d == nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	_, err := r.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS platform_jobs ("+
		"id "+autoInc+", "+
		"type TEXT NOT NULL, "+
		"status TEXT NOT NULL DEFAULT 'queued', "+
		"progress INTEGER NOT NULL DEFAULT 0, "+
		"result TEXT NOT NULL DEFAULT '', "+
		"error TEXT NOT NULL DEFAULT '', "+
		"idempotency_key TEXT NOT NULL DEFAULT '', "+
		"worker_leased_to TEXT NOT NULL DEFAULT '', "+
		"attempt_count INTEGER NOT NULL DEFAULT 0, "+
		"max_attempts INTEGER NOT NULL DEFAULT 3, "+
		"payload TEXT NOT NULL DEFAULT '', "+
		"tenant_id INTEGER NOT NULL DEFAULT 0, "+
		"version INTEGER NOT NULL DEFAULT 1, "+
		"created_at "+ts+" NOT NULL, "+
		"updated_at "+ts+" NOT NULL, "+
		"started_at "+ts+", "+
		"completed_at "+ts+", "+
		"next_run_at "+ts+")")
	return err
}

func (r *Repository) Insert(ctx context.Context, j *Job) error {
	res, err := r.db.ExecContext(ctx, "INSERT INTO platform_jobs (type, status, progress, result, error, idempotency_key, worker_leased_to, attempt_count, max_attempts, payload, tenant_id, version, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,'1',?,?)",
		j.Type, StatusQueued, 0, "", "", "", "", 0, 3, string(j.Payload), j.TenantID, time.Now().UTC(), time.Now().UTC())
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	j.ID = uint(id)
	j.Status = StatusQueued
	j.Version = 1
	return nil
}

func (r *Repository) Claim(ctx context.Context, workerID string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id, type, status, progress, result, error, idempotency_key, worker_leased_to, attempt_count, max_attempts, payload, tenant_id, version, created_at, updated_at, started_at, completed_at, next_run_at FROM platform_jobs WHERE status IN ('queued','failed') AND (next_run_at IS NULL OR next_run_at <= ?) ORDER BY created_at ASC LIMIT ?", time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	var jobs []Job
	for rows.Next() {
		var j Job
		var result, errMsg, worker, idem, payload string
		var started, completed, nextRun sql.NullTime
		if err := rows.Scan(&j.ID, &j.Type, &j.Status, &j.Progress, &result, &errMsg, &idem, &worker, &j.Attempt, &j.MaxAttempts, &payload, &j.TenantID, &j.Version, &j.CreatedAt, &j.UpdatedAt, &started, &completed, &nextRun); err != nil {
			rows.Close()
			return nil, err
		}
		j.Result = json.RawMessage(result)
		j.Error = errMsg
		j.Idempotency = idem
		j.WorkerID = worker
		j.Payload = json.RawMessage(payload)
		if started.Valid {
			j.StartedAt = &started.Time
		}
		if completed.Valid {
			j.CompletedAt = &completed.Time
		}
		if nextRun.Valid {
			j.NextRunAt = &nextRun.Time
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	now := time.Now().UTC()
	for i := range jobs {
		_, execErr := tx.ExecContext(ctx, "UPDATE platform_jobs SET status='running', worker_leased_to=?, started_at=?, attempt_count=attempt_count+1, version=version+1, updated_at=? WHERE id=? AND version=?",
			workerID, now, now, jobs[i].ID, jobs[i].Version)
		if execErr != nil {
			return nil, execErr
		}
		jobs[i].Status = StatusRunning
		jobs[i].WorkerID = workerID
		jobs[i].Attempt++
		jobs[i].Version++
		jobs[i].StartedAt = &now
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *Repository) Update(ctx context.Context, j *Job) error {
	j.UpdatedAt = time.Now().UTC()
	res, err := r.db.ExecContext(ctx, "UPDATE platform_jobs SET status=?, progress=?, result=?, error=?, worker_leased_to=?, attempt_count=?, completed_at=?, next_run_at=?, version=version+1, updated_at=? WHERE id=? AND version=?",
		j.Status, j.Progress, string(j.Result), j.Error, j.WorkerID, j.Attempt, j.CompletedAt, j.NextRunAt, j.UpdatedAt, j.ID, j.Version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &jobError{"concurrent modification detected"}
	}
	j.Version++
	return nil
}

func (r *Repository) Get(ctx context.Context, id uint) (*Job, error) {
	var j Job
	var result, errMsg, worker, idem, payload string
	var started, completed, nextRun sql.NullTime
	err := r.db.QueryRowContext(ctx, "SELECT id, type, status, progress, result, error, idempotency_key, worker_leased_to, attempt_count, max_attempts, payload, tenant_id, version, created_at, updated_at, started_at, completed_at, next_run_at FROM platform_jobs WHERE id=?", id).
		Scan(&j.ID, &j.Type, &j.Status, &j.Progress, &result, &errMsg, &idem, &worker, &j.Attempt, &j.MaxAttempts, &payload, &j.TenantID, &j.Version, &j.CreatedAt, &j.UpdatedAt, &started, &completed, &nextRun)
	if err == sql.ErrNoRows {
		return nil, &jobError{"job not found"}
	}
	if err != nil {
		return nil, err
	}
	j.Result = json.RawMessage(result)
	j.Error = errMsg
	j.Idempotency = idem
	j.WorkerID = worker
	j.Payload = json.RawMessage(payload)
	if started.Valid {
		j.StartedAt = &started.Time
	}
	if completed.Valid {
		j.CompletedAt = &completed.Time
	}
	if nextRun.Valid {
		j.NextRunAt = &nextRun.Time
	}
	return &j, nil
}

func (r *Repository) List(ctx context.Context, tenantID uint, status string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	switch {
	case tenantID > 0 && status != "":
		rows, err = r.db.QueryContext(ctx, "SELECT id, type, status, progress, tenant_id, created_at, updated_at FROM platform_jobs WHERE tenant_id=? AND status=? ORDER BY created_at DESC LIMIT ?", tenantID, status, limit)
	case tenantID > 0:
		rows, err = r.db.QueryContext(ctx, "SELECT id, type, status, progress, tenant_id, created_at, updated_at FROM platform_jobs WHERE tenant_id=? ORDER BY created_at DESC LIMIT ?", tenantID, limit)
	case status != "":
		rows, err = r.db.QueryContext(ctx, "SELECT id, type, status, progress, tenant_id, created_at, updated_at FROM platform_jobs WHERE status=? ORDER BY created_at DESC LIMIT ?", status, limit)
	default:
		rows, err = r.db.QueryContext(ctx, "SELECT id, type, status, progress, tenant_id, created_at, updated_at FROM platform_jobs ORDER BY created_at DESC LIMIT ?", limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Type, &j.Status, &j.Progress, &j.TenantID, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *Repository) StaleJobs(ctx context.Context, threshold time.Duration, limit int) ([]Job, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	cutoff := time.Now().UTC().Add(-threshold)
	rows, err := r.db.QueryContext(ctx, "SELECT id, type, status, progress, result, error, idempotency_key, worker_leased_to, attempt_count, max_attempts, payload, tenant_id, version, created_at, updated_at, started_at, completed_at, next_run_at FROM platform_jobs WHERE status='running' AND updated_at <= ? ORDER BY updated_at ASC LIMIT ?", cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var result, errMsg, worker, idem, payload string
		var started, completed, nextRun sql.NullTime
		if err := rows.Scan(&j.ID, &j.Type, &j.Status, &j.Progress, &result, &errMsg, &idem, &worker, &j.Attempt, &j.MaxAttempts, &payload, &j.TenantID, &j.Version, &j.CreatedAt, &j.UpdatedAt, &started, &completed, &nextRun); err != nil {
			return nil, err
		}
		j.Result = json.RawMessage(result)
		j.Error = errMsg
		j.Idempotency = idem
		j.WorkerID = worker
		j.Payload = json.RawMessage(payload)
		if started.Valid {
			j.StartedAt = &started.Time
		}
		if completed.Valid {
			j.CompletedAt = &completed.Time
		}
		if nextRun.Valid {
			j.NextRunAt = &nextRun.Time
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

type jobError struct{ msg string }

func (e *jobError) Error() string { return e.msg }
