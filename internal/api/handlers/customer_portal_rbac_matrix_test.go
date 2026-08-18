package handlers_test

// Customer Portal RBAC matrix — the full Organization Admin console
// authorization contract, exercised against the REAL router + SQLite DB:
//
//   - TenantAdmin (and the canonical tenant family) can use every customer
//     page; per-role write subsets are enforced by permission groups;
//   - RoleUser (per-mailbox webmail end-user) is denied the ENTIRE console
//     at the group gate — reads AND writes — and /me fails closed;
//   - a TenantAdmin gains NO platform surface (every /platform/*, /admin/*,
//     /queue, /monitoring/*, /firewall/*, /modules, /license route denies);
//   - no customer route crosses tenant boundaries (cross-tenant ids 404).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

type portalMatrixHarness struct {
	router *api.Router
	sqlDB  *sql.DB
	authn  *auth.Authenticator

	tenantA    uint
	tenantB    uint
	adminAID   uint
	operatorID uint
	supportID  uint
	readonlyID uint
	userAID    uint
	adminBID   uint
}

func newPortalMatrixHarness(t *testing.T) *portalMatrixHarness {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "portal-matrix.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	cfg.Auth.JWTSecret = "test-secret-64-bytes-portal-matrix-rbac-fixture-XXXXXXX"
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt.pem")

	logger := zap.NewNop()
	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := gdb.DB()
	t.Cleanup(func() { sqlDB.Close() })

	authn, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authn: %v", err)
	}

	adminUI := filepath.Join(t.TempDir(), "admin")
	webmailUI := filepath.Join(t.TempDir(), "webmail")
	os.MkdirAll(adminUI, 0755)
	os.MkdirAll(webmailUI, 0755)
	os.WriteFile(filepath.Join(adminUI, "index.html"), []byte("<html></html>"), 0644)
	os.WriteFile(filepath.Join(webmailUI, "index.html"), []byte("<html></html>"), 0644)
	os.WriteFile(filepath.Join(webmailUI, "webmail.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(webmailUI, "webmail.css"), []byte(""), 0644)
	cfg.Server.AdminUIDir = adminUI
	cfg.Server.WebmailUIDir = webmailUI

	router := api.NewRouter(cfg, authn, logger, gdb, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { router.App().Shutdown() })

	now := time.Now().UTC()
	// Two tenants so cross-tenant isolation can be proven.
	ra, _ := sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('matrix-a', 'matrix-a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
	tidA, _ := ra.LastInsertId()
	rb, _ := sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('matrix-b', 'matrix-b', 'b.example', 'enterprise', 1, ?, ?)`, now, now)
	tidB, _ := rb.LastInsertId()

	insert := func(email, role string, tenant int64) uint {
		pw, _ := auth.HashPassword("MatrixPass!2026")
		r, err := sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, 1, 1)`, now, now, email, pw, role, tenant)
		if err != nil {
			t.Fatalf("insert %s: %v", email, err)
		}
		id, _ := r.LastInsertId()
		return uint(id)
	}

	return &portalMatrixHarness{
		router: router, sqlDB: sqlDB, authn: authn,
		tenantA:    uint(tidA),
		tenantB:    uint(tidB),
		adminAID:   insert("admin-a@matrix.local", "tenant_admin", tidA),
		operatorID: insert("operator-a@matrix.local", "tenant_operator", tidA),
		supportID:  insert("support-a@matrix.local", "tenant_support", tidA),
		readonlyID: insert("readonly-a@matrix.local", "tenant_readonly", tidA),
		userAID:    insert("webmail-a@matrix.local", "user", tidA),
		adminBID:   insert("admin-b@matrix.local", "tenant_admin", tidB),
	}
}

func (h *portalMatrixHarness) login(t *testing.T, email string) (string, string) {
	t.Helper()
	body := fmt.Sprintf(`{"username":"%s","password":"MatrixPass!2026"}`, email)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d body=%s", email, resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(raw, &out)
	if out.AccessToken == "" {
		t.Fatalf("login %s: empty token", email)
	}

	csrfReq, _ := http.NewRequestWithContext(context.Background(), "GET", "/api/v1/csrf-token", nil)
	csrfReq.Header.Set("Authorization", "Bearer "+out.AccessToken)
	csrfResp, err := h.router.App().Test(csrfReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf %s: %v", email, err)
	}
	defer csrfResp.Body.Close()
	csrfRaw, _ := io.ReadAll(csrfResp.Body)
	var csrfOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	json.Unmarshal(csrfRaw, &csrfOut)
	return out.AccessToken, csrfOut.CSRFToken
}

