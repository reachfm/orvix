package policy

import (
	"fmt"
	"sync"
	"testing"
)

func uintPtr(u uint) *uint { return &u }

// ── Policy Evaluation Tests ─────────────────────────────────

func TestTenantPolicy(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, InternalOnly)

	res := e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: Internal, TenantID: 1, Domain: "test.com",
	})
	if res.Action != ActionAllow {
		t.Fatalf("expected allow for internal send, got %s", res.Reason)
	}

	res = e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: External, TenantID: 1, Domain: "test.com",
	})
	if res.Action != ActionBlock {
		t.Fatalf("expected block for external send, got %s", res.Reason)
	}
}

func TestDomainOverride(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, Disabled)
	e.SetDomainPolicy("override.com", AllowAll)

	// Without domain override, should be blocked (tenant disabled).
	res := e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: Internal, TenantID: 1, Domain: "other.com",
	})
	if res.Action != ActionBlock {
		t.Fatal("expected block for non-override domain")
	}

	// With domain override, should be allowed.
	res = e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: Internal, TenantID: 1, Domain: "override.com",
	})
	if res.Action != ActionAllow {
		t.Fatal("expected allow for override domain")
	}
}

func TestMailboxOverride(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, Disabled)
	e.SetDomainPolicy("test.com", Disabled)
	e.SetMailboxPolicy(5, AllowAll)

	res := e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: Internal, TenantID: 1, Domain: "test.com",
		MailboxID: uintPtr(5),
	})
	if res.Action != ActionAllow {
		t.Fatal("expected allow for mailbox override")
	}

	// Different mailbox without override should be blocked.
	res = e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: Internal, TenantID: 1, Domain: "test.com",
		MailboxID: uintPtr(3),
	})
	if res.Action != ActionBlock {
		t.Fatal("expected block for non-override mailbox")
	}
}

func TestPrecedenceOrder(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, InternalOnly)
	e.SetDomainPolicy("test.com", AllowAll)
	e.SetMailboxPolicy(5, Disabled)

	// Mailbox takes precedence over domain.
	res := e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: Internal, TenantID: 1, Domain: "test.com",
		MailboxID: uintPtr(5),
	})
	if res.Policy.Level != "mailbox" {
		t.Fatalf("expected mailbox level, got %s", res.Policy.Level)
	}
	if res.Action != ActionBlock {
		t.Fatal("expected block from mailbox policy")
	}
}

// ── Policy Mode Tests ───────────────────────────────────────

func TestModeAllowAll(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, AllowAll)

	cases := []struct {
		dir Direction
		sc  Scope
	}{
		{Send, Internal},
		{Send, External},
		{Receive, Internal},
		{Receive, External},
	}
	for _, c := range cases {
		res := e.Evaluate(&EvaluationRequest{
			Direction: c.dir, Scope: c.sc, TenantID: 1, Domain: "t.com",
		})
		if res.Action != ActionAllow {
			t.Fatalf("AllowAll: expected allow for %s %s", dirString(c.dir), scopeString(c.sc))
		}
	}
}

func TestModeInternalOnly(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, InternalOnly)

	if r := e.Evaluate(&EvaluationRequest{Send, Internal, 1, "t.com", nil}); r.Action != ActionAllow {
		t.Fatal("InternalOnly: internal send should be allowed")
	}
	if r := e.Evaluate(&EvaluationRequest{Send, External, 1, "t.com", nil}); r.Action != ActionBlock {
		t.Fatal("InternalOnly: external send should be blocked")
	}
}

func TestModeExternalOnly(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, ExternalOnly)

	if r := e.Evaluate(&EvaluationRequest{Send, External, 1, "t.com", nil}); r.Action != ActionAllow {
		t.Fatal("ExternalOnly: external send should be allowed")
	}
	if r := e.Evaluate(&EvaluationRequest{Send, Internal, 1, "t.com", nil}); r.Action != ActionBlock {
		t.Fatal("ExternalOnly: internal send should be blocked")
	}
}

func TestModeSendOnly(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, SendOnly)

	if r := e.Evaluate(&EvaluationRequest{Send, Internal, 1, "t.com", nil}); r.Action != ActionAllow {
		t.Fatal("SendOnly: send should be allowed")
	}
	if r := e.Evaluate(&EvaluationRequest{Receive, Internal, 1, "t.com", nil}); r.Action != ActionBlock {
		t.Fatal("SendOnly: receive should be blocked")
	}
}

