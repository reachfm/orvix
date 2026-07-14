package backup

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// These tests pin the BLOCKER 2 contract: a restore must not report success
// unless validation, safety backup, activation, an actual service restart,
// and post-restart service health verification have all succeeded. Missing,
// failing, or timed-out restart/health must fail closed and roll back.

func newRestoreTestBackup(t *testing.T, s *Service, name string) string {
	t.Helper()
	b, err := s.CreateBackup(context.Background(), name)
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	return b.ID
}

// 1. restart callback missing → restore fails (fail closed, no success).
func TestRestore_MissingRestartIntegration_FailsClosed(t *testing.T) {
	s := testService(t)
	s.SetRestoreRestart(nil) // clear the default wired by testService
	id := newRestoreTestBackup(t, s, "missing-restart")

	result, err := s.RestoreBackup(context.Background(), id)
	if err == nil {
		t.Fatalf("expected fail-closed error, got result=%+v", result)
	}
	if result != nil && result.Status == RestoreStatusActivated {
		t.Fatalf("restore must not activate without restart integration: %+v", result)
	}
}

// health callback missing → restore fails (post-restart verification required).
func TestRestore_MissingHealthIntegration_FailsClosed(t *testing.T) {
	s := testService(t)
	s.SetRestoreHealthCheck(nil)
	id := newRestoreTestBackup(t, s, "missing-health")

	result, err := s.RestoreBackup(context.Background(), id)
	if err == nil {
		t.Fatalf("expected fail-closed error, got result=%+v", result)
	}
	if result != nil && result.Status == RestoreStatusActivated {
		t.Fatalf("restore must not activate without health verification: %+v", result)
	}
}

// 2. restart returns error → rollback (safety reactivated; rollback restart
// succeeds; rollback health succeeds; healthCalls==1 from rollback verification).
func TestRestore_RestartError_RollsBackAndSkipsHealth(t *testing.T) {
	s := testService(t)
	var restartCalls, healthCalls int32
	s.SetRestoreRestart(func(context.Context) error {
		if atomic.AddInt32(&restartCalls, 1) == 1 {
			return fmt.Errorf("systemctl restart failed")
		}
		return nil // rollback restart recovers the service
	})
	s.SetRestoreHealthCheck(func(context.Context) error {
		atomic.AddInt32(&healthCalls, 1)
		return nil
	})
	id := newRestoreTestBackup(t, s, "restart-error")

	result, err := s.RestoreBackup(context.Background(), id)
	if err == nil {
		t.Fatal("expected restart error")
	}
	if result == nil || result.Status != RestoreStatusRolledBack || !result.RolledBack {
		t.Fatalf("expected rolled_back, got %+v", result)
	}
	// Rollback health check runs (and succeeds); primary restore health never runs.
	if atomic.LoadInt32(&healthCalls) != 1 {
		t.Fatalf("expected exactly 1 health call (rollback verification), got %d", healthCalls)
	}
}

// 3. restart times out → rollback (bounded, even if the callback ignores ctx).
// The restore-target restart hangs and is cut off by the bounded timeout; the
// rollback restart returns promptly so the safety backup is reactivated.
func TestRestore_RestartTimeout_RollsBack(t *testing.T) {
	s := testService(t)
	s.SetRestoreVerifyTimeout(150 * time.Millisecond)
	var restartCalls int32
	s.SetRestoreRestart(func(ctx context.Context) error {
		if atomic.AddInt32(&restartCalls, 1) == 1 {
			<-ctx.Done() // simulate a hung service manager
			return ctx.Err()
		}
		return nil // rollback restart recovers
	})
	id := newRestoreTestBackup(t, s, "restart-timeout")

	start := time.Now()
	result, err := s.RestoreBackup(context.Background(), id)
	if err == nil {
		t.Fatal("expected restart timeout error")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("bounded restart timeout did not fire promptly")
	}
	if result == nil || result.Status != RestoreStatusRolledBack || !result.RolledBack {
		t.Fatalf("expected rolled_back after timeout, got %+v", result)
	}
}

// 4. service health fails during primary restore → rollback. Rollback health
// must succeed for RolledBack: true.
func TestRestore_HealthFailure_RollsBack(t *testing.T) {
	s := testService(t)
	var healthCalls int32
	s.SetRestoreHealthCheck(func(context.Context) error {
		if atomic.AddInt32(&healthCalls, 1) == 1 {
			return fmt.Errorf("service unhealthy after restart")
		}
		return nil // rollback health passes
	})
	id := newRestoreTestBackup(t, s, "health-fail")

	result, err := s.RestoreBackup(context.Background(), id)
	if err == nil {
		t.Fatal("expected health failure error")
	}
	if result == nil || result.Status != RestoreStatusRolledBack || !result.RolledBack {
		t.Fatalf("expected rolled_back, got %+v", result)
	}
}

// 5 & 6. health succeeds only after restart → success; and no success result
// is produced before BOTH restart and health complete (ordering + gating).
func TestRestore_HealthAfterRestart_Success_Ordered(t *testing.T) {
	s := testService(t)
	var order []string
	restartDone := false
	s.SetRestoreRestart(func(context.Context) error {
		order = append(order, "restart")
		restartDone = true
		return nil
	})
	s.SetRestoreHealthCheck(func(context.Context) error {
		if !restartDone {
			t.Error("health check ran before restart completed")
		}
		order = append(order, "health")
		return nil
	})
	id := newRestoreTestBackup(t, s, "ordered-success")

	result, err := s.RestoreBackup(context.Background(), id)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if result == nil || result.Status != RestoreStatusActivated {
		t.Fatalf("expected activated, got %+v", result)
	}
	if result.Message != RestoreActivatedMessage {
		t.Fatalf("expected %q, got %q", RestoreActivatedMessage, result.Message)
	}
	if len(order) != 2 || order[0] != "restart" || order[1] != "health" {
		t.Fatalf("expected restart before health, got %v", order)
	}
}
