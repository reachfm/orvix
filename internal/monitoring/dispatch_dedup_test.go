package monitoring

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestEvaluateAlertsDispatchesOnlyNewAlerts proves the first evaluation
// creates and dispatches each newly-raised alert exactly once, and the
// second consecutive evaluation of the SAME condition does NOT dispatch
// again. This is the BLOCKER 2 contract: re-evaluation must not spam
// webhook/in-app delivery for an alert that was already active.
func TestEvaluateAlertsDispatchesOnlyNewAlerts(t *testing.T) {
	var webhookCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&webhookCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := deliveryTestDB(t)
	wh := NewWebhookProvider(WebhookConfig{Enabled: true, URL: srv.URL})
	d := NewDispatcher(db, nil, NewInAppProvider(), wh)

	svc := testService(t, &DataSources{
		QueueDeadLetter: func() (int64, error) { return 7, nil },
	})
	svc.SetDispatcher(d)
	ctx := context.Background()

	// First evaluation: alert is newly raised → dispatch.
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 1 {
		t.Fatalf("first evaluation: expected 1 webhook call, got %d", got)
	}

	// Second evaluation: same condition still active → no dispatch.
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 1 {
		t.Fatalf("second evaluation should not re-dispatch, got %d webhook calls", got)
	}

	// Third evaluation: same condition still active → still no dispatch.
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("third evaluate: %v", err)
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 1 {
		t.Fatalf("third evaluation should not re-dispatch, got %d webhook calls", got)
	}
}

// TestMonitoringReadEndpointDoesNotRedeliverActiveAlert simulates the
// repeated-read pattern: the same Service object is evaluated over and
// over (mirroring what happens when every GET endpoint calls
// EvaluateAlerts) and the dispatcher should only fire on the very first
// detection of a new alert condition.
func TestMonitoringReadEndpointDoesNotRedeliverActiveAlert(t *testing.T) {
	var webhookCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&webhookCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := deliveryTestDB(t)
	wh := NewWebhookProvider(WebhookConfig{Enabled: true, URL: srv.URL})
	d := NewDispatcher(db, nil, NewInAppProvider(), wh)

	svc := testService(t, &DataSources{
		QueueDeadLetter: func() (int64, error) { return 9, nil },
	})
	svc.SetDispatcher(d)
	ctx := context.Background()

	// First detection: dispatch.
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 1 {
		t.Fatalf("expected 1 webhook after first eval, got %d", got)
	}

	// Simulate 5 read polls. None should re-deliver.
	for i := 0; i < 5; i++ {
		if _, err := svc.EvaluateAlerts(ctx); err != nil {
			t.Fatalf("evaluate[%d]: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 1 {
		t.Fatalf("5 read polls should not re-deliver, got %d total webhook calls", got)
	}

	// And the delivery records table should not have grown either.
	recs, err := d.ListDeliveries(ctx, 100)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	// 1 inapp record + 1 webhook record from the first dispatch only.
	if got := len(recs); got != 2 {
		t.Fatalf("expected exactly 2 delivery records (inapp+webhook), got %d: %+v", got, recs)
	}
}

// TestAlertDispatchAfterResolveAndReRaise proves the dedup is keyed on
// the previously-active set: an alert that resolves (the condition
// clears) and then re-fires IS delivered again. The previously-active
// snapshot no longer contains its key.
func TestAlertDispatchAfterResolveAndReRaise(t *testing.T) {
	var webhookCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&webhookCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// deadLetterFn starts as "active", flips to "clear", then back to
	// "active" to simulate a transient condition.
	var dead int64 = 5
	deadFn := func() (int64, error) { return dead, nil }

	db := deliveryTestDB(t)
	wh := NewWebhookProvider(WebhookConfig{Enabled: true, URL: srv.URL})
	d := NewDispatcher(db, nil, NewInAppProvider(), wh)
	svc := testService(t, &DataSources{QueueDeadLetter: deadFn})
	svc.SetDispatcher(d)
	ctx := context.Background()

	// Eval 1: condition active → dispatch (1 webhook call).
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("eval1: %v", err)
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 1 {
		t.Fatalf("after eval1 expected 1 webhook call, got %d", got)
	}

	// Eval 2: condition clears → no alert at all, no dispatch.
	dead = 0
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("eval2: %v", err)
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 1 {
		t.Fatalf("after eval2 expected still 1 webhook call, got %d", got)
	}

	// Eval 3: condition re-fires → dispatch again (2 webhook calls).
	dead = 5
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("eval3: %v", err)
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 2 {
		t.Fatalf("after eval3 expected 2 webhook calls (re-dispatched), got %d", got)
	}

	// Eval 4: condition still active → no re-dispatch.
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("eval4: %v", err)
	}
	if got := atomic.LoadInt32(&webhookCalls); got != 2 {
		t.Fatalf("after eval4 expected still 2 webhook calls, got %d", got)
	}
}

// TestEvaluateAlertsWithoutDispatcherStillMarksAlertActive confirms
// the dedup logic does not regress alert persistence: even when no
// dispatcher is configured, the active alert list and the row count
// behave correctly across repeated evaluations.
func TestEvaluateAlertsWithoutDispatcherStillMarksAlertActive(t *testing.T) {
	svc := testService(t, &DataSources{
		QueueDeadLetter: func() (int64, error) { return 3, nil },
	})
	ctx := context.Background()

	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("eval1: %v", err)
	}
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("eval2: %v", err)
	}

	// The "all alerts" history should now contain 2 rows for the same
	// (category, severity, title) — each evaluation resolves the
	// previous one and creates a new one. The active list should have
	// exactly one.
	all, err := svc.ListAllAlerts(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 historical alert rows, got %d", len(all))
	}
	active, err := svc.ListActiveAlerts(ctx)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("expected exactly 1 active alert, got %d", len(active))
	}
}