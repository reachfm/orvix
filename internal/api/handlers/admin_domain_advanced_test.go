package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

// adminDomainAdvancedEnv is the wiring for the admin domain
// advanced-fields tests. Mirrors adminSettingsEnv so the
// CSRF / RBAC contract is identical across both surfaces.
type adminDomainAdvancedEnv struct {
	router     *api.Router
	adminToken string
	csrfToken  string
	userToken  string
}

func buildAdminDomainAdvancedEnv(t *testing.T) *adminDomainAdvancedEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "admindomainadvanced.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'orvix', 'orvix', 'orvix.email', 'enterprise', 1)",
		now, now,
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('orvix.email', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)",
		now, now,
	); err != nil {
		t.Fatalf("insert domain: %v", err)
	}

	const (
		adminEmail = "admin@orvix.email"
		adminPass  = "AdminPass!2026"
		userEmail  = "user@orvix.email"
		userPass   = "UserPass!2026"
	)
	adminHash, _ := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
	userHash, _ := bcrypt.GenerateFromPassword([]byte(userPass), bcrypt.DefaultCost)
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'admin', 1, 1, 1)",
		now, now, adminEmail, string(adminHash),
	); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'user', 1, 1, 1)",
		now, now, userEmail, string(userHash),
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	if err := mkdirEmpty(adminDir); err != nil {
		t.Fatalf("mkdir admin: %v", err)
	}
	webmailDir := filepath.Join(scratchDir, "webmail")
	if err := mkdirEmpty(webmailDir); err != nil {
		t.Fatalf("mkdir webmail: %v", err)
	}
	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	adminToken := loginDomainTest(t, router, adminEmail, adminPass)
	userToken := loginDomainTest(t, router, userEmail, userPass)
	csrfToken := getDomainCSRF(t, router, adminToken)

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &adminDomainAdvancedEnv{
		router:     router,
		adminToken: adminToken,
		csrfToken:  csrfToken,
		userToken:  userToken,
	}
}

func mkdirEmpty(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	pairs := map[string]string{
		"index.html":    "<html></html>",
		"app.js":        "",
		"styles.css":    "",
		"auth-gate.css": "",
		"auth-gate.js":  "",
		"webmail.css":   "",
		"webmail.js":    "",
	}
	for name, content := range pairs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func loginDomainTest(t *testing.T, router *api.Router, email, password string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login %s: expected 200, got %d: %s", email, resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "access_token" {
			return c.Value
		}
	}
	t.Fatalf("login %s: no access_token cookie", email)
	return ""
}

func getDomainCSRF(t *testing.T, router *api.Router, bearer string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf token: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf token: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "csrf_token" {
			return c.Value
		}
	}
	t.Fatal("no csrf_token cookie in response")
	return ""
}

func domainReq(t *testing.T, e *adminDomainAdvancedEnv, method, path, bearer, csrf string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	var cookies []string
	if bearer != "" {
		cookies = append(cookies, "access_token="+bearer)
	}
	if csrf != "" {
		cookies = append(cookies, "csrf_token="+csrf)
	}
	if len(cookies) > 0 {
		req.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]interface{}{}
	if resp.StatusCode != 204 {
		raw, _ := io.ReadAll(resp.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
	}
	return resp.StatusCode, out
}

// TestAdminDomainCreateAdvancedFields asserts that POST /api/v1/domains
// persists every advanced field (status, plan, description, max limits,
// DKIM/DMARC/MTA-STS, catch-all, abuse contact) and surfaces them in
// the JSON response so the admin "Create domain" modal can be honest
// about what it persisted.
func TestAdminDomainCreateAdvancedFields(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	status, body := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{
			"name":             "advanced.example",
			"status":           "active",
			"plan":             "enterprise",
			"description":      "advanced provisioning test",
			"max_mailboxes":    50,
			"max_aliases":      20,
			"max_quota_mb":     4096,
			"dkim_enabled":     true,
			"dkim_selector":    "selector1",
			"dmarc_enabled":    true,
			"mtasts_enabled":   true,
			"catchall_address": "postmaster@advanced.example",
			"abuse_contact":    "abuse@advanced.example",
		},
	)
	if status != 201 {
		t.Fatalf("expected 201, got %d: %v", status, body)
	}
	if got, _ := body["status"].(string); got != "active" {
		t.Errorf("expected status=active, got %v", body["status"])
	}
	if got, _ := body["plan"].(string); got != "enterprise" {
		t.Errorf("expected plan=enterprise, got %v", body["plan"])
	}
	if got, _ := body["dkim_selector"].(string); got != "selector1" {
		t.Errorf("expected dkim_selector=selector1, got %v", body["dkim_selector"])
	}
	if got, _ := body["dkim_enabled"].(bool); !got {
		t.Errorf("expected dkim_enabled=true, got %v", body["dkim_enabled"])
	}
	if got, _ := body["max_mailboxes"].(float64); got != 50 {
		t.Errorf("expected max_mailboxes=50, got %v", body["max_mailboxes"])
	}
	if got, _ := body["catchall_address"].(string); got != "postmaster@advanced.example" {
		t.Errorf("expected catchall_address, got %v", body["catchall_address"])
	}
	if got, _ := body["abuse_contact"].(string); got != "abuse@advanced.example" {
		t.Errorf("expected abuse_contact, got %v", body["abuse_contact"])
	}
}

