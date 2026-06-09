package trustmgmt

import (
	"context"
	"testing"

	"github.com/orvix/orvix/internal/trust"
)

func TestTrustSummary(t *testing.T) {
	eng := trust.NewEngine()
	svc := NewService(eng)
	ctx := context.Background()

	sum := svc.Summary(ctx)
	if sum == nil {
		t.Fatal("expected non-nil summary")
	}
}

func TestListLockouts(t *testing.T) {
	eng := trust.NewEngine()
	svc := NewService(eng)
	ctx := context.Background()

	// Trigger a lockout.
	eng.RecordAuthFailure("test:user@test.com")
	eng.RecordAuthFailure("test:user@test.com")
	eng.RecordAuthFailure("test:user@test.com")
	eng.RecordAuthFailure("test:user@test.com")
	eng.RecordAuthFailure("test:user@test.com") // 5th triggers lockout

	lockouts := svc.ListLockouts(ctx)
	if len(lockouts) == 0 {
		t.Fatal("expected at least 1 lockout")
	}
}

func TestClearLockout(t *testing.T) {
	eng := trust.NewEngine()
	svc := NewService(eng)
	ctx := context.Background()

	eng.RecordAuthFailure("test:clear@test.com")
	eng.RecordAuthFailure("test:clear@test.com")
	eng.RecordAuthFailure("test:clear@test.com")
	eng.RecordAuthFailure("test:clear@test.com")
	eng.RecordAuthFailure("test:clear@test.com")

	if err := svc.ClearLockout(ctx, "test:clear@test.com"); err != nil {
		t.Fatalf("clear lockout: %v", err)
	}

	// Verify cleared.
	if eng.IsLockedOut("test:clear@test.com") {
		t.Fatal("lockout should be cleared")
	}
}

func TestClearLockoutNotFound(t *testing.T) {
	eng := trust.NewEngine()
	svc := NewService(eng)
	ctx := context.Background()

	if err := svc.ClearLockout(ctx, "nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent lockout")
	}
}
