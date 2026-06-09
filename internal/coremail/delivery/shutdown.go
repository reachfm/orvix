package delivery

import (
	"context"
	"sync"
	"sync/atomic"
)

// ShutdownManager coordinates graceful worker shutdown.
type ShutdownManager struct {
	mu         sync.Mutex
	shutdown   chan struct{}
	activeJobs int32
	isShutdown int32
}

// NewShutdownManager creates a shutdown manager.
func NewShutdownManager() *ShutdownManager {
	return &ShutdownManager{
		shutdown: make(chan struct{}),
	}
}

// BeginJob marks a job as started. Returns false if shutdown is in progress.
func (sm *ShutdownManager) BeginJob() bool {
	if atomic.LoadInt32(&sm.isShutdown) > 0 {
		return false
	}
	atomic.AddInt32(&sm.activeJobs, 1)
	if atomic.LoadInt32(&sm.isShutdown) > 0 {
		atomic.AddInt32(&sm.activeJobs, -1)
		return false
	}
	return true
}

// EndJob marks a job as completed.
func (sm *ShutdownManager) EndJob() {
	atomic.AddInt32(&sm.activeJobs, -1)
}

// ActiveJobs returns the number of currently running jobs.
func (sm *ShutdownManager) ActiveJobs() int32 {
	return atomic.LoadInt32(&sm.activeJobs)
}

// Shutdown signals all workers to stop and waits for active jobs to complete.
// Returns a channel that is closed when all jobs are done.
func (sm *ShutdownManager) Shutdown(ctx context.Context) <-chan struct{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	atomic.StoreInt32(&sm.isShutdown, 1)
	close(sm.shutdown)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if atomic.LoadInt32(&sm.activeJobs) <= 0 {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
	return done
}

// ShutdownRequested returns a channel that is closed when shutdown is requested.
func (sm *ShutdownManager) ShutdownRequested() <-chan struct{} {
	return sm.shutdown
}

// IsShutdown returns true if shutdown has been requested.
func (sm *ShutdownManager) IsShutdown() bool {
	return atomic.LoadInt32(&sm.isShutdown) > 0
}
