package main

// End-to-end reproduction tests for the production blockers.
// These tests run the FULL stack (router + admin UI + webmail UI
// + bootstrap admin) and exercise the exact cookie flow a real
// browser uses after the user signs in at /admin and then
// navigates to /webmail. They were written to make the
// "webmail frontend is broken" report reproducible in CI
// without a real Ubuntu VPS.

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

// fullStackHarness is the same shape as freshInstallHarness but
// is built inline (no helper) to keep the cookie-flow tests
// readable in isolation.
type fullStackHarness struct {
	router   *api.Router
	sqlDB    *sql.DB
	email    string
	password string
	scratch  string
}

func buildFullStackHarness(t *testing.T, email, password string) *fullStackHarness {
	t.Helper()
	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(password)))
	t.Setenv("ORVIX_ADMIN_PASSWORD", "wrong-plain-fallback")

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	seedAdminUser(db, authenticator, logger)

	scratch, err := os.MkdirTemp("", "orvix-fullstack-*")
	if err != nil {
		t.Fatalf("scratch: %v", err)
	}
	adminDir := filepath.Join(scratch, "admin")
	webmailDir := filepath.Join(scratch, "webmail")
	mkAll(t, adminDir)
	writeFile(t, filepath.Join(adminDir, "index.html"), "<html><body>admin</body></html>")
	writeFile(t, filepath.Join(adminDir, "app.js"), "console.log('admin')")
	writeFile(t, filepath.Join(adminDir, "styles.css"), "body{color:#000}")
	mkAll(t, webmailDir)
	// Use the EXACT index.html shape the production webmail
	// ships with so the test catches the same failure mode.
	writeFile(t, filepath.Join(webmailDir, "index.html"),
		`<!doctype html><html><head><title>Orvix Webmail</title>`+
			`<script type="module" crossorigin src="/webmail/assets/index.js"></script>`+
			`<link rel="modulepreload" crossorigin href="/webmail/assets/vendor.js">`+
			`<link rel="stylesheet" crossorigin href="/webmail/assets/index.css">`+
			`</head><body><div id="root"></div></body></html>`)
	mkAll(t, filepath.Join(webmailDir, "assets"))
	writeFile(t, filepath.Join(webmailDir, "assets", "index.js"), "console.log('webmail')")
	writeFile(t, filepath.Join(webmailDir, "assets", "vendor.js"), "console.log('vendor')")
	writeFile(t, filepath.Join(webmailDir, "assets", "index.css"), "body{color:#fff}")
	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)
	return &fullStackHarness{
		router:   router,
		sqlDB:    sqlDB,
		email:    email,
		password: password,
		scratch:  scratch,
	}
}

func (h *fullStackHarness) close(t *testing.T) {
	t.Helper()
	if h.router != nil {
		_ = h.router.App().Shutdown()
	}
	if h.sqlDB != nil {
		_ = h.sqlDB.Close()
	}
	if h.scratch != "" {
		_ = os.RemoveAll(h.scratch)
	}
}

func mkAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func (h *fullStackHarness) do(method, path string, headers map[string]string, body string) (int, []byte, []*http.Cookie) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		panic(err)
	}
	buf, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, buf, resp.Cookies()
}

