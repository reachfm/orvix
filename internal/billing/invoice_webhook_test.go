package billing

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/dbdialect"

	_ "modernc.org/sqlite"
)

type invoiceWebhookEnv struct {
	app     *fiber.App
	db      *sql.DB
	svc     *Service
	webhook *WebhookService
	invSvc  *InvoiceService
	secret  string
}

func invoicePayload(t *testing.T, eventID, eventType string, extra map[string]interface{}, createdTime ...time.Time) []byte {
	t.Helper()
	ct := time.Now().UTC()
	if len(createdTime) > 0 {
		ct = createdTime[0]
	}
	obj := map[string]interface{}{
		"id":             eventID,
		"subscription":   "sub_inv_test",
		"status":         "active",
		"payment_status": "paid",
		"invoice_number": "INV-001",
		"amount_paid":    10000,
		"amount_due":     10000,
		"subtotal":       10000,
		"tax":            0,
		"total":          10000,
		"currency":       "usd",
		"period_start":   ct.Add(-30 * 24 * time.Hour).Unix(),
		"period_end":     ct.Unix(),
		"created":        ct.Unix(),
	}
	for k, v := range extra {
		obj[k] = v
	}
	p, _ := json.Marshal(map[string]interface{}{
		"type": eventType,
		"data": map[string]interface{}{"object": obj},
	})
	return p
}

