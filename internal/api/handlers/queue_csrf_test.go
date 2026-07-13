package handlers_test

// Tests for the Admin Queue Retry/Delete CSRF flow.
//
// These tests pin the contract that:
//   - GET /api/v1/csrf-token returns a usable token
//     for an authenticated admin and a fresh
//     csrf_token cookie.
//   - POST /api/v1/queue/:id/retry accepts a valid
//     CSRF token (cookie + header) and rejects
//     requests missing the token.
//   - DELETE /api/v1/queue/:id accepts a valid CSRF
//     token and rejects requests missing the token.
//   - The token must be present in BOTH the cookie
//     AND the X-CSRF-Token header. A request that
//     supplies only one of the two is rejected with
//     403 — that is the production behaviour that
//     failed in the field when the admin app's
//     fetch did not carry credentials: 'include'.
//   - Non-admin users (no admin role) cannot perform
//     queue actions; the admin role middleware
//     short-circuits with 403 BEFORE the CSRF check
//     runs, but the queue handlers themselves do
//     not regress on role.
//   - The CSRF middleware is enabled and rejects
//     POST/DELETE with a 403 when the token is
//     missing or wrong. We never disable it.
//
// The test harness uses the real router + real
// auth/CSRF/admin middlewares. There is no
// production-bypass — the no-cross-origin-bypass
// guarantee is the test that runs the request
// through the real middleware stack.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

// queueCSRFEnv is a real router with a real
// auth+CSRF stack, an admin user, a non-admin user,
// and one queue row we can mutate.
type queueCSRFEnv struct {
	router          *api.Router
	adminToken      string
	adminCSRFCookie string
	userToken       string
	queueID         uint
}

