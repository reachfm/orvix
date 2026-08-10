package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaimRaceOnlyOneWorkerOwnsLease(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)
	job, _, err := svc.Submit(context.Background(), validSubmission("claim-race"))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	claimed := make(chan *Job, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			got, claimErr := svc.Claim(context.Background(), "worker-"+string(rune('a'+worker)), time.Minute)
			claimed <- got
			errs <- claimErr
		}(i)
	}
	wg.Wait()
	close(claimed)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	for got := range claimed {
		if got != nil {
			winners++
			if got.ID != job.ID || got.LeaseToken == "" || got.LeaseVersion != 1 {
				t.Fatalf("invalid claim: %+v", got)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners=%d", winners)
	}
}

func TestHeartbeatProgressAndStaleLeaseFencing(t *testing.T) {
	svc, _, _, clock, _ := newTestService(t)
	job, _, _ := svc.Submit(context.Background(), validSubmission("heartbeat"))
	claimed, err := svc.Claim(context.Background(), "worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	lease := leaseFor(claimed)
	clock.Advance(20 * time.Second)
	if err = svc.Heartbeat(context.Background(), lease, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = svc.UpdateProgress(context.Background(), lease, 45); err != nil {
		t.Fatal(err)
	}
	stored, _ := svc.Get(context.Background(), job.ID, 7, ScopeTenant)
	if stored.Progress != 45 || stored.HeartbeatAt == nil || !stored.LeaseExpiresAt.Equal(clock.Now().Add(2*time.Minute)) {
		t.Fatalf("heartbeat/progress not persisted: %+v", stored)
	}
	stale := lease
	stale.Token = "stale"
	if err = svc.Heartbeat(context.Background(), stale, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale heartbeat err=%v", err)
	}
	if err = svc.Complete(context.Background(), stale, json.RawMessage(`{"ok":true}`)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale completion err=%v", err)
	}
	if err = svc.Fail(context.Background(), stale, "STALE", "stale", false); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale failure err=%v", err)
	}
}

func TestLeaseExpiryRecoveryAndMaxAttempts(t *testing.T) {
	svc, _, _, clock, _ := newTestService(t)
	input := validSubmission("recover")
	input.MaxAttempts = 2
	job, _, _ := svc.Submit(context.Background(), input)
	first, _ := svc.Claim(context.Background(), "worker-a", time.Minute)
	firstLease := leaseFor(first)
	clock.Advance(61 * time.Second)
	if count, err := svc.RecoverExpired(context.Background(), 10); err != nil || count != 1 {
		t.Fatalf("recover count=%d err=%v", count, err)
	}
	if err := svc.Complete(context.Background(), firstLease, json.RawMessage(`{}`)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired worker completion err=%v", err)
	}
	clock.Advance(backoff(1))
	second, err := svc.Claim(context.Background(), "worker-b", time.Minute)
	if err != nil || second == nil {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
	clock.Advance(61 * time.Second)
	if count, err := svc.RecoverExpired(context.Background(), 10); err != nil || count != 1 {
		t.Fatalf("terminal recover count=%d err=%v", count, err)
	}
	stored, _ := svc.Get(context.Background(), job.ID, 7, ScopeTenant)
	if stored.Status != StatusFailed || stored.ErrorCode != "LEASE_EXPIRED" {
		t.Fatalf("expected terminal lease failure: %+v", stored)
	}
}

func TestRetryBackoffAndManualRetryIdempotency(t *testing.T) {
	svc, _, _, clock, _ := newTestService(t)
	job, _, _ := svc.Submit(context.Background(), validSubmission("retry"))
	claimed, _ := svc.Claim(context.Background(), "worker", time.Minute)
	if err := svc.Fail(context.Background(), leaseFor(claimed), "TEMPORARY", "temporary failure", true); err != nil {
		t.Fatal(err)
	}
	queued, _ := svc.Get(context.Background(), job.ID, 7, ScopeTenant)
	if queued.Status != StatusQueued || !queued.RunAfter.Equal(clock.Now().Add(backoff(1))) {
		t.Fatalf("retry backoff invalid: %+v", queued)
	}
	clock.Set(queued.RunAfter)
	claimed, _ = svc.Claim(context.Background(), "worker", time.Minute)
	if err := svc.Fail(context.Background(), leaseFor(claimed), "PERMANENT", "failed", false); err != nil {
		t.Fatal(err)
	}
	retried, replay, err := svc.ManualRetry(context.Background(), job.ID, 7, ScopeTenant, "manual-1")
	if err != nil || replay || retried.Status != StatusQueued {
		t.Fatalf("manual retry=%+v replay=%v err=%v", retried, replay, err)
	}
	again, replay, err := svc.ManualRetry(context.Background(), job.ID, 7, ScopeTenant, "manual-1")
	if err != nil || !replay || again.ID != retried.ID {
		t.Fatalf("manual retry replay=%+v replay=%v err=%v", again, replay, err)
	}
}

func TestQueuedAndRunningCancellation(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)
	queued, _, _ := svc.Submit(context.Background(), validSubmission("cancel-queued"))
	cancelled, err := svc.RequestCancellation(context.Background(), queued.ID, 7, ScopeTenant)
	if err != nil || cancelled.Status != StatusCancelled || cancelled.CancelledAt == nil {
		t.Fatalf("queued cancellation=%+v err=%v", cancelled, err)
	}
	running, _, _ := svc.Submit(context.Background(), validSubmission("cancel-running"))
	claim, _ := svc.Claim(context.Background(), "worker", time.Minute)
	if claim.ID != running.ID {
		t.Fatalf("claimed wrong job: %d", claim.ID)
	}
	requested, err := svc.RequestCancellation(context.Background(), running.ID, 7, ScopeTenant)
	if err != nil || requested.Status != StatusRunning || requested.CancellationAskedAt == nil {
		t.Fatalf("running cancellation request=%+v err=%v", requested, err)
	}
	if err = svc.Complete(context.Background(), leaseFor(claim), json.RawMessage(`{}`)); !errors.Is(err, ErrCancellationAsked) {
		t.Fatalf("completion beat durable cancellation: %v", err)
	}
	if err = svc.FinishCancellation(context.Background(), leaseFor(claim)); err != nil {
		t.Fatal(err)
	}
	final, _ := svc.Get(context.Background(), running.ID, 7, ScopeTenant)
	if final.Status != StatusCancelled {
		t.Fatalf("running cancellation final=%s", final.Status)
	}
}

func TestCancellationCompletionRaceHasSingleTerminalOutcome(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)
	job, _, _ := svc.Submit(context.Background(), validSubmission("cancel-race"))
	claim, _ := svc.Claim(context.Background(), "worker", time.Minute)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.RequestCancellation(context.Background(), job.ID, 7, ScopeTenant)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		errs <- svc.Complete(context.Background(), leaseFor(claim), json.RawMessage(`{"ok":true}`))
	}()
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one racing mutation to win, successes=%d", successes)
	}
	stored, err := svc.Get(context.Background(), job.ID, 7, ScopeTenant)
	if err != nil || (stored.Status != StatusSucceeded && stored.Status != StatusRunning) {
		t.Fatalf("unexpected race state=%+v err=%v", stored, err)
	}
	if stored.Status == StatusRunning {
		if stored.CancellationAskedAt == nil {
			t.Fatal("running race winner has no cancellation request")
		}
		if err := svc.FinishCancellation(context.Background(), leaseFor(claim)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkerExtendsLeaseWhileHandlerRuns(t *testing.T) {
	_, repo, _, clock, _ := newTestService(t)
	registry := NewRegistry()
	if err := registry.Register(Definition{Type: "tenant.slow", Scope: ScopeTenant, PayloadVersion: 1, Timeout: time.Second, Validate: func(json.RawMessage) error { return nil }, Handle: func(ctx context.Context, _ Execution, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(45 * time.Millisecond):
			return json.RawMessage(`{"ok":true}`), nil
		}
	}}); err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithRegistry(repo, registry, clock)
	input := validSubmission("slow")
	input.Type, input.Payload = "tenant.slow", json.RawMessage(`{}`)
	job, _, err := svc.Submit(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(svc, registry, "worker").WithIntervals(time.Millisecond, 30*time.Millisecond)
	if worked, err := worker.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	stored, _ := svc.Get(context.Background(), job.ID, 7, ScopeTenant)
	if stored.Status != StatusSucceeded || stored.Version < 4 {
		t.Fatalf("automatic heartbeat not observed: %+v", stored)
	}
}

func TestWorkerPanicRecoveryAndUnknownTypeFailure(t *testing.T) {
	svc, _, registry, _, _ := newTestService(t)
	if err := registry.Register(Definition{Type: "tenant.panic", Scope: ScopeTenant, PayloadVersion: 1, Timeout: time.Second, Validate: func(json.RawMessage) error { return nil }, Handle: func(context.Context, Execution, json.RawMessage) (json.RawMessage, error) { panic("sensitive panic") }}); err != nil {
		t.Fatal(err)
	}
	input := validSubmission("panic")
	input.Type, input.Payload = "tenant.panic", json.RawMessage(`{}`)
	job, _, err := svc.Submit(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(svc, registry, "worker")
	if worked, err := worker.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("panic run worked=%v err=%v", worked, err)
	}
	failed, _ := svc.Get(context.Background(), job.ID, 7, ScopeTenant)
	if failed.Status != StatusFailed || failed.ErrorCode != "JOB_HANDLER_PANIC" || strings.Contains(failed.ErrorMessage, "sensitive") {
		t.Fatalf("panic not safely failed: %+v", failed)
	}
}

func TestFailureMetadataRedactsConnectionSecrets(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)
	job, _, err := svc.Submit(context.Background(), validSubmission("safe-error"))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := svc.Claim(context.Background(), "worker", time.Minute)
	if err != nil || claimed == nil || claimed.ID != job.ID {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err = svc.Fail(context.Background(), leaseFor(claimed), "DATABASE_ERROR", "postgres://user:secret@db/jobs", false); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Get(context.Background(), job.ID, 7, ScopeTenant)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ErrorMessage != "automation job execution failed" || strings.Contains(stored.ErrorMessage, "secret") {
		t.Fatalf("unsafe failure metadata: %q", stored.ErrorMessage)
	}
}

type fakeDomainVerifier struct {
	tenant, domain uint
	calls          atomic.Int32
}

func (f *fakeDomainVerifier) VerifyDomain(_ context.Context, tenant, domain uint) error {
	f.tenant, f.domain = tenant, domain
	f.calls.Add(1)
	return nil
}

type fakeWebhookProcessor struct{ outbox, deliveries atomic.Int32 }

func (f *fakeWebhookProcessor) ProcessOutbox(context.Context, int) error { f.outbox.Add(1); return nil }
func (f *fakeWebhookProcessor) ProcessPendingDeliveries(context.Context, int) error {
	f.deliveries.Add(1)
	return nil
}

func TestProductionHandlersReuseTenantAndWebhookServices(t *testing.T) {
	_, repo, _, clock, _ := newTestService(t)
	registry := NewRegistry()
	domains, hooks := &fakeDomainVerifier{}, &fakeWebhookProcessor{}
	if err := RegisterProductionHandlers(registry, domains, hooks); err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithRegistry(repo, registry, clock)
	tenantInput := validSubmission("domain-real")
	tenantInput.Payload = json.RawMessage(`{"domain_id":55}`)
	if _, _, err := svc.Submit(context.Background(), tenantInput); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"platform.webhooks.outbox", "platform.webhooks.deliveries"} {
		if _, _, err := svc.Submit(context.Background(), Submission{Scope: ScopePlatform, Actor: "user:1", Type: typ, Payload: json.RawMessage(`{}`), IdempotencyKey: typ}); err != nil {
			t.Fatal(err)
		}
	}
	worker := NewWorker(svc, registry, "worker")
	for i := 0; i < 3; i++ {
		if worked, err := worker.RunOnce(context.Background()); err != nil || !worked {
			t.Fatalf("run %d worked=%v err=%v", i, worked, err)
		}
	}
	if domains.calls.Load() != 1 || domains.tenant != 7 || domains.domain != 55 || hooks.outbox.Load() != 1 || hooks.deliveries.Load() != 1 {
		t.Fatalf("real handler calls domain=%d/%d/%d hooks=%d/%d", domains.calls.Load(), domains.tenant, domains.domain, hooks.outbox.Load(), hooks.deliveries.Load())
	}
}