// do performs an authenticated request against the real router.
func (h *portalMatrixHarness) do(t *testing.T, bearer, csrf, method, path string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, path, rd)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrf})
	}
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestCustomerPortal_TenantAdminFullConsole(t *testing.T) {
	h := newPortalMatrixHarness(t)
	tok, csrf := h.login(t, "admin-a@matrix.local")

	// Every customer page read the portal mounts for a TenantAdmin.
	reads := []string{
		"/api/v1/enterprise/dashboard",
		"/api/v1/enterprise/domains",
		"/api/v1/enterprise/mailboxes",
		"/api/v1/enterprise/organizations/current",
		"/api/v1/enterprise/members",
		"/api/v1/enterprise/aliases",
		"/api/v1/enterprise/groups",
		"/api/v1/enterprise/billing/usage",
		"/api/v1/enterprise/billing/subscription",
		"/api/v1/enterprise/billing/invoices",
		"/api/v1/enterprise/invitations",
		"/api/v1/enterprise/api-keys",
		"/api/v1/enterprise/audit/logs",
		"/api/v1/enterprise/abuse/send-limit",
		"/api/v1/enterprise/status",
		"/api/v1/account/profile",
		"/api/v1/account/sessions",
		"/api/v1/account/mfa/status",
	}
	for _, path := range reads {
		status, body := h.do(t, tok, "", "GET", path, nil)
		if status == http.StatusForbidden || status == http.StatusUnauthorized {
			t.Errorf("TenantAdmin GET %s → %d %s (must be authorized)", path, status, body)
		}
	}

	// TenantAdmin writes — authorization must pass (business rules may
	// still 4xx on bad input or quota/license limits; a 403 carrying
	// "insufficient permissions" is the ONLY forbidden outcome).
	writes := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/api/v1/enterprise/domains", map[string]any{"name": "new.example", "quota_mb": 1024}},
		{"POST", "/api/v1/enterprise/mailboxes", map[string]any{"email": "mb@new.example", "password": "StrongPass123"}},
		{"POST", "/api/v1/enterprise/aliases", map[string]any{"alias": "a@new.example", "target_email": "mb@new.example"}},
		{"POST", "/api/v1/enterprise/groups", map[string]any{"name": "team"}},
		{"POST", "/api/v1/enterprise/invitations", map[string]any{"email": "invite@example.com", "role": "tenant_operator"}},
		{"PATCH", fmt.Sprintf("/api/v1/enterprise/members/%d/role", h.operatorID), map[string]any{"role": "tenant_operator"}},
		{"POST", "/api/v1/enterprise/ownership/request", map[string]any{"target_user_id": h.operatorID}},
		{"POST", "/api/v1/enterprise/billing/subscription", map[string]any{"plan_id": "free", "billing_interval": "monthly"}},
		{"POST", "/api/v1/enterprise/api-keys", map[string]any{"name": "ci-key", "scopes": []string{"domains.read"}}},
		{"POST", "/api/v1/enterprise/deletion", map[string]any{}},
	}
	for _, w := range writes {
		status, body := h.do(t, tok, csrf, w.method, w.path, w.body)
		if status == http.StatusUnauthorized {
			t.Errorf("TenantAdmin %s %s → 401 (must be authenticated)", w.method, w.path)
		}
		if status == http.StatusForbidden && strings.Contains(body, "insufficient permissions") {
			t.Errorf("TenantAdmin %s %s → 403 RBAC denial %s (must be authorized)", w.method, w.path, body)
		}
	}

	// /me classifies the tenant admin into the organization portal.
	status, body := h.do(t, tok, "", "GET", "/api/v1/me", nil)
	if status != http.StatusOK || !strings.Contains(body, `"portal":"organization"`) {
		t.Fatalf("TenantAdmin /me → %d %s (want portal=organization)", status, body)
	}
}

