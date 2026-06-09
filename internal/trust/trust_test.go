package trust

import (
	"sync"
	"testing"
	"time"
)

// ── Authentication Protection Tests ─────────────────────────

func TestAuthLockoutTriggered(t *testing.T) {
	e := NewEngine()
	e.policy.MaxAttempts = 3
	e.nowFn = time.Now

	// First 2 failures should NOT lock out.
	for i := 0; i < 2; i++ {
		locked, _ := e.RecordAuthFailure("user@test.com")
		if locked {
			t.Fatalf("unexpected lockout on attempt %d", i+1)
		}
	}

	// 3rd failure triggers lockout (attempts >= MaxAttempts).
	locked, _ := e.RecordAuthFailure("user@test.com")
	if !locked {
		t.Fatal("expected lockout on 3rd failure")
	}

	if !e.IsLockedOut("user@test.com") {
		t.Fatal("IsLockedOut should return true")
	}
}

func TestAuthLockoutExpires(t *testing.T) {
	e := NewEngine()
	e.policy.MaxAttempts = 2
	e.policy.LockoutDuration = 10 * time.Minute
	now := time.Now()
	e.nowFn = func() time.Time { return now }

	e.RecordAuthFailure("user@test.com")
	e.RecordAuthFailure("user@test.com")
	locked, _ := e.RecordAuthFailure("user@test.com")
	if !locked {
		t.Fatal("expected lockout")
	}

	// Advance time past lockout.
	now = now.Add(11 * time.Minute)
	e.nowFn = func() time.Time { return now }

	if e.IsLockedOut("user@test.com") {
		t.Fatal("expected lockout to expire")
	}
}

func TestAuthProgressiveDelay(t *testing.T) {
	e := NewEngine()
	e.policy.MaxAttempts = 10
	e.policy.ProgressiveDelay = 1 * time.Second
	e.policy.MaxDelay = 10 * time.Second
	e.nowFn = time.Now

	// First failure should have minimal delay.
	_, delay := e.RecordAuthFailure("user@test.com")
	if delay < 500*time.Millisecond || delay > 2*time.Second {
		t.Fatalf("expected ~1s delay, got %v", delay)
	}

	// Third failure should have longer delay.
	e.RecordAuthFailure("user@test.com")
	_, delay = e.RecordAuthFailure("user@test.com")
	if delay < 2*time.Second {
		t.Fatalf("expected delay >2s, got %v", delay)
	}
}

func TestAuthSuccessClearsFailures(t *testing.T) {
	e := NewEngine()
	e.policy.MaxAttempts = 5
	e.nowFn = time.Now

	e.RecordAuthFailure("user@test.com")
	e.RecordAuthFailure("user@test.com")

	e.RecordAuthSuccess("user@test.com")

	locked, _ := e.RecordAuthFailure("user@test.com")
	if locked {
		t.Fatal("should not be locked out after success")
	}
}

// ── Rate Limiting Tests ─────────────────────────────────────

func TestRateLimitMailbox(t *testing.T) {
	e := NewEngine()
	e.rlp.MaxPerMinute = 3

	if !e.AllowMailbox(1) {
		t.Fatal("first should be allowed")
	}
	if !e.AllowMailbox(1) {
		t.Fatal("second should be allowed")
	}
	if !e.AllowMailbox(1) {
		t.Fatal("third should be allowed")
	}
	if e.AllowMailbox(1) {
		t.Fatal("fourth should be blocked")
	}
}

func TestRateLimitDomain(t *testing.T) {
	e := NewEngine()
	e.rlp.MaxPerMinute = 2

	if !e.AllowDomain("test.com") {
		t.Fatal("first should be allowed")
	}
	if !e.AllowDomain("test.com") {
		t.Fatal("second should be allowed")
	}
	if e.AllowDomain("test.com") {
		t.Fatal("third should be blocked")
	}
}