func TestModeReceiveOnly(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, ReceiveOnly)

	if r := e.Evaluate(&EvaluationRequest{Receive, Internal, 1, "t.com", nil}); r.Action != ActionAllow {
		t.Fatal("ReceiveOnly: receive should be allowed")
	}
	if r := e.Evaluate(&EvaluationRequest{Send, Internal, 1, "t.com", nil}); r.Action != ActionBlock {
		t.Fatal("ReceiveOnly: send should be blocked")
	}
}

func TestModeDisabled(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, Disabled)

	if r := e.Evaluate(&EvaluationRequest{Send, Internal, 1, "t.com", nil}); r.Action != ActionBlock {
		t.Fatal("Disabled: send should be blocked")
	}
	if r := e.Evaluate(&EvaluationRequest{Receive, Internal, 1, "t.com", nil}); r.Action != ActionBlock {
		t.Fatal("Disabled: receive should be blocked")
	}
}

// ── Default Mode Tests ──────────────────────────────────────

func TestDefaultMode(t *testing.T) {
	e := NewEngine()
	if e.Resolve(1, "test.com", nil).Mode != AllowAll {
		t.Fatal("default mode should be AllowAll")
	}

	e.SetDefaultMode(InternalOnly)
	if e.Resolve(1, "test.com", nil).Mode != InternalOnly {
		t.Fatal("default mode should be changed")
	}
}

// ── SMTP Integration Pattern Tests ─────────────────────────

func TestOutboundBlocked(t *testing.T) {
	e := NewEngine()
	e.SetDomainPolicy("blocked.com", Disabled)

	res := e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: External, TenantID: 1, Domain: "blocked.com",
	})
	if res.Action != ActionBlock {
		t.Fatal("outbound from blocked domain should be blocked")
	}
}

func TestOutboundAllowed(t *testing.T) {
	e := NewEngine()
	e.SetDomainPolicy("allowed.com", AllowAll)

	res := e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: External, TenantID: 1, Domain: "allowed.com",
	})
	if res.Action != ActionAllow {
		t.Fatal("outbound from allowed domain should be allowed")
	}
}

func TestInboundBlocked(t *testing.T) {
	e := NewEngine()
	e.SetDomainPolicy("no-inbound.com", SendOnly)

	res := e.Evaluate(&EvaluationRequest{
		Direction: Receive, Scope: External, TenantID: 1, Domain: "no-inbound.com",
	})
	if res.Action != ActionBlock {
		t.Fatal("inbound to SendOnly domain should be blocked")
	}
}

func TestInboundAllowed(t *testing.T) {
	e := NewEngine()
	e.SetDomainPolicy("receive.com", ReceiveOnly)

	res := e.Evaluate(&EvaluationRequest{
		Direction: Receive, Scope: External, TenantID: 1, Domain: "receive.com",
	})
	if res.Action != ActionAllow {
		t.Fatal("inbound to ReceiveOnly domain should be allowed")
	}
}

// ── Concurrency Tests ───────────────────────────────────────

func TestConcurrentPolicyEvaluation(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, InternalOnly)

	var wg sync.WaitGroup
	errs := make(chan error, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// Concurrent policy CRUD.
			if id%2 == 0 {
				e.SetTenantPolicy(uint(id), AllowAll)
			} else {
				r := e.Evaluate(&EvaluationRequest{
					Direction: Send, Scope: Internal, TenantID: uint(id % 5), Domain: "test.com",
				})
				if r.Action != ActionAllow && r.Action != ActionBlock {
					errs <- fmt.Errorf("invalid action %d", r.Action)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Fatal(e)
	}
}

// ── Delete Policy Tests ─────────────────────────────────────

func TestDeletePolicyFallsBack(t *testing.T) {
	e := NewEngine()
	e.SetTenantPolicy(1, Disabled)
	e.SetDomainPolicy("test.com", AllowAll)

	// Delete domain policy — should fall back to tenant policy.
	e.DeleteDomainPolicy("test.com")

	res := e.Evaluate(&EvaluationRequest{
		Direction: Send, Scope: Internal, TenantID: 1, Domain: "test.com",
	})
	if res.Action != ActionBlock {
		t.Fatal("after delete, should fall back to tenant policy (disabled)")
	}
}
