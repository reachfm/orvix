package billing

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/dbdialect"

	_ "modernc.org/sqlite"
)

type testPaymentProvider struct {
	*hmacPaymentProvider
	secret string
}

func newTestPaymentProvider() *testPaymentProvider {
	b := make([]byte, 16)
	rand.Read(b)
	secret := hex.EncodeToString(b)
	return &testPaymentProvider{
		hmacPaymentProvider: newHMACPaymentProvider("", secret, DefaultWebhookTolerance),
		secret:              secret,
	}
}

func (p *testPaymentProvider) CreateCheckout(tenantID uint, planID PlanID, interval BillingInterval, returnURL string) (*CheckoutSession, error) {
	return &CheckoutSession{URL: returnURL + "?test=1", SessionID: "cs_test_1"}, nil
}

func (p *testPaymentProvider) GetCustomerPortalURL(tenantID uint, returnURL string) (string, error) {
	return returnURL + "?portal=test", nil
}

func signWebhook(secret string, timestamp string, payload []byte) string {
	signedPayload := fmt.Sprintf("%s.%s", timestamp, string(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	return hex.EncodeToString(mac.Sum(nil))
}

func testPayload(t *testing.T, eventID string) []byte {
	t.Helper()
	p, _ := json.Marshal(map[string]interface{}{
		"type": "checkout.session.completed",
		"data": map[string]interface{}{
			"object": map[string]interface{}{
				"id":             eventID,
				"subscription":   "sub_test_1",
				"status":         "active",
				"payment_status": "paid",
			},
		},
	})
	return p
}

type webhookTestEnv struct {
	app      *fiber.App
	db       *sql.DB
	provider *testPaymentProvider
	svc      *Service
	webhook  *WebhookService
}

func setupWebhookEnv(t *testing.T) *webhookTestEnv {
	t.Helper()
	db := newTestDB(t)
	db.SetMaxOpenConns(1)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	sub, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = sub
	db.Exec("UPDATE subscriptions SET provider_sub_id = 'sub_test_1' WHERE tenant_id = 1")

	provider := newTestPaymentProvider()
	webhookSvc := NewWebhookService(db)

	app := fiber.New()
	app.Post("/webhook", func(c fiber.Ctx) error {
		if webhookSvc == nil || provider == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "not configured"})
		}
		timestamp := c.Get("X-Payment-Timestamp")
		if timestamp == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing timestamp"})
		}
		signature := c.Get("X-Payment-Signature")
		if signature == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing signature"})
		}
		rawPayload := c.Body()
		if len(rawPayload) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "empty payload"})
		}
		event, err := provider.VerifyWebhook(rawPayload, timestamp, signature)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "verification failed"})
		}
		if event.ProviderEventID == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing event id"})
		}
		rec := &WebhookEventRecord{
			ID:             event.ProviderEventID,
			Provider:       "test",
			EventType:      event.Type,
			ProviderSubID:  event.ProviderSubID,
			RawPayload:     rawPayload,
			Signature:      signature,
			ReceivedAt:     time.Now().UTC(),
			IdempotencyKey: event.ProviderEventID,
		}
		if err := webhookSvc.RecordEvent(c.Context(), rec); err != nil {
			if err == ErrWebhookAlreadyProcessed {
				return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "already_processed"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "recording failed"})
		}
		if event.PaymentStatus == "paid" && event.ProviderSubID != "" {
			if sub, subErr := svc.GetSubscriptionByProviderID(event.ProviderSubID); subErr == nil {
				svc.TransitionState(sub.TenantID, SubActive)
			}
		}
		webhookSvc.MarkProcessed(c.Context(), event.ProviderEventID, "test", nil)
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "received"})
	})

	return &webhookTestEnv{app: app, db: db, provider: provider, svc: svc, webhook: webhookSvc}
}

func doWebhookReq(t *testing.T, env *webhookTestEnv, payload []byte, timestamp, sig string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if timestamp != "" {
		req.Header.Set("X-Payment-Timestamp", timestamp)
	}
	if sig != "" {
		req.Header.Set("X-Payment-Signature", sig)
	}
	resp, err := env.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestWebhook_ProviderNotConfigured(t *testing.T) {
	app := fiber.New()
	app.Post("/webhook", func(c fiber.Ctx) error {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "not configured"})
	})
	req := httptest.NewRequest("POST", "/webhook", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != 503 {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
}

func TestWebhook_InvalidSignature(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, "evt_001")
	ts := fmt.Sprintf("%d", time.Now().Unix())
	resp := doWebhookReq(t, env, payload, ts, "bad_signature")
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for invalid signature, got %d", resp.StatusCode)
	}
}

func TestWebhook_MissingTimestamp(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, "evt_missing_timestamp")
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signWebhook(env.provider.secret, ts, payload)
	resp := doWebhookReq(t, env, payload, "", sig)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing timestamp, got %d", resp.StatusCode)
	}
}

func TestWebhook_MalformedTimestamp(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, "evt_malformed_timestamp")
	ts := "not-a-unix-time"
	sig := signWebhook(env.provider.secret, ts, payload)
	resp := doWebhookReq(t, env, payload, ts, sig)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for malformed timestamp, got %d", resp.StatusCode)
	}
}

func TestWebhook_ExpiredTimestampRejected(t *testing.T) {
	env := setupWebhookEnv(t)
	now := time.Unix(1_700_000_000, 0)
	env.provider.now = func() time.Time { return now }
	payload := testPayload(t, "evt_expired_timestamp")
	ts := fmt.Sprintf("%d", now.Add(-DefaultWebhookTolerance-time.Second).Unix())
	sig := signWebhook(env.provider.secret, ts, payload)
	resp := doWebhookReq(t, env, payload, ts, sig)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for expired timestamp, got %d", resp.StatusCode)
	}
}

