package jobs

import (
	"context"
	"errors"
	"time"
)

// Service is the automation jobs service.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repo.EnsureSchema(ctx)
}

// CreateJob creates a new job with idempotency key support.
func (s *Service) CreateJob(ctx context.Context, jobType string, payload []byte, tenantID uint) (*Job, error) {
	j := &Job{Type: jobType, Payload: payload, TenantID: tenantID}
	if err := s.repo.Insert(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// Claim claims pending jobs for a worker.
func (s *Service) Claim(ctx context.Context, workerID string, limit int) ([]Job, error) {
	return s.repo.Claim(ctx, workerID, limit)
}

// Complete marks a job as succeeded.
func (s *Service) Complete(ctx context.Context, id uint, result []byte) error {
	j, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !j.Status.CanTransition(StatusSucceeded) {
		return &jobError{"cannot transition to succeeded from " + string(j.Status)}
	}
	j.Status = StatusSucceeded
	j.Progress = 100
	j.Result = result
	now := time.Now().UTC()
	j.CompletedAt = &now
	return s.repo.Update(ctx, j)
}

// Fail marks a job as failed and schedules a retry if attempts remain.
func (s *Service) Fail(ctx context.Context, id uint, errMsg string) error {
	j, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !j.Status.CanTransition(StatusFailed) {
		return &jobError{"cannot transition to failed from " + string(j.Status)}
	}
	j.Status = StatusFailed
	j.Error = errMsg
	j.Attempt++
	if j.Attempt < j.MaxAttempts {
		// Re-queue for retry
		j.Status = StatusQueued
		j.NextRunAt = ptr(time.Now().UTC().Add(backoff(j.Attempt)))
	}
	return s.repo.Update(ctx, j)
}

// Cancel cancels a job.
func (s *Service) Cancel(ctx context.Context, id uint) error {
	j, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !j.Status.CanTransition(StatusCancelled) {
		return &jobError{"cannot transition to cancelled from " + string(j.Status)}
	}
	j.Status = StatusCancelled
	now := time.Now().UTC()
	j.CompletedAt = &now
	return s.repo.Update(ctx, j)
}

// RecoverStaleJobs resets stale running jobs back to queued.
func (s *Service) RecoverStaleJobs(ctx context.Context, threshold time.Duration, limit int) (int, error) {
	stale, err := s.repo.StaleJobs(ctx, threshold, limit)
	if err != nil {
		return 0, err
	}
	for i := range stale {
		stale[i].Status = StatusQueued
		stale[i].WorkerID = ""
		if err := s.repo.Update(ctx, &stale[i]); err != nil {
			return i, err
		}
	}
	return len(stale), nil
}

// Get returns a job by ID.
func (s *Service) Get(ctx context.Context, id uint) (*Job, error) {
	return s.repo.Get(ctx, id)
}

// List returns jobs with optional filters.
func (s *Service) List(ctx context.Context, tenantID uint, status string, limit int) ([]Job, error) {
	return s.repo.List(ctx, tenantID, status, limit)
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt)) * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

func ptr[T any](v T) *T { return &v }

var (
	ErrNotFound          = &jobError{"job not found"}
	ErrInvalidTransition = &jobError{"invalid job status transition"}
)

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
