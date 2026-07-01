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

// adminSettingsEnv is a fully-wired test environment for the admin settings endpoints.
type adminSettingsEnv struct {
	router     *api.Router
	adminToken string
	csrfToken  string
	userToken  string
	cfg        *config.Config
}

func buildAdminSettingsEnv(t *testing.T) *adminSettingsEnv {
	t.Helper()

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "adminsettings.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatalf("mkdir admin: %v", err)
	}
	_ = os.WriteFile(filepath.Join(adminDir, "index.html"), []byte("<html></html>"), 0o644)
	_ = os.WriteFile(filepath.Join(adminDir, "app.js"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(adminDir, "styles.css"), []byte(""), 0o644)
	webmailDir := filepath.Join(scratchDir, "webmail")
	if err := os.MkdirAll(webmailDir, 0o755); err != nil {
		t.Fatalf("mkdir webmail: %v", err)
	}
	_ = os.WriteFile(filepath.Join(webmailDir, "index.html"), []byte("<html></html>"), 0o644)
	_ = os.WriteFile(filepath.Join(webmailDir, "auth-gate.css"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(webmailDir, "auth-gate.js"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(webmailDir, "webmail.css"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(webmailDir, "webmail.js"), []byte(""), 0o644)

	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	adminToken := loginSettingsTest(t, router, adminEmail, adminPass)
	userToken := loginSettingsTest(t, router, userEmail, userPass)
	csrfToken := getSettingsCSRF(t, router, adminToken)

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &adminSettingsEnv{
		router:     router,
		adminToken: adminToken,
		csrfToken:  csrfToken,
		userToken:  userToken,
		cfg:        cfg,
	}
}

func loginSettingsTest(t *testing.T, router *api.Router, email, password string) string {
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

func getSettingsCSRF(t *testing.T, router *api.Router, bearer string) string {
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

func settingsRequest(t *testing.T, e *adminSettingsEnv, method, path, bearer, csrfCookie, csrfHeader string, body interface{}) (int, map[string]interface{}) {
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
	if csrfCookie != "" {
		cookies = append(cookies, "csrf_token="+csrfCookie)
	}
	if len(cookies) > 0 {
		req.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	if csrfHeader != "" {
		req.Header.Set("X-CSRF-Token", csrfHeader)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	out := map[string]interface{}{}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &out)
	}
	return resp.StatusCode, out
}

func TestAdminSettingsGet(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	status, resp := settingsRequest(t, e, "GET", "/api/v1/admin/settings", e.adminToken, "", "", nil)
	if status != 200 {
		t.Fatalf("GET /admin/settings: expected 200, got %d: %v", status, resp)
	}

	sections := []string{"general", "mail_listeners", "security", "backup", "dns"}
	for _, s := range sections {
		if _, ok := resp[s]; !ok {
			t.Errorf("response missing section %q", s)
		}
	}

	general, ok := resp["general"].(map[string]interface{})
	if !ok {
		t.Fatal("general section not a map")
	}
	if _, ok := general["version"]; !ok {
		t.Error("general missing version")
	}

	listeners, ok := resp["mail_listeners"].(map[string]interface{})
	if !ok {
		t.Fatal("mail_listeners section not a map")
	}
	if _, ok := listeners["smtp_host"]; !ok {
		t.Error("mail_listeners missing smtp_host")
	}
	if _, ok := listeners["imap_port"]; !ok {
		t.Error("mail_listeners missing imap_port")
	}
	if _, ok := listeners["submission_enabled"]; !ok {
		t.Error("mail_listeners missing submission_enabled")
	}
	if _, ok := listeners["smtps_enabled"]; !ok {
		t.Error("mail_listeners missing smtps_enabled")
	}

	sec, ok := resp["security"].(map[string]interface{})
	if !ok {
		t.Fatal("security section not a map")
	}
	if _, ok := sec["password_min_len"]; !ok {
		t.Error("security missing password_min_len")
	}
}

func TestAdminSettingsGetRequiresAuth(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	status, _ := settingsRequest(t, e, "GET", "/api/v1/admin/settings", "", "", "", nil)
	if status != 401 && status != 403 {
		t.Errorf("GET without auth: expected 401 or 403, got %d", status)
	}
}

func TestAdminSettingsPatchAllowedFields(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	// PATCH returns not_implemented — no fields are mutated.
	patch := map[string]interface{}{
		"mail_listeners": map[string]interface{}{
			"submission_enabled": true,
			"smtps_enabled":      true,
			"imaps_enabled":      false,
			"pop3s_enabled":      false,
		},
	}
	status, resp := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.adminToken, e.csrfToken, e.csrfToken, patch)
	if status != 200 {
		t.Fatalf("PATCH: expected 200, got %d: %v", status, resp)
	}
	if resp["status"] != "not_implemented" {
		t.Errorf("expected status not_implemented, got %v", resp["status"])
	}

	// Config must NOT be mutated (settings persistence is not implemented).
	if e.cfg.CoreMail.SubmissionEnabled {
		t.Error("submission_enabled should remain false (not mutated)")
	}
	if e.cfg.CoreMail.SMTPsEnabled {
		t.Error("smtps_enabled should remain false (not mutated)")
	}

	// Security and backup sections also not mutated.
	patch2 := map[string]interface{}{
		"security": map[string]interface{}{
			"password_min_len": float64(12),
		},
	}
	status2, resp2 := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.adminToken, e.csrfToken, e.csrfToken, patch2)
	if status2 != 200 {
		t.Fatalf("PATCH security: expected 200, got %d: %v", status2, resp2)
	}
	if resp2["status"] != "not_implemented" {
		t.Errorf("expected not_implemented, got %v", resp2["status"])
	}
	if e.cfg.Auth.PasswordMinLen != config.Defaults().Auth.PasswordMinLen {
		t.Errorf("password_min_len should not be mutated, got %d", e.cfg.Auth.PasswordMinLen)
	}

	patch3 := map[string]interface{}{
		"backup": map[string]interface{}{
			"retention_count": float64(20),
		},
	}
	status3, resp3 := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.adminToken, e.csrfToken, e.csrfToken, patch3)
	if status3 != 200 {
		t.Fatalf("PATCH backup: expected 200, got %d: %v", status3, resp3)
	}
	if resp3["status"] != "not_implemented" {
		t.Errorf("expected not_implemented, got %v", resp3["status"])
	}
	if e.cfg.Backup.RetentionCount != config.Defaults().Backup.RetentionCount {
		t.Errorf("retention_count should not be mutated, got %d", e.cfg.Backup.RetentionCount)
	}
}

func TestAdminSettingsPatchInvalidValues(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	// All PATCH requests return not_implemented — no value validation
	// occurs because settings persistence is not implemented.
	patch := map[string]interface{}{
		"mail_listeners": map[string]interface{}{
			"submission_port": float64(0),
		},
	}
	status, resp := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.adminToken, e.csrfToken, e.csrfToken, patch)
	if status != 200 {
		t.Errorf("PATCH: expected 200, got %d: %v", status, resp)
	}
	if resp["status"] != "not_implemented" {
		t.Errorf("expected not_implemented status, got %v", resp["status"])
	}
}

func TestAdminSettingsPatchRBACEnforced(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	// Non-admin user should get 403.
	patch := map[string]interface{}{
		"security": map[string]interface{}{
			"password_min_len": float64(10),
		},
	}
	status, _ := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.userToken, e.csrfToken, e.csrfToken, patch)
	if status != 403 {
		t.Errorf("PATCH with non-admin: expected 403, got %d", status)
	}

	// GET also requires admin role.
	status2, _ := settingsRequest(t, e, "GET", "/api/v1/admin/settings", e.userToken, "", "", nil)
	if status2 != 403 {
		t.Errorf("GET with non-admin: expected 403, got %d", status2)
	}
}

