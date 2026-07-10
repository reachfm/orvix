package messagetrace

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/dbdialect"
)

// AttemptLister defines the interface for reading delivery attempts.
type AttemptLister interface {
	ListByEntry(ctx context.Context, queueEntryID uint, tx interface{}) ([]DeliveryAttemptRow, error)
}

// DeliveryAttemptRow mirrors the coremail_delivery_attempts table row.
type DeliveryAttemptRow struct {
	ID            uint
	QueueEntryID  uint
	AttemptNumber int
	Status        string
	RemoteHost    string
	RemoteIP      string
	StatusCode    int
	StatusMsg     string
	EnhancedCode  string
	DurationMs    int64
	TLSUsed       bool
	WorkerID      string
	AttemptedAt   time.Time
}

// Service provides message trace capabilities.
type Service struct {
	queueRepo  queue.Repository
	attemptsDB *sql.DB
	dialect    *dbdialect.Info
}

// NewService creates a message trace service.
func NewService(qe *queue.QueueEngine, attDB *sql.DB) *Service {
	dialect, err := dbdialect.Detect(attDB)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Service{
		queueRepo:  qe.Repo,
		attemptsDB: attDB,
		dialect:    dialect,
	}
}

func entryToTrace(e *queue.QueueEntry) TraceResult {
	return TraceResult{
		ID:          e.ID,
		MessageID:   e.MessageID,
		FromAddress: e.FromAddress,
		ToAddress:   e.ToAddress,
		Status:      string(e.Status),
		Attempts:    e.AttemptCount,
		CreatedAt:   e.CreatedAt,
	}
}

