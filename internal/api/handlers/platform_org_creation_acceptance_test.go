package handlers_test

// Route-level acceptance tests for Platform Super Admin organization
// creation (POST /api/v1/platform/organizations) — the closure of the
// documented MISSING_BACKEND capability. Exercised through the REAL
// router (api.NewRouter) and REAL middleware (auth, RBAC, CSRF,
// idempotency) against the production wiring:
//
//   - PSA-only gate (tenant roles denied);
//   - owner_email required (no ownerless ACTIVE organization);
//   - tenant + subscription + owner invitation created in one flow;
//   - owner invitation is the real invitation/activation model
//     (tenant_admin role, pending status, hashed token, expiry);
//   - Idempotency-Key required with replay returning the stored result;
//   - duplicate slug → stable 409 conflict.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type createOrgOut struct {
	Organization struct {
		ID     uint   `json:"id"`
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		Active bool   `json:"active"`
	} `json:"organization"`
	Invitation struct {
		ID             uint   `json:"id"`
		OrganizationID uint   `json:"organization_id"`
		Email          string `json:"email"`
		Role           string `json:"role"`
		Status         string `json:"status"`
	} `json:"invitation"`
	InviteToken string `json:"invite_token"`
}

func TestPlatformCreateOrganization_PSAValidCreate(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "PSA Created Org", "owner_email": "owner@psa-created.test", "plan_id": "free",
	}, map[string]string{"Idempotency-Key": "org-create-1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", resp.StatusCode, raw)
	}
	var out createOrgOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Organization.ID == 0 || out.Organization.Slug != "psa-created-org" {
		t.Fatalf("unexpected organization: %+v", out.Organization)
	}
	if !out.Organization.Active {
		t.Fatal("created organization must be active for the invitation/activation model")
	}
	// The owner invitation is the real activation path.
	if out.Invitation.Email != "owner@psa-created.test" || out.Invitation.Role != "tenant_admin" {
		t.Fatalf("owner invitation must be tenant_admin for the owner email, got %+v", out.Invitation)
	}
	if out.Invitation.Status != "pending" {
		t.Fatalf("owner invitation must start pending, got %q", out.Invitation.Status)
	}
	if out.InviteToken == "" || len(out.InviteToken) < 32 {
		t.Fatalf("one-time invite token must be returned once, got %q", out.InviteToken)
	}
	if out.Invitation.OrganizationID != out.Organization.ID {
		t.Fatalf("invitation must bind to the created organization")
	}

	// Tenant row exists and is NOT ownerless: a pending tenant_admin
	// invitation row exists for it, token stored only hashed.
	var inviteCount, tokenHashLen int
	var expiresAt time.Time
	err := env.db.QueryRow(`SELECT COUNT(*), LENGTH(token_hash), expires_at FROM org_invitations WHERE organization_id=? AND role='tenant_admin' AND status='pending'`, out.Organization.ID).Scan(&inviteCount, &tokenHashLen, &expiresAt)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("query invitation: %v", err)
	}
	if inviteCount != 1 {
		t.Fatalf("expected exactly one pending tenant_admin invitation, found %d", inviteCount)
	}
	if tokenHashLen != 64 {
		t.Fatalf("invitation token must be stored as a 64-char SHA-256 hash, got length %d", tokenHashLen)
	}
	if time.Until(expiresAt) < 6*24*time.Hour {
		t.Fatalf("owner invitation must carry a 7-day expiry, got %v", expiresAt)
	}
	// The raw token is NEVER persisted: the invitations table stores
	// only the SHA-256 hash, so the token can never leak from storage.
	var storedHash string
	if err := env.db.QueryRow(`SELECT token_hash FROM org_invitations WHERE id=?`, out.Invitation.ID).Scan(&storedHash); err != nil {
		t.Fatalf("read token hash: %v", err)
	}
	if storedHash == out.InviteToken {
		t.Fatal("raw invitation token must never be persisted — only its hash")
	}
	if len(storedHash) != 64 {
		t.Fatalf("token must be stored as a 64-char SHA-256 hash, got %q", storedHash)
	}

	// Subscription initialized consistently with self-signup.
	var subPlan string
	var subTenant int
	if err := env.db.QueryRow(`SELECT plan_id, tenant_id FROM subscriptions WHERE tenant_id=?`, out.Organization.ID).Scan(&subPlan, &subTenant); err != nil {
		t.Fatalf("subscription must exist for the new organization: %v", err)
	}
	if subPlan != "free" || subTenant != int(out.Organization.ID) {
		t.Fatalf("unexpected subscription: plan=%s tenant=%d", subPlan, subTenant)
	}
}

func TestPlatformCreateOrganization_OwnerEmailRequired(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "No Owner Org",
	}, map[string]string{"Idempotency-Key": "org-no-owner"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing owner_email must be 400, got %d: %s", resp.StatusCode, raw)
	}
	// No tenant may be left behind: never an ownerless ACTIVE org.
	var count int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE slug='no-owner-org'`).Scan(&count)
	if count != 0 {
		t.Fatalf("rejected creation must not leave a tenant row, found %d", count)
	}
}

func TestPlatformCreateOrganization_InvalidOwnerEmailRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "Bad Owner Org", "owner_email": "not-an-email",
	}, map[string]string{"Idempotency-Key": "org-bad-owner"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid owner_email must be 400, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateOrganization_TenantAdminDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/platform/organizations", env.tenantAdm, map[string]interface{}{
		"name": "Denied Org", "owner_email": "owner@denied.test",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF, "Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant admin must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateOrganization_MissingCSRFDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/platform/organizations", env.psaToken, map[string]interface{}{
		"name": "NoCSRF Org", "owner_email": "owner@nocsrf.test",
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateOrganization_MissingIdempotencyKey(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "NoKey Org", "owner_email": "owner@nokey.test",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key must be 400, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateOrganization_SameKeyReplayDoesNotDuplicate(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	body := map[string]interface{}{"name": "Replay Org", "owner_email": "owner@replay.test", "plan_id": "free"}
	resp1, raw1 := env.psaDo(t, "POST", "/api/v1/platform/organizations", body, map[string]string{"Idempotency-Key": "org-replay-key"})
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first create: %d %s", resp1.StatusCode, raw1)
	}
	var out1 createOrgOut
	if err := json.Unmarshal(raw1, &out1); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	resp2, raw2 := env.psaDo(t, "POST", "/api/v1/platform/organizations", body, map[string]string{"Idempotency-Key": "org-replay-key"})
	if resp2.StatusCode != http.StatusCreated || resp2.Header.Get("X-Idempotency-Replay") != "true" {
		t.Fatalf("replay must return the original 201 with X-Idempotency-Replay, got %d %s", resp2.StatusCode, raw2)
	}
	var count int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE slug='replay-org'`).Scan(&count)
	if count != 1 {
		t.Fatalf("replay must not create a second organization, found %d", count)
	}
	// The stored replay body never contains the one-time token.
	if stringContains(string(raw2), out1.InviteToken) {
		t.Fatal("replayed body must not contain the one-time invite token")
	}
}

func TestPlatformCreateOrganization_DuplicateSlugConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	body := map[string]interface{}{"name": "Dup Org", "owner_email": "owner@dup.test"}
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations", body, map[string]string{"Idempotency-Key": "org-dup-1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: %d %s", resp.StatusCode, raw)
	}
	resp2, raw2 := env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{"name": "Dup Org", "owner_email": "other@dup.test"}, map[string]string{"Idempotency-Key": "org-dup-2"})
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate slug must be 409, got %d: %s", resp2.StatusCode, raw2)
	}
}

func stringContains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
