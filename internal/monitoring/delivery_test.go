package monitoring

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func deliveryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/delivery_test.db", t.TempDir()))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range Schema() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

func sampleAlert() Alert {
	return Alert{
		Category:  CatQueue,
		Severity:  SeverityCritical,
		Title:     "Queue dead-letter",
		Message:   "5 messages in dead-letter",
		Source:    string(CatQueue),
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}
}

// TestAlertGeneratedPersistedListedResolved exercises the full alert
// lifecycle: an alert is raised, persisted, listed, and resolved.
func TestAlertGeneratedPersistedListedResolved(t *testing.T) {
	svc := testService(t, &DataSources{
		QueueDeadLetter: func() (int64, error) { return 5, nil },
	})
	ctx := context.Background()

	active, err := svc.EvaluateAlerts(ctx)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(active) == 0 {
		t.Fatal("expected at least one active alert")
	}

	// Listed.
	listed, err := svc.ListActiveAlerts(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var target uint
	for _, a := range listed {
		if a.Category == CatQueue {
			target = a.ID
		}
	}
	if target == 0 {
		t.Fatal("queue alert not found in list")
	}

	// Resolved.
	rows, err := svc.ResolveAlert(ctx, target)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected 1 row resolved, got %d", rows)
	}
	still, _ := svc.ListActiveAlerts(ctx)
	for _, a := range still {
		if a.ID == target {
			t.Fatal("alert still active after resolve")
		}
	}
}

// TestInAppDeliverySucceeds proves the always-on in-app provider records
// a successful delivery.
func TestInAppDeliverySucceeds(t *testing.T) {
	db := deliveryTestDB(t)
	d := NewDispatcher(db, nil, NewInAppProvider())
	ctx := context.Background()

	d.Dispatch(ctx, sampleAlert())

	recs, err := d.ListDeliveries(ctx, 10)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 delivery record, got %d", len(recs))
	}
	if recs[0].Provider != "inapp" || recs[0].Status != DeliverySuccess {
		t.Fatalf("expected inapp/success, got %s/%s", recs[0].Provider, recs[0].Status)
	}
}

// TestWebhookDeliverySuccess proves a webhook delivery to a live test
// server records success and sends the expected payload.
func TestWebhookDeliverySuccess(t *testing.T) {
	var gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := deliveryTestDB(t)
	wh := NewWebhookProvider(WebhookConfig{Enabled: true, URL: srv.URL, Token: "super-secret-token"})
	d := NewDispatcher(db, nil, wh)
	ctx := context.Background()

	d.Dispatch(ctx, sampleAlert())

	recs, err := d.ListDeliveries(ctx, 10)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(recs) != 1 || recs[0].Status != DeliverySuccess {
		t.Fatalf("expected 1 success delivery, got %+v", recs)
	}
	if gotAuth != "Bearer super-secret-token" {
		t.Fatalf("webhook did not receive bearer token, got %q", gotAuth)
	}
	if !strings.Contains(string(gotBody), "Queue dead-letter") {
		t.Fatalf("webhook payload missing alert title: %s", gotBody)
	}
	// Payload must NOT contain the token.
	if strings.Contains(string(gotBody), "super-secret-token") {
		t.Fatal("webhook payload body leaked the token")
	}
}

