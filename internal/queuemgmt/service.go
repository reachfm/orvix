package queuemgmt

import (
	"context"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/coremail/queue"
)

// AttemptRepository defines the interface for reading delivery attempts.
type AttemptRepository interface {
	ListByEntry(ctx context.Context, queueEntryID uint, tx interface{}) ([]interface{}, error)
}

// Service manages the mail delivery queue.
type Service struct {
	queue    *queue.QueueEngine
	attempts AttemptRepository
}

// NewService creates a queue management service.
func NewService(qe *queue.QueueEngine, attRepo AttemptRepository) *Service {
	return &Service{queue: qe, attempts: attRepo}
}

func entryToAdmin(e *queue.QueueEntry) *Entry {
	return &Entry{
		ID:            e.ID,
		MessageID:     e.MessageID,
		FromAddress:   e.FromAddress,
		ToAddress:     e.ToAddress,
		Status:        string(e.Status),
		Direction:     string(e.Direction),
		AttemptCount:  e.AttemptCount,
		MaxAttempts:   e.MaxAttempts,
		LastError:     e.LastError,
		CreatedAt:     e.CreatedAt,
		UpdatedAt:     e.UpdatedAt,
		LastAttemptAt: e.LastAttemptAt,
		NextAttemptAt: e.NextAttemptAt,
	}
}

// GetSummary returns queue metrics.
func (s *Service) GetSummary(ctx context.Context) (*Summary, error) {
	metrics, err := s.queue.Metrics(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Summary{
		Pending:    metrics.Pending,
		Leased:     metrics.Leased,
		Delivering: metrics.Delivering,
		Deferred:   metrics.Deferred,
		Delivered:  metrics.Delivered,
		Bounced:    metrics.Bounced,
		DeadLetter: metrics.DeadLetter,
		Cancelled:  metrics.Cancelled,
		Total:      metrics.Total,
	}, nil
}

// ListEntries returns queue entries filtered by status.
func (s *Service) ListEntries(ctx context.Context, status string, limit, offset int) (*ListResponse, error) {
	filter := queue.QueueFilter{Limit: limit, Offset: offset}
	if status != "" {
		s := queue.QueueStatus(status)
		filter.Status = &s
	}
	entries, total, err := s.queue.Repo.List(ctx, filter, nil)
	if err != nil {
		return nil, err
	}
	result := make([]Entry, len(entries))
	for i, e := range entries {
		result[i] = *entryToAdmin(&e)
	}
	return &ListResponse{Entries: result, Total: total}, nil
}

// GetEntry returns a single queue entry.
func (s *Service) GetEntry(ctx context.Context, id uint) (*Entry, error) {
	e, err := s.queue.Repo.Get(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	return entryToAdmin(e), nil
}

// ListAttempts returns delivery attempts for a queue entry.
func (s *Service) ListAttempts(ctx context.Context, entryID uint) ([]Attempt, error) {
	if s.attempts == nil {
		return nil, nil
	}
	raw, err := s.attempts.ListByEntry(ctx, entryID, nil)
	if err != nil {
		return nil, err
	}
	result := make([]Attempt, len(raw))
	for i, r := range raw {
		if a, ok := r.(attemptLike); ok {
			result[i] = Attempt{
				ID:            a.GetID(),
				AttemptNumber: a.GetAttemptNumber(),
				Status:        a.GetStatus(),
				RemoteHost:    a.GetRemoteHost(),
				RemoteIP:      a.GetRemoteIP(),
				StatusCode:    a.GetStatusCode(),
				StatusMsg:     a.GetStatusMsg(),
				DurationMs:    a.GetDurationMs(),
				TLSUsed:       a.GetTLSUsed(),
				AttemptedAt:   a.GetAttemptedAt(),
			}
		}
	}
	return result, nil
}

// attemptLike is an internal interface to read attempt fields.
type attemptLike interface {
	GetID() uint
	GetAttemptNumber() int
	GetStatus() string
	GetRemoteHost() string
	GetRemoteIP() string
	GetStatusCode() int
	GetStatusMsg() string
	GetDurationMs() int64
	GetTLSUsed() bool
	GetAttemptedAt() time.Time
}

// RetryEntry requeues a deferred or dead_letter entry.
func (s *Service) RetryEntry(ctx context.Context, id uint) error {
	e, err := s.queue.Repo.Get(ctx, id, nil)
	if err != nil {
		return fmt.Errorf("get entry: %w", err)
	}
	if e == nil {
		return fmt.Errorf("entry not found")
	}
	allowed := e.Status == queue.StatusDeferred || e.Status == queue.StatusDeadLetter
	if !allowed {
		return fmt.Errorf("cannot retry entry with status: %s", e.Status)
	}
	return s.queue.RetryNow(ctx, id)
}

// CancelEntry cancels a pending or deferred entry.
func (s *Service) CancelEntry(ctx context.Context, id uint) error {
	e, err := s.queue.Repo.Get(ctx, id, nil)
	if err != nil {
		return fmt.Errorf("get entry: %w", err)
	}
	if e == nil {
		return fmt.Errorf("entry not found")
	}
	allowed := e.Status == queue.StatusPending || e.Status == queue.StatusDeferred
	if !allowed {
		return fmt.Errorf("cannot cancel entry with status: %s", e.Status)
	}
	return s.queue.Cancel(ctx, id)
}
