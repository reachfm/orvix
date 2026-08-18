package handlers_test

// Contract-shape tests for the new/changed platform + enterprise
// routes introduced by the Enterprise Product Completion Pass (Phase 2:
// API Platform). These pin the EXACT JSON shapes, stable error codes,
// idempotency replay/conflict semantics, confirmation headers, and
// Cache-Control conventions the matrices and the OpenAPI spec promise —
// the backend phase's acceptance tests covered lifecycle/state/RBAC;
// these cover the wire contract itself:
//
//   - POST /api/v1/platform/organizations: stable error codes, one-time
//     token never cached (live AND replay), idempotency-key reuse mismatch
//   - POST /api/v1/enterprise/invitations/:id/resend: one-time token
//     never cached, stable INVALID_STATE_TRANSITION/NOT_FOUND codes
//   - Platform groups CRUD: validation/confirmation/not-found shapes
//   - POST /api/v1/platform/domains/:tenant_id/:id/dkim/revoke: response
//     and conflict shapes
//   - GET /api/v1/platform/billing/tenants/:tenant_id/overview: envelope
//     shape pins (non-null arrays, honest provider state)
//   - GET /api/v1/audit/logs: result filter, limit clamping, detail
//     invalid-id error shape

import (
	"encoding/json"
	"net/http"
	"testing"
)

// errBody is the stable error shape {error, code} every new/changed
// route in this surface must produce. A frontend branches on `code`.
type errBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func decodeErr(t *testing.T, raw []byte) errBody {
	t.Helper()
	var e errBody
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("error body must decode as {error, code}: %v (raw %s)", err, raw)
	}
	if e.Code == "" {
		t.Fatalf("error body must carry a stable code, got %q (raw %s)", e.Code, raw)
	}
	return e
}

// ── POST /platform/organizations ───────────────────────────────────

func TestPlatformCreateOrganization_ErrorShapeAndIdempotencyConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Missing Idempotency-Key → 400 with stable VALIDATION_FAILED code.
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "NoKey Org", "owner_email": "owner@nokey.test",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing key must be 400, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "VALIDATION_FAILED" {
		t.Fatalf("missing key code must be VALIDATION_FAILED, got %+v", e)
	}

	// Duplicate slug → 409 with stable CONFLICT code.
	body := map[string]interface{}{"name": "Dup Org", "owner_email": "owner@dup.test", "domain": "dup-org.test"}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/organizations", body, map[string]string{"Idempotency-Key": "org-shape-dup-1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: %d %s", resp.StatusCode, raw)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/organizations", body, map[string]string{"Idempotency-Key": "org-shape-dup-2"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate slug must be 409, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "CONFLICT" {
		t.Fatalf("duplicate slug code must be CONFLICT, got %+v", e)
	}

	// Duplicate DOMAIN (distinct slug) → 409 with a truthful message,
	// never a misleading "slug already exists".
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "Other Org", "owner_email": "other@dup.test", "domain": "dup-org.test",
	}, map[string]string{"Idempotency-Key": "org-shape-dup-3"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate domain must be 409, got %d: %s", resp.StatusCode, raw)
	}
	e := decodeErr(t, raw)
	if e.Code != "CONFLICT" || e.Error != "an organization with this domain already exists" {
		t.Fatalf("duplicate domain must carry the domain-conflict contract, got %+v", e)
	}

	// Idempotency-key reuse with a DIFFERENT body → 409 with the stable
	// IDEMPOTENCY_KEY_REUSE_MISMATCH code (not a silent 201, not a dup).
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "Reuse Org", "owner_email": "owner@reuse.test", "domain": "reuse-org.test",
	}, map[string]string{"Idempotency-Key": "org-shape-reuse"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create with reuse key: %d %s", resp.StatusCode, raw)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/organizations", map[string]interface{}{
		"name": "Reuse Org DIFFERENT", "owner_email": "other@reuse.test", "domain": "reuse-org.test",
	}, map[string]string{"Idempotency-Key": "org-shape-reuse"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("key reuse with different body must be 409, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "IDEMPOTENCY_KEY_REUSE_MISMATCH" {
		t.Fatalf("reuse code must be IDEMPOTENCY_KEY_REUSE_MISMATCH, got %+v", e)
	}
}

func TestPlatformCreateOrganization_OneTimeTokenNeverCached(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	body := map[string]interface{}{"name": "NoStore Org", "owner_email": "owner@nostore.test"}

	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/organizations", body, map[string]string{"Idempotency-Key": "org-nostore"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("live one-time-token response must carry Cache-Control: no-store, got %q", cc)
	}
	var live struct {
		InviteToken string `json:"invite_token"`
	}
	if err := json.Unmarshal(raw, &live); err != nil || live.InviteToken == "" {
		t.Fatalf("live response must include invite_token: %v %s", err, raw)
	}

	// Replay: same no-store header, and the token must NOT reappear.
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/organizations", body, map[string]string{"Idempotency-Key": "org-nostore"})
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Idempotency-Replay") != "true" {
		t.Fatalf("replay must be 201 with X-Idempotency-Replay: %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("replayed one-time-token response must carry Cache-Control: no-store, got %q", cc)
	}
	if stringContains(string(raw), live.InviteToken) {
		t.Fatal("replayed body must never contain the one-time invite token")
	}
}

