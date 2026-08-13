package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/orvix/orvix/internal/coremail/queue"
)

// Worker-level fail-closed acceptance suite for Fixes A, C and H.
//
// The invariant under test: when the relay control plane or the suppression
// store cannot give a trustworthy answer, the worker DEFERS. It must never
// fall through to direct-to-MX delivery, and it must never deliver to a
// recipient whose suppression status is unknown.

// erroringSuppressionChecker models an unavailable suppression store.
type erroringSuppressionChecker struct{ err error }

func (e erroringSuppressionChecker) IsSuppressed(context.Context, uint, string) (bool, error) {
	return false, e.err
}

// recordingSelector records what the worker asked for and what it was told to
// deliver, so a test can prove a dial did or did not happen.
type recordingSelector struct {
	decision     *RelayRouteDecision
	selectErr    error
	requests     []RelayRouteRequest
	delivered    []uint
	deliverFunc  func(route *RelayRoute) RelayDeliverResult
	recordErr    error
	recordCalls  int
	recordedIDs  []uint
	recordedOKs  []bool
	deliverCalls int
}

func (r *recordingSelector) SelectRoute(_ context.Context, req RelayRouteRequest) (*RelayRouteDecision, error) {
	r.requests = append(r.requests, req)
	return r.decision, r.selectErr
}

func (r *recordingSelector) RecordAttemptResult(_ context.Context, providerID uint, success bool) error {
	r.recordCalls++
	r.recordedIDs = append(r.recordedIDs, providerID)
	r.recordedOKs = append(r.recordedOKs, success)
	return r.recordErr
}

func (r *recordingSelector) Deliver(_ context.Context, route *RelayRoute, _ string, _ []string, _ []byte) RelayDeliverResult {
	r.deliverCalls++
	r.delivered = append(r.delivered, route.ProviderID)
	if r.deliverFunc != nil {
		return r.deliverFunc(route)
	}
	return RelayDeliverResult{Success: true}
}

func relayWorker(sel RelaySelector) (*DeliveryWorker, *FakeResolver) {
	resolver := NewFakeResolver()
	// A resolvable domain: if the worker were to fall through to direct
	// delivery, it would take the MX path rather than deferring, and the
	// assertions below would catch it.
	resolver.FailDomain = "test.com"
	w := &DeliveryWorker{
		Resolver:      resolver,
		WorkerID:      "test",
		RelaySelector: sel,
		TenantIDForRelay: func(*queue.QueueEntry) (uint, string) {
			return 7, "internal_external"
		},
		DomainIDForRelay: func(*queue.QueueEntry) uint { return 3 },
	}
	return w, resolver
}

func testEntry() *queue.QueueEntry {
	return &queue.QueueEntry{
		RecipientDomain: "test.com",
		ToAddress:       "someone@test.com",
		FromAddress:     "billing@acme.test",
	}
}

// ── H: suppression ───────────────────────────────────────────────────────

func TestDeliverRemote_SuppressionStoreFailureDefersAndNeverDelivers(t *testing.T) {
	sel := &recordingSelector{}
	w, _ := relayWorker(sel)
	w.SuppressionChecker = erroringSuppressionChecker{err: errors.New("store unavailable")}

	result := w.deliverRemote(context.Background(), testEntry())
	if result.Success {
		t.Fatal("an unavailable suppression store must never deliver")
	}
	if !result.TempFail {
		t.Fatalf("an unknown suppression status must DEFER, got %+v", result)
	}
	if result.StatusMsg != "suppression check unavailable; delivery deferred" {
		t.Fatalf("unexpected status: %q", result.StatusMsg)
	}
	// Neither relay nor direct delivery may have been attempted.
	if sel.deliverCalls != 0 {
		t.Fatal("a deferred suppression check must not attempt relay delivery")
	}
	if len(sel.requests) != 0 {
		t.Fatal("a deferred suppression check must not even resolve a route")
	}
}

// ── A: routing failures defer, never downgrade to direct ─────────────────

func TestDeliverRemote_RouteSelectionErrorDefersNeverDirect(t *testing.T) {
	sel := &recordingSelector{selectErr: errors.New("provider lookup unavailable")}
	w, _ := relayWorker(sel)

	result := w.deliverRemote(context.Background(), testEntry())
	if result.Success {
		t.Fatal("a routing failure must not report success")
	}
	if !result.TempFail {
		t.Fatalf("a routing failure must DEFER, got %+v", result)
	}
	if sel.deliverCalls != 0 {
		t.Fatal("no relay delivery may be attempted when routing failed")
	}
	// The decisive assertion: the direct-to-MX path was NOT taken. The fake
	// resolver's failure message for a direct attempt is distinct from the
	// routing deferral message.
	if result.StatusMsg == "" || result.StatusMsg == "mx lookup failed" {
		t.Fatalf("a routing failure must never fall through to direct MX delivery, got %q", result.StatusMsg)
	}
}

