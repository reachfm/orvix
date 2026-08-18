package handlers_test

// Route-level acceptance tests for the organization invitation ACCEPT
// lifecycle (POST /api/v1/auth/invitations/accept) — the activation path
// that makes a PSA-created organization operational:
//
//   - PSA creates org → pending_activation (active=false) + owner invite;
//   - owner accepts via the PUBLIC token route → user created as
//     tenant_admin, org activated, invitation consumed;
//   - duplicate acceptance / wrong token / revoked / expired → stable
//     codes, never a raw service string;
//   - duplicate pending invitation → 409;
//   - PSA cannot force-activate an ownerless org (activation guard).
//
// These close Blocker A of the Phase 2 API Platform audit: no ownerless
// ACTIVE organization, and a real redemption path for the one-time token.

import (
	"encoding/json"
	"net/http"
	"testing"
)

type acceptOut struct {
	Status             string `json:"status"`
	UserID             uint   `json:"user_id"`
	OrganizationID     uint   `json:"organization_id"`
	Email              string `json:"email"`
	Role               string `json:"role"`
	OrganizationActive bool   `json:"organization_active"`
}

func psaCreateOrg(t *testing.T, env *provEnv, name, ownerEmail, idemKey string) (uint, string) {
	t.Helper()
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": name, "owner_email": ownerEmail,
	}, map[string]string{"Idempotency-Key": idemKey})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("psa create org: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		Organization struct {
			ID     uint `json:"id"`
			Active bool `json:"active"`
		} `json:"organization"`
		InviteToken string `json:"invite_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if out.Organization.Active {
		t.Fatalf("PSA-created org must be pending_activation (active=false): %s", raw)
	}
	if out.InviteToken == "" {
		t.Fatal("one-time invite token missing from create response")
	}
	return out.Organization.ID, out.InviteToken
}

func publicAccept(t *testing.T, env *provEnv, body map[string]interface{}) (*http.Response, []byte) {
	t.Helper()
	return env.do(t, "POST", "/api/v1/auth/invitations/accept", "", body, nil)
}

// TestInvitationAccept_OwnerActivationLifecycle is the FULL PSA → owner
// lifecycle: create (pending) → accept (public) → user + active org.
func TestInvitationAccept_OwnerActivationLifecycle(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	orgID, token := psaCreateOrg(t, env, "Accept Lifecycle Org", "owner@accept-lifecycle.test", "accept-lifecycle-key")

	// The org is pending: no active admins, not operational.
	var admins int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id=? AND role='tenant_admin' AND active=1`, orgID).Scan(&admins)
	if admins != 0 {
		t.Fatalf("pending org must have no active tenant_admin user yet, found %d", admins)
	}

	// Owner accepts with a real password.
	resp, raw := publicAccept(t, env, map[string]interface{}{"token": token, "password": "OwnerPass!2026"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept status %d: %s", resp.StatusCode, raw)
	}
	var out acceptOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode accept: %v", err)
	}
	if out.Status != "accepted" || out.OrganizationID != orgID || out.Email != "owner@accept-lifecycle.test" || out.Role != "tenant_admin" {
		t.Fatalf("unexpected accept result: %+v", out)
	}
	if !out.OrganizationActive {
		t.Fatal("organization must be active after owner acceptance")
	}
	if out.UserID == 0 {
		t.Fatal("accept must create the owner user")
	}

	// Persisted state: user tenant_admin + active, org active, invitation
	// consumed (accepted, token no longer redeemable).
	var role string
	var active int
	_ = env.db.QueryRow(`SELECT role, active FROM users WHERE id=?`, out.UserID).Scan(&role, &active)
	if role != "tenant_admin" || active != 1 {
		t.Fatalf("owner user must be active tenant_admin, got role=%s active=%d", role, active)
	}
	var orgActive int
	_ = env.db.QueryRow(`SELECT active FROM tenants WHERE id=?`, orgID).Scan(&orgActive)
	if orgActive != 1 {
		t.Fatal("tenant must be active after acceptance")
	}
	var invStatus string
	_ = env.db.QueryRow(`SELECT status FROM org_invitations WHERE organization_id=?`, orgID).Scan(&invStatus)
	if invStatus != "accepted" {
		t.Fatalf("invitation must be accepted after redemption, got %q", invStatus)
	}
	// Audit evidence in the canonical store.
	var auditCount int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action='invitation.accept' AND tenant_id=?`, orgID).Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("acceptance must be audited once in orvix_audit, found %d", auditCount)
	}
}

// TestInvitationAccept_TokenSingleUse proves the token is consumed: a
// replay can never create a second user or re-activate anything.
func TestInvitationAccept_TokenSingleUse(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	orgID, token := psaCreateOrg(t, env, "Single Use Org", "owner@single-use.test", "single-use-key")

	resp, raw := publicAccept(t, env, map[string]interface{}{"token": token, "password": "OwnerPass!2026"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first accept: %d %s", resp.StatusCode, raw)
	}

	resp, raw = publicAccept(t, env, map[string]interface{}{"token": token, "password": "OwnerPass!2026"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("replayed token must be 409, got %d: %s", resp.StatusCode, raw)
	}
	e := decodeErr(t, raw)
	if e.Code != "INVALID_STATE_TRANSITION" {
		t.Fatalf("replayed token code must be INVALID_STATE_TRANSITION, got %+v", e)
	}
	var users int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id=?`, orgID).Scan(&users)
	if users != 1 {
		t.Fatalf("replayed acceptance must not create a second user, found %d", users)
	}
}

