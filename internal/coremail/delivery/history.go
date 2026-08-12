package delivery

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DeliveryAttempt records a single delivery attempt for persistence.
type DeliveryAttempt struct {
	ID            uint      `json:"id"`
	QueueEntryID  uint      `json:"queue_entry_id"`
	AttemptNumber int       `json:"attempt_number"`
	Status        string    `json:"status"` // success, deferred, bounced, dead_letter
	RemoteHost    string    `json:"remote_host"`
	RemoteIP      string    `json:"remote_ip"`
	StatusCode    int       `json:"status_code"`
	StatusMsg     string    `json:"status_msg"`
	EnhancedCode  string    `json:"enhanced_code"`
	DurationMs    int64     `json:"duration_ms"`
	TLSUsed       bool      `json:"tls_used"`
	WorkerID      string    `json:"worker_id"`
	AttemptedAt   time.Time `json:"attempted_at"`
}

// AttemptHistoryTable returns DDL for the delivery_attempts table.
func AttemptHistoryTable() string {
	return `CREATE TABLE IF NOT EXISTS coremail_delivery_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		queue_entry_id INTEGER NOT NULL,
		attempt_number INTEGER NOT NULL,
		status TEXT NOT NULL,
		remote_host TEXT NOT NULL DEFAULT '',
		remote_ip TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		status_msg TEXT NOT NULL DEFAULT '',
		enhanced_code TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER NOT NULL DEFAULT 0,
		tls_used INTEGER NOT NULL DEFAULT 0,
		worker_id TEXT NOT NULL DEFAULT '',
		attempted_at DATETIME NOT NULL
	)`
}

// AttemptHistoryIndexes returns index DDL for the delivery_attempts table.
func AttemptHistoryIndexes() []string {
	return []string{
		`CREATE INDEX IF NOT EXISTS idx_delivery_attempts_entry ON coremail_delivery_attempts(queue_entry_id, attempt_number)`,
		`CREATE INDEX IF NOT EXISTS idx_delivery_attempts_status ON coremail_delivery_attempts(status)`,
		`CREATE INDEX IF NOT EXISTS idx_delivery_attempts_time ON coremail_delivery_attempts(attempted_at)`,
	}
}

// AttemptHistoryRepository persists delivery attempt records.
type AttemptHistoryRepository interface {
	RecordAttempt(ctx context.Context, attempt *DeliveryAttempt, tx interface{}) error
	ListByEntry(ctx context.Context, queueEntryID uint, tx interface{}) ([]DeliveryAttempt, error)
	CountByEntry(ctx context.Context, queueEntryID uint, tx interface{}) (int, error)
	LastAttempt(ctx context.Context, queueEntryID uint, tx interface{}) (*DeliveryAttempt, error)
	// ListRecent is the immutable delivery-history read path (Milestone
	// 8): cross-entry, cursor-paginated, filterable by status and
	// remote host, distinct from the live queue's mutable state. The
	// cursor is the attempt's own ID (monotonic, unambiguous under
	// same-timestamp writes — attempted_at alone is not, since
	// multiple attempts can share a timestamp at second resolution).
	ListRecent(ctx context.Context, filter HistoryFilter, tx interface{}) ([]DeliveryAttempt, error)
}

// HistoryFilter narrows ListRecent. Zero values mean "no filter" on
// that dimension. AfterID is the cursor: results are IDs strictly
// greater than AfterID, ascending, capped at Limit — a caller pages
// forward by passing the last row's ID as the next call's AfterID.
type HistoryFilter struct {
	Status     string
	RemoteHost string
	AfterID    uint
	Limit      int
}

var _ AttemptHistoryRepository = (*AttemptHistorySQLRepo)(nil)

// AttemptHistorySQLRepo implements AttemptHistoryRepository.
type AttemptHistorySQLRepo struct {
	db *sql.DB
}

func NewAttemptHistorySQLRepo(db *sql.DB) *AttemptHistorySQLRepo {
	return &AttemptHistorySQLRepo{db: db}
}

func (r *AttemptHistorySQLRepo) exec(tx interface{}) interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
} {
	if tx != nil {
		if t, ok := tx.(*sql.Tx); ok {
			return t
		}
	}
	return r.db
}