func TestCustomerPortal_CanonicalRoleWriteSubsets(t *testing.T) {
	h := newPortalMatrixHarness(t)
	opTok, opCSRF := h.login(t, "operator-a@matrix.local")
	supTok, supCSRF := h.login(t, "support-a@matrix.local")
	roTok, roCSRF := h.login(t, "readonly-a@matrix.local")

	// Operator: mailbox writes allowed; domains/billing/api-keys/ownership denied.
	if s, b := h.do(t, opTok, opCSRF, "POST", "/api/v1/enterprise/mailboxes", map[string]any{"email": "op@new.example", "password": "StrongPass123"}); s == 401 || (s == 403 && strings.Contains(b, "insufficient permissions")) {
		t.Errorf("tenant_operator POST mailboxes → %d %s (must be authorized)", s, b)
	}
	for _, w := range []struct {
		path string
		body any
	}{
		{"/api/v1/enterprise/domains", map[string]any{"name": "op.example"}},
		{"/api/v1/enterprise/billing/subscription", map[string]any{"plan_id": "free"}},
		{"/api/v1/enterprise/api-keys", map[string]any{"name": "k", "scopes": []string{"domains.read"}}},
		{"/api/v1/enterprise/ownership/request", map[string]any{"target_user_id": h.operatorID}},
		{"/api/v1/enterprise/invitations", map[string]any{"email": "x@example.com", "role": "tenant_operator"}},
	} {
		if s, b := h.do(t, opTok, opCSRF, "POST", w.path, w.body); s != http.StatusForbidden {
			t.Errorf("tenant_operator POST %s → %d %s (want 403)", w.path, s, b)
		}
	}

	// Support: mailbox writes allowed; billing/api-keys/domains denied.
	if s, b := h.do(t, supTok, supCSRF, "POST", "/api/v1/enterprise/mailboxes", map[string]any{"email": "sup@new.example", "password": "StrongPass123"}); s == 401 || (s == 403 && strings.Contains(b, "insufficient permissions")) {
		t.Errorf("tenant_support POST mailboxes → %d %s (must be authorized)", s, b)
	}
	for _, w := range []struct {
		path string
		body any
	}{
		{"/api/v1/enterprise/billing/subscription", map[string]any{"plan_id": "free"}},
		{"/api/v1/enterprise/api-keys", map[string]any{"name": "k", "scopes": []string{"domains.read"}}},
		{"/api/v1/enterprise/domains", map[string]any{"name": "sup.example"}},
		{"/api/v1/enterprise/ownership/request", map[string]any{"target_user_id": h.supportID}},
	} {
		if s, b := h.do(t, supTok, supCSRF, "POST", w.path, w.body); s != http.StatusForbidden {
			t.Errorf("tenant_support POST %s → %d %s (want 403)", w.path, s, b)
		}
	}

	// ReadOnly: reads fine, ALL writes denied.
	if s, b := h.do(t, roTok, "", "GET", "/api/v1/enterprise/domains", nil); s == 401 || s == 403 {
		t.Errorf("tenant_readonly GET domains → %d %s (must be authorized)", s, b)
	}
	for _, w := range []struct {
		path string
		body any
	}{
		{"/api/v1/enterprise/domains", map[string]any{"name": "ro.example"}},
		{"/api/v1/enterprise/mailboxes", map[string]any{"email": "ro@new.example", "password": "StrongPass123"}},
		{"/api/v1/enterprise/invitations", map[string]any{"email": "x@example.com", "role": "tenant_operator"}},
		{"/api/v1/enterprise/billing/subscription", map[string]any{"plan_id": "free"}},
		{"/api/v1/enterprise/api-keys", map[string]any{"name": "k", "scopes": []string{"domains.read"}}},
		{"/api/v1/enterprise/ownership/request", map[string]any{"target_user_id": h.readonlyID}},
	} {
		if s, b := h.do(t, roTok, roCSRF, "POST", w.path, w.body); s != http.StatusForbidden {
			t.Errorf("tenant_readonly POST %s → %d %s (want 403)", w.path, s, b)
		}
	}
}

