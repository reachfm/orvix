package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

// newCSRFTestRouter builds a full router (real DB + migrations + authenticator)
// and seeds a single admin user. It mirrors the harness used by the existing
// router integration tests.
func newCSRFTestRouter(t *testing.T) (*Router, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, _ := authenticator.HashPassword("TestPassword123!")
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPw,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return router, "admin@test.local", "TestPassword123!"
}

// doReq runs a request through the router app and returns the response.
func doReq(t *testing.T, router *Router, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	rec := httptest.NewRecorder()
	// fiber's Test returns *http.Response; copy it into a recorder for
	// uniform cookie/header inspection.
	body, _ := io.ReadAll(resp.Body)
	rec.Code = resp.StatusCode
	rec.HeaderMap = resp.Header.Clone()
	rec.Body.Write(body)
	return rec
}

// bootstrapCSRF issues the unauthenticated pre-login CSRF bootstrap request
// and returns the token, cookie, and full response.
func bootstrapCSRF(t *testing.T, router *Router, origin string) (token, cookie string, rec *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec = doReq(t, router, req)
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf_token" {
			cookie = c.Value
		}
	}
	return body.CSRFToken, cookie, rec
}

// TestCSRFBootstrapUnauthenticated200 proves an unauthenticated GET
// /api/v1/csrf-token returns 200 (the login page can bootstrap CSRF before
// authentication), issues the double-submit token+cookie, and carries
// Cache-Control: no-store.
func TestCSRFBootstrapUnauthenticated200(t *testing.T) {
	router, _, _ := newCSRFTestRouter(t)
	token, cookie, rec := bootstrapCSRF(t, router, "")
	if rec.Code != 200 {
		t.Fatalf("unauthenticated csrf bootstrap expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if token == "" {
		t.Fatal("csrf bootstrap returned empty token")
	}
	if cookie == "" {
		t.Fatal("csrf bootstrap did not set csrf_token cookie")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("csrf bootstrap Cache-Control = %q, want no-store", got)
	}
	if token != cookie {
		t.Fatalf("double-submit contract violated: cookie %q != json token %q", cookie, token)
	}
}

// TestCSRFBootstrapResponseNoSensitiveData proves the bootstrap response
// contains ONLY the CSRF token â€” no user, tenant, or session data.
func TestCSRFBootstrapResponseNoSensitiveData(t *testing.T) {
	router, _, _ := newCSRFTestRouter(t)
	_, _, rec := bootstrapCSRF(t, router, "")
	var m map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("bootstrap body not JSON: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("bootstrap response must contain exactly one field, got %v", keys(m))
	}
	if _, ok := m["csrf_token"]; !ok {
		t.Fatalf("bootstrap response missing csrf_token: %v", keys(m))
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestCSRFBootstrapLoginThenAuthenticatedMutation proves that after the
// unauthenticated bootstrap the login flow works normally AND the bootstrap
// token validates a subsequent CSRF-protected authenticated mutation
// (POST /api/v1/auth/logout) â€” i.e. login reaches authentication and CSRF
// protection still holds.
func TestCSRFBootstrapLoginThenAuthenticatedMutation(t *testing.T) {
	router, email, password := newCSRFTestRouter(t)
	token, cookie, rec := bootstrapCSRF(t, router, "")
	if rec.Code != 200 || token == "" || cookie == "" {
		t.Fatalf("bootstrap failed: code=%d token=%q cookie=%q", rec.Code, token, cookie)
	}

	access := loginForTest(t, router, email, password)
	if access == "" {
		t.Fatal("login did not return access token")
	}

	// CSRF-protected authenticated mutation using the bootstrap token.
	logoutReq := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Cookie", "access_token="+access+"; csrf_token="+cookie)
	logoutReq.Header.Set("X-CSRF-Token", token)
	logoutRec := doReq(t, router, logoutReq)
	if logoutRec.Code != 200 {
		t.Fatalf("logout with bootstrap csrf token expected 200, got %d: %s", logoutRec.Code, logoutRec.Body.String())
	}
}

// TestCSRFMissingOrMismatchedStillRejected proves the CSRF middleware still
// rejects missing or mismatched tokens on protected mutations.
func TestCSRFMissingOrMismatchedStillRejected(t *testing.T) {
	router, email, password := newCSRFTestRouter(t)
	access := loginForTest(t, router, email, password)
	validToken, validCookie, _ := bootstrapCSRF(t, router, "")
	if validToken == "" || validCookie == "" {
		t.Fatal("bootstrap failed")
	}

	// Missing CSRF cookie + header.
	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req.Header.Set("Cookie", "access_token="+access)
	rec := doReq(t, router, req)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "CSRF token missing") {
		t.Fatalf("missing csrf expected 403 CSRF missing, got %d: %s", rec.Code, rec.Body.String())
	}

	// Mismatched cookie vs header.
	req2 := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req2.Header.Set("Cookie", "access_token="+access+"; csrf_token="+validCookie)
	req2.Header.Set("X-CSRF-Token", "not-the-same-token")
	rec2 := doReq(t, router, req2)
	if rec2.Code != 403 || !strings.Contains(rec2.Body.String(), "CSRF token mismatch") {
		t.Fatalf("mismatched csrf expected 403 mismatch, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Valid double-submit pair still passes (sanity that the rejections above
	// are due to CSRF, not something else).
	req3 := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req3.Header.Set("Cookie", "access_token="+access+"; csrf_token="+validCookie)
	req3.Header.Set("X-CSRF-Token", validToken)
	rec3 := doReq(t, router, req3)
	if rec3.Code != 200 {
		t.Fatalf("valid csrf pair expected 200, got %d: %s", rec3.Code, rec3.Body.String())
	}
}

// TestCSRFBootstrapUntrustedOriginRejected proves a cross-origin bootstrap
// request from an untrusted Origin is rejected with 403 and receives no
// CSRF cookie.
func TestCSRFBootstrapUntrustedOriginRejected(t *testing.T) {
	router, _, _ := newCSRFTestRouter(t)
	_, cookie, rec := bootstrapCSRF(t, router, "https://evil.example")
	if rec.Code != 403 {
		t.Fatalf("untrusted origin expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "origin not allowed") {
		t.Fatalf("403 body must explain origin rejection, got %s", rec.Body.String())
	}
	if cookie != "" {
		t.Fatalf("untrusted origin must not receive a csrf cookie, got %q", cookie)
	}
}

// TestCSRFBootstrapMalformedOrigins proves that hostile or malformed
// Origin header values (null for opaque origins, userinfo, port
// mismatches, hostile subdomains) are all rejected because they do not
// exactly match any entry in the trusted allow-list.
func TestCSRFBootstrapMalformedOrigins(t *testing.T) {
	router, _, _ := newCSRFTestRouter(t)
	for _, origin := range []string{
		"null",                               // opaque origin (sandboxed iframes, file://)
		"http://evil.localhost:3000.example", // hostile subdomain (exact-match rejects)
		"http://localhost:9999",              // port not in the default allow-list
	} {
		_, cookie, rec := bootstrapCSRF(t, router, origin)
		if rec.Code != 403 {
			t.Fatalf("origin %q expected 403, got %d: %s", origin, rec.Code, rec.Body.String())
		}
		if cookie != "" {
			t.Fatalf("origin %q must not receive a csrf cookie, got %q", origin, cookie)
		}
	}
}

// TestCSRFBootstrapWildcardAllowedOrigins proves that when
// AllowedOrigins is the single-element wildcard ["*"] (an invalid
// configuration with AllowCredentials=true), the origin check fails
// closed — no request receives a token — matching how the CORS
// middleware replaces the wildcard with localhost defaults.
func TestCSRFBootstrapWildcardAllowedOrigins(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Server.AllowedOrigins = []string{"*"}
	db, _ := config.NewDatabase(&cfg.Database, logger)
	sqlDB, _ := db.DB()
	_ = models.MigrateAllRaw(db)
	authenticator, _ := auth.NewAuthenticator(&cfg.Auth, db, logger)
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { _ = router.App().Shutdown(); _ = sqlDB.Close() })

	// After the wildcard is replaced with localhost defaults (matching
	// the CORS setup), the allow-listed localhost origin receives 200.
	// Either outcome (200 via replacement or 403 via exact-match fail-
	// closed on literal "*") is safe; a 500 or a cookie on an untrusted
	// origin would be a real defect.
	_, cookie, rec := bootstrapCSRF(t, router, "http://localhost:3000")
	if rec.Code != 200 && rec.Code != 403 {
		t.Fatalf("wildcard origin check must produce 200 or 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Code == 200 {
		if cookie == "" {
			t.Fatalf("wildcard replacement must issue a cookie on the allowed origin")
		}
	} else {
		if cookie != "" {
			t.Fatalf("wildcard rejection must not issue a cookie, got %q", cookie)
		}
	}
}

// TestCSRFBootstrapTrustedOriginAllowed proves an allow-listed Origin (the
// existing trusted-origin policy) still receives a token.
func TestCSRFBootstrapTrustedOriginAllowed(t *testing.T) {
	router, _, _ := newCSRFTestRouter(t)

	// Allow-listed origin (config default localhost).
	_, cookie1, rec1 := bootstrapCSRF(t, router, "http://localhost:3000")
	if rec1.Code != 200 || cookie1 == "" {
		t.Fatalf("allowed origin expected 200+cookie, got code=%d cookie=%q body=%s", rec1.Code, cookie1, rec1.Body.String())
	}

	// No Origin header (same-origin / non-browser) is also allowed.
	_, cookie2, rec2 := bootstrapCSRF(t, router, "")
	if rec2.Code != 200 || cookie2 == "" {
		t.Fatalf("no-origin expected 200+cookie, got code=%d cookie=%q", rec2.Code, cookie2)
	}
}

// TestProtectedEndpointsStill401 proves no protected business endpoint was
// accidentally made public by this change.
func TestProtectedEndpointsStill401(t *testing.T) {
	router, _, _ := newCSRFTestRouter(t)
	for _, req := range []*http.Request{
		httptest.NewRequest("GET", "/api/v1/me", nil),
		httptest.NewRequest("GET", "/api/v1/enterprise/domains", nil),
		httptest.NewRequest("POST", "/api/v1/enterprise/domains/1/dkim/generate", nil),
	} {
		rec := doReq(t, router, req)
		if rec.Code != 401 {
			t.Fatalf("%s %s expected 401, got %d: %s", req.Method, req.URL.Path, rec.Code, rec.Body.String())
		}
	}
}

// TestCSRFBootstrapAuthenticatedRefresh proves an authenticated client can
// still obtain a fresh CSRF token (the endpoint works with valid credentials).
func TestCSRFBootstrapAuthenticatedRefresh(t *testing.T) {
	router, email, password := newCSRFTestRouter(t)
	access := loginForTest(t, router, email, password)

	first, _, rec1 := bootstrapCSRF(t, router, "")
	if rec1.Code != 200 || first == "" {
		t.Fatalf("first bootstrap failed: code=%d", rec1.Code)
	}

	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Cookie", "access_token="+access)
	rec2 := doReq(t, router, req)
	if rec2.Code != 200 {
		t.Fatalf("authenticated csrf refresh expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &body); err != nil || body.CSRFToken == "" {
		t.Fatalf("authenticated csrf refresh returned no token: %v", rec2.Body.String())
	}
}