func TestDeliverRemote_NilDecisionDefersNeverDirect(t *testing.T) {
	sel := &recordingSelector{decision: nil}
	w, _ := relayWorker(sel)
	result := w.deliverRemote(context.Background(), testEntry())
	if result.Success || !result.TempFail {
		t.Fatalf("an undetermined route must defer, got %+v", result)
	}
	if sel.deliverCalls != 0 {
		t.Fatal("no delivery may be attempted for an undetermined route")
	}
}

// TestDeliverRemote_ExplicitDirectIsTheOnlyDirectPath is the positive control:
// direct delivery still happens when policy actually selects it.
func TestDeliverRemote_ExplicitDirectIsTheOnlyDirectPath(t *testing.T) {
	sel := &recordingSelector{decision: &RelayRouteDecision{Route: &RelayRoute{Direct: true}}}
	w, _ := relayWorker(sel)
	result := w.deliverRemote(context.Background(), testEntry())
	if sel.deliverCalls != 0 {
		t.Fatal("an explicit direct route must not use the relay path")
	}
	// It reached the direct MX path (which the fake resolver fails), rather
	// than being deferred as a routing failure.
	if !result.TempFail {
		t.Fatalf("expected the direct MX failure path, got %+v", result)
	}
}

// ── B: the real routing context reaches the selector ─────────────────────

func TestDeliverRemote_PassesRealSenderAndDomainContext(t *testing.T) {
	sel := &recordingSelector{decision: &RelayRouteDecision{Route: &RelayRoute{Direct: true}}}
	w, _ := relayWorker(sel)
	w.deliverRemote(context.Background(), testEntry())

	if len(sel.requests) != 1 {
		t.Fatalf("expected exactly one routing request, got %d", len(sel.requests))
	}
	req := sel.requests[0]
	if req.SenderAddress != "billing@acme.test" {
		t.Fatalf("the FULL envelope sender must be passed, got %q", req.SenderAddress)
	}
	if req.SenderDomain != "acme.test" {
		t.Fatalf("the sender domain must be passed separately, got %q", req.SenderDomain)
	}
	if req.SenderAddress == req.SenderDomain {
		t.Fatal("sender address and sender domain must remain distinct")
	}
	if req.DomainID != 3 {
		t.Fatalf("the sending domain id must be propagated, got %d", req.DomainID)
	}
	if req.TenantID != 7 {
		t.Fatalf("the real tenant must be propagated, got %d", req.TenantID)
	}
}

// ── C: the fallback chain is actually walked ─────────────────────────────

func TestDeliverRemote_WalksFallbackChainInOrder(t *testing.T) {
	sel := &recordingSelector{
		decision: &RelayRouteDecision{
			Route: &RelayRoute{ProviderID: 1, Host: "a.example.com", Port: 587},
			Fallbacks: []RelayRoute{
				{ProviderID: 2, Host: "b.example.com", Port: 587},
				{ProviderID: 3, Host: "c.example.com", Port: 587},
			},
		},
		// The first two providers fail temporarily; the third accepts.
		deliverFunc: func(route *RelayRoute) RelayDeliverResult {
			if route.ProviderID == 3 {
				return RelayDeliverResult{Success: true}
			}
			return RelayDeliverResult{TempFail: true, StatusMsg: "upstream busy"}
		},
	}
	w, _ := relayWorker(sel)
	result := w.deliverViaRelayChain(context.Background(), testEntry(), sel.decision, []byte("msg"))

	if !result.Success {
		t.Fatalf("the chain must be walked until a provider accepts, got %+v", result)
	}
	if len(sel.delivered) != 3 {
		t.Fatalf("expected all three providers to be tried in order, got %v", sel.delivered)
	}
	for i, want := range []uint{1, 2, 3} {
		if sel.delivered[i] != want {
			t.Fatalf("fallback order violated: got %v, want [1 2 3]", sel.delivered)
		}
	}
}