// TestFullStack_AdminLoginToWebmailAPICookieFlow is the
// real reproduction of the production failure. The test
// runs the EXACT browser flow:
//
//  1. POST /api/v1/auth/login with bootstrap credentials.
//  2. Capture the access_token Set-Cookie.
//  3. GET /webmail (SPA index) — must return 200.
//  4. GET /webmail/assets/index.js with NO cookie — must
//     return 200 (assets are public).
//  5. GET /api/v1/queue?folder=inbox WITH the access_token
//     cookie — must return 200, NOT 401.
//
// Step 5 is the call the React app makes on mount. If the
// access_token cookie does not authenticate against
// /api/v1/queue, the user sees an empty inbox forever and
// reports the webmail as "broken". This test catches that.
func TestFullStack_AdminLoginToWebmailAPICookieFlow(t *testing.T) {
	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	h := buildFullStackHarness(t, email, password)
	t.Cleanup(func() { h.close(t) })

	// 1. Login
	loginPayload := fmt.Sprintf(`{"email":%q,"username":%q,"password":%q}`, email, email, password)
	status, body, cookies := h.do("POST", "/api/v1/auth/login", nil, loginPayload)
	if status != 200 {
		t.Fatalf("login: expected 200, got %d: %s", status, string(body))
	}

	// 2. Capture access_token cookie
	var accessToken string
	for _, c := range cookies {
		if c.Name == "access_token" {
			accessToken = c.Value
			break
		}
	}
	if accessToken == "" {
		t.Fatal("login response did not set access_token cookie")
	}

	// 3. Load the webmail SPA index
	status, body, _ = h.do("GET", "/webmail", nil, "")
	if status != 200 {
		t.Fatalf("GET /webmail: expected 200, got %d", status)
	}
	html := string(body)
	if !strings.Contains(html, "/webmail/assets/index.js") {
		t.Fatalf("webmail index missing /webmail/assets/index.js, got: %s", html)
	}

	// 4. Public asset (no cookie)
	status, _, _ = h.do("GET", "/webmail/assets/index.js", nil, "")
	if status != 200 {
		t.Fatalf("GET /webmail/assets/index.js: expected 200, got %d", status)
	}

	// 5. THE CALL: webmail's React app → /api/v1/queue with cookie
	status, body, _ = h.do("GET", "/api/v1/queue?folder=inbox",
		map[string]string{"Cookie": "access_token=" + accessToken}, "")
	if status != 200 {
		t.Fatalf("GET /api/v1/queue with access_token cookie: expected 200, got %d: %s",
			status, string(body))
	}
	// The body must be a JSON array (empty for a fresh install).
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("/api/v1/queue body is not a JSON array: %s: %v", string(body), err)
	}
}

// TestFullStack_WebmailPageIsPublicEvenWithoutCookie
// confirms the page and every asset it references are
// served without auth. A real user lands on /webmail
// before any login; the page must load, even if the
// subsequent /api/v1/queue call returns 401.
func TestFullStack_WebmailPageIsPublicEvenWithoutCookie(t *testing.T) {
	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	h := buildFullStackHarness(t, email, password)
	t.Cleanup(func() { h.close(t) })

	for _, path := range []string{
		"/webmail",
		"/webmail/assets/index.js",
		"/webmail/assets/vendor.js",
		"/webmail/assets/index.css",
		"/webmail/inbox",
		"/webmail/compose",
	} {
		status, body, _ := h.do("GET", path, nil, "")
		if status != 200 {
			t.Fatalf("GET %s (no cookie): expected 200, got %d: %s", path, status, string(body))
		}
	}
}

// TestFullStack_AdminLoginAtAdminEndpointAlsoWorks covers
// the legacy /admin/login endpoint the installer uses. If
// the installer's verify_install and the user-facing
// /api/v1/auth/login diverge in their Set-Cookie behaviour,
// the smoke tests will pass and the user-facing webmail
// will fail.
func TestFullStack_AdminLoginAtAdminEndpointAlsoWorks(t *testing.T) {
	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	h := buildFullStackHarness(t, email, password)
	t.Cleanup(func() { h.close(t) })

	for _, endpoint := range []string{"/api/v1/auth/login", "/admin/login"} {
		payload := fmt.Sprintf(`{"username":%q,"password":%q}`, email, password)
		status, body, cookies := h.do("POST", endpoint, nil, payload)
		if status != 200 {
			t.Fatalf("POST %s: expected 200, got %d: %s", endpoint, status, string(body))
		}
		var found bool
		for _, c := range cookies {
			if c.Name == "access_token" && c.Value != "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("POST %s: response did not set access_token cookie", endpoint)
		}
	}
}

