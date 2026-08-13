package webhooks

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/webhooks/ssrf"
	_ "modernc.org/sqlite"
)

type deliveryHarness struct {
	db        *sql.DB
	repo      *Repository
	outbox    *kernel.OutboxRepository
	service   *Service
	publisher *OutboxPublisher
	clock     *kernel.FixedClock
}

func newDeliveryHarness(t *testing.T, receiver *httptest.Server) *deliveryHarness {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "delivery.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	outbox := kernel.NewOutboxRepository(dbdialect.FromDriver("sqlite"))
	if err := outbox.EnsureSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	clock := kernel.NewFixedClock(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	options := ssrf.ClientOptions{Timeout: time.Second}
	allow := &ssrf.Allowlist{DevMode: true, AllowedHosts: map[string]bool{}}
	if receiver != nil {
		u, _ := url.Parse(receiver.URL)
		allow.AllowedHosts[strings.ToLower(u.Hostname())] = true
		options.TLSConfig = receiver.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	}
	service := NewService(repo, allow).WithOutbox(outbox).WithClock(clock).WithHTTPOptions(options)
	return &deliveryHarness{db: db, repo: repo, outbox: outbox, service: service, publisher: NewOutboxPublisher(outbox), clock: clock}
}

func (h *deliveryHarness) subscribe(t *testing.T, endpoint string, secret []byte) *Subscription {
	t.Helper()
	sub, _, err := h.service.CreateSubscriptionWithSecret(context.Background(), 1, ScopeTenant, endpoint, []string{"domain.created"}, secret)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func (h *deliveryHarness) publish(t *testing.T, payload any) string {
	t.Helper()
	ctx := context.Background()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.publisher.Publish(ctx, tx, "domain.created", "domain:1", 1, payload, h.clock.Now())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = h.service.ProcessOutbox(ctx, 10); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeliveryTLSReceiverVerifiesCanonicalSignature(t *testing.T) {
	secret := []byte("receiver-secret")
	var received atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		var event Event
		if json.Unmarshal(body, &event) != nil {
			t.Error("invalid event JSON")
		}
		var ts int64
		if _, err := fmt.Sscan(r.Header.Get("X-Orvix-Timestamp"), &ts); err != nil {
			t.Error(err)
		}
		if r.Header.Get("X-Orvix-Event-ID") != event.ID || !VerifyAt(secret, body, ts, r.Header.Get("X-Orvix-Signature"), 5*time.Minute, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)) {
			t.Error("signature or event headers invalid")
		}
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	h := newDeliveryHarness(t, server)
	h.subscribe(t, server.URL, secret)
	eventID := h.publish(t, map[string]any{"domain_id": 1})
	if err := h.service.ProcessPendingDeliveries(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Fatalf("received=%d", received.Load())
	}
	event, err := h.repo.GetEvent(context.Background(), eventID)
	if err != nil || event.Type != "domain.created" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	history, _ := h.repo.DeliveryHistory(context.Background(), 1, 10)
	if len(history) != 1 || history[0].Status != "delivered" || history[0].AttemptCount != 1 {
		t.Fatalf("history=%+v", history)
	}
	attempts, _ := h.repo.Attempts(context.Background(), history[0].ID)
	if len(attempts) != 1 || attempts[0].Status != "delivered" {
		t.Fatalf("attempts=%+v", attempts)
	}
}

func TestDeliveryRetryThenSuccessAndTerminalSuspension(t *testing.T) {
	var calls atomic.Int32
	var alwaysFail atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if alwaysFail.Load() {
			http.Error(w, "still failing", 500)
			return
		}
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", 500)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	h := newDeliveryHarness(t, server)
	sub := h.subscribe(t, server.URL, []byte("retry-secret"))
	h.publish(t, map[string]int{"domain_id": 1})
	if err := h.service.ProcessPendingDeliveries(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	history, _ := h.repo.DeliveryHistory(context.Background(), sub.ID, 10)
	if history[0].Status != "retrying" {
		t.Fatalf("status=%s", history[0].Status)
	}
	h.clock.Advance(2 * time.Second)
	if err := h.service.ProcessPendingDeliveries(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	history, _ = h.repo.DeliveryHistory(context.Background(), sub.ID, 10)
	if history[0].Status != "delivered" || history[0].AttemptCount != 2 {
		t.Fatalf("history=%+v", history)
	}

	alwaysFail.Store(true)
	h.service.WithRetryPolicy(2, 2, time.Second)
	h.publish(t, map[string]int{"domain_id": 2})
	if err := h.service.ProcessPendingDeliveries(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(2 * time.Second)
	if err := h.service.ProcessPendingDeliveries(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	stored, _ := h.repo.GetSubscription(context.Background(), sub.ID)
	if !stored.Suspended || stored.FailureCount < 2 {
		t.Fatalf("subscription not suspended: %+v", stored)
	}
}

func TestDuplicateOutboxAndConcurrentWorkersDeliverOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	h := newDeliveryHarness(t, server)
	sub := h.subscribe(t, server.URL, []byte("concurrent-secret"))
	event, err := NewEvent("domain.created", 1, map[string]int{"domain_id": 7}, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := h.outbox.Enqueue(ctx, h.db, OutboxTopic, event.ID, event, event.OccurredAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.service.ProcessOutbox(ctx, 10); err != nil {
		t.Fatal(err)
	}
	history, _ := h.repo.DeliveryHistory(ctx, sub.ID, 10)
	if len(history) != 1 {
		t.Fatalf("duplicate fanout created %d deliveries", len(history))
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = h.service.ProcessPendingDeliveries(ctx, 10) }()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent workers delivered %d times", calls.Load())
	}
}

func TestStaleWorkerCompletionIsFenced(t *testing.T) {
	h := newDeliveryHarness(t, nil)
	ctx := context.Background()
	if err := h.repo.InsertSubscription(ctx, &Subscription{TenantID: 1, Scope: ScopeTenant, URL: "https://example.com", Events: []string{"domain.created"}, Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.repo.InsertDelivery(ctx, &Delivery{EventID: "evt_fence", SubscriptionID: 1, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	first, err := h.repo.ClaimDeliveries(ctx, 1, h.clock.Now(), time.Second)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	h.clock.Advance(2 * time.Second)
	second, err := h.repo.ClaimDeliveries(ctx, 1, h.clock.Now(), time.Second)
	if err != nil || len(second) != 1 || second[0].LeaseToken == first[0].LeaseToken {
		t.Fatalf("reclaim=%+v err=%v", second, err)
	}
	if err := h.repo.CompleteDelivery(ctx, first[0], "delivered", 204, "", "", nil, h.clock.Now()); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker completion err=%v", err)
	}
}

func TestRotationInvalidatesOldSecret(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer server.Close()
	h := newDeliveryHarness(t, server)
	old := []byte("old-secret")
	sub := h.subscribe(t, server.URL, old)
	_, newHex, err := h.service.RotateSecretForTenant(context.Background(), sub.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	newSecret, err := hexDecode(newHex)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"evt"}`)
	ts := h.clock.Now().Unix()
	signature := Sign(newSecret, body, ts)
	if VerifyAt(old, body, ts, signature, time.Minute, h.clock.Now()) {
		t.Fatal("old secret still verifies rotated signature")
	}
	if !VerifyAt(newSecret, body, ts, signature, time.Minute, h.clock.Now()) {
		t.Fatal("new secret does not verify")
	}
}

func hexDecode(value string) ([]byte, error) {
	return hex.DecodeString(value)
}

func TestTimeoutAndOversizedResponsesAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.Handler
		want    string
	}{
		{"timeout", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond); w.WriteHeader(204) }), "delivery transport failed"},
		{"oversized", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", defaultResponseLimit+1)))
		}), "response body exceeded limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(tc.handler)
			defer server.Close()
			h := newDeliveryHarness(t, server)
			if tc.name == "timeout" {
				options := h.service.httpOptions
				options.Timeout = 20 * time.Millisecond
				h.service.WithHTTPOptions(options)
			}
			sub := h.subscribe(t, server.URL, []byte("bounded-secret"))
			h.publish(t, map[string]int{"domain_id": 1})
			_ = h.service.ProcessPendingDeliveries(context.Background(), 10)
			history, _ := h.repo.DeliveryHistory(context.Background(), sub.ID, 10)
			if len(history) != 1 || history[0].RedactedError != tc.want {
				t.Fatalf("history=%+v", history)
			}
		})
	}
}

type sequenceResolver struct {
	mu      sync.Mutex
	answers [][]net.IPAddr
}

func (r *sequenceResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.answers) == 0 {
		return nil, fmt.Errorf("no answer")
	}
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return answer, nil
}

func TestSSRFRejectsForbiddenAddressesRedirectAndRebinding(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1/h", "https://10.0.0.1/h", "https://169.254.169.254/latest", "https://[::1]/h", "https://user:pass@example.com/h"} {
		if err := ssrf.ValidateURL(raw, nil); err == nil {
			t.Errorf("accepted forbidden URL %s", raw)
		}
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "https://localhost/private", 302) }))
	defer redirect.Close()
	u, _ := url.Parse(redirect.URL)
	allow := &ssrf.Allowlist{DevMode: true, AllowedHosts: map[string]bool{u.Hostname(): true}}
	client := ssrf.SafeHTTPClientWithOptions(ssrf.ClientOptions{Timeout: time.Second, Allowlist: allow, TLSConfig: redirect.Client().Transport.(*http.Transport).TLSClientConfig.Clone()})
	if _, err := client.Get(redirect.URL); err == nil {
		t.Fatal("followed redirect to forbidden address")
	}
	resolver := &sequenceResolver{answers: [][]net.IPAddr{{{IP: net.ParseIP("8.8.8.8")}}, {{IP: net.ParseIP("127.0.0.1")}}}}
	client = ssrf.SafeHTTPClientWithOptions(ssrf.ClientOptions{Timeout: 100 * time.Millisecond, Resolver: resolver, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}})
	if err := ssrf.ValidateURLContext(context.Background(), "https://rebind.example/h", nil, resolver); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get("https://rebind.example/h"); err == nil || !strings.Contains(err.Error(), "prohibited") {
		t.Fatalf("DNS rebinding was not rejected: %v", err)
	}
}