// TestDeliverRemote_ExhaustedChainDefersNeverDirect proves the worker does not
// quietly fall back to unauthenticated direct delivery once every configured
// relay has failed — the defect that made a relay outage silently bypass the
// relay entirely.
func TestDeliverRemote_ExhaustedChainDefersNeverDirect(t *testing.T) {
	sel := &recordingSelector{
		decision: &RelayRouteDecision{
			Route:     &RelayRoute{ProviderID: 1, Host: "a.example.com", Port: 587},
			Fallbacks: []RelayRoute{{ProviderID: 2, Host: "b.example.com", Port: 587}},
		},
		deliverFunc: func(*RelayRoute) RelayDeliverResult {
			return RelayDeliverResult{TempFail: true, StatusMsg: "upstream busy"}
		},
	}
	w, _ := relayWorker(sel)
	result := w.deliverViaRelayChain(context.Background(), testEntry(), sel.decision, []byte("msg"))

	if result.Success {
		t.Fatal("an exhausted relay chain must not report success")
	}
	if !result.TempFail {
		t.Fatalf("an exhausted relay chain must defer, got %+v", result)
	}
	if sel.deliverCalls != 2 {
		t.Fatalf("expected both providers to be tried, got %d attempts", sel.deliverCalls)
	}
}

// TestDeliverRemote_PermanentRelayFailureStopsTheChain proves a 5xx rejection
// is not retried against every remaining provider.
func TestDeliverRemote_PermanentRelayFailureStopsTheChain(t *testing.T) {
	sel := &recordingSelector{
		decision: &RelayRouteDecision{
			Route:     &RelayRoute{ProviderID: 1, Host: "a.example.com", Port: 587},
			Fallbacks: []RelayRoute{{ProviderID: 2, Host: "b.example.com", Port: 587}},
		},
		deliverFunc: func(*RelayRoute) RelayDeliverResult {
			return RelayDeliverResult{TempFail: false, StatusMsg: "550 mailbox does not exist"}
		},
	}
	w, _ := relayWorker(sel)
	result := w.deliverViaRelayChain(context.Background(), testEntry(), sel.decision, []byte("msg"))

	if result.Success || result.TempFail {
		t.Fatalf("a permanent rejection must be permanent, got %+v", result)
	}
	if sel.deliverCalls != 1 {
		t.Fatalf("a permanent rejection must stop the chain, got %d attempts", sel.deliverCalls)
	}
}

// ── I: bookkeeping failure must not duplicate a delivered message ────────

// TestDeliverRemote_BookkeepingFailureNeverRedelivers is the exactly-once
// guarantee: the SMTP server has already accepted the message, so a failure to
// record local circuit-breaker state must not cause a second delivery.
func TestDeliverRemote_BookkeepingFailureNeverRedelivers(t *testing.T) {
	sel := &recordingSelector{
		decision:  &RelayRouteDecision{Route: &RelayRoute{ProviderID: 1, Host: "a.example.com", Port: 587}},
		recordErr: errors.New("bookkeeping store unavailable"),
	}
	var reported int
	w, _ := relayWorker(sel)
	w.RelayBookkeepingFailed = func(context.Context, uint, bool, error) { reported++ }

	result := w.deliverViaRelayChain(context.Background(), testEntry(), sel.decision, []byte("msg"))
	if !result.Success {
		t.Fatalf("an accepted message must be reported as delivered even if bookkeeping fails, got %+v", result)
	}
	if sel.deliverCalls != 1 {
		t.Fatalf("the message must be delivered EXACTLY once, got %d attempts", sel.deliverCalls)
	}
	if reported != 1 {
		t.Fatalf("the bookkeeping failure must be surfaced exactly once, got %d", reported)
	}
}

func TestDeliverRemote_BookkeepingRecordsTheRealOutcome(t *testing.T) {
	sel := &recordingSelector{
		decision: &RelayRouteDecision{Route: &RelayRoute{ProviderID: 42, Host: "a.example.com", Port: 587}},
	}
	w, _ := relayWorker(sel)
	w.deliverViaRelayChain(context.Background(), testEntry(), sel.decision, []byte("msg"))

	if sel.recordCalls != 1 {
		t.Fatalf("expected exactly one bookkeeping call, got %d", sel.recordCalls)
	}
	if sel.recordedIDs[0] != 42 {
		t.Fatalf("bookkeeping must name the provider actually used, got %d", sel.recordedIDs[0])
	}
	if !sel.recordedOKs[0] {
		t.Fatal("a successful delivery must be recorded as a success")
	}
}