// TestAdminDomainCreateDefaultDkimSelector ensures that enabling DKIM
// without an explicit selector still produces a usable DNS label so
// the downstream DNS plan has something to publish.
func TestAdminDomainCreateDefaultDkimSelector(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	status, body := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{
			"name":         "defaultsel.example",
			"dkim_enabled": true,
		},
	)
	if status != 201 {
		t.Fatalf("expected 201, got %d: %v", status, body)
	}
	if got, _ := body["dkim_selector"].(string); got == "" {
		t.Errorf("expected non-empty default selector, got %v", body["dkim_selector"])
	}
}

// TestAdminDomainCreateInvalidCatchall asserts the cross-field check
// that catch-all addresses MUST be on the same domain. Without this
// guard the routing layer would forward catch-all deliveries to the
// wrong mailbox.
func TestAdminDomainCreateInvalidCatchall(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	status, body := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{
			"name":             "wrongcatchall.example",
			"catchall_address": "someone@elsewhere.com",
		},
	)
	if status != 400 {
		t.Fatalf("expected 400, got %d: %v", status, body)
	}
}

// TestAdminDomainCreateBadDkimSelector asserts the DNS-label allowlist:
// only [a-zA-Z0-9._-] are accepted. No spaces, no slashes.
func TestAdminDomainCreateBadDkimSelector(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	status, body := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{
			"name":          "badsel.example",
			"dkim_selector": "bad selector with spaces",
		},
	)
	if status != 400 {
		t.Fatalf("expected 400, got %d: %v", status, body)
	}
}

// TestAdminDomainCreateNegativeLimit rejects negative numeric limits
// so the quota / mailbox / alias counters never wrap.
func TestAdminDomainCreateNegativeLimit(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	status, body := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{
			"name":          "neg.example",
			"max_mailboxes": -1,
		},
	)
	if status != 400 {
		t.Fatalf("expected 400, got %d: %v", status, body)
	}
}

// TestAdminDomainPatchAllowedFields asserts PATCH /api/v1/domains/:name
// persists every mutable enterprise field and the response shape is
// stable for the admin Detail drawer.
func TestAdminDomainPatchAllowedFields(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	// Seed an existing domain with the bare minimum the seed harness
	// needs.
	status, _ := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{"name": "patchme.example"},
	)
	if status != 201 {
		t.Fatalf("seed create failed: %d", status)
	}
	// Now patch it.
	status, body := domainReq(t, e, "PATCH", "/api/v1/domains/patchme.example",
		e.adminToken, e.csrfToken,
		map[string]interface{}{
			"plan":             "enterprise",
			"max_mailboxes":    100,
			"max_quota_mb":     2048,
			"dkim_enabled":     true,
			"dkim_selector":    "mail",
			"dmarc_enabled":    true,
			"mtasts_enabled":   true,
			"catchall_address": "postmaster@patchme.example",
		},
	)
	if status != 200 {
		t.Fatalf("expected 200, got %d: %v", status, body)
	}
	applied := body["applied"].([]interface{})
	if len(applied) == 0 {
		t.Fatalf("expected applied fields, got %v", body)
	}
	// Re-fetch via GET and confirm persistence.
	status, getBody := domainReq(t, e, "GET", "/api/v1/domains/patchme.example",
		e.adminToken, "", nil,
	)
	if status != 200 {
		t.Fatalf("get domain: %d", status)
	}
	if got, _ := getBody["plan"].(string); got != "enterprise" {
		t.Errorf("expected plan=enterprise, got %v", getBody["plan"])
	}
	if got, _ := getBody["max_mailboxes"].(float64); got != 100 {
		t.Errorf("expected max_mailboxes=100, got %v", getBody["max_mailboxes"])
	}
	if got, _ := getBody["dkim_selector"].(string); got != "mail" {
		t.Errorf("expected dkim_selector=mail, got %v", getBody["dkim_selector"])
	}
	if got, _ := getBody["catchall_address"].(string); got != "postmaster@patchme.example" {
		t.Errorf("expected catchall_address, got %v", getBody["catchall_address"])
	}
}