// TestFullStack_WebmailAPIAuthenticationContract codifies
// the rule: every /api/v1/* endpoint the webmail SPA calls
// must respond 200 with a valid payload when the request
// carries the access_token cookie. If a future refactor
// breaks that contract, this test fails before the
// webmail ships a regression.
func TestFullStack_WebmailAPIAuthenticationContract(t *testing.T) {
	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	h := buildFullStackHarness(t, email, password)
	t.Cleanup(func() { h.close(t) })

	payload := fmt.Sprintf(`{"username":%q,"password":%q}`, email, password)
	status, _, cookies := h.do("POST", "/api/v1/auth/login", nil, payload)
	if status != 200 {
		t.Fatalf("login: %d", status)
	}
	var accessToken string
	for _, c := range cookies {
		if c.Name == "access_token" {
			accessToken = c.Value
			break
		}
	}
	if accessToken == "" {
		t.Fatal("no access_token cookie")
	}

	// Endpoints the webmail SPA calls. Every one of these
	// must return 200 with a valid JSON payload when the
	// access_token cookie is present.
	endpoints := []struct {
		path        string
		wantJSONObj bool // true => body is a JSON object, false => array
	}{
		{"/api/v1/queue?folder=inbox", false},
		{"/api/v1/queue?folder=sent", false},
		{"/api/v1/queue?folder=drafts", false},
	}
	for _, ep := range endpoints {
		status, body, _ := h.do("GET", ep.path,
			map[string]string{"Cookie": "access_token=" + accessToken}, "")
		if status != 200 {
			t.Fatalf("GET %s with cookie: expected 200, got %d: %s", ep.path, status, string(body))
		}
		if ep.wantJSONObj {
			var obj map[string]interface{}
			if err := json.Unmarshal(body, &obj); err != nil {
				t.Fatalf("%s body is not a JSON object: %s", ep.path, string(body))
			}
		} else {
			var arr []map[string]interface{}
			if err := json.Unmarshal(body, &arr); err != nil {
				t.Fatalf("%s body is not a JSON array: %s", ep.path, string(body))
			}
		}
	}
}

// bytes alias used by the import of the bytes package
// (kept for any future buffer-based tests).
var _ = bytes.NewReader

