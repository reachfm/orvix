package handlers_test

// Route-level acceptance tests for the Platform Billing control-plane
// overview (GET /api/v1/platform/billing/tenants/:tenant_id/overview).
// Every field must come from the REAL billing/usage/invoice/ledger
// stores â€” no fabricated MRR, cards, or paid invoices â€” and the
// payment/provider state must be an honest configuration read.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestPlatformBillingOverview_RealDataAndHonestProvider(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Seed a real subscription + usage + ledger balance for tenant 1 so
	// the overview has real rows to read (the same stores the tenant
	// console and the platform ledger routes use). Usage records are
	// keyed by period_start = first day of the current month.
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, err := env.db.Exec(`INSERT INTO usage_records (tenant_id, period_start, period_end, mailboxes_used, domains_used, emails_sent) VALUES (1, ?, ?, 3, 2, 120)`, periodStart, periodStart.AddDate(0, 1, 0)); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	if _, err := env.db.Exec(`INSERT INTO platform_billing_balances (tenant_id, currency, balance_cents, version, updated_at) VALUES (1, 'usd', 1500, 1, datetime('now'))`); err != nil {
		t.Fatalf("seed balance: %v", err)
	}

	resp, raw := env.psaDo(t, "GET", "/api/v1/platform/billing/tenants/1/overview", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		TenantID     uint `json:"tenant_id"`
		Subscription *struct {
			TenantID uint   `json:"tenant_id"`
			PlanID   string `json:"plan_id"`
			Status   string `json:"status"`
		} `json:"subscription"`
		Plan *struct {
			ID string `json:"id"`
		} `json:"plan"`
		Usage *struct {
			TenantID      uint  `json:"tenant_id"`
			MailboxesUsed int   `json:"mailboxes_used"`
			DomainsUsed   int   `json:"domains_used"`
			EmailsSent    int64 `json:"emails_sent"`
		} `json:"usage"`
		Invoices []map[string]interface{} `json:"invoices"`
		Balance  *struct {
			TenantID     uint   `json:"tenant_id"`
			BalanceCents int64  `json:"balance_cents"`
			Currency     string `json:"currency"`
		} `json:"balance"`
		Adjustments []map[string]interface{} `json:"adjustments"`
		Payment     struct {
			Provider   string `json:"provider"`
			Enabled    bool   `json:"enabled"`
			Configured bool   `json:"configured"`
			Note       string `json:"note"`
		} `json:"payment_provider"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TenantID != 1 {
		t.Fatalf("wrong tenant: %+v", out.TenantID)
	}
	// Subscription is the real backfilled row (billing.Initialize
	// backfills seeded tenants).
	if out.Subscription == nil || out.Subscription.TenantID != 1 || out.Subscription.PlanID == "" {
		t.Fatalf("subscription must be the real row, got %+v", out.Subscription)
	}
	if out.Plan == nil || out.Plan.ID != out.Subscription.PlanID {
		t.Fatalf("plan must match the subscription plan, got %+v / %+v", out.Plan, out.Subscription)
	}
	if out.Usage == nil || out.Usage.MailboxesUsed != 3 || out.Usage.DomainsUsed != 2 || out.Usage.EmailsSent != 120 {
		t.Fatalf("usage must be the seeded real row, got %+v", out.Usage)
	}
	if out.Invoices == nil {
		t.Fatal("invoices must be an empty array (never null) when no invoices exist")
	}
	if out.Balance == nil || out.Balance.BalanceCents != 1500 || out.Balance.Currency != "usd" {
		t.Fatalf("balance must be the seeded ledger row, got %+v", out.Balance)
	}
	if out.Adjustments == nil {
		t.Fatal("adjustments must be an empty array (never null)")
	}
	// Provider honesty: the test config has no payment provider.
	if out.Payment.Configured || out.Payment.Provider != "" {
		t.Fatalf("no provider is configured in the test env, must be honest: %+v", out.Payment)
	}
}

func TestPlatformBillingOverview_TenantAdminDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "GET", "/api/v1/platform/billing/tenants/1/overview", env.tenantAdm, nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant admin must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformBillingOverview_InvalidTenant(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "GET", "/api/v1/platform/billing/tenants/0/overview", nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid tenant must be 400, got %d: %s", resp.StatusCode, raw)
	}
}