// ── POST /enterprise/invitations/:id/resend ────────────────────────

func TestInvitationResend_ErrorShapeAndNoStore(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Invalid id → 400 with stable VALIDATION_FAILED code.
	resp, raw := env.do(t, "POST", "/api/v1/enterprise/invitations/notanumber/resend", env.tenantAdm, nil,
		map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid id must be 400, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "VALIDATION_FAILED" {
		t.Fatalf("invalid-id code must be VALIDATION_FAILED, got %+v", e)
	}

	// Missing invitation → 404 with stable NOT_FOUND code.
	resp, raw = env.do(t, "POST", "/api/v1/enterprise/invitations/999999/resend", env.tenantAdm, nil,
		map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing invitation must be 404, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "NOT_FOUND" {
		t.Fatalf("missing code must be NOT_FOUND, got %+v", e)
	}

	// Revoked invitation → 409 with stable INVALID_STATE_TRANSITION code
	// (never the raw service error string).
	resp, raw = env.do(t, "POST", "/api/v1/enterprise/invitations", env.tenantAdm, map[string]interface{}{
		"email": "revoked-shape@t1.example", "role": "tenant_support",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invitation: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Invitation struct {
			ID uint `json:"id"`
		} `json:"invitation"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	env.do(t, "POST", "/api/v1/enterprise/invitations/"+uiToStr(created.Invitation.ID)+"/revoke", env.tenantAdm, nil,
		map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	resp, raw = env.do(t, "POST", "/api/v1/enterprise/invitations/"+uiToStr(created.Invitation.ID)+"/resend", env.tenantAdm, nil,
		map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resend of revoked invitation must be 409, got %d: %s", resp.StatusCode, raw)
	}
	e := decodeErr(t, raw)
	if e.Code != "INVALID_STATE_TRANSITION" {
		t.Fatalf("revoked-resend code must be INVALID_STATE_TRANSITION, got %+v", e)
	}
	if stringContains(e.Error, "cannot rotate token") {
		t.Fatalf("client must never see the raw service error, got %q", e.Error)
	}

	// Valid resend → 200 with the one-time token response never cached.
	resp, raw = env.do(t, "POST", "/api/v1/enterprise/invitations", env.tenantAdm, map[string]interface{}{
		"email": "resend-shape@t1.example", "role": "tenant_operator",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invitation: %d %s", resp.StatusCode, raw)
	}
	_ = json.Unmarshal(raw, &created)
	resp, raw = env.do(t, "POST", "/api/v1/enterprise/invitations/"+uiToStr(created.Invitation.ID)+"/resend", env.tenantAdm, nil,
		map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid resend must be 200, got %d: %s", resp.StatusCode, raw)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("one-time token response must carry Cache-Control: no-store, got %q", cc)
	}
	var resent struct {
		Invitation struct {
			ID     uint   `json:"id"`
			Status string `json:"status"`
		} `json:"invitation"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &resent); err != nil {
		t.Fatalf("decode resend: %v", err)
	}
	if resent.Token == "" || resent.Invitation.Status != "pending" {
		t.Fatalf("resend must return the new pending invitation + token: %+v", resent)
	}
}

// ── Platform groups CRUD ───────────────────────────────────────────

func TestPlatformGroups_ContractShapes(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Create: missing name → 400 {error, code: VALIDATION_FAILED}.
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/groups/1", map[string]interface{}{"name": "  "}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing name must be 400, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "VALIDATION_FAILED" {
		t.Fatalf("missing-name code must be VALIDATION_FAILED, got %+v", e)
	}

	// Create: valid → 201 with the full PlatformGroup shape.
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/groups/1", map[string]interface{}{
		"name": "Shape Group", "description": "contract-shape group",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create group: %d %s", resp.StatusCode, raw)
	}
	var g struct {
		ID          uint   `json:"id"`
		TenantID    uint   `json:"tenant_id"`
		Name        string `json:"name"`
		MemberCount int    `json:"member_count"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("create group body must be a PlatformGroup: %v (raw %s)", err, raw)
	}
	if g.ID == 0 || g.TenantID != 1 || g.Name != "Shape Group" || g.CreatedAt == "" || g.UpdatedAt == "" {
		t.Fatalf("unexpected group shape: %+v", g)
	}

	// Delete: missing confirmation → 428 {error, code: PRECONDITION_FAILED}.
	resp, raw = env.psaDo(t, "DELETE", "/api/v1/platform/groups/1/"+uiToStr(g.ID), nil, nil)
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("delete without confirmation must be 428, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "PRECONDITION_FAILED" {
		t.Fatalf("missing-confirmation code must be PRECONDITION_FAILED, got %+v", e)
	}

	// Add member: missing email → 400 VALIDATION_FAILED; valid → 201.
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members", map[string]interface{}{"email": ""}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing email must be 400, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "VALIDATION_FAILED" {
		t.Fatalf("missing-email code must be VALIDATION_FAILED, got %+v", e)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members", map[string]interface{}{"email": "SHAPE@T1.Example"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add member: %d %s", resp.StatusCode, raw)
	}
	var added struct {
		Status  string `json:"status"`
		GroupID uint   `json:"group_id"`
		Email   string `json:"email"`
	}
	if err := json.Unmarshal(raw, &added); err != nil {
		t.Fatalf("add-member body: %v (raw %s)", err, raw)
	}
	if added.Status != "ok" || added.GroupID != g.ID || added.Email != "shape@t1.example" {
		t.Fatalf("add-member response must be {status, group_id, normalized email}, got %+v", added)
	}

	// Remove member: missing member → 404 NOT_FOUND; valid → 200 {status, group_id, member_id}.
	resp, raw = env.psaDo(t, "DELETE", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members/999999", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("removing a missing member must be 404, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "NOT_FOUND" {
		t.Fatalf("missing-member code must be NOT_FOUND, got %+v", e)
	}
	var memberID int
	if err := env.db.QueryRow(`SELECT id FROM coremail_group_members WHERE group_id=? AND email='shape@t1.example'`, g.ID).Scan(&memberID); err != nil {
		t.Fatalf("read member id: %v", err)
	}
	resp, raw = env.psaDo(t, "DELETE", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members/"+uiToStr(uint(memberID)), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove member: %d %s", resp.StatusCode, raw)
	}
	var removed struct {
		Status   string `json:"status"`
		GroupID  uint   `json:"group_id"`
		MemberID uint   `json:"member_id"`
	}
	if err := json.Unmarshal(raw, &removed); err != nil || removed.Status != "ok" || removed.MemberID != uint(memberID) {
		t.Fatalf("remove-member response must be {status, group_id, member_id}: %+v (raw %s)", removed, raw)
	}

	// Delete: confirmed → 200 {status, id}.
	resp, raw = env.psaDo(t, "DELETE", "/api/v1/platform/groups/1/"+uiToStr(g.ID), nil, map[string]string{"X-Confirm": "DELETE-GROUP-" + uiToStr(g.ID)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmed delete: %d %s", resp.StatusCode, raw)
	}
	var del struct {
		Status string `json:"status"`
		ID     uint   `json:"id"`
	}
	if err := json.Unmarshal(raw, &del); err != nil || del.Status != "ok" || del.ID != g.ID {
		t.Fatalf("delete response must be {status, id}: %+v (raw %s)", del, raw)
	}
}

// ── POST /platform/domains/:tenant_id/:id/dkim/revoke ──────────────

func TestPlatformDKIMRevoke_ResponseAndErrorShapes(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Domain with DKIM, then revoke → 200 {status, domain_id, revoked}.
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "shape-revoke.example.com", "status": "active", "dkim": map[string]interface{}{"generate": true, "selector": "mail"},
	}, map[string]string{"Idempotency-Key": "shape-revoke-create"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create domain: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Domain struct {
			ID uint `json:"id"`
		} `json:"domain"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/domains/1/"+uiToStr(created.Domain.ID)+"/dkim/revoke", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d %s", resp.StatusCode, raw)
	}
	var rev struct {
		Status   string `json:"status"`
		DomainID uint   `json:"domain_id"`
		Revoked  bool   `json:"revoked"`
	}
	if err := json.Unmarshal(raw, &rev); err != nil || rev.Status != "ok" || rev.DomainID != created.Domain.ID || !rev.Revoked {
		t.Fatalf("revoke response must be {status, domain_id, revoked}: %+v (raw %s)", rev, raw)
	}

	// Domain WITHOUT DKIM → 409 {error, code: CONFLICT}.
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "shape-nodkim.example.com", "status": "active",
	}, map[string]string{"Idempotency-Key": "shape-nodkim-create"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create no-dkim domain: %d %s", resp.StatusCode, raw)
	}
	_ = json.Unmarshal(raw, &created)
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/domains/1/"+uiToStr(created.Domain.ID)+"/dkim/revoke", nil, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("revoking unconfigured DKIM must be 409, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "CONFLICT" {
		t.Fatalf("not-configured code must be CONFLICT, got %+v", e)
	}
}

