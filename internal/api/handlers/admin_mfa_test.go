package handlers_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// mfaComputeTOTP computes a 6-digit TOTP value for the given base32 secret.
// Uses HMAC-SHA1 per RFC 6238 (matching the server-side computeTOTP).
func mfaComputeTOTP(secretBase32 string, t time.Time) string {
	secretBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretBase32)
	if err != nil {
		return "000000"
	}
	counter := t.Unix() / 30
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))
	mac := hmac.New(sha1.New, secretBytes)
	mac.Write(buf)
	hash := mac.Sum(nil)
	offset := hash[len(hash)-1] & 0x0f
	bin := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1000000)
}

// mfaTestEnv holds the test harness for MFA endpoints.
type mfaTestEnv struct {
	router     *api.Router
	adminToken string
	csrfToken  string
	userToken  string
}

func buildMFATestEnv(t *testing.T) *mfaTestEnv {
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

	adminToken := mfaLogin(t, router, adminEmail, adminPass)
	userToken := mfaLogin(t, router, userEmail, userPass)
	csrfToken := mfaGetCSRF(t, router, adminToken)

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &mfaTestEnv{
		router:     router,
		adminToken: adminToken,
		csrfToken:  csrfToken,
		userToken:  userToken,
	}
}

func mfaLogin(t *testing.T, router *api.Router, email, password string) string {
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

func mfaGetCSRF(t *testing.T, router *api.Router, bearer string) string {
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
	t.Fatal("no csrf_token cookie in /api/v1/csrf-token response")
	return ""
}

// mfaRequest issues an authenticated admin request with CSRF cookies/headers.
func mfaRequest(t *testing.T, e *mfaTestEnv, method, path, bearer, csrfCookie string, body map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, bodyReader)
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
	if csrfCookie != "" {
		req.Header.Set("X-CSRF-Token", csrfCookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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

// ────────────────────────────────────────────────────────────
// Tests
// ────────────────────────────────────────────────────────────

func TestMFAStatusDisabledByDefault(t *testing.T) {
	e := buildMFATestEnv(t)
	status, resp := mfaRequest(t, e, "GET", "/api/v1/admin/mfa/status", e.adminToken, "", nil)
	if status != 200 {
		t.Fatalf("GET /admin/mfa/status: expected 200, got %d: %v", status, resp)
	}
	if enabled, ok := resp["enabled"].(bool); !ok || enabled {
		t.Errorf("MFA should be disabled by default, got enabled=%v", resp["enabled"])
	}
	if label, ok := resp["label"].(string); ok && label != "" {
		t.Errorf("MFA label should be empty by default, got %q", label)
	}
}

func TestMFASetupBeginRequiresPassword(t *testing.T) {
	e := buildMFATestEnv(t)

	// Missing current_password
	status, resp := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/begin", e.adminToken, e.csrfToken, map[string]interface{}{})
	if status != 400 {
		t.Fatalf("expected 400 for missing current_password, got %d: %v", status, resp)
	}

	// Incorrect current_password
	status, resp = mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/begin", e.adminToken, e.csrfToken, map[string]interface{}{
		"current_password": "WrongPass!1",
	})
	if status != 401 {
		t.Fatalf("expected 401 for incorrect password, got %d: %v", status, resp)
	}
}

func TestMFASetupFlow(t *testing.T) {
	e := buildMFATestEnv(t)

	// Step 1: Begin setup
	status, resp := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/begin", e.adminToken, e.csrfToken, map[string]interface{}{
		"current_password": "AdminPass!2026",
	})
	if status != 200 {
		t.Fatalf("setup begin: expected 200, got %d: %v", status, resp)
	}
	secret, _ := resp["secret"].(string)
	if secret == "" {
		t.Fatalf("setup begin must return secret, got %v", resp)
	}
	otpauthURL, _ := resp["otpauth_url"].(string)
	if !strings.HasPrefix(otpauthURL, "otpauth://totp/") {
		t.Errorf("otpauth_url should start with otpauth://totp/, got %q", otpauthURL)
	}
	if !strings.Contains(otpauthURL, secret) {
		t.Errorf("otpauth_url should contain the secret")
	}
	label, _ := resp["label"].(string)
	if label != "admin@orvix.email" {
		t.Errorf("label should be admin@orvix.email, got %q", label)
	}

	// Step 2: Wrong code should fail
	status, resp = mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/verify", e.adminToken, e.csrfToken, map[string]interface{}{
		"code": "000000",
	})
	if status != 400 {
		t.Fatalf("setup verify with wrong code: expected 400, got %d: %v", status, resp)
	}
	if !strings.Contains(fmt.Sprintf("%v", resp["error"]), "invalid") {
		t.Errorf("error should say invalid TOTP code, got %v", resp["error"])
	}

	// Step 3: Correct code should succeed — compute real TOTP from the secret.
	correctCode := mfaComputeTOTP(secret, time.Now().UTC())
	t.Logf("TOTP secret: %s, computed code: %s", secret, correctCode)
	status, resp = mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/verify", e.adminToken, e.csrfToken, map[string]interface{}{
		"code": correctCode,
	})
	if status != 200 {
		t.Fatalf("setup verify with correct code: expected 200, got %d: %v", status, resp)
	}
	if resp["status"] != "mfa_enabled" {
		t.Errorf("expected mfa_enabled status, got %v", resp["status"])
	}
	codes, ok := resp["recovery_codes"].([]interface{})
	if !ok || len(codes) != 8 {
		t.Errorf("expected 8 recovery codes, got %v", codes)
	}

	// Step 4: Verify MFA is now enabled
	status, resp = mfaRequest(t, e, "GET", "/api/v1/admin/mfa/status", e.adminToken, "", nil)
	if status != 200 {
		t.Fatalf("GET status after enable: expected 200, got %d: %v", status, resp)
	}
	if !resp["enabled"].(bool) {
		t.Errorf("MFA should be enabled after setup, got %v", resp["enabled"])
	}
}

