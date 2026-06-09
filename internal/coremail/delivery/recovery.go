package delivery

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/orvix/orvix/internal/coremail/queue"
)

// WorkerCrashRecovery handles lease recovery and worker crash detection.
type WorkerCrashRecovery struct {
	Queue       *queue.QueueEngine
	WorkerID    string
	LeaseSeconds int
	recovered   int64
}

// NewWorkerCrashRecovery creates a worker crash recovery handler.
func NewWorkerCrashRecovery(qe *queue.QueueEngine, workerID string, leaseSeconds int) *WorkerCrashRecovery {
	return &WorkerCrashRecovery{
		Queue:        qe,
		WorkerID:     workerID,
		LeaseSeconds: leaseSeconds,
	}
}

// RecoverAbandonedLeases scans for expired leases and returns them to pending.
// This handles cases where a worker crashed or was killed while holding a lease.
// Returns the number of recovered entries.
func (r *WorkerCrashRecovery) RecoverAbandonedLeases(ctx context.Context) (int64, error) {
	if r.Queue == nil {
		return 0, fmt.Errorf("queue not configured")
	}
	recovered, err := r.Queue.ReleaseExpiredLeases(ctx)
	if err != nil {
		return 0, fmt.Errorf("recover leases: %w", err)
	}
	if recovered > 0 {
		atomic.AddInt64(&r.recovered, recovered)
	}
	return recovered, nil
}

// RecoveredCount returns the total number of leases recovered since creation.
func (r *WorkerCrashRecovery) RecoveredCount() int64 {
	return atomic.LoadInt64(&r.recovered)
}

// StartRecoveryLoop begins a background goroutine that periodically checks
// for abandoned leases. The loop exits when ctx is cancelled.
func (r *WorkerCrashRecovery) StartRecoveryLoop(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				recovered, err := r.RecoverAbandonedLeases(ctx)
				if err != nil {
					// Log error — in production this would go to structured logging.
					_ = fmt.Errorf("recovery loop: %w", err)
				}
				_ = recovered
			}
		}
	}()
}