func TestCustomerPortal_RoleUserDeniedEntireConsole(t *testing.T) {
	h := newPortalMatrixHarness(t)
	tok, csrf := h.login(t, "webmail-a@matrix.local")

	// /me fails closed: no portal for a webmail end-user.
	status, body := h.do(t, tok, "", "GET", "/api/v1/me", nil)
	if status != http.StatusOK || !strings.Contains(body, `"portal":""`) {
		t.Fatalf("RoleUser /me → %d %s (want portal empty / fail closed)", status, body)
	}

	// Every enterprise console read AND write is denied at the group gate.
	denied := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/enterprise/dashboard"},
		{"GET", "/api/v1/enterprise/domains"},
		{"GET", "/api/v1/enterprise/mailboxes"},
		{"GET", "/api/v1/enterprise/organizations/current"},
		{"GET", "/api/v1/enterprise/members"},
		{"GET", "/api/v1/enterprise/aliases"},
		{"GET", "/api/v1/enterprise/groups"},
		{"GET", "/api/v1/enterprise/billing/usage"},
		{"GET", "/api/v1/enterprise/billing/subscription"},
		{"GET", "/api/v1/enterprise/billing/invoices"},
		{"GET", "/api/v1/enterprise/invitations"},
		{"GET", "/api/v1/enterprise/api-keys"},
		{"GET", "/api/v1/enterprise/audit/logs"},
		{"GET", "/api/v1/enterprise/status"},
		{"POST", "/api/v1/enterprise/domains"},
		{"POST", "/api/v1/enterprise/mailboxes"},
		{"POST", "/api/v1/enterprise/aliases"},
		{"POST", "/api/v1/enterprise/groups"},
		{"POST", "/api/v1/enterprise/invitations"},
		{"PATCH", fmt.Sprintf("/api/v1/enterprise/members/%d/role", h.operatorID)},
		{"POST", "/api/v1/enterprise/ownership/request"},
		{"POST", "/api/v1/enterprise/billing/subscription"},
		{"POST", "/api/v1/enterprise/api-keys"},
	}
	for _, d := range denied {
		var b any
		switch d.path {
		case "/api/v1/enterprise/domains":
			b = map[string]any{"name": "web.example"}
		case "/api/v1/enterprise/mailboxes":
			b = map[string]any{"email": "web@example.com", "password": "StrongPass123"}
		case "/api/v1/enterprise/invitations":
			b = map[string]any{"email": "x@example.com", "role": "tenant_operator"}
		case "/api/v1/enterprise/ownership/request":
			b = map[string]any{"target_user_id": h.operatorID}
		case "/api/v1/enterprise/billing/subscription":
			b = map[string]any{"plan_id": "free"}
		case "/api/v1/enterprise/api-keys":
			b = map[string]any{"name": "k", "scopes": []string{"domains.read"}}
		case "/api/v1/enterprise/members/" + fmt.Sprint(h.operatorID) + "/role":
			b = map[string]any{"role": "tenant_operator"}
		}
		s, respBody := h.do(t, tok, csrf, d.method, d.path, b)
		if s != http.StatusForbidden {
			t.Errorf("RoleUser %s %s → %d %s (want 403)", d.method, d.path, s, respBody)
		} else if !strings.Contains(respBody, "insufficient permissions") {
			t.Errorf("RoleUser %s %s 403 without stable error token: %s", d.method, d.path, respBody)
		}
	}

	// RoleUser cannot call the tenant-admin family routes directly
	// (tenantCompatMW + SupportAccessMiddleware deny).
	for _, path := range []string{
		"/api/v1/domains", "/api/v1/mailboxes", "/api/v1/users", "/api/v1/api-keys",
	} {
		s, b := h.do(t, tok, "", "GET", path, nil)
		if s != http.StatusForbidden {
			t.Errorf("RoleUser GET %s → %d %s (want 403)", path, s, b)
		}
	}

	// RoleUser keeps ONLY self-scoped account access (their own profile).
	if s, b := h.do(t, tok, "", "GET", "/api/v1/account/profile", nil); s == http.StatusForbidden || s == http.StatusUnauthorized {
		t.Errorf("RoleUser GET /account/profile → %d %s (self-scoped must be allowed)", s, b)
	}
}