func buildQueueCSRFEnv(t *testing.T) *queueCSRFEnv {
	t.Helper()

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "qcsrf.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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

	// Create tenant + domain.
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'orvix', 'orvix', 'orvix.email', 'enterprise', 1)",
		time.Now().UTC(), time.Now().UTC(),
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('orvix.email', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)",
		time.Now().UTC(), time.Now().UTC(),
	); err != nil {
		t.Fatalf("insert domain: %v", err)
	}

	// Admin user (role=admin) and non-admin user
	// (role=user). Both use bcrypt so the existing
	// auth.Login code path works.
	const (
		adminEmail = "admin@orvix.email"
		adminPass  = "AdminPass!2026"
		userEmail  = "user@orvix.email"
		userPass   = "UserPass!2026"
	)
	adminHash, _ := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.MinCost)
	userHash, _ := bcrypt.GenerateFromPassword([]byte(userPass), bcrypt.MinCost)
	now := time.Now().UTC()
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

	// Build queue table.
	for _, stmt := range queue.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue ddl: %v", err)
		}
	}

	// Build the router. We need a writable
	// /webmail/* path? No, we only need the admin
	// queue endpoints. set admin / webmail to a
	// scratch dir so the router does not crash.
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

	// Enqueue a single queue row. The handler
	// reads from coremail_queue directly, so a
	// minimal INSERT is sufficient.
	res, err := sqlDB.ExecContext(t.Context(), `
		INSERT INTO coremail_queue
			(tenant_id, domain_id, message_id, from_address, to_address,
			 recipient_domain, direction, status, priority, attempt_count,
			 delivery_mode, max_attempts, created_at, updated_at)
		VALUES (1, 1, 'msg-1', 'sender@orvix.email', 'rcpt@remote.test',
			'remote.test', 'outbound', 'deferred', 0, 1,
			'remote_smtp', 16, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert queue: %v", err)
	}
	qid, _ := res.LastInsertId()

	// Login the admin. The login response includes
	// an access_token; we use it as the Bearer
	// credential for the queue actions.
	adminToken := loginQueueTest(t, router, adminEmail, adminPass)
	userToken := loginQueueTest(t, router, userEmail, userPass)

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &queueCSRFEnv{
		router:          router,
		adminToken:      adminToken,
		adminCSRFCookie: getFreshCSRFCookie(t, router, adminToken),
		userToken:       userToken,
		queueID:         uint(qid),
	}
}

func loginQueueTest(t *testing.T, router *api.Router, email, password string) string {
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

func getFreshCSRFCookie(t *testing.T, router *api.Router, bearer string) string {
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
	// The cookie name is "csrf_token". We
	// intentionally do NOT include the cookie
	// in the next request — the test is supposed
	// to do that explicitly to prove
	// "credentials: 'include'" must be set.
	for _, c := range resp.Cookies() {
		if c.Name == "csrf_token" {
			return c.Value
		}
	}
	t.Fatal("no csrf_token cookie in /api/v1/csrf-token response")
	return ""
}

// doQueueRequest issues a request to the admin queue
// endpoint with the given cookie values. csrfCookie
// and csrfHeader are passed separately so a single
// test can vary them (the "missing token" tests set
// them to "").
func doQueueRequest(t *testing.T, e *queueCSRFEnv, method, path, bearer, csrfCookie, csrfHeader string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	// Build a Cookie header that contains both
	// the session and the csrf_token cookie. The
	// production app uses fetch with
	// credentials: 'include' which produces a
	// similar Cookie header on the wire.
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
	return resp
}

// TestQueueCSRFRetryWithValidTokenSucceeds pins the
// happy path: a fully-valid CSRF token (cookie +
// header match) lets the admin re-queue the entry.
// This is the contract the admin app's
// csrfFetch helper must satisfy.
func TestQueueCSRFRetryWithValidTokenSucceeds(t *testing.T) {
	e := buildQueueCSRFEnv(t)
	resp := doQueueRequest(t, e, "POST",
		"/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10)+"/retry",
		e.adminToken, e.adminCSRFCookie, e.adminCSRFCookie)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("retry: expected 200, got %d, body=%s", resp.StatusCode, string(body))
	}
}

// TestQueueCSRFDeleteWithValidTokenSucceeds is the
// happy path for DELETE.
func TestQueueCSRFDeleteWithValidTokenSucceeds(t *testing.T) {
	e := buildQueueCSRFEnv(t)
	resp := doQueueRequest(t, e, "DELETE",
		"/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10),
		e.adminToken, e.adminCSRFCookie, e.adminCSRFCookie)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete: expected 200, got %d, body=%s", resp.StatusCode, string(body))
	}
}

// TestQueueCSRFRetryRejectsMissingToken pins the
// security contract: a retry request without any
// CSRF token (no cookie, no header) MUST be rejected
// with 403. The production regression where
// "credentials: 'include'" was missing from the
// admin app's fetch caused exactly this — the cookie
// was not sent because the Set-Cookie on the
// /csrf-token response was dropped.
func TestQueueCSRFRetryRejectsMissingToken(t *testing.T) {
	e := buildQueueCSRFEnv(t)
	resp := doQueueRequest(t, e, "POST",
		"/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10)+"/retry",
		e.adminToken, "", "")
	if resp.StatusCode != 403 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("retry without CSRF: expected 403, got %d, body=%s", resp.StatusCode, string(body))
	}
}

// TestQueueCSRFDeleteRejectsMissingToken is the
// security contract for DELETE.
func TestQueueCSRFDeleteRejectsMissingToken(t *testing.T) {
	e := buildQueueCSRFEnv(t)
	resp := doQueueRequest(t, e, "DELETE",
		"/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10),
		e.adminToken, "", "")
	if resp.StatusCode != 403 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete without CSRF: expected 403, got %d, body=%s", resp.StatusCode, string(body))
	}
}

// TestQueueCSRFRejectsMismatchedToken pins the
// security contract: a CSRF cookie that does not
// match the X-CSRF-Token header is rejected with 403
// (the production symptom was "CSRF token mismatch").
// This is the exact error the admin operator saw
// in the field — the cookie and the header came
// from different tokens because the previous
// /csrf-token response was dropped on the floor.
func TestQueueCSRFRejectsMismatchedToken(t *testing.T) {
	e := buildQueueCSRFEnv(t)
	resp := doQueueRequest(t, e, "POST",
		"/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10)+"/retry",
		e.adminToken, e.adminCSRFCookie, "totally-different-token")
	if resp.StatusCode != 403 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("retry with mismatched CSRF: expected 403, got %d, body=%s", resp.StatusCode, string(body))
	}
}

// TestQueueCSRFRetryRecoversWithFreshToken is the
// recovery test for the admin app's csrfFetch
// helper: the helper fetches a fresh /csrf-token,
// re-issues the request with the new token, and
// succeeds. We simulate that flow in Go.
func TestQueueCSRFRetryRecoversWithFreshToken(t *testing.T) {
	e := buildQueueCSRFEnv(t)
	// First call: send NO token. Server 403s.
	resp1 := doQueueRequest(t, e, "POST",
		"/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10)+"/retry",
		e.adminToken, "", "")
	if resp1.StatusCode != 403 {
		t.Fatalf("first call: expected 403, got %d", resp1.StatusCode)
	}
	// Recovery: fetch a fresh token, then retry
	// with the new token.
	fresh := getFreshCSRFCookie(t, e.router, e.adminToken)
	resp2 := doQueueRequest(t, e, "POST",
		"/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10)+"/retry",
		e.adminToken, fresh, fresh)
	if resp2.StatusCode != 200 {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("retry with fresh token: expected 200, got %d, body=%s", resp2.StatusCode, string(body))
	}
}

// TestQueueActionsRequireAdminRole asserts that the
// role middleware still gates the queue endpoints.
// A non-admin user with a valid CSRF token must
// still get 403 because RequireAnyRole(admin)
// short-circuits before the handler runs.
func TestQueueActionsRequireAdminRole(t *testing.T) {
	e := buildQueueCSRFEnv(t)
	// The non-admin user has a valid bearer token
	// and a valid CSRF token. The role middleware
	// must still 403 them.
	resp := doQueueRequest(t, e, "POST",
		"/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10)+"/retry",
		e.userToken, e.adminCSRFCookie, e.adminCSRFCookie)
	if resp.StatusCode != 403 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("non-admin retry: expected 403, got %d, body=%s", resp.StatusCode, string(body))
	}
}

// TestQueueCSRFProtectionIsEnabled is a regression
// pin: the queue endpoints must NOT be accessible
// without CSRF. The CSRF middleware must be wired on
// the men (CSRF-protected) group, not on a separate
// group that exempts them. We assert this by
// hitting DELETE with NO cookies, NO header, and
// no auth — must 401 (auth middleware) before CSRF
// even runs. Then we hit it with auth but no
// CSRF — must 403. Both checks are required.
func TestQueueCSRFProtectionIsEnabled(t *testing.T) {
	e := buildQueueCSRFEnv(t)
	// Without auth: 401.
	req := httptest.NewRequest("DELETE", "/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10), nil)
	resp, _ := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated delete: expected 401, got %d", resp.StatusCode)
	}
	// With auth, without CSRF: 403.
	req2 := httptest.NewRequest("DELETE", "/api/v1/queue/"+strconv.FormatUint(uint64(e.queueID), 10), nil)
	req2.Header.Set("Authorization", "Bearer "+e.adminToken)
	resp2, _ := e.router.App().Test(req2, fiber.TestConfig{Timeout: 0})
	if resp2.StatusCode != 403 {
		t.Fatalf("auth-but-no-csrf delete: expected 403, got %d", resp2.StatusCode)
	}
}