// Search searches queue entries by various criteria using direct SQL.
func (s *Service) Search(ctx context.Context, messageID, sender, recipient, domain string, limit, offset int) (*SearchResponse, error) {
	if s.attemptsDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	db := s.attemptsDB
	if limit <= 0 || limit > 200 { limit = 100 }

	d := s.dialect
	var where []string
	var args []interface{}
	argNum := 0

	if messageID != "" {
		argNum++
		where = append(where, "message_id LIKE "+d.Placeholder(argNum))
		args = append(args, "%"+messageID+"%")
	}
	if sender != "" {
		argNum++
		where = append(where, "from_address LIKE "+d.Placeholder(argNum))
		args = append(args, "%"+sender+"%")
	}
	if recipient != "" {
		argNum++
		where = append(where, "to_address LIKE "+d.Placeholder(argNum))
		args = append(args, "%"+recipient+"%")
	}
	if domain != "" {
		argNum++
		where = append(where, "recipient_domain LIKE "+d.Placeholder(argNum))
		args = append(args, "%"+domain+"%")
	}

	whereClause := "deleted_at IS NULL"
	if len(where) > 0 {
		whereClause += " AND " + strings.Join(where, " AND ")
	}

	// Count.
	var total int64
	countQuery := "SELECT COUNT(*) FROM coremail_queue WHERE " + whereClause
	if err := db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	// Data.
	query := "SELECT id, message_id, from_address, to_address, status, attempt_count, created_at FROM coremail_queue WHERE " + whereClause + " ORDER BY id DESC LIMIT " + d.Placeholder(argNum+1) + " OFFSET " + d.Placeholder(argNum+2)
	args = append(args, limit, offset)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var results []TraceResult
	for rows.Next() {
		var r TraceResult
		if err := rows.Scan(&r.ID, &r.MessageID, &r.FromAddress, &r.ToAddress, &r.Status, &r.Attempts, &r.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}

	return &SearchResponse{Results: results, Total: total}, rows.Err()
}

// GetTrace returns full trace information for a queue entry.
func (s *Service) GetTrace(ctx context.Context, id uint) (*TraceDetail, error) {
	entry, err := s.queueRepo.Get(ctx, id, nil)
	if err != nil {
		return nil, fmt.Errorf("get entry: %w", err)
	}
	if entry == nil {
		return nil, nil
	}

	detail := &TraceDetail{
		Entry:     entryToTrace(entry),
		LastError: entry.LastError,
	}

	// Load delivery attempts.
	attempts, err := s.loadAttempts(ctx, id)
	if err == nil {
		detail.Attempts = attempts
	}

	// Build timeline.
	detail.Timeline = s.buildTimeline(entry, attempts)

	return detail, nil
}

// loadAttempts reads delivery attempts from the database.
func (s *Service) loadAttempts(ctx context.Context, entryID uint) ([]DeliveryAttemptInfo, error) {
	if s.attemptsDB == nil {
		return nil, nil
	}

	rows, err := s.attemptsDB.QueryContext(ctx,
		`SELECT id, queue_entry_id, attempt_number, status, remote_host, remote_ip,
		        status_code, status_msg, duration_ms, tls_used, attempted_at
		 FROM coremail_delivery_attempts WHERE queue_entry_id = `+s.dialect.Placeholder(1)+` ORDER BY attempt_number`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []DeliveryAttemptInfo
	for rows.Next() {
		var a DeliveryAttemptInfo
		var queueEntryID uint
		if err := rows.Scan(&a.ID, &queueEntryID, &a.AttemptNumber, &a.Status, &a.RemoteHost, &a.RemoteIP,
			&a.StatusCode, &a.StatusMsg, &a.DurationMs, &a.TLSUsed, &a.AttemptedAt); err != nil {
			return nil, err
		}
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}

// buildTimeline creates a chronological event timeline for an entry.
func (s *Service) buildTimeline(entry *queue.QueueEntry, attempts []DeliveryAttemptInfo) []TimelineEvent {
	var timeline []TimelineEvent

	// Received event.
	timeline = append(timeline, TimelineEvent{
		Time:  entry.CreatedAt,
		Event: "Received",
	})

	// Queued event.
	queuedTime := entry.CreatedAt
	if entry.UpdatedAt.After(entry.CreatedAt) {
		queuedTime = entry.UpdatedAt
	}
	timeline = append(timeline, TimelineEvent{
		Time:  queuedTime,
		Event: "Queued",
	})

	// Attempt events.
	for _, a := range attempts {
		detail := fmt.Sprintf("Status: %s", a.Status)
		if a.StatusMsg != "" {
			detail += " - " + a.StatusMsg
		}
		timeline = append(timeline, TimelineEvent{
			Time:   a.AttemptedAt,
			Event:  fmt.Sprintf("Attempt #%d", a.AttemptNumber),
			Detail: detail,
		})
	}

	// Final status event.
	switch entry.Status {
	case queue.StatusDelivered:
		timeline = append(timeline, TimelineEvent{
			Time:  safeTime(entry.CompletedAt, entry.UpdatedAt),
			Event: "Delivered",
		})
	case queue.StatusDeferred:
		timeline = append(timeline, TimelineEvent{
			Time:   safeTime(entry.LastAttemptAt, entry.UpdatedAt),
			Event:  "Deferred",
			Detail: entry.LastError,
		})
	case queue.StatusBounced:
		timeline = append(timeline, TimelineEvent{
			Time:   safeTime(entry.LastAttemptAt, entry.UpdatedAt),
			Event:  "Bounced",
			Detail: entry.LastError,
		})
	case queue.StatusDeadLetter:
		timeline = append(timeline, TimelineEvent{
			Time:   safeTime(entry.DeadLetterAt, entry.UpdatedAt),
			Event:  "Dead Letter",
			Detail: entry.LastError,
		})
	case queue.StatusCancelled:
		timeline = append(timeline, TimelineEvent{
			Time:  entry.UpdatedAt,
			Event: "Cancelled",
		})
	}

	return timeline
}

func safeTime(t1 *time.Time, t2 time.Time) time.Time {
	if t1 != nil && !t1.IsZero() {
		return *t1
	}
	if !t2.IsZero() {
		return t2
	}
	return time.Now()
}
