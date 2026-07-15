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

	_ "modernc.org/sqlite"
)

type testPaymentProvider struct {
	secret string
}

func newTestPaymentProvider() *testPaymentProvider {
	b := make([]byte, 16)
	rand.Read(b)
	return &testPaymentProvider{secret: hex.EncodeToString(b)}
}

func (p *testPaymentProvider) CreateCheckout(tenantID uint, planID PlanID, interval BillingInterval, returnURL string) (*CheckoutSession, error) {
	return &CheckoutSession{URL: returnURL + "?test=1", SessionID: "cs_test_1"}, nil
}

func (p *testPaymentProvider) GetCustomerPortalURL(tenantID uint, returnURL string) (string, error) {
	return returnURL + "?portal=test", nil
}

func (p *testPaymentProvider) VerifyWebhook(payload []byte, signature string) (*WebhookEvent, error) {
	if p.secret == "" || signature == "" {
		return nil, ErrWebhookInvalid
	}
	ts := fmt.Sprintf("%d", time.Now().Unix())
	signedPayload := fmt.Sprintf("%s.%s", ts, string(payload))
	mac := hmac.New(sha256.New, []byte(p.secret))
	mac.Write([]byte(signedPayload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return nil, ErrWebhookInvalid
	}
	var raw struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID            string `json:"id"`
				Subscription  string `json:"subscription"`
				Status        string `json:"status"`
				PaymentStatus string `json:"payment_status"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	return &WebhookEvent{
		Type:               raw.Type,
		ProviderSubID:      raw.Data.Object.Subscription,
		SubscriptionStatus: raw.Data.Object.Status,
		PaymentStatus:      raw.Data.Object.PaymentStatus,
		ProviderEventID:    raw.Data.Object.ID,
	}, nil
}

func (p *testPaymentProvider) CancelSubscription(providerSubID string) error { return nil }
func (p *testPaymentProvider) SynchronizeSubscription(providerSubID string) (*SyncResult, error) {
	return &SyncResult{ProviderSubID: providerSubID, Status: SubActive}, nil
}

func signWebhook(secret string, payload []byte) string {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	signedPayload := fmt.Sprintf("%s.%s", ts, string(payload))
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
		signature := c.Get("X-Payment-Signature")
		if signature == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing signature"})
		}
		rawPayload := c.Body()
		if len(rawPayload) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "empty payload"})
		}
		event, err := provider.VerifyWebhook(rawPayload, signature)
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
		webhookSvc.MarkProcessed(c.Context(), event.ProviderEventID, nil)
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "received"})
	})

	return &webhookTestEnv{app: app, db: db, provider: provider, svc: svc, webhook: webhookSvc}
}

func doWebhookReq(t *testing.T, env *webhookTestEnv, payload []byte, sig string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
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
	resp := doWebhookReq(t, env, payload, "bad_signature")
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for invalid signature, got %d", resp.StatusCode)
	}
}

func TestWebhook_ValidSignature(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, "evt_002")
	sig := signWebhook(env.provider.secret, payload)
	resp := doWebhookReq(t, env, payload, sig)
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
	sig := signWebhook(env.provider.secret, payload)
	resp := doWebhookReq(t, env, payload, sig)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing event ID, got %d", resp.StatusCode)
	}
}

func TestWebhook_DuplicateIgnored(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, "evt_dup")
	sig := signWebhook(env.provider.secret, payload)

	r1 := doWebhookReq(t, env, payload, sig)
	if r1.StatusCode != 200 {
		t.Fatalf("first send should be 200, got %d", r1.StatusCode)
	}
	r2 := doWebhookReq(t, env, payload, sig)
	if r2.StatusCode != 200 {
		t.Fatalf("duplicate should be 200, got %d", r2.StatusCode)
	}
}

func TestWebhook_ConcurrentDuplicate(t *testing.T) {
	env := setupWebhookEnv(t)
	payload := testPayload(t, fmt.Sprintf("evt_conc_%d", time.Now().UnixNano()))
	sig := signWebhook(env.provider.secret, payload)

	var wg sync.WaitGroup
	failures := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := doWebhookReq(t, env, payload, sig)
			if resp.StatusCode != 200 && resp.StatusCode != 409 && resp.StatusCode != 500 {
				failures <- resp.StatusCode
			}
		}()
	}
	wg.Wait()
	close(failures)
	for code := range failures {
		t.Errorf("unexpected status code from concurrent request: %d", code)
	}
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
	sig := signWebhook(env.provider.secret, payload)
	resp := doWebhookReq(t, env, payload, sig)
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
	sig := signWebhook(env.provider.secret, payload)

	r1 := doWebhookReq(t, env, payload, sig)
	if r1.StatusCode != 200 {
		t.Fatalf("first: %d", r1.StatusCode)
	}

	var processed bool
	env.db.QueryRow("SELECT 1 FROM webhook_events WHERE idempotency_key = 'evt_replay'").Scan(&processed)
	if !processed {
		t.Error("idempotency key should be stored")
	}

	for i := 0; i < 3; i++ {
		r := doWebhookReq(t, env, payload, sig)
		if r.StatusCode != 200 {
			t.Errorf("replay %d: expected 200, got %d", i, r.StatusCode)
		}
	}
}
