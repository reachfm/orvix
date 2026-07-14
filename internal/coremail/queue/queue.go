package queue

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

// QueueEngine orchestrates queue operations with transaction support.
type QueueEngine struct {
	DB   *sql.DB
	Repo Repository
	// Tenant fairness configuration.
	MaxWorkersPerTenant int
	GlobalMaxWorkers    int
	pendingClaims       map[string]int // tenant_id -> active claims (in-memory best-effort)
}

// NewQueueEngine creates a queue engine with default tenant fairness limits.
func NewQueueEngine(db *sql.DB) *QueueEngine {
	return &QueueEngine{
		DB:                  db,
		Repo:                NewSQLRepo(db),
		MaxWorkersPerTenant: 4,
		GlobalMaxWorkers:    100,
		pendingClaims:       make(map[string]int),
	}
}

// BeginTx starts a new transaction.
func (qe *QueueEngine) BeginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := qe.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("queue begin tx: %w", err)
	}
	return tx, nil
}

// WithTx executes the given function within a transaction.
func (qe *QueueEngine) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := qe.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Enqueue adds a message to the queue.
func (qe *QueueEngine) Enqueue(ctx context.Context, entry *QueueEntry) error {
	// Determine delivery mode and recipient domain if not set.
	if entry.DeliveryMode == "" {
		entry.DeliveryMode = DeliveryRemoteSMTP
	}
	if entry.RecipientDomain == "" {
		// Extract domain from ToAddress
		entry.RecipientDomain = extractDomain(entry.ToAddress)
	}
	return qe.Repo.Enqueue(ctx, entry, nil)
}

// LeaseNext claims the next available job for a worker.
func (qe *QueueEngine) LeaseNext(ctx context.Context, owner string) (*QueueEntry, error) {
	return qe.LeaseNextWithStatuses(ctx, owner, []QueueStatus{StatusPending, StatusDeferred})
}

// LeaseNextWithStatuses claims the next available job from the given statuses.
func (qe *QueueEngine) LeaseNextWithStatuses(ctx context.Context, owner string, allowedStatuses []QueueStatus) (*QueueEntry, error) {
	return qe.Repo.LeaseNext(ctx, owner, DefaultLeaseSeconds, allowedStatuses, nil)
}

// LeaseNextTenantFair claims the next available job respecting per-tenant worker ceilings.
// Prevents one tenant from starving others by limiting active leases per tenant.
func (qe *QueueEngine) LeaseNextTenantFair(ctx context.Context, owner string) (*QueueEntry, error) {
	return qe.Repo.LeaseNextTenantFair(ctx, owner, DefaultLeaseSeconds,
		[]QueueStatus{StatusPending, StatusDeferred}, qe.MaxWorkersPerTenant, qe.GlobalMaxWorkers, nil)
}

// ReleaseClaim decrements the in-memory claim counter for a tenant.
// Should be called when a worker completes or releases a claim.
func (qe *QueueEngine) ReleaseClaim(tenantID string) {
	if qe.pendingClaims == nil {
		return
	}
	qe.pendingClaims[tenantID]--
	if qe.pendingClaims[tenantID] <= 0 {
		delete(qe.pendingClaims, tenantID)
	}
}

// AckDelivered marks a job as delivered.
func (qe *QueueEngine) AckDelivered(ctx context.Context, id uint) error {
	return qe.Repo.AckDelivered(ctx, id, nil)
}

// HandleDeliveryResult processes the result of a delivery attempt.
// On success: acks. On failure: defers or bounces based on attempt count.
func (qe *QueueEngine) HandleDeliveryResult(ctx context.Context, entry *QueueEntry, success bool, errMsg string) error {
	if success {
		return qe.AckDelivered(ctx, entry.ID)
	}

	maxAttempts := entry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}

	if entry.AttemptCount >= maxAttempts {
		return qe.Repo.DeadLetter(ctx, entry.ID, errMsg, nil)
	}

	nextAttempt := computeNextAttempt(entry.AttemptCount)
	return qe.Repo.Defer(ctx, entry.ID, nextAttempt, errMsg, nil)
}

// Defer reschedules a job.
func (qe *QueueEngine) Defer(ctx context.Context, id uint, nextAttempt time.Time, lastError string) error {
	return qe.Repo.Defer(ctx, id, nextAttempt, lastError, nil)
}

// Bounce marks a job as bounced.
func (qe *QueueEngine) Bounce(ctx context.Context, id uint, lastError string) error {
	return qe.Repo.Bounce(ctx, id, lastError, nil)
}

// DeadLetter moves a job to the dead letter queue.
func (qe *QueueEngine) DeadLetter(ctx context.Context, id uint, lastError string) error {
	return qe.Repo.DeadLetter(ctx, id, lastError, nil)
}

// Cancel cancels a job.
func (qe *QueueEngine) Cancel(ctx context.Context, id uint) error {
	return qe.Repo.Cancel(ctx, id, nil)
}

// RetryNow resets a job for immediate retry.
func (qe *QueueEngine) RetryNow(ctx context.Context, id uint) error {
	return qe.Repo.RetryNow(ctx, id, nil)
}

// ReleaseExpiredLeases recovers jobs from workers that failed.
func (qe *QueueEngine) ReleaseExpiredLeases(ctx context.Context) (int64, error) {
	return qe.Repo.ReleaseExpiredLeases(ctx, nil)
}

// PurgeCompleted removes old completed/delivered entries.
func (qe *QueueEngine) PurgeCompleted(ctx context.Context, olderThan time.Time) (int64, error) {
	return qe.Repo.PurgeCompleted(ctx, olderThan, nil)
}

// PurgeDeadLetters removes old dead letter entries.
func (qe *QueueEngine) PurgeDeadLetters(ctx context.Context, olderThan time.Time) (int64, error) {
	return qe.Repo.PurgeDeadLetters(ctx, olderThan, nil)
}

// Metrics returns queue statistics.
func (qe *QueueEngine) Metrics(ctx context.Context, tenantID *uint) (*QueueMetrics, error) {
	return qe.Repo.Metrics(ctx, tenantID, nil)
}

// computeNextAttempt calculates exponential backoff for retry scheduling.
// attempt 1: 1 minute
// attempt 2: 5 minutes
// attempt 3: 15 minutes
// attempt 4: 1 hour
// attempt 5: 4 hours
// attempt 6+: 24 hours
func computeNextAttempt(attemptCount int) time.Time {
	now := nowFn()

	var d time.Duration
	switch {
	case attemptCount <= 0:
		d = 0 // immediate
	case attemptCount == 1:
		d = 1 * time.Minute
	case attemptCount == 2:
		d = 5 * time.Minute
	case attemptCount == 3:
		d = 15 * time.Minute
	case attemptCount == 4:
		d = 1 * time.Hour
	case attemptCount == 5:
		d = 4 * time.Hour
	default:
		d = 24 * time.Hour
	}

	// Add jitter: up to 10% of the delay
	jitter := time.Duration(float64(d) * 0.1 * (float64(attemptCount) / float64(attemptCount+1)))
	total := d + time.Duration(int64(jitter))
	_ = math.Abs // ensure math is importable for future use

	return now.Add(total)
}

func extractDomain(email string) string {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[i+1:]
		}
	}
	return email
}