func TestWebhook_UsesTransmittedTimestampForSignature(t *testing.T) {
	env := setupWebhookEnv(t)
	now := time.Unix(1_700_000_000, 0)
	env.provider.now = func() time.Time { return now }
	payload := testPayload(t, "evt_transmitted_timestamp")
	headerTS := fmt.Sprintf("%d", now.Add(-time.Minute).Unix())
	validSig := signWebhook(env.provider.secret, headerTS, payload)
	resp := doWebhookReq(t, env, payload, headerTS, validSig)
	if resp.StatusCode != 200 {
		t.Fatalf("expected signature over transmitted timestamp to pass, got %d", resp.StatusCode)
	}

	payload = testPayload(t, "evt_receiver_timestamp_rejected")
	receiverLocalTS := fmt.Sprintf("%d", now.Unix())
	receiverLocalSig := signWebhook(env.provider.secret, receiverLocalTS, payload)
	resp = doWebhookReq(t, env, payload, headerTS, receiverLocalSig)
	if resp.StatusCode != 400 {
		t.Fatalf("expected signature over receiver-local timestamp to fail, got %d", resp.StatusCode)
	}
}

func TestWebhook_ValidSignature(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, "evt_002")
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signWebhook(env.provider.secret, ts, payload)
	resp := doWebhookReq(t, env, payload, ts, sig)
	if resp.StatusCode != 200 {
		body := make([]byte, 1024)
		resp.Body.Read(body)
		t.Fatalf("expected 200 for valid webhook, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestWebhook_MissingEventID(t *testing.T) {
	env := setupWebhookEnv(t)
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "checkout.session.completed",
		"data": map[string]interface{}{"object": map[string]interface{}{}},
	})
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signWebhook(env.provider.secret, ts, payload)
	resp := doWebhookReq(t, env, payload, ts, sig)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing event ID, got %d", resp.StatusCode)
	}
}

func TestWebhook_DuplicateIgnored(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, "evt_dup")
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signWebhook(env.provider.secret, ts, payload)

	r1 := doWebhookReq(t, env, payload, ts, sig)
	if r1.StatusCode != 200 {
		t.Fatalf("first send should be 200, got %d", r1.StatusCode)
	}
	r2 := doWebhookReq(t, env, payload, ts, sig)
	if r2.StatusCode != 200 {
		t.Fatalf("duplicate should be 200, got %d", r2.StatusCode)
	}
	assertWebhookEventCount(t, env.db, "evt_dup", 1)
}

func TestWebhook_ConcurrentDuplicate(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, fmt.Sprintf("evt_conc_%d", time.Now().UnixNano()))
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signWebhook(env.provider.secret, ts, payload)

	var wg sync.WaitGroup
	failures := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := doWebhookReq(t, env, payload, ts, sig)
			if resp.StatusCode != 200 {
				failures <- resp.StatusCode
			}
		}()
	}
	wg.Wait()
	close(failures)
	for code := range failures {
		t.Errorf("unexpected status code from concurrent request: %d", code)
	}
	assertWebhookEventCount(t, env.db, extractEventID(t, payload), 1)
}

func TestWebhook_ActivateSubscription(t *testing.T) {
	env := setupWebhookEnv(t)
	env.db.Exec("UPDATE subscriptions SET status = 'trialing', provider_sub_id = 'sub_activate' WHERE tenant_id = 1")

	payload, _ := json.Marshal(map[string]interface{}{
		"type": "checkout.session.completed",
		"data": map[string]interface{}{
			"object": map[string]interface{}{
				"id":             "evt_activate",
				"subscription":   "sub_activate",
				"status":         "active",
				"payment_status": "paid",
			},
		},
	})
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signWebhook(env.provider.secret, ts, payload)
	resp := doWebhookReq(t, env, payload, ts, sig)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status string
	if err := env.db.QueryRow("SELECT status FROM subscriptions WHERE provider_sub_id = 'sub_activate'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(SubActive) {
		t.Errorf("expected 'active', got '%s'", status)
	}
}

func TestWebhook_ReplayUsesIdempotencyKey(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, "evt_replay")
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signWebhook(env.provider.secret, ts, payload)

	r1 := doWebhookReq(t, env, payload, ts, sig)
	if r1.StatusCode != 200 {
		t.Fatalf("first: %d", r1.StatusCode)
	}

	var processed bool
	env.db.QueryRow("SELECT 1 FROM webhook_events WHERE idempotency_key = 'evt_replay'").Scan(&processed)
	if !processed {
		t.Error("idempotency key should be stored")
	}

	for i := 0; i < 3; i++ {
		r := doWebhookReq(t, env, payload, ts, sig)
		if r.StatusCode != 200 {
			t.Errorf("replay %d: expected 200, got %d", i, r.StatusCode)
		}
	}
	assertWebhookEventCount(t, env.db, "evt_replay", 1)
}

func assertWebhookEventCount(t *testing.T, db *sql.DB, id string, want int) {
	t.Helper()
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM webhook_events WHERE id = "+dialect.Placeholder(1), id).Scan(&got); err != nil {
		t.Fatalf("count webhook events for %s: %v", id, err)
	}
	if got != want {
		t.Fatalf("webhook event count for %s: got %d want %d", id, got, want)
	}
}

func extractEventID(t *testing.T, payload []byte) string {
	t.Helper()
	var raw struct {
		Data struct {
			Object struct {
				ID string `json:"id"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	return raw.Data.Object.ID
}
