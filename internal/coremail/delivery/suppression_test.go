package delivery

import (
	"context"
	"testing"

	"github.com/orvix/orvix/internal/coremail/queue"
)

type fakeSuppressionChecker struct {
	suppressed map[string]bool
}

func (f *fakeSuppressionChecker) IsSuppressed(ctx context.Context, tenantID uint, address string) (bool, error) {
	return f.suppressed[address], nil
}

func TestDeliverRemote_SuppressedRecipientNeverDials(t *testing.T) {
	resolver := NewFakeResolver() // no MX entries configured — a real dial attempt would fail with mx lookup error, not "recipient is suppressed"
	w := &DeliveryWorker{
		Resolver:           resolver,
		WorkerID:           "test",
		SuppressionChecker: &fakeSuppressionChecker{suppressed: map[string]bool{"blocked@test.com": true}},
	}
	result := w.deliverRemote(context.Background(), &queue.QueueEntry{RecipientDomain: "test.com", ToAddress: "blocked@test.com"})
	if result.Success {
		t.Fatal("expected a suppressed recipient to never succeed")
	}
	if result.TempFail {
		t.Fatal("expected a suppressed recipient to fail permanently, not temporarily")
	}
	if result.StatusMsg != "recipient is suppressed" {
		t.Fatalf("expected the stable suppression message, got %q", result.StatusMsg)
	}
}

func TestDeliverRemote_NonSuppressedRecipientProceedsToMXLookup(t *testing.T) {
	resolver := NewFakeResolver()
	resolver.FailDomain = "test.com" // force a distinguishable failure path past the suppression check
	w := &DeliveryWorker{
		Resolver:           resolver,
		WorkerID:           "test",
		SuppressionChecker: &fakeSuppressionChecker{suppressed: map[string]bool{"blocked@test.com": true}},
	}
	result := w.deliverRemote(context.Background(), &queue.QueueEntry{RecipientDomain: "test.com", ToAddress: "allowed@test.com"})
	if result.StatusMsg == "recipient is suppressed" {
		t.Fatal("a non-suppressed recipient must not be rejected as suppressed")
	}
	if !result.TempFail {
		t.Fatalf("expected the MX-lookup failure path (TempFail=true), got %+v", result)
	}
}

func TestDeliverRemote_NilSuppressionCheckerNeverBlocks(t *testing.T) {
	resolver := NewFakeResolver()
	resolver.FailDomain = "test.com"
	w := &DeliveryWorker{Resolver: resolver, WorkerID: "test"} // SuppressionChecker left nil
	result := w.deliverRemote(context.Background(), &queue.QueueEntry{RecipientDomain: "test.com", ToAddress: "anyone@test.com"})
	if result.StatusMsg == "recipient is suppressed" {
		t.Fatal("a nil SuppressionChecker must never suppress anything")
	}
}
