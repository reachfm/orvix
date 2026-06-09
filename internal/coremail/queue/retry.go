package queue

import (
	"context"
	"fmt"
	"time"
)

// RetryScheduler manages retry scheduling and dead letter transitions.
type RetryScheduler struct {
	Engine *QueueEngine
}

// NewRetryScheduler creates a retry scheduler.
func NewRetryScheduler(qe *QueueEngine) *RetryScheduler {
	return &RetryScheduler{Engine: qe}
}

// ProcessRetryQueue checks all deferred jobs whose next_attempt_at has passed
// and leases them for retry. Returns the number of jobs moved to pending.
func (rs *RetryScheduler) ProcessRetryQueue(ctx context.Context, owner string) (int, error) {
	count := 0
	// Release expired leases first to recover stuck jobs.
	released, err := rs.Engine.ReleaseExpiredLeases(ctx)
	if err != nil {
		return 0, fmt.Errorf("release expired: %w", err)
	}
	_ = released

	// Lease deferred jobs that are due for retry.
	for {
		entry, err := rs.Engine.LeaseNextWithStatuses(ctx, owner, []QueueStatus{StatusDeferred})
		if err != nil {
			return count, fmt.Errorf("lease deferred: %w", err)
		}
		if entry == nil {
			break
		}
		count++
		// The leased job is now owned by the worker to process.
		// After processing, the worker should call HandleDeliveryResult.
	}
	return count, nil
}

// RetryAllDeadLetters resets all dead letter entries to pending.
// This is a manual recovery operation for operators.
func (rs *RetryScheduler) RetryAllDeadLetters(ctx context.Context, maxItems int) (int, error) {
	filter := QueueFilter{
		Status: statusPtr(StatusDeadLetter),
		Limit:  maxItems,
	}
	entries, _, err := rs.Engine.Repo.List(ctx, filter, nil)
	if err != nil {
		return 0, fmt.Errorf("list dead letters: %w", err)
	}
	for _, e := range entries {
		if err := rs.Engine.Repo.RetryNow(ctx, e.ID, nil); err != nil {
			return 0, fmt.Errorf("retry dead letter %d: %w", e.ID, err)
		}
	}
	return len(entries), nil
}

// ScheduleCleanup removes old completed and dead letter entries.
func (rs *RetryScheduler) ScheduleCleanup(ctx context.Context, olderThan time.Time) (completedPurged, deadLetterPurged int64, err error) {
	completedPurged, err = rs.Engine.PurgeCompleted(ctx, olderThan)
	if err != nil {
		return 0, 0, err
	}
	deadLetterPurged, err = rs.Engine.PurgeDeadLetters(ctx, olderThan)
	if err != nil {
		return 0, 0, err
	}
	return
}
