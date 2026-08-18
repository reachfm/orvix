package handlers_test

// Route-level acceptance tests for the organization-portal invitation
// resend surface (POST /enterprise/invitations/:id/resend) and the
// tenant billing state surface (GET /enterprise/billing/state).

import (
	"encoding/json"
	"net/http"
	"testing"
)

// ── Invitation resend (R-6) ────────────────────────────────────────

func TestInvitationResend_ValidLifecycle(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Tenant 1 admin creates an invitation.
	resp, raw := env.do(t, "POST", "/api/v1/enterprise/invitations", env.tenantAdm, map[string]interface{}{
		"email": "new-admin@t1.example", "role": "tenant_operator",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invitation: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Invitation struct {
			ID             uint   `json:"id"`
			OrganizationID uint   `json:"organization_id"`
			Status         string `json:"status"`
		} `json:"invitation"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Token == "" {
		t.Fatal("create must return the one-time token")
	}

	// Re-issue the token.
	path := "/api/v1/enterprise/invitations/" + uiToStr(created.Invitation.ID) + "/resend"
	resp, raw = env.do(t, "POST", path, env.tenantAdm, nil, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resend status %d: %s", resp.StatusCode, raw)
	}
	var resent struct {
		Invitation struct {
			ID             uint   `json:"id"`
			OrganizationID uint   `json:"organization_id"`
			Status         string `json:"status"`
		} `json:"invitation"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &resent); err != nil {
		t.Fatalf("decode resent: %v", err)
	}
	if resent.Token == "" || resent.Token == created.Token {
		t.Fatal("resend must return a NEW one-time token")
	}
	if resent.Invitation.Status != "pending" {
		t.Fatalf("resend must keep the invitation pending, got %q", resent.Invitation.Status)
	}
	// The old token must be invalidated: the stored hash changed.
	var oldHash, newHash string
	_ = env.db.QueryRow(`SELECT token_hash FROM org_invitations WHERE id=?`, created.Invitation.ID).Scan(&newHash)
	if newHash == "" || newHash == oldHash {
		t.Fatal("stored token hash must rotate")
	}
}

func TestInvitationResend_RevokedInvitationRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/enterprise/invitations", env.tenantAdm, map[string]interface{}{
		"email": "revoked@t1.example", "role": "tenant_support",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invitation: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Invitation struct {
			ID uint `json:"id"`
		} `json:"invitation"`
	}
	_ = json.Unmarshal(raw, &created)

	// Revoke, then resend → must be rejected.
	env.do(t, "POST", "/api/v1/enterprise/invitations/"+uiToStr(created.Invitation.ID)+"/revoke", env.tenantAdm, nil, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	resp, raw = env.do(t, "POST", "/api/v1/enterprise/invitations/"+uiToStr(created.Invitation.ID)+"/resend", env.tenantAdm, nil, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resend of revoked invitation must be 409, got %d: %s", resp.StatusCode, raw)
	}
}

func TestInvitationResend_CrossTenantNotFound(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	// Tenant 1 creates an invitation; tenant 2 (env.otherAdm) resends it.
	resp, raw := env.do(t, "POST", "/api/v1/enterprise/invitations", env.tenantAdm, map[string]interface{}{
		"email": "other-tenant@t1.example", "role": "tenant_admin",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invitation: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Invitation struct {
			ID uint `json:"id"`
		} `json:"invitation"`
	}
	_ = json.Unmarshal(raw, &created)

	resp, _ = env.do(t, "POST", "/api/v1/enterprise/invitations/"+uiToStr(created.Invitation.ID)+"/resend", env.otherAdm, nil, map[string]string{"Cookie": "csrf_token=" + env.otherCSRF, "X-CSRF-Token": env.otherCSRF})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant resend must be 404, got %d", resp.StatusCode)
	}
}

// ── Tenant billing state (R-7) ─────────────────────────────────────

func TestBillingState_RealDataAndHonestProvider(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "GET", "/api/v1/enterprise/billing/state", env.tenantAdm, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("billing state status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		TenantID     uint `json:"tenant_id"`
		Subscription *struct {
			TenantID uint   `json:"tenant_id"`
			PlanID   string `json:"plan_id"`
		} `json:"subscription"`
		Plan *struct {
			ID string `json:"id"`
		} `json:"plan"`
		Usage *struct {
			TenantID uint `json:"tenant_id"`
		} `json:"usage"`
		Invoices []map[string]interface{} `json:"invoices"`
		Payment  struct {
			Provider   string `json:"provider"`
			Configured bool   `json:"configured"`
			Note       string `json:"note"`
		} `json:"payment_provider"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TenantID != 1 {
		t.Fatalf("billing state must be the authenticated tenant, got %d", out.TenantID)
	}
	// The seeded tenant has a backfilled real subscription.
	if out.Subscription == nil || out.Subscription.TenantID != 1 {
		t.Fatalf("subscription must be the real backfilled row: %+v", out.Subscription)
	}
	if out.Plan == nil || out.Plan.ID != out.Subscription.PlanID {
		t.Fatalf("plan must match subscription: %+v / %+v", out.Plan, out.Subscription)
	}
	if out.Invoices == nil {
		t.Fatal("invoices must be an empty array, never null")
	}
	// No provider configured in the test env → honest state.
	if out.Payment.Configured || out.Payment.Provider != "" {
		t.Fatalf("provider must be honestly not-configured: %+v", out.Payment)
	}
	if out.Payment.Note == "" {
		t.Fatal("provider note must explain the not-configured state")
	}
}