// TestFullStack_SessionLifecycle exercises the full auth
// session that a real user experiences:
//
//  1. Login (no session).
//  2. Use the session: /api/v1/me (proves the access_token
//     cookie actually authenticates protected routes, not
//     just /api/v1/queue).
//  3. Refresh the session: /api/v1/auth/refresh with the
//     refresh_token cookie (proves the session can be
//     extended without a fresh username/password).
//  4. Logout: /api/v1/auth/logout (proves the session can
//     be terminated server-side; this is CSRF-protected so
//     the test must include the CSRF token).
//  5. Second login: prove the user can sign in again after
//     a clean logout (this is the "subsequent password
//     verification failures for the same admin account"
//     production symptom; the test passes only if both the
//     first AND the second login return 200).
//
// This is the "admin authentication behavior is not fully
// validated" the CTO called out. The previous "verify_install"
// only did step 1; if any later step regressed, the
// installer would still print "INSTALLATION VERIFICATION
// PASSED" while the user couldn't actually use the admin
// UI.
func TestFullStack_SessionLifecycle(t *testing.T) {
	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	h := buildFullStackHarness(t, email, password)
	t.Cleanup(func() { h.close(t) })

	// Step 1: first login.
	loginPayload := fmt.Sprintf(`{"email":%q,"username":%q,"password":%q}`, email, email, password)
	status, body, cookies := h.do("POST", "/api/v1/auth/login", nil, loginPayload)
	if status != 200 {
		t.Fatalf("first login: expected 200, got %d: %s", status, string(body))
	}
	firstAccess, firstRefresh := readAuthCookies(t, cookies)
	if firstAccess == "" {
		t.Fatal("first login: missing access_token cookie")
	}

	// Step 2: protected route /api/v1/me with the access cookie.
	status, body, _ = h.do("GET", "/api/v1/me",
		map[string]string{"Cookie": "access_token=" + firstAccess}, "")
	if status != 200 {
		t.Fatalf("/api/v1/me with first access_token: expected 200, got %d: %s", status, string(body))
	}

	// Step 3: refresh the session.
	if firstRefresh != "" {
		status, body, cookies = h.do("POST", "/api/v1/auth/refresh",
			map[string]string{"Cookie": "refresh_token=" + firstRefresh}, "")
		if status != 200 {
			t.Fatalf("refresh: expected 200, got %d: %s", status, string(body))
		}
		// The refresh handler rotates both tokens. Confirm a
		// fresh access_token cookie came back.
		newAccess, _ := readAuthCookies(t, cookies)
		if newAccess == "" {
			t.Fatal("refresh: response did not rotate access_token")
		}
		if newAccess == firstAccess {
			t.Fatal("refresh: access_token was not rotated")
		}
		// Use the rotated token.
		firstAccess = newAccess
	}

	// Step 4: logout (CSRF-protected).
	// The CSRF token is fetched from /api/v1/csrf-token which
	// requires the access_token cookie. After logout the
	// session is gone; we can't call /me again. The double-
	// submit contract: csrf_token cookie AND X-CSRF-Token
	// header must both be present and equal.
	csrfReq := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	csrfReq.Header.Set("Cookie", "access_token="+firstAccess)
	csrfResp, err := h.router.App().Test(csrfReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	if csrfResp.StatusCode != 200 {
		t.Fatalf("csrf: expected 200, got %d", csrfResp.StatusCode)
	}
	var csrfData struct {
		CSRFToken string `json:"csrf_token"`
	}
	csrfBody, _ := io.ReadAll(csrfResp.Body)
	if err := json.Unmarshal(csrfBody, &csrfData); err != nil {
		t.Fatalf("csrf decode: %v", err)
	}
	if csrfData.CSRFToken == "" {
		t.Fatal("csrf response missing csrf_token")
	}
	// Extract the csrf_token cookie the response set, so the
	// logout request can replay the double-submit pair.
	var csrfCookie string
	for _, c := range csrfResp.Cookies() {
		if c.Name == "csrf_token" {
			csrfCookie = c.Value
			break
		}
	}
	if csrfCookie == "" {
		t.Fatal("csrf response did not set csrf_token cookie")
	}
	if csrfCookie != csrfData.CSRFToken {
		t.Fatal("csrf cookie value must equal csrf_token JSON field for double-submit")
	}

	logoutReq := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Cookie", "access_token="+firstAccess+"; csrf_token="+csrfCookie)
	logoutReq.Header.Set("X-CSRF-Token", csrfData.CSRFToken)
	logoutResp, err := h.router.App().Test(logoutReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if logoutResp.StatusCode != 200 {
		logoutBody, _ := io.ReadAll(logoutResp.Body)
		t.Fatalf("logout: expected 200, got %d: %s", logoutResp.StatusCode, string(logoutBody))
	}

	// Step 5: second login. The exact credential set is the
	// same; this is the call that fails in the production
	// "subsequent password verification failures" symptom.
	status, body, cookies = h.do("POST", "/api/v1/auth/login", nil, loginPayload)
	if status != 200 {
		t.Fatalf("second login: expected 200, got %d: %s", status, string(body))
	}
	secondAccess, _ := readAuthCookies(t, cookies)
	if secondAccess == "" {
		t.Fatal("second login: missing access_token cookie")
	}
	// The second token MUST be a fresh JWT (different from the
	// first). If seedAdminUser somehow re-used the same token
	// the second login would still appear to succeed but
	// session rotation would be broken.
	if secondAccess == firstAccess {
		t.Fatal("second login: access_token was not rotated after logout")
	}

	// And the rotated token must still work for a protected
	// route.
	status, body, _ = h.do("GET", "/api/v1/me",
		map[string]string{"Cookie": "access_token=" + secondAccess}, "")
	if status != 200 {
		t.Fatalf("/api/v1/me with second access_token: expected 200, got %d: %s", status, string(body))
	}
}

// readAuthCookies extracts the access_token and refresh_token
// cookies from a login response. The refresh_token is optional
// — older builds may not set it — so the caller must treat the
// refresh side as "best effort".
func readAuthCookies(t *testing.T, cookies []*http.Cookie) (string, string) {
	t.Helper()
	var access, refresh string
	for _, c := range cookies {
		switch c.Name {
		case "access_token":
			access = c.Value
		case "refresh_token":
			refresh = c.Value
		}
	}
	return access, refresh
}