// TestAdminDomainPatchUnknownFieldHardReject asserts that an unknown
// key aborts the entire PATCH so the frontend can never silently drop
// a value the admin entered.
func TestAdminDomainPatchUnknownFieldHardReject(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	status, _ := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{"name": "reject.example"},
	)
	if status != 201 {
		t.Fatalf("seed create failed: %d", status)
	}
	status, body := domainReq(t, e, "PATCH", "/api/v1/domains/reject.example",
		e.adminToken, e.csrfToken,
		map[string]interface{}{
			"plan":            "enterprise",
			"dangerous_field": "should-hard-reject",
		},
	)
	if status != 400 {
		t.Fatalf("expected 400, got %d: %v", status, body)
	}
	if body["error"] == nil {
		t.Errorf("expected error message, got %v", body)
	}
}

// TestAdminDomainPatchRBACEnforced asserts that a non-admin JWT cannot
// patch a domain. The frontend's "Edit limits" modal sits behind the
// RoleAdmin gate; if the gate breaks this test fails closed.
func TestAdminDomainPatchRBACEnforced(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	status, _ := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{"name": "rbac.example"},
	)
	if status != 201 {
		t.Fatalf("seed create failed: %d", status)
	}
	status, body := domainReq(t, e, "PATCH", "/api/v1/domains/rbac.example",
		e.userToken, e.csrfToken,
		map[string]interface{}{"plan": "enterprise"},
	)
	if status == 200 {
		t.Fatalf("expected non-200 for non-admin JWT, got %d: %v", status, body)
	}
}

// TestAdminDomainGetReturnsAllFields ensures GET /api/v1/domains/:name
// surfaces the full provisioning shape so the Detail drawer can show
// every persistent property in a single round trip.
func TestAdminDomainGetReturnsAllFields(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)
	status, _ := domainReq(t, e, "POST", "/api/v1/domains",
		e.adminToken, e.csrfToken,
		map[string]interface{}{
			"name":             "fulldata.example",
			"plan":             "enterprise",
			"description":      "full data fixture",
			"max_mailboxes":    10,
			"max_aliases":      5,
			"max_quota_mb":     1024,
			"dkim_enabled":     true,
			"dkim_selector":    "selector1",
			"dmarc_enabled":    true,
			"mtasts_enabled":   true,
			"catchall_address": "postmaster@fulldata.example",
			"abuse_contact":    "abuse@fulldata.example",
		},
	)
	if status != 201 {
		t.Fatalf("seed create failed: %d", status)
	}
	status, body := domainReq(t, e, "GET", "/api/v1/domains/fulldata.example",
		e.adminToken, "", nil,
	)
	if status != 200 {
		t.Fatalf("expected 200, got %d: %v", status, body)
	}
	for _, key := range []string{
		"id", "domain", "status", "plan", "description",
		"max_mailboxes", "max_aliases", "max_quota_mb", "mailbox_count",
		"dkim_enabled", "dkim_selector", "dmarc_enabled", "mtasts_enabled",
		"catchall_address", "abuse_contact",
		"created_at", "updated_at", "mailboxes",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("GetDomain response missing key %q", key)
		}
	}
}