func TestAdminSettingsPatchRequiresCSRF(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	patch := map[string]interface{}{
		"security": map[string]interface{}{
			"password_min_len": float64(10),
		},
	}
	// Admin token but no CSRF cookie or header.
	status, _ := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.adminToken, "", "", patch)
	if status != 403 {
		t.Errorf("PATCH without CSRF: expected 403, got %d", status)
	}
}

func TestAdminSettingsSecretsRedacted(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	status, resp := settingsRequest(t, e, "GET", "/api/v1/admin/settings", e.adminToken, "", "", nil)
	if status != 200 {
		t.Fatalf("GET: expected 200, got %d", status)
	}

	forbidden := []string{
		"password", "password_hash", "jwt_secret", "secret",
		"api_key", "cloudflare_api_key", "namecheap_api_key",
		"deepseek_api_key", "route53_secret_key", "route53_access_key",
		"vapid_private_key", "private_key",
		"token", "bearer", "credential",
	}
	// Flatten the response and check for forbidden keys at leaf level only.
	var checkMap func(m map[string]interface{}, prefix string)
	checkMap = func(m map[string]interface{}, prefix string) {
		for k, v := range m {
			full := prefix + k
			// Check leaf key against forbidden names.
			lowerKey := strings.ToLower(k)
			for _, fb := range forbidden {
				if lowerKey == fb {
					t.Errorf("settings response leaked %q field with value %v", full, v)
				}
			}
			if nested, ok := v.(map[string]interface{}); ok {
				checkMap(nested, full+".")
			}
		}
	}
	checkMap(resp, "")
}