// TestWebhookDeliveryFailureRecordedNoCrash proves a failing webhook is
// recorded but does not crash monitoring and does not abort alert
// evaluation.
func TestWebhookDeliveryFailureRecordedNoCrash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	db := deliveryTestDB(t)
	wh := NewWebhookProvider(WebhookConfig{Enabled: true, URL: srv.URL, Token: "tkn"})
	d := NewDispatcher(db, nil, NewInAppProvider(), wh)
	ctx := context.Background()

	// Must not panic.
	d.Dispatch(ctx, sampleAlert())

	recs, err := d.ListDeliveries(ctx, 10)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	var webhookRec *DeliveryRecord
	var inappRec *DeliveryRecord
	for i := range recs {
		switch recs[i].Provider {
		case "webhook":
			webhookRec = &recs[i]
		case "inapp":
			inappRec = &recs[i]
		}
	}
	if webhookRec == nil || webhookRec.Status != DeliveryFailed {
		t.Fatalf("expected failed webhook delivery record, got %+v", recs)
	}
	// The in-app provider still delivered despite the webhook failure.
	if inappRec == nil || inappRec.Status != DeliverySuccess {
		t.Fatalf("expected in-app delivery to still succeed, got %+v", recs)
	}
	// The failure detail must not leak the URL or token.
	if strings.Contains(webhookRec.Detail, srv.URL) || strings.Contains(webhookRec.Detail, "tkn") {
		t.Fatalf("failure detail leaked a secret: %q", webhookRec.Detail)
	}
}

// TestWebhookSecretRedactedInLogsAndStatus proves the webhook URL and
// token never appear in the delivery logs or the status report.
func TestWebhookSecretRedactedInLogsAndStatus(t *testing.T) {
	// A server that always fails so the dispatcher logs the failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	db := deliveryTestDB(t)
	wh := NewWebhookProvider(WebhookConfig{Enabled: true, URL: srv.URL, Token: "hunter2-token"})
	d := NewDispatcher(db, logger, wh)
	ctx := context.Background()

	d.Dispatch(ctx, sampleAlert())

	logs := buf.String()
	if logs == "" {
		t.Fatal("expected a log line for the webhook failure")
	}
	if strings.Contains(logs, srv.URL) {
		t.Fatalf("log leaked the webhook URL: %s", logs)
	}
	if strings.Contains(logs, "hunter2-token") {
		t.Fatalf("log leaked the webhook token: %s", logs)
	}

	// Status report must be redacted and honest.
	statuses := d.Providers()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 provider status, got %d", len(statuses))
	}
	st := statuses[0]
	if st.Name != "webhook" || !st.Enabled || !st.HasSecret {
		t.Fatalf("unexpected status: %+v", st)
	}
	if st.Target == srv.URL || strings.Contains(st.Target, srv.URL) {
		t.Fatalf("status leaked the URL: %+v", st)
	}
	if strings.Contains(fmt.Sprintf("%+v", st), "hunter2-token") {
		t.Fatalf("status leaked the token: %+v", st)
	}
}

// TestDisabledProviderSkippedHonestly proves a disabled provider is not
// delivered to and is honestly recorded as skipped and reported as
// disabled.
func TestDisabledProviderSkippedHonestly(t *testing.T) {
	var called int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		called++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := deliveryTestDB(t)
	// enabled=false → skipped honestly even though a URL is set.
	wh := NewWebhookProvider(WebhookConfig{Enabled: false, URL: srv.URL})
	d := NewDispatcher(db, nil, wh)
	ctx := context.Background()

	d.Dispatch(ctx, sampleAlert())

	mu.Lock()
	c := called
	mu.Unlock()
	if c != 0 {
		t.Fatalf("disabled provider was delivered to (%d calls)", c)
	}

	recs, _ := d.ListDeliveries(ctx, 10)
	if len(recs) != 1 || recs[0].Status != DeliverySkipped {
		t.Fatalf("expected a single skipped record, got %+v", recs)
	}

	st := wh.Status()
	if st.Enabled {
		t.Fatalf("disabled provider reported as enabled: %+v", st)
	}
}

// TestWebhookEnabledButNoURLIsDisabled proves an enabled flag with no
// URL is honestly reported as disabled (no fake "enabled").
func TestWebhookEnabledButNoURLIsDisabled(t *testing.T) {
	wh := NewWebhookProvider(WebhookConfig{Enabled: true, URL: ""})
	if wh.Enabled() {
		t.Fatal("webhook with no URL must not be enabled")
	}
	st := wh.Status()
	if st.Enabled {
		t.Fatalf("status must report disabled, got %+v", st)
	}
}
