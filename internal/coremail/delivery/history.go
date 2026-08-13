package delivery

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
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
	return AttemptHistoryTableForDialect(dbdialect.FromDriver("sqlite"))
}

// AttemptHistoryTableForDialect returns portable DDL for the active backend.
func AttemptHistoryTableForDialect(dialect *dbdialect.Info) string {
	if dialect == nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS coremail_delivery_attempts (
		id %s,
		queue_entry_id BIGINT NOT NULL,
		attempt_number INTEGER NOT NULL,
		status TEXT NOT NULL,
		remote_host TEXT NOT NULL DEFAULT '',
		remote_ip TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		status_msg TEXT NOT NULL DEFAULT '',
		enhanced_code TEXT NOT NULL DEFAULT '',
		duration_ms BIGINT NOT NULL DEFAULT 0,
		tls_used %s NOT NULL DEFAULT %s,
		worker_id TEXT NOT NULL DEFAULT '',
		attempted_at %s NOT NULL
	)`, dialect.AutoIncrement(), dialect.BooleanType(), map[bool]string{true: "FALSE", false: "0"}[dialect.IsPostgres()], dialect.TimestampType())
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
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewAttemptHistorySQLRepo(db *sql.DB) *AttemptHistorySQLRepo {
	repo, err := NewAttemptHistorySQLRepoChecked(db)
	if err != nil {
		return &AttemptHistorySQLRepo{db: db}
	}
	return repo
}

func NewAttemptHistorySQLRepoChecked(db *sql.DB) (*AttemptHistorySQLRepo, error) {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		return nil, fmt.Errorf("delivery history dialect detection: %w", err)
	}
	return &AttemptHistorySQLRepo{db: db, dialect: dialect}, nil
}

func (r *AttemptHistorySQLRepo) q(query string) string {
	if r.dialect == nil {
		return "/* delivery history dialect unavailable */ " + query
	}
	return r.dialect.Rewrite(query)
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
	insertSQL := `
		INSERT INTO coremail_delivery_attempts
			(queue_entry_id, attempt_number, status, remote_host, remote_ip,
			 status_code, status_msg, enhanced_code, duration_ms, tls_used,
			 worker_id, attempted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	tlsUsed := interface{}(boolToInt(a.TLSUsed))
	if r.dialect != nil && r.dialect.IsPostgres() {
		tlsUsed = a.TLSUsed
	}
	args := []interface{}{
		a.QueueEntryID, a.AttemptNumber, a.Status, a.RemoteHost, a.RemoteIP,
		a.StatusCode, a.StatusMsg, a.EnhancedCode, a.DurationMs, tlsUsed,
		a.WorkerID, a.AttemptedAt,
	}
	if r.dialect != nil && r.dialect.IsPostgres() {
		if err := e.QueryRowContext(ctx, r.q(insertSQL+" RETURNING id"), args...).Scan(&a.ID); err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}
		return nil
	}
	res, err := e.ExecContext(ctx, r.q(insertSQL), args...)
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
	rows, err := e.QueryContext(ctx, r.q(`
		SELECT id, queue_entry_id, attempt_number, status, remote_host, remote_ip,
		       status_code, status_msg, enhanced_code, duration_ms, tls_used,
		       worker_id, attempted_at
		FROM coremail_delivery_attempts
		WHERE queue_entry_id = ?
		ORDER BY attempt_number ASC`), queueEntryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []DeliveryAttempt
	for rows.Next() {
		var a DeliveryAttempt
		var tlsUsed interface{}
		if err := rows.Scan(&a.ID, &a.QueueEntryID, &a.AttemptNumber, &a.Status,
			&a.RemoteHost, &a.RemoteIP, &a.StatusCode, &a.StatusMsg, &a.EnhancedCode,
			&a.DurationMs, &tlsUsed, &a.WorkerID, &a.AttemptedAt); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		a.TLSUsed = databaseBool(tlsUsed)
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

	rows, err := e.QueryContext(ctx, r.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []DeliveryAttempt
	for rows.Next() {
		var a DeliveryAttempt
		var tlsUsed interface{}
		if err := rows.Scan(&a.ID, &a.QueueEntryID, &a.AttemptNumber, &a.Status,
			&a.RemoteHost, &a.RemoteIP, &a.StatusCode, &a.StatusMsg, &a.EnhancedCode,
			&a.DurationMs, &tlsUsed, &a.WorkerID, &a.AttemptedAt); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		a.TLSUsed = databaseBool(tlsUsed)
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}

func (r *AttemptHistorySQLRepo) CountByEntry(ctx context.Context, queueEntryID uint, tx interface{}) (int, error) {
	e := r.exec(tx)
	var count int
	err := e.QueryRowContext(ctx, r.q("SELECT COUNT(*) FROM coremail_delivery_attempts WHERE queue_entry_id=?"), queueEntryID).Scan(&count)
	return count, err
}

func (r *AttemptHistorySQLRepo) LastAttempt(ctx context.Context, queueEntryID uint, tx interface{}) (*DeliveryAttempt, error) {
	e := r.exec(tx)
	row := e.QueryRowContext(ctx, r.q(`
		SELECT id, queue_entry_id, attempt_number, status, remote_host, remote_ip,
		       status_code, status_msg, enhanced_code, duration_ms, tls_used,
		       worker_id, attempted_at
		FROM coremail_delivery_attempts
		WHERE queue_entry_id = ?
		ORDER BY attempt_number DESC LIMIT 1`), queueEntryID)
	var a DeliveryAttempt
	var tlsUsed interface{}
	err := row.Scan(&a.ID, &a.QueueEntryID, &a.AttemptNumber, &a.Status,
		&a.RemoteHost, &a.RemoteIP, &a.StatusCode, &a.StatusMsg, &a.EnhancedCode,
		&a.DurationMs, &tlsUsed, &a.WorkerID, &a.AttemptedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan last attempt: %w", err)
	}
	a.TLSUsed = databaseBool(tlsUsed)
	return &a, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func databaseBool(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int:
		return x != 0
	case []byte:
		return string(x) == "1" || string(x) == "true" || string(x) == "t"
	case string:
		return x == "1" || x == "true" || x == "t"
	default:
		return false
	}
}