func setupInvoiceWebhookEnv(t *testing.T) *invoiceWebhookEnv {
	t.Helper()
	db := newTestDB(t)
	db.SetMaxOpenConns(1)

	// Create invoices table for test — dialect-aware DDL.
	dial, err := dbdialect.Detect(db)
	if err != nil {
		dial = dbdialect.FromDriver("sqlite")
	}
	var createInvSQL string
	if dial.IsPostgres() {
		createInvSQL = `CREATE TABLE IF NOT EXISTS invoices (
			id ` + dial.AutoIncrement() + `,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			tenant_id INTEGER NOT NULL, subscription_id INTEGER,
			provider TEXT NOT NULL DEFAULT '', provider_invoice_id TEXT,
			invoice_number TEXT, currency TEXT NOT NULL DEFAULT 'usd',
			subtotal BIGINT NOT NULL DEFAULT 0, tax BIGINT NOT NULL DEFAULT 0,
			total BIGINT NOT NULL DEFAULT 0, amount_paid BIGINT NOT NULL DEFAULT 0,
			amount_due BIGINT NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'draft',
			period_start TIMESTAMP, period_end TIMESTAMP, issued_at TIMESTAMP,
			due_at TIMESTAMP, paid_at TIMESTAMP, hosted_invoice_url TEXT, pdf_url TEXT,
			provider_event_created_at TIMESTAMP, provider_event_id TEXT,
			provider_updated_at TIMESTAMP,
			UNIQUE(provider, provider_invoice_id)
		)`
	} else {
		createInvSQL = `CREATE TABLE IF NOT EXISTS invoices (
			id ` + dial.AutoIncrement() + `,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			tenant_id INTEGER NOT NULL, subscription_id INTEGER,
			provider TEXT NOT NULL DEFAULT '', provider_invoice_id TEXT,
			invoice_number TEXT, currency TEXT NOT NULL DEFAULT 'usd',
			subtotal INTEGER NOT NULL DEFAULT 0, tax INTEGER NOT NULL DEFAULT 0,
			total INTEGER NOT NULL DEFAULT 0, amount_paid INTEGER NOT NULL DEFAULT 0,
			amount_due INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'draft',
			period_start DATETIME, period_end DATETIME, issued_at DATETIME,
			due_at DATETIME, paid_at DATETIME, hosted_invoice_url TEXT, pdf_url TEXT,
			provider_event_created_at DATETIME, provider_event_id TEXT,
			provider_updated_at DATETIME,
			UNIQUE(provider, provider_invoice_id)
		)`
	}
	if _, err := db.Exec(createInvSQL); err != nil {
		t.Fatal(err)
	}

	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	sub, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = sub
	db.Exec("UPDATE subscriptions SET provider_sub_id = 'sub_inv_test' WHERE tenant_id = 1")

	webhookSvc := NewWebhookService(db)
	invSvc := NewInvoiceService(db)

	b := make([]byte, 16)
	secret := fmt.Sprintf("%x", b)

	app := fiber.New()
	app.Post("/api/v1/billing/webhook", func(c fiber.Ctx) error {
		if webhookSvc == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "not configured"})
		}
		ts := c.Get("X-Payment-Timestamp")
		if ts == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing timestamp"})
		}
		sig := c.Get("X-Payment-Signature")
		if sig == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing signature"})
		}
		raw := c.Body()
		if len(raw) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "empty payload"})
		}

		// Parse full payload for invoice fields.
		var full struct {
			Type string `json:"type"`
			Data struct {
				Object struct {
					ID            string `json:"id"`
					InvoiceID     string `json:"invoice_id"`
					Subscription  string `json:"subscription"`
					InvoiceNumber string `json:"invoice_number"`
					AmountPaid    int64  `json:"amount_paid"`
					AmountDue     int64  `json:"amount_due"`
					Subtotal      int64  `json:"subtotal"`
					Tax           int64  `json:"tax"`
					Total         int64  `json:"total"`
					Currency      string `json:"currency"`
					PaymentStatus string `json:"payment_status"`
					PeriodStart   int64  `json:"period_start"`
					PeriodEnd     int64  `json:"period_end"`
					Created       int64  `json:"created"`
				} `json:"object"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &full); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid json"})
		}

		eventID := full.Data.Object.ID
		if eventID == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing event id"})
		}

		// Use invoice_id if present, otherwise fall back to the object ID.
		invoiceID := full.Data.Object.InvoiceID
		if invoiceID == "" {
			invoiceID = eventID
		}

		rec := &WebhookEventRecord{
			ID:             eventID,
			Provider:       "test",
			EventType:      full.Type,
			ProviderSubID:  full.Data.Object.Subscription,
			RawPayload:     raw,
			Signature:      sig,
			ReceivedAt:     time.Now().UTC(),
			IdempotencyKey: eventID,
		}
		var invoice *InvoiceRecord

		// If this is an invoice event, create/update the invoice using invoiceID.
		if strings.HasPrefix(full.Type, "invoice.") && invSvc != nil {
			sub, subErr := svc.GetSubscriptionByProviderID(full.Data.Object.Subscription)
			if subErr == nil && sub != nil {
				now := time.Now().UTC()
				created := &now
				if full.Data.Object.Created > 0 {
					tm := time.Unix(full.Data.Object.Created, 0)
					created = &tm
				}
				invoice = &InvoiceRecord{
					TenantID:          sub.TenantID,
					SubscriptionID:    &sub.ID,
					Provider:          "test",
					ProviderInvoiceID: invoiceID,
					InvoiceNumber:     full.Data.Object.InvoiceNumber,
					Currency:          strings.ToUpper(full.Data.Object.Currency),
					Subtotal:          full.Data.Object.Subtotal,
					Tax:               full.Data.Object.Tax,
					Total:             full.Data.Object.Total,
					AmountPaid:        full.Data.Object.AmountPaid,
					AmountDue:         full.Data.Object.AmountDue,
					Status:            mapTestInvoiceStatus(full.Data.Object.PaymentStatus, full.Type),
				}
				invoice.IssuedAt = created
			}
		}

		err := webhookSvc.ProcessEvent(c.Context(), rec, func(tx *sql.Tx) error {
			if invoice == nil {
				return nil
			}
			_, err := invSvc.UpsertFromProviderEventTx(c.Context(), tx, invoice, invoice.IssuedAt, eventID)
			return err
		})
		if err != nil {
			if err == ErrWebhookAlreadyProcessed {
				return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "already_processed"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "event processing failed"})
		}

		if full.Data.Object.PaymentStatus == "paid" && full.Data.Object.Subscription != "" {
			if sub, subErr := svc.GetSubscriptionByProviderID(full.Data.Object.Subscription); subErr == nil {
				svc.TransitionState(sub.TenantID, SubActive)
			}
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "received"})
	})

	return &invoiceWebhookEnv{
		app: app, db: db, svc: svc, webhook: webhookSvc, invSvc: invSvc, secret: secret,
	}
}

func mapTestInvoiceStatus(paymentStatus, eventType string) string {
	if paymentStatus == "paid" || strings.HasSuffix(eventType, ".paid") {
		return "paid"
	}
	if strings.HasSuffix(eventType, ".voided") {
		return "void"
	}
	if strings.HasSuffix(eventType, ".payment_failed") {
		return "past_due"
	}
	if strings.HasSuffix(eventType, ".finalized") {
		return "open"
	}
	switch paymentStatus {
	case "open":
		return "open"
	case "past_due":
		return "past_due"
	default:
		return "draft"
	}
}

func doInvoiceWebhook(t *testing.T, env *invoiceWebhookEnv, payload []byte) *http.Response {
	t.Helper()
	ts := fmt.Sprintf("%d", time.Now().Unix())
	req := httptest.NewRequest("POST", "/api/v1/billing/webhook", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Payment-Timestamp", ts)
	req.Header.Set("X-Payment-Signature", fmt.Sprintf("%x", []byte("test-sig")))
	resp, err := env.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestInvoiceWebhook_Created(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	payload := invoicePayload(t, "evt_inv_created", "invoice.created", nil)
	resp := doInvoiceWebhook(t, env, payload)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var count int64
	env.db.QueryRow("SELECT COUNT(*) FROM invoices WHERE provider_invoice_id = 'evt_inv_created'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 invoice, got %d", count)
	}
}

func TestInvoiceWebhook_Updated(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	now := time.Now().UTC()

	payload := invoicePayload(t, "evt_create_inv", "invoice.created", map[string]interface{}{"total": 5000}, now)
	doInvoiceWebhook(t, env, payload)

	var total int64
	err := env.db.QueryRow("SELECT total FROM invoices WHERE provider_invoice_id = 'evt_create_inv'").Scan(&total)
	if err != nil {
		t.Fatalf("first select: %v", err)
	}
	if total != 5000 {
		t.Fatalf("expected total 5000 after create, got %d", total)
	}

	// Update webhook with different event ID but same invoice_id.
	payload2 := invoicePayload(t, "evt_update_inv_2", "invoice.updated",
		map[string]interface{}{
			"total":      7500,
			"invoice_id": "evt_create_inv",
		}, now.Add(time.Hour))
	resp := doInvoiceWebhook(t, env, payload2)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	env.db.QueryRow("SELECT total FROM invoices WHERE provider_invoice_id = 'evt_create_inv'").Scan(&total)
	if total != 7500 {
		t.Fatalf("expected total 7500 after update, got %d", total)
	}
}

func TestInvoiceWebhook_Duplicate(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	payload := invoicePayload(t, "evt_inv_dup", "invoice.created", nil)

	r1 := doInvoiceWebhook(t, env, payload)
	if r1.StatusCode != 200 {
		t.Fatalf("first: expected 200, got %d", r1.StatusCode)
	}

	r2 := doInvoiceWebhook(t, env, payload)
	if r2.StatusCode != 200 {
		t.Fatalf("duplicate: expected 200, got %d", r2.StatusCode)
	}

	var count int64
	env.db.QueryRow("SELECT COUNT(*) FROM invoices WHERE provider_invoice_id = 'evt_inv_dup'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 invoice row, got %d", count)
	}
}

func TestInvoiceWebhook_Paid(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	payload := invoicePayload(t, "evt_inv_paid", "invoice.paid", map[string]interface{}{
		"payment_status": "paid",
		"amount_paid":    10000,
	})
	resp := doInvoiceWebhook(t, env, payload)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status string
	env.db.QueryRow("SELECT status FROM invoices WHERE provider_invoice_id = 'evt_inv_paid'").Scan(&status)
	if status != "paid" {
		t.Fatalf("expected 'paid', got '%s'", status)
	}
}

func TestInvoiceWebhook_Voided(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	payload := invoicePayload(t, "evt_inv_voided", "invoice.voided", map[string]interface{}{
		"payment_status": "void",
	})
	resp := doInvoiceWebhook(t, env, payload)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status string
	env.db.QueryRow("SELECT status FROM invoices WHERE provider_invoice_id = 'evt_inv_voided'").Scan(&status)
	if status != "void" {
		t.Fatalf("expected 'void', got '%s'", status)
	}
}

func TestInvoiceWebhook_PaymentFailed(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	payload := invoicePayload(t, "evt_inv_failed", "invoice.payment_failed", map[string]interface{}{
		"payment_status": "past_due",
	})
	resp := doInvoiceWebhook(t, env, payload)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status string
	env.db.QueryRow("SELECT status FROM invoices WHERE provider_invoice_id = 'evt_inv_failed'").Scan(&status)
	if status != "past_due" {
		t.Fatalf("expected 'past_due', got '%s'", status)
	}
}

func TestInvoiceWebhook_Finalized(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	payload := invoicePayload(t, "evt_inv_finalized", "invoice.finalized", map[string]interface{}{
		"payment_status": "open",
	})
	resp := doInvoiceWebhook(t, env, payload)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status string
	env.db.QueryRow("SELECT status FROM invoices WHERE provider_invoice_id = 'evt_inv_finalized'").Scan(&status)
	if status != "open" {
		t.Fatalf("expected 'open', got '%s'", status)
	}
}

func TestInvoiceWebhook_UnknownSubscription(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	// Override subscription to a non-existent one.
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "invoice.created",
		"data": map[string]interface{}{
			"object": map[string]interface{}{
				"id":             "evt_unknown_sub",
				"subscription":   "sub_nonexistent",
				"status":         "active",
				"payment_status": "open",
			},
		},
	})
	resp := doInvoiceWebhook(t, env, payload)
	// Even with unknown subscription, webhook should be recorded.
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var eventCount int64
	env.db.QueryRow("SELECT COUNT(*) FROM webhook_events WHERE id = 'evt_unknown_sub'").Scan(&eventCount)
	if eventCount != 1 {
		t.Fatal("webhook event should be recorded even with unknown subscription")
	}
	// No invoice should be created since subscription lookup failed.
	var invCount int64
	env.db.QueryRow("SELECT COUNT(*) FROM invoices WHERE provider_invoice_id = 'evt_unknown_sub'").Scan(&invCount)
	if invCount != 0 {
		t.Fatal("no invoice should be created for unknown subscription")
	}
}

func TestInvoiceWebhook_TenantIsolationUsesSubscriptionOwner(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	if _, err := env.svc.CreateSubscription(2, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Exec("UPDATE subscriptions SET provider_sub_id = 'sub_inv_tenant_2' WHERE tenant_id = 2"); err != nil {
		t.Fatal(err)
	}

	payload := invoicePayload(t, "evt_tenant_2", "invoice.created", map[string]interface{}{
		"subscription": "sub_inv_tenant_2",
	})
	resp := doInvoiceWebhook(t, env, payload)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var tenantID uint
	if err := env.db.QueryRow("SELECT tenant_id FROM invoices WHERE provider_invoice_id = 'evt_tenant_2'").Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if tenantID != 2 {
		t.Fatalf("invoice tenant: got %d want 2", tenantID)
	}
	var tenantOneCount int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM invoices WHERE provider_invoice_id = 'evt_tenant_2' AND tenant_id = 1").Scan(&tenantOneCount); err != nil {
		t.Fatal(err)
	}
	if tenantOneCount != 0 {
		t.Fatalf("tenant 1 can observe tenant 2 invoice row: count=%d", tenantOneCount)
	}
}

func TestInvoiceWebhook_OlderEventDoesNotRegressPaid(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	now := time.Now().UTC()

	// Send paid event with recent timestamp
	payload := invoicePayload(t, "evt_ordering", "invoice.paid", map[string]interface{}{
		"payment_status": "paid",
	}, now)
	doInvoiceWebhook(t, env, payload)

	var status string
	env.db.QueryRow("SELECT status FROM invoices WHERE provider_invoice_id = 'evt_ordering'").Scan(&status)
	if status != "paid" {
		t.Fatalf("expected 'paid', got '%s'", status)
	}

	// Send an older payment_failed event
	oldPayload := invoicePayload(t, "evt_ordering", "invoice.payment_failed", map[string]interface{}{
		"payment_status": "past_due",
	}, now.Add(-2*time.Hour))
	doInvoiceWebhook(t, env, oldPayload)

	env.db.QueryRow("SELECT status FROM invoices WHERE provider_invoice_id = 'evt_ordering'").Scan(&status)
	if status != "paid" {
		t.Fatalf("expected 'paid' after older event (got '%s')", status)
	}
}

func TestInvoiceWebhook_NewerVoidOverridesOpen(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	now := time.Now().UTC()

	payload := invoicePayload(t, "evt_void_create", "invoice.created", map[string]interface{}{
		"payment_status": "open",
	}, now)
	doInvoiceWebhook(t, env, payload)

	// Send void with newer timestamp and different event ID, referencing the same invoice
	voidPayload := invoicePayload(t, "evt_void_2", "invoice.voided", map[string]interface{}{
		"payment_status": "void",
		"invoice_id":     "evt_void_create",
	}, now.Add(time.Hour))
	doInvoiceWebhook(t, env, voidPayload)

	var status string
	env.db.QueryRow("SELECT status FROM invoices WHERE provider_invoice_id = 'evt_void_create'").Scan(&status)
	if status != "void" {
		t.Fatalf("expected 'void', got '%s'", status)
	}
}

func TestInvoiceWebhook_RollbackOnDBFailure(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	defer func() {
		// Cleanup
		env.db.Exec("DROP TABLE IF EXISTS invoices")
	}()

	payload := invoicePayload(t, "evt_rollback", "invoice.created", nil, time.Now().UTC())

	// Simulate DB failure by dropping the invoices table
	env.db.Exec("DROP TABLE IF EXISTS invoices")

	resp := doInvoiceWebhook(t, env, payload)
	if resp.StatusCode != 500 {
		t.Fatalf("webhook DB failure: expected 500, got %d", resp.StatusCode)
	}
	var eventCount int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM webhook_events WHERE provider = 'test' AND id = 'evt_rollback'").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("failed invoice write permanently consumed event: count=%d", eventCount)
	}

	// Recreate the table
	dial, err := dbdialect.Detect(env.db)
	if err != nil {
		t.Fatal(err)
	}
	timestampType := dial.TimestampType()
	rebuildSQL := `CREATE TABLE IF NOT EXISTS invoices (
		id ` + dial.AutoIncrement() + `,
		created_at ` + timestampType + ` NOT NULL, updated_at ` + timestampType + ` NOT NULL,
		tenant_id INTEGER NOT NULL, subscription_id INTEGER,
		provider TEXT NOT NULL DEFAULT '', provider_invoice_id TEXT,
		invoice_number TEXT, currency TEXT NOT NULL DEFAULT 'usd',
		subtotal BIGINT NOT NULL DEFAULT 0, tax BIGINT NOT NULL DEFAULT 0,
		total BIGINT NOT NULL DEFAULT 0, amount_paid BIGINT NOT NULL DEFAULT 0,
		amount_due BIGINT NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'draft',
		period_start ` + timestampType + `, period_end ` + timestampType + `, issued_at ` + timestampType + `,
		due_at ` + timestampType + `, paid_at ` + timestampType + `, hosted_invoice_url TEXT, pdf_url TEXT,
		provider_event_created_at ` + timestampType + `, provider_event_id TEXT,
		provider_updated_at ` + timestampType + `,
		UNIQUE(provider, provider_invoice_id)
	)`
	if _, err := env.db.Exec(rebuildSQL); err != nil {
		t.Fatal(err)
	}

	// Retry the same provider event after restoring the invoice table.
	resp = doInvoiceWebhook(t, env, payload)
	if resp.StatusCode != 200 {
		t.Fatalf("retry after DB restore: expected 200, got %d", resp.StatusCode)
	}

	var count int64
	env.db.QueryRow("SELECT COUNT(*) FROM invoices WHERE provider_invoice_id = 'evt_rollback'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 invoice after retry, got %d", count)
	}
}

func TestInvoiceWebhook_ConcurrentNoDuplicate(t *testing.T) {
	env := setupInvoiceWebhookEnv(t)
	payload := invoicePayload(t, fmt.Sprintf("evt_conc_%d", time.Now().UnixNano()), "invoice.created", nil)

	var wg sync.WaitGroup
	statusCodes := make(chan int, 10)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := doInvoiceWebhook(t, env, payload)
			statusCodes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(statusCodes)

	for code := range statusCodes {
		if code != 200 {
			t.Errorf("expected 200, got %d", code)
		}
	}

	var count int64
	env.db.QueryRow("SELECT COUNT(*) FROM invoices WHERE provider_invoice_id LIKE 'evt_conc_%'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 invoice, got %d", count)
	}
}