func TestCustomerPortal_TenantAdminNoPlatformSurface(t *testing.T) {
	h := newPortalMatrixHarness(t)
	tok, csrf := h.login(t, "admin-a@matrix.local")

	platformRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/platform/dashboard"},
		{"GET", "/api/v1/platform/organizations"},
		{"GET", "/api/v1/platform/organizations/1/detail"},
		{"GET", "/api/v1/admin/summary"},
		{"GET", "/api/v1/admin/backups"},
		{"GET", "/api/v1/admin/backups/schedule"},
		{"GET", "/api/v1/admin/queue/summary"},
		{"GET", "/api/v1/admin/queue/messages"},
		{"GET", "/api/v1/queue"},
		{"GET", "/api/v1/monitoring/health"},
		{"GET", "/api/v1/monitoring/alerts"},
		{"GET", "/api/v1/firewall/rules"},
		{"GET", "/api/v1/firewall/logs"},
		{"GET", "/api/v1/modules"},
		{"GET", "/api/v1/license"},
		{"GET", "/api/v1/admin/security/antivirus"},
		{"GET", "/api/v1/admin/settings/protocol/smtp"},
		{"GET", "/api/v1/feature-flags"},
		{"GET", "/api/v1/admin/runtime"},
		{"GET", "/api/v1/admin/ssl/certificates"},
		{"GET", "/api/v1/admin/fs/browse"},
		{"GET", "/api/v1/admin/cluster/status"},
		{"GET", "/api/v1/audit/logs"},
		{"GET", "/api/v1/platform/relays"},
		{"GET", "/api/v1/platform/suppressions/1"},
		{"GET", "/api/v1/platform/deliverability/1/metrics"},
		{"GET", "/api/v1/platform/domains/1"},
		{"GET", "/api/v1/platform/mailboxes/1"},
		{"GET", "/api/v1/platform/billing/tenants/1/balance"},
		{"GET", "/api/v1/platform/automation/jobs"},
		{"GET", "/api/v1/platform/support/grants"},
		{"GET", "/api/v1/platform/config"},
		{"GET", "/api/v1/platform/capabilities"},
		{"GET", "/api/v1/admin/backups/restore-jobs/1"},
		{"POST", "/api/v1/platform/organizations/1/active"},
	}
	for _, r := range platformRoutes {
		s, b := h.do(t, tok, csrf, r.method, r.path, nil)
		if s != http.StatusForbidden {
			t.Errorf("TenantAdmin %s %s → %d %s (want 403 — no platform surface)", r.method, r.path, s, b)
		}
	}

	// And the platform /me classification is never granted.
	s, b := h.do(t, tok, "", "GET", "/api/v1/me", nil)
	if strings.Contains(b, `"portal":"platform"`) {
		t.Fatalf("TenantAdmin /me classified as platform: %s", b)
	}
	_ = s
}

func TestCustomerPortal_TenantIsolation(t *testing.T) {
	h := newPortalMatrixHarness(t)
	tok, csrf := h.login(t, "admin-a@matrix.local")

	// Tenant A admin cannot read Tenant B's organization (404, not 403, so
	// existence is not leaked).
	if s, b := h.do(t, tok, "", "GET", fmt.Sprintf("/api/v1/enterprise/organizations/%d", h.tenantB), nil); s != http.StatusNotFound {
		t.Errorf("cross-tenant GET organization %d → %d %s (want 404)", h.tenantB, s, b)
	}
	if s, b := h.do(t, tok, "", "GET", fmt.Sprintf("/api/v1/enterprise/organizations/%d", h.tenantA), nil); s == http.StatusNotFound || s == http.StatusForbidden {
		t.Errorf("own-tenant GET organization → %d %s (must be readable)", s, b)
	}

	// Tenant A admin cannot manage Tenant B's member.
	if s, b := h.do(t, tok, csrf, "PATCH", fmt.Sprintf("/api/v1/enterprise/members/%d/role", h.adminBID), map[string]any{"role": "tenant_readonly"}); s != http.StatusBadRequest {
		t.Errorf("cross-tenant member role update → %d %s (want 400 member not found)", s, b)
	}
	// Tenant A admin cannot remove Tenant B's member.
	if s, b := h.do(t, tok, csrf, "DELETE", fmt.Sprintf("/api/v1/enterprise/members/%d", h.adminBID), nil); s != http.StatusBadRequest {
		t.Errorf("cross-tenant member delete → %d %s (want 400 member not found)", s, b)
	}
	// Tenant B admin still works fine in their own tenant.
	tokB, csrfB := h.login(t, "admin-b@matrix.local")
	if s, b := h.do(t, tokB, csrfB, "GET", fmt.Sprintf("/api/v1/enterprise/organizations/%d", h.tenantB), nil); s == http.StatusNotFound || s == http.StatusForbidden {
		t.Errorf("tenant B own-organization → %d %s", s, b)
	}
}

func TestCustomerPortal_CSRFEnforcedOnMutations(t *testing.T) {
	h := newPortalMatrixHarness(t)
	tok, _ := h.login(t, "admin-a@matrix.local")
	// No CSRF token on a mutation → 403 from the CSRF middleware itself.
	s, b := h.do(t, tok, "", "POST", "/api/v1/enterprise/invitations", map[string]any{"email": "x@example.com", "role": "tenant_operator"})
	if s != http.StatusForbidden {
		t.Errorf("mutation without CSRF → %d %s (want 403)", s, b)
	}
}
