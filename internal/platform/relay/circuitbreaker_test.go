package relay

import (
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedIsAlwaysAvailable(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	state, ok := cb.IsAvailable(CircuitClosed, nil, time.Now())
	if !ok || state != CircuitClosed {
		t.Fatalf("expected closed/available, got %s/%v", state, ok)
	}
}

func TestCircuitBreaker_OpenBlocksUntilCooldownElapses(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	openedAt := time.Now().Add(-30 * time.Second)
	state, ok := cb.IsAvailable(CircuitOpen, &openedAt, time.Now())
	if ok || state != CircuitOpen {
		t.Fatalf("expected still open/unavailable before cooldown, got %s/%v", state, ok)
	}

	openedAt = time.Now().Add(-90 * time.Second)
	state, ok = cb.IsAvailable(CircuitOpen, &openedAt, time.Now())
	if !ok || state != CircuitHalfOpen {
		t.Fatalf("expected half_open/available after cooldown, got %s/%v", state, ok)
	}
}

func TestCircuitBreaker_OpensAfterThresholdFailures(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	now := time.Now()
	state, failures := CircuitClosed, 0
	var openedAt *time.Time
	for i := 0; i < 3; i++ {
		state, failures, openedAt = cb.OnFailure(state, failures, now)
	}
	if state != CircuitOpen {
		t.Fatalf("expected circuit to open after 3 failures, got %s (failures=%d)", state, failures)
	}
	if openedAt == nil {
		t.Fatal("expected openedAt to be set when the circuit opens")
	}
}

func TestCircuitBreaker_BelowThresholdStaysClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	state, failures, openedAt := cb.OnFailure(CircuitClosed, 0, time.Now())
	if state != CircuitClosed || failures != 1 || openedAt != nil {
		t.Fatalf("expected closed/1/nil after a single failure, got %s/%d/%v", state, failures, openedAt)
	}
}

func TestCircuitBreaker_SuccessResetsToClosedFromAnyState(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	state, failures := cb.OnSuccess()
	if state != CircuitClosed || failures != 0 {
		t.Fatalf("expected closed/0, got %s/%d", state, failures)
	}
}

func TestCircuitBreaker_HalfOpenFailureReopensImmediately(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	now := time.Now()
	state, failures, openedAt := cb.OnFailure(CircuitHalfOpen, 3, now)
	if state != CircuitOpen {
		t.Fatalf("expected a half-open trial failure to reopen immediately, got %s", state)
	}
	if openedAt == nil {
		t.Fatal("expected openedAt to be reset on re-open")
	}
	_ = failures
}