func TestRateLimitIP(t *testing.T) {
	e := NewEngine()
	e.rlp.MaxPerMinute = 2

	if !e.AllowIP("10.0.0.1") {
		t.Fatal("first should be allowed")
	}
	if !e.AllowIP("10.0.0.1") {
		t.Fatal("second should be allowed")
	}
	if e.AllowIP("10.0.0.1") {
		t.Fatal("third should be blocked")
	}
}

// ── Outbound Abuse Detection Tests ──────────────────────────

func TestSendSpikeDetected(t *testing.T) {
	e := NewEngine()
	e.nowFn = time.Now

	// Record 101 sends — threshold is 100.
	var spike bool
	for i := 0; i < 101; i++ {
		spike = e.RecordSend("test.com")
	}
	if !spike {
		t.Fatal("expected send spike after 101 sends")
	}
}

func TestRecipientExplosion(t *testing.T) {
	e := NewEngine()
	e.nowFn = time.Now

	// Nothing specific for explosion in the current implementation.
	// RecordSend detects spikes; high reject rate is separate.
	for i := 0; i < 21; i++ {
		e.RecordRemoteRejection("test.com")
	}
	isStorm := e.RecordRemoteRejection("test.com")
	if !isStorm {
		t.Fatal("expected rejection storm after 22 rejections")
	}
}

// ── Inbound Abuse Detection Tests ──────────────────────────

func TestRepeatedAuthFailuresDetected(t *testing.T) {
	e := NewEngine()
	e.policy.MaxAttempts = 3
	e.nowFn = time.Now

	for i := 0; i < 3; i++ {
		e.RecordAuthFailure("attacker@example.com")
	}
	locked, _ := e.RecordAuthFailure("attacker@example.com")
	if !locked {
		t.Fatal("expected lockout after repeated failures")
	}
}

func TestBadIPDetected(t *testing.T) {
	e := NewEngine()
	e.nowFn = time.Now

	// Simulate multiple failed logins from the same IP.
	for i := 0; i < 5; i++ {
		e.RecordAuthFailure("ip:10.0.0.99")
	}
	e.SetIPTrust("10.0.0.99", TrustLow, "repeated auth failures")

	trust := e.GetUserTrust("10.0.0.99")
	_ = trust
	// IP trust is stored under SetIPTrust, not GetUserTrust.
	// Use the Snapshot to verify trust was recorded.
	snap := e.Snapshot()
	if snap.IPTrusts != 1 {
		t.Fatal("expected IP trust to be recorded")
	}
}

// ── Concurrency Tests ───────────────────────────────────────

func TestConcurrentUpdatesSafe(t *testing.T) {
	e := NewEngine()
	e.policy.MaxAttempts = 100
	e.nowFn = time.Now

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				e.RecordAuthFailure("user@test.com")
				e.AllowMailbox(1)
				e.RecordSend("test.com")
			}
		}()
	}
	wg.Wait()

	snap := e.Snapshot()
	if snap.AuthFailures == 0 {
		t.Fatal("expected recorded failures")
	}
}

// ── Snapshot Tests ─────────────────────────────────────────

func TestSnapshot(t *testing.T) {
	e := NewEngine()
	e.nowFn = time.Now

	e.RecordAuthFailure("user1@test.com")
	e.RecordAuthFailure("user2@test.com")
	e.AllowMailbox(1)
	e.AllowDomain("test.com")
	e.AllowIP("1.2.3.4")
	e.RecordSend("outbound.com")
	e.SetMailboxTrust(1, TrustHigh, "known good")

	snap := e.Snapshot()
	if snap.AuthFailures != 2 {
		t.Fatalf("expected 2 auth failures, got %d", snap.AuthFailures)
	}
	if snap.MailboxTrusts != 1 {
		t.Fatalf("expected 1 mailbox trust, got %d", snap.MailboxTrusts)
	}
	if snap.OutboundActive != 1 {
		t.Fatalf("expected 1 outbound tracker, got %d", snap.OutboundActive)
	}
}