func (r *AttemptHistorySQLRepo) RecordAttempt(ctx context.Context, a *DeliveryAttempt, tx interface{}) error {
	if a.AttemptedAt.IsZero() {
		a.AttemptedAt = time.Now().UTC()
	}
	e := r.exec(tx)
	res, err := e.ExecContext(ctx, `
		INSERT INTO coremail_delivery_attempts
			(queue_entry_id, attempt_number, status, remote_host, remote_ip,
			 status_code, status_msg, enhanced_code, duration_ms, tls_used,
			 worker_id, attempted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.QueueEntryID, a.AttemptNumber, a.Status, a.RemoteHost, a.RemoteIP,
		a.StatusCode, a.StatusMsg, a.EnhancedCode, a.DurationMs, boolToInt(a.TLSUsed),
		a.WorkerID, a.AttemptedAt,
	)
	if err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	a.ID = uint(id)
	return nil
}

func (r *AttemptHistorySQLRepo) ListByEntry(ctx context.Context, queueEntryID uint, tx interface{}) ([]DeliveryAttempt, error) {
	e := r.exec(tx)
	rows, err := e.QueryContext(ctx, `
		SELECT id, queue_entry_id, attempt_number, status, remote_host, remote_ip,
		       status_code, status_msg, enhanced_code, duration_ms, tls_used,
		       worker_id, attempted_at
		FROM coremail_delivery_attempts
		WHERE queue_entry_id = ?
		ORDER BY attempt_number ASC`, queueEntryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []DeliveryAttempt
	for rows.Next() {
		var a DeliveryAttempt
		var tlsUsed int
		if err := rows.Scan(&a.ID, &a.QueueEntryID, &a.AttemptNumber, &a.Status,
			&a.RemoteHost, &a.RemoteIP, &a.StatusCode, &a.StatusMsg, &a.EnhancedCode,
			&a.DurationMs, &tlsUsed, &a.WorkerID, &a.AttemptedAt); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		a.TLSUsed = tlsUsed == 1
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}

func (r *AttemptHistorySQLRepo) ListRecent(ctx context.Context, filter HistoryFilter, tx interface{}) ([]DeliveryAttempt, error) {
	e := r.exec(tx)
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, queue_entry_id, attempt_number, status, remote_host, remote_ip,
		       status_code, status_msg, enhanced_code, duration_ms, tls_used,
		       worker_id, attempted_at
		FROM coremail_delivery_attempts WHERE id > ?`
	args := []interface{}{filter.AfterID}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	if filter.RemoteHost != "" {
		query += " AND remote_host = ?"
		args = append(args, filter.RemoteHost)
	}
	query += " ORDER BY id ASC LIMIT ?"
	args = append(args, limit)

	rows, err := e.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []DeliveryAttempt
	for rows.Next() {
		var a DeliveryAttempt
		var tlsUsed int
		if err := rows.Scan(&a.ID, &a.QueueEntryID, &a.AttemptNumber, &a.Status,
			&a.RemoteHost, &a.RemoteIP, &a.StatusCode, &a.StatusMsg, &a.EnhancedCode,
			&a.DurationMs, &tlsUsed, &a.WorkerID, &a.AttemptedAt); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		a.TLSUsed = tlsUsed == 1
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}

func (r *AttemptHistorySQLRepo) CountByEntry(ctx context.Context, queueEntryID uint, tx interface{}) (int, error) {
	e := r.exec(tx)
	var count int
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_delivery_attempts WHERE queue_entry_id=?", queueEntryID).Scan(&count)
	return count, err
}

func (r *AttemptHistorySQLRepo) LastAttempt(ctx context.Context, queueEntryID uint, tx interface{}) (*DeliveryAttempt, error) {
	e := r.exec(tx)
	row := e.QueryRowContext(ctx, `
		SELECT id, queue_entry_id, attempt_number, status, remote_host, remote_ip,
		       status_code, status_msg, enhanced_code, duration_ms, tls_used,
		       worker_id, attempted_at
		FROM coremail_delivery_attempts
		WHERE queue_entry_id = ?
		ORDER BY attempt_number DESC LIMIT 1`, queueEntryID)
	var a DeliveryAttempt
	var tlsUsed int
	err := row.Scan(&a.ID, &a.QueueEntryID, &a.AttemptNumber, &a.Status,
		&a.RemoteHost, &a.RemoteIP, &a.StatusCode, &a.StatusMsg, &a.EnhancedCode,
		&a.DurationMs, &tlsUsed, &a.WorkerID, &a.AttemptedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan last attempt: %w", err)
	}
	a.TLSUsed = tlsUsed == 1
	return &a, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