// ── GET /platform/billing/tenants/:tenant_id/overview ──────────────

func TestPlatformBillingOverview_EnvelopeShapePins(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "GET", "/api/v1/platform/billing/tenants/1/overview", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		TenantID uint `json:"tenant_id"`
		// Subscription/plan/usage may be null when no rows exist; the
		// shape must still be present.
		Subscription *json.RawMessage `json:"subscription"`
		Plan         *json.RawMessage `json:"plan"`
		Usage        *json.RawMessage `json:"usage"`
		// Invoices and adjustments must ALWAYS be arrays, never null.
		Invoices        []json.RawMessage `json:"invoices"`
		Adjustments     []json.RawMessage `json:"adjustments"`
		Balance         *json.RawMessage  `json:"balance"`
		Reconciliation  *json.RawMessage  `json:"reconciliation"`
		PaymentProvider struct {
			Provider   string `json:"provider"`
			Enabled    bool   `json:"enabled"`
			Configured bool   `json:"configured"`
			Note       string `json:"note"`
		} `json:"payment_provider"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode overview: %v (raw %s)", err, raw)
	}
	if out.TenantID != 1 {
		t.Fatalf("overview must be for the requested tenant, got %d", out.TenantID)
	}
	if out.Invoices == nil {
		t.Fatal("invoices must be a non-null array")
	}
	if out.Adjustments == nil {
		t.Fatal("adjustments must be a non-null array")
	}
	if out.PaymentProvider.Note == "" {
		t.Fatal("payment_provider must carry an honest note (provider not configured in test env)")
	}
	if out.PaymentProvider.Configured {
		t.Fatal("payment_provider must be honestly not configured in the test env")
	}
}

// ── GET /audit/logs ────────────────────────────────────────────────

func TestAuditLogList_ResultFilterLimitClampAndDetailShape(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	seedAuditRows(t, env)
	// A failure-result row.
	if _, err := env.db.Exec(`INSERT INTO orvix_audit (actor, actor_id, actor_role, tenant_id, action, target, result, ip, user_agent, timestamp) VALUES ('user:13', 13, 'platform_super_admin', 0, 'organization.create', 'tenant:2', 'failure', '127.0.0.1', 'test-agent', datetime('now'))`); err != nil {
		t.Fatalf("seed failure row: %v", err)
	}

	// result filter.
	resp, raw := env.psaDo(t, "GET", "/api/v1/audit/logs?result=failure", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("result filter: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		Entries []map[string]interface{} `json:"entries"`
		Total   int                      `json:"total"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total != 1 || len(out.Entries) != 1 || out.Entries[0]["result"] != "failure" {
		t.Fatalf("result filter must return exactly the failure row, got total=%d entries=%+v", out.Total, out.Entries)
	}

	// Limit clamping (max 500, never an unbounded request).
	resp, raw = env.psaDo(t, "GET", "/api/v1/audit/logs?limit=100000", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clamped limit: %d %s", resp.StatusCode, raw)
	}
	var clamped struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(raw, &clamped); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if clamped.Limit != 500 {
		t.Fatalf("limit must clamp to the 500 bound, got %d", clamped.Limit)
	}

	// Detail invalid id → 400 with stable VALIDATION_FAILED code.
	resp, raw = env.psaDo(t, "GET", "/api/v1/audit/logs/notanid", nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid audit id must be 400, got %d: %s", resp.StatusCode, raw)
	}
	if e := decodeErr(t, raw); e.Code != "VALIDATION_FAILED" {
		t.Fatalf("invalid-id code must be VALIDATION_FAILED, got %+v", e)
	}
}