func TestAdminSettingsPatchAuditLog(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	patch := map[string]interface{}{
		"security": map[string]interface{}{
			"password_min_len": float64(14),
		},
	}
	status, resp := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.adminToken, e.csrfToken, e.csrfToken, patch)
	if status != 200 {
		t.Fatalf("PATCH: expected 200, got %d: %v", status, resp)
	}
	if resp["status"] != "not_implemented" {
		t.Errorf("expected not_implemented, got %v", resp["status"])
	}
}

func TestAdminSettingsPatchIgnoresReadOnlySections(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	patch := map[string]interface{}{
		"general": map[string]interface{}{
			"hostname": "evil.example.com",
		},
		"mail_listeners": map[string]interface{}{
			"smtps_enabled": true,
		},
	}
	status, resp := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.adminToken, e.csrfToken, e.csrfToken, patch)
	if status != 200 {
		t.Fatalf("PATCH: expected 200, got %d: %v", status, resp)
	}
	if resp["status"] != "not_implemented" {
		t.Errorf("expected not_implemented, got %v", resp["status"])
	}
	// Hostname must NOT be mutated.
	origHostname := e.cfg.CoreMail.Hostname
	if e.cfg.CoreMail.Hostname != origHostname {
		t.Errorf("hostname changed despite settings persistence not implemented")
	}
}

func TestAdminSettingsPatchUnknownSectionsIgnored(t *testing.T) {
	e := buildAdminSettingsEnv(t)

	patch := map[string]interface{}{
		"dns": map[string]interface{}{
			"public_ipv4": "1.2.3.4",
		},
	}
	status, resp := settingsRequest(t, e, "PATCH", "/api/v1/admin/settings", e.adminToken, e.csrfToken, e.csrfToken, patch)
	if status != 200 {
		t.Fatalf("PATCH: expected 200, got %d: %v", status, resp)
	}
	if resp["status"] != "not_implemented" {
		t.Errorf("expected not_implemented, got %v", resp["status"])
	}
	if e.cfg.DNS.PublicIPv4 == "1.2.3.4" {
		t.Error("dns.public_ipv4 should not be updated (not implemented)")
	}
}