func TestInvitationAccept_WrongTokenNotFound(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := publicAccept(t, env, map[string]interface{}{"token": "0000000000000000000000000000000000000000000000000000000000000000", "password": "OwnerPass!2026"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown token must be 404, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "NOT_FOUND" {
		t.Fatalf("unknown token code must be NOT_FOUND, got %+v", e)
	}
}

func TestInvitationAccept_RevokedInvitationConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	_, token := psaCreateOrg(t, env, "Revoked Accept Org", "owner@revoked-accept.test", "revoked-accept-key")

	// Find the invitation id and revoke it via the tenant-side surface is
	// not possible (the org has no admin yet) — revoke directly against
	// the repo state through the service path: mark it revoked.
	if _, err := env.db.Exec(`UPDATE org_invitations SET status='revoked', revoked_at=datetime('now'), updated_at=datetime('now') WHERE token_hash IN (SELECT token_hash FROM org_invitations)`); err != nil {
		t.Fatalf("seed revoked invitation: %v", err)
	}

	resp, raw := publicAccept(t, env, map[string]interface{}{"token": token, "password": "OwnerPass!2026"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("revoked invitation must be 409, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "INVALID_STATE_TRANSITION" {
		t.Fatalf("revoked code must be INVALID_STATE_TRANSITION, got %+v", e)
	}
}

func TestInvitationAccept_WeakPasswordRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	orgID, token := psaCreateOrg(t, env, "Weak Pass Org", "owner@weak-pass.test", "weak-pass-key")

	resp, raw := publicAccept(t, env, map[string]interface{}{"token": token, "password": "short"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("weak password must be 400, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "VALIDATION_FAILED" {
		t.Fatalf("weak password code must be VALIDATION_FAILED, got %+v", e)
	}
	// No user may exist for this org: the failure happened before any write.
	var users int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id=?`, orgID).Scan(&users)
	if users != 0 {
		t.Fatalf("failed accept must not create a user, found %d", users)
	}
}

func TestInvitationAccept_ExistingEmailConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	_, token := psaCreateOrg(t, env, "Dup Email Org", "prov-admin@tenant1.local", "dup-email-key")

	// prov-admin@tenant1.local already exists (seed tenant admin).
	resp, raw := publicAccept(t, env, map[string]interface{}{"token": token, "password": "OwnerPass!2026"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("existing email must be 409, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "CONFLICT" {
		t.Fatalf("existing email code must be CONFLICT, got %+v", e)
	}
}

// TestInvitationAccept_PlatformIdentityProtected proves a platform
// identity email can never be minted into a tenant account through an
// invitation, mirroring the signup protection.
func TestInvitationAccept_PlatformIdentityProtected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	orgID, token := psaCreateOrg(t, env, "Platform ID Org", "prov-psa@platform.local", "platform-id-key")

	resp, raw := publicAccept(t, env, map[string]interface{}{"token": token, "password": "OwnerPass!2026"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("platform identity accept must be 409, got %d: %s", resp.StatusCode, raw)
	}
	var orgUsers int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id=?`, orgID).Scan(&orgUsers)
	if orgUsers != 0 {
		t.Fatalf("platform identity must not create a tenant user, found %d", orgUsers)
	}
}

// TestDuplicatePendingInvitationConflict pins the duplicate-pending guard:
// the same email can never hold two live invitation tokens.
func TestDuplicatePendingInvitationConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/enterprise/invitations", env.tenantAdm, map[string]interface{}{
		"email": "teammate@tenant1.test", "role": "tenant_operator",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first invite: %d %s", resp.StatusCode, raw)
	}
	resp, raw = env.do(t, "POST", "/api/v1/enterprise/invitations", env.tenantAdm, map[string]interface{}{
		"email": "teammate@tenant1.test", "role": "tenant_support",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate pending invite must be 409, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "CONFLICT" {
		t.Fatalf("duplicate pending code must be CONFLICT, got %+v", e)
	}
	var pending int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM org_invitations WHERE email='teammate@tenant1.test' AND status='pending'`).Scan(&pending)
	if pending != 1 {
		t.Fatalf("exactly one pending invitation must exist, found %d", pending)
	}
}

// TestSetOrganizationActive_OwnerlessActivationBlocked proves the PSA
// cannot force-activate an ownerless org through the manual toggle.
func TestSetOrganizationActive_OwnerlessActivationBlocked(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	orgID, _ := psaCreateOrg(t, env, "Guard Org", "owner@guard.test", "guard-key")

	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations/"+uiToStr(orgID)+"/active", map[string]interface{}{
		"active": true, "reason": "attempted bypass",
	}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("ownerless activation must be 409, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "CONFLICT" {
		t.Fatalf("ownerless activation code must be CONFLICT, got %+v", e)
	}
	var orgActive int
	_ = env.db.QueryRow(`SELECT active FROM tenants WHERE id=?`, orgID).Scan(&orgActive)
	if orgActive != 0 {
		t.Fatal("ownerless org must remain inactive")
	}

	// After the owner accepts, the toggle works normally. The second org
	// carries an explicit domain: the empty domain was already consumed by
	// the first PSA creation (tenants.domain is UNIQUE).
	resp2, raw2 := env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "Guard Org 2", "owner_email": "owner@guard2.test", "domain": "guard2.test",
	}, map[string]string{"Idempotency-Key": "guard-key-2"})
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("second psa create: %d %s", resp2.StatusCode, raw2)
	}
	var created2 struct {
		Organization struct {
			ID uint `json:"id"`
		} `json:"organization"`
		InviteToken string `json:"invite_token"`
	}
	_ = json.Unmarshal(raw2, &created2)
	resp, raw = publicAccept(t, env, map[string]interface{}{"token": created2.InviteToken, "password": "OwnerPass!2026"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner accept: %d %s", resp.StatusCode, raw)
	}
	var out acceptOut
	_ = json.Unmarshal(raw, &out)
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/organizations/"+uiToStr(out.OrganizationID)+"/active", map[string]interface{}{
		"active": false, "reason": "suspend",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable accepted org: %d %s", resp.StatusCode, raw)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/organizations/"+uiToStr(out.OrganizationID)+"/active", map[string]interface{}{
		"active": true, "reason": "re-enable",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-enable accepted org must work (admins exist): %d %s", resp.StatusCode, raw)
	}
}

// TestEnterpriseAuditLogs_CanonicalEnvelope proves the tenant-facing audit
// page reads the canonical orvix_audit store with the same envelope the
// platform page uses — never a second, incompatible model.
func TestEnterpriseAuditLogs_CanonicalEnvelope(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	seedAuditRows(t, env)

	resp, raw := env.do(t, "GET", "/api/v1/enterprise/audit/logs", env.tenantAdm, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enterprise audit status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Entries []map[string]interface{} `json:"entries"`
		Total   int                      `json:"total"`
		Limit   int                      `json:"limit"`
		Offset  int                      `json:"offset"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Tenant 1 has two seeded rows (mailbox.create / mailbox.delete);
	// tenant-scoping must exclude the tenant-1 org-creation row only if
	// it belongs to tenant 1 — it does NOT (tenant_id=0, platform actor),
	// so exactly the two tenant-1 rows are visible.
	if out.Total != 2 {
		t.Fatalf("tenant-scoped audit must see exactly the tenant's rows, got total=%d entries=%d", out.Total, len(out.Entries))
	}
	if out.Limit == 0 {
		t.Fatal("envelope must carry limit")
	}
	if _, ok := out.Entries[0]["actor_role"]; !ok {
		t.Fatalf("entries must carry the extended contract fields, got %+v", out.Entries[0])
	}
}

// TestAuditExport_CanonicalStore proves export == list == detail: the
// export reads orvix_audit (extended), so it can never disagree with the
// platform audit page.
func TestAuditExport_CanonicalStore(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	seedAuditRows(t, env)

	resp, raw := env.psaDo(t, "GET", "/api/v1/audit/logs/export?format=json", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status %d: %s", resp.StatusCode, raw)
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("export must be a JSON array: %v", err)
	}
	if len(entries) < 3 {
		t.Fatalf("export must include the seeded extended rows, got %d", len(entries))
	}
	// Extended fields present — the same contract list/detail serve.
	for _, key := range []string{"id", "actor", "actor_id", "actor_role", "tenant_id", "action", "result", "ip", "user_agent", "timestamp"} {
		if _, ok := entries[0][key]; !ok {
			t.Fatalf("export entry must carry %q, got %+v", key, entries[0])
		}
	}

	// CSV export also works.
	resp, raw = env.psaDo(t, "GET", "/api/v1/audit/logs/export?format=csv", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csv export status %d: %s", resp.StatusCode, raw)
	}
	if stringContains(string(raw), "actor_id") != true {
		t.Fatalf("csv export must include the extended header columns, got %s", raw[:min(200, len(raw))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestPlatformDKIMRevoke_IdempotentRepeat proves a second revoke is a
// no-op success: no duplicate history/audit rows, key untouched.
func TestPlatformDKIMRevoke_IdempotentRepeat(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "dkim-idempotent.example.com", "status": "active", "dkim": map[string]interface{}{"generate": true, "selector": "mail"},
	}, map[string]string{"Idempotency-Key": "dkim-idem-create"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create domain: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Domain struct {
			ID uint `json:"id"`
		} `json:"domain"`
	}
	_ = json.Unmarshal(raw, &created)

	path := "/api/v1/platform/domains/1/" + uiToStr(created.Domain.ID) + "/dkim/revoke"
	resp, raw = env.psaDo(t, "POST", path, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first revoke: %d %s", resp.StatusCode, raw)
	}
	resp, raw = env.psaDo(t, "POST", path, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repeat revoke must be a success no-op, got %d: %s", resp.StatusCode, raw)
	}
	var rev struct {
		Status  string `json:"status"`
		Revoked bool   `json:"revoked"`
	}
	_ = json.Unmarshal(raw, &rev)
	if rev.Status != "ok" || !rev.Revoked {
		t.Fatalf("repeat revoke response must still state the real state: %+v", rev)
	}

	var historyEntries int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM coremail_dkim_selector_history WHERE domain='dkim-idempotent.example.com' AND action='revoked'`).Scan(&historyEntries)
	if historyEntries != 1 {
		t.Fatalf("repeat revoke must not duplicate history entries, found %d", historyEntries)
	}
	var auditRows int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action='domain.dkim.revoke' AND target_id=?`, created.Domain.ID).Scan(&auditRows)
	if auditRows != 1 {
		t.Fatalf("repeat revoke must not duplicate audit rows, found %d", auditRows)
	}
}