func TestMFASecretNeverReturnedAfterSetup(t *testing.T) {
	e := buildMFATestEnv(t)

	// Complete setup with real TOTP
	status, resp := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/begin", e.adminToken, e.csrfToken, map[string]interface{}{
		"current_password": "AdminPass!2026",
	})
	if status != 200 {
		t.Fatalf("setup begin: expected 200, got %d", status)
	}
	secret, _ := resp["secret"].(string)
	correctCode := mfaComputeTOTP(secret, time.Now().UTC())
	status, _ = mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/verify", e.adminToken, e.csrfToken, map[string]interface{}{
		"code": correctCode,
	})
	if status != 200 {
		t.Fatalf("setup verify: expected 200, got %d", status)
	}

	// Status endpoint must not expose the secret
	status, resp = mfaRequest(t, e, "GET", "/api/v1/admin/mfa/status", e.adminToken, "", nil)
	if status != 200 {
		t.Fatalf("GET status: expected 200, got %d", status)
	}
	for _, forbidden := range []string{"secret", "mfa_secret", "pending_mfa_secret", "pending_mfa_secret_raw", "mfa_secret_raw"} {
		if _, ok := resp[forbidden]; ok {
			t.Errorf("status response leaked %q field: %v", forbidden, resp[forbidden])
		}
	}
}

func TestMFADisableRequiresPassword(t *testing.T) {
	e := buildMFATestEnv(t)

	// Enable MFA first with real TOTP
	resp := mfaSetupAndEnable(t, e)

	// Disable with wrong password should fail
	status, resp2 := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/disable", e.adminToken, e.csrfToken, map[string]interface{}{
		"current_password": "WrongPass!1",
		"code":             "000000",
	})
	if status != 401 {
		t.Fatalf("expected 401 for wrong password, got %d: %v", status, resp2)
	}

	// MFA should still be enabled
	status, resp2 = mfaRequest(t, e, "GET", "/api/v1/admin/mfa/status", e.adminToken, "", nil)
	if status != 200 {
		t.Fatalf("GET status: expected 200, got %d", status)
	}
	if !resp2["enabled"].(bool) {
		t.Errorf("MFA should still be enabled after failed disable attempt")
	}

	// Disable with correct password but wrong MFA code
	status, resp2 = mfaRequest(t, e, "POST", "/api/v1/admin/mfa/disable", e.adminToken, e.csrfToken, map[string]interface{}{
		"current_password": "AdminPass!2026",
		"code":             "000000",
	})
	if status != 401 {
		t.Fatalf("expected 401 for wrong MFA code, got %d: %v", status, resp2)
	}

	// Disable with correct password and correct MFA code
	secret, _ := resp["setup_secret"].(string)
	correctCode := mfaComputeTOTP(secret, time.Now().UTC())
	status, resp2 = mfaRequest(t, e, "POST", "/api/v1/admin/mfa/disable", e.adminToken, e.csrfToken, map[string]interface{}{
		"current_password": "AdminPass!2026",
		"code":             correctCode,
	})
	if status != 200 {
		t.Fatalf("expected 200 for disable, got %d: %v", status, resp2)
	}

	// MFA should now be disabled
	status, resp2 = mfaRequest(t, e, "GET", "/api/v1/admin/mfa/status", e.adminToken, "", nil)
	if status != 200 {
		t.Fatalf("GET status after disable: expected 200, got %d", status)
	}
	if resp2["enabled"].(bool) {
		t.Errorf("MFA should be disabled, got enabled=%v", resp2["enabled"])
	}
}

// mfaSetupAndEnable completes the MFA setup flow and returns the begin response
// (which contains the secret) plus the verify response.
func mfaSetupAndEnable(t *testing.T, e *mfaTestEnv) map[string]interface{} {
	t.Helper()
	status, resp := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/begin", e.adminToken, e.csrfToken, map[string]interface{}{
		"current_password": "AdminPass!2026",
	})
	if status != 200 {
		t.Fatalf("setup begin: expected 200, got %d", status)
	}
	secret, _ := resp["secret"].(string)
	// Store secret for caller so they can compute TOTP codes later.
	resp["setup_secret"] = secret
	correctCode := mfaComputeTOTP(secret, time.Now().UTC())
	status, _ = mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/verify", e.adminToken, e.csrfToken, map[string]interface{}{
		"code": correctCode,
	})
	if status != 200 {
		t.Fatalf("setup verify: expected 200, got %d", status)
	}
	return resp
}

func TestMFAStatusRequiresAuth(t *testing.T) {
	e := buildMFATestEnv(t)

	// No authentication
	req := httptest.NewRequest("GET", "/api/v1/admin/mfa/status", nil)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.StatusCode)
	}
}

func TestMFAStatusRequiresAdminRole(t *testing.T) {
	e := buildMFATestEnv(t)

	// Non-admin user should get 403
	status, resp := mfaRequest(t, e, "GET", "/api/v1/admin/mfa/status", e.userToken, "", nil)
	if status != 403 {
		t.Errorf("expected 403 for non-admin, got %d: %v", status, resp)
	}
}

func TestMFASetupRequiresCSRF(t *testing.T) {
	e := buildMFATestEnv(t)

	// Without CSRF token, should get 403
	status, resp := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/begin", e.adminToken, "", map[string]interface{}{
		"current_password": "AdminPass!2026",
	})
	if status != 403 {
		t.Errorf("expected 403 without CSRF, got %d: %v", status, resp)
	}
}

func TestMFASetupVerifyRejectsWithoutPending(t *testing.T) {
	e := buildMFATestEnv(t)

	// Verify without beginning setup
	status, resp := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/verify", e.adminToken, e.csrfToken, map[string]interface{}{
		"code": "123456",
	})
	if status != 400 {
		t.Errorf("expected 400 for verify without pending setup, got %d: %v", status, resp)
	}
}

func TestMFASetupVerifyRejectsWrongCode(t *testing.T) {
	e := buildMFATestEnv(t)

	// Begin setup
	status, _ := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/begin", e.adminToken, e.csrfToken, map[string]interface{}{
		"current_password": "AdminPass!2026",
	})
	if status != 200 {
		t.Fatalf("setup begin: expected 200, got %d", status)
	}

	// Try wrong code (zeros — will be wrong unless by astronomical chance)
	status, resp := mfaRequest(t, e, "POST", "/api/v1/admin/mfa/setup/verify", e.adminToken, e.csrfToken, map[string]interface{}{
		"code": "000000",
	})
	if status != 400 {
		t.Fatalf("setup verify with wrong code: expected 400, got %d: %v", status, resp)
	}
	if s, _ := resp["error"].(string); !strings.Contains(strings.ToLower(s), "invalid") && !strings.Contains(strings.ToLower(s), "totp") {
		t.Errorf("error should mention invalid TOTP code, got %q", s)
	}

	// MFA should still not be enabled
	status, resp = mfaRequest(t, e, "GET", "/api/v1/admin/mfa/status", e.adminToken, "", nil)
	if status != 200 {
		t.Fatalf("GET status: expected 200, got %d", status)
	}
	if resp["enabled"].(bool) {
		t.Errorf("MFA should remain disabled after wrong code, got enabled=%v", resp["enabled"])
	}
}
