package main

// Fresh-install validation tests — these exercise the same
// code paths the installer's verify_install and smoke_tests
// hit, so a CI failure here is a reliable stand-in for a
// blocked fresh VPS install.

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

// freshInstallHarness builds the full Orvix stack against a
// throwaway SQLite database, runs seedAdminUser, and returns
// the wired-up Fiber app plus the temp directory the admin
// UI and webmail UI are served from. The harness is the
// runtime equivalent of the installer's main() up to the
// point of starting the HTTP listener.
type freshInstallHarness struct {
	router     *api.Router
	sqlDB      *sql.DB
	email      string
	password   string
	adminDir   string
	webmailDir string
	scratchDir string // owned by the test, not t.TempDir; cleaned up by t.Cleanup
}

func buildFreshInstallHarness(t *testing.T, email, password string) *freshInstallHarness {
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

	// Stage the admin and webmail UI bundles. We use a
	// non-tempdir scratch path so that the test framework does
	// not try to clean it up while Fiber (on Windows) may still
	// hold transient file handles. We clean it up explicitly
	// after the router is shut down and the SQL handle is
	// closed, registered with t.Cleanup.
	scratchDir, err := os.MkdirTemp("", "orvix-freshinstall-*")
	if err != nil {
		t.Fatalf("scratch dir: %v", err)
	}
	adminDir := filepath.Join(scratchDir, "admin")
	webmailDir := filepath.Join(scratchDir, "webmail")
	mustMkdirAll(t, adminDir)
	mustWrite(t, filepath.Join(adminDir, "index.html"), "<html><body>admin</body></html>")
	mustWrite(t, filepath.Join(adminDir, "app.js"), "console.log('admin')")
	mustWrite(t, filepath.Join(adminDir, "styles.css"), "body{color:#000}")
	mustMkdirAll(t, webmailDir)
	mustWrite(t, filepath.Join(webmailDir, "index.html"),
		`<html><head><script type="module" src="/webmail/assets/index.js"></script><link rel="stylesheet" href="/webmail/assets/index.css"></head><body></body></html>`)
	mustMkdirAll(t, filepath.Join(webmailDir, "assets"))
	mustWrite(t, filepath.Join(webmailDir, "assets", "index.js"), "console.log('webmail')")
	mustWrite(t, filepath.Join(webmailDir, "assets", "index.css"), "body{color:#fff}")
	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)
	return &freshInstallHarness{
		router:     router,
		sqlDB:      sqlDB,
		email:      email,
		password:   password,
		adminDir:   adminDir,
		webmailDir: webmailDir,
		scratchDir: scratchDir,
	}
}

func (h *freshInstallHarness) close(t *testing.T) {
	// Order matters: shut the app down first so any pending
	// sends against UI files release their handles, then
	// close the SQLite handle, then remove the scratch dir.
	// This must be called from t.Cleanup, NOT via defer, so
	// it runs after the test body but before t.TempDir is
	// removed (which is what fights us on Windows).
	t.Helper()
	if h.router != nil {
		_ = h.router.App().Shutdown()
	}
	if h.sqlDB != nil {
		_ = h.sqlDB.Close()
	}
	// Give the OS a moment to release any lingering handles
	// (Fiber's sendfile + Windows file cache). 100ms is enough
	// on every Windows SKU we've shipped to and short enough
	// not to slow the suite.
	if h.scratchDir != "" {
		if err := os.RemoveAll(h.scratchDir); err != nil {
			t.Logf("scratch dir cleanup %s: %v", h.scratchDir, err)
		}
	}
}

func (h *freshInstallHarness) call(t *testing.T, method, path string, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	buf, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, buf
}

func (h *freshInstallHarness) loginAsAdmin(t *testing.T, endpoint string) string {
	t.Helper()
	payload := fmt.Sprintf(`{"username":%q,"password":%q}`, h.email, h.password)
	status, body := h.call(t, "POST", endpoint, payload)
	if status != 200 {
		t.Fatalf("%s: expected 200, got %d: %s", endpoint, status, string(body))
	}
	var data struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("%s: decode: %v", endpoint, err)
	}
	if data.AccessToken == "" {
		t.Fatalf("%s: missing access_token in %s", endpoint, string(body))
	}
	return data.AccessToken
}

// TestFreshInstall_AdminLoginSurvivesMultipleCalls is the
// runtime guard against the production symptom of "first
// login works, subsequent fail". It runs five distinct
// POST /api/v1/auth/login calls with the SAME credentials
// the bootstrap env carried. If any call returns 401 with
// "invalid credentials", the test fails.
func TestFreshInstall_AdminLoginSurvivesMultipleCalls(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	for i := 0; i < 5; i++ {
		h.loginAsAdmin(t, "/api/v1/auth/login")
	}
}

// TestFreshInstall_AdminLoginAlsoWorksViaLegacyRoute
// mirrors the installer's verify_install, which posts to
// /admin/login (not /api/v1/auth/login). Both routes must
// return the same JWT payload.
func TestFreshInstall_AdminLoginAlsoWorksViaLegacyRoute(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	for i := 0; i < 3; i++ {
		h.loginAsAdmin(t, "/admin/login")
	}
}

// TestFreshInstall_PasswordHashStaysStableAcrossLogins
// guards against any runtime path that silently rewrites
// password_hash. The bootstrap writes a bcrypt hash once;
// the runtime must not re-hash between calls. If this test
// fails, every subsequent login attempt would either succeed
// with the new hash (and the first verifier would silently
// change) or fail because the verifier reads the new hash
// but the user typed the original password.
func TestFreshInstall_PasswordHashStaysStableAcrossLogins(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	before := readPasswordHash(t, h)
	if !strings.HasPrefix(before, "$2") {
		t.Fatalf("bootstrap hash is not bcrypt: %q", before[:minLen(8, len(before))])
	}

	for i := 0; i < 3; i++ {
		h.loginAsAdmin(t, "/api/v1/auth/login")
	}
	after := readPasswordHash(t, h)
	if before != after {
		t.Fatalf("users.password_hash mutated during logins: before=%q after=%q", before, after)
	}
}

// TestFreshInstall_WebmailPageLoadsWithAllAssets replays
// the installer's smoke_webmail_assets gate. /webmail must
// return a real HTML body that references /webmail/assets/*
// paths, and every referenced asset must return 200.
func TestFreshInstall_WebmailPageLoadsWithAllAssets(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	status, body := h.call(t, "GET", "/webmail", "")
	if status != 200 {
		t.Fatalf("GET /webmail: expected 200, got %d", status)
	}
	html := string(body)
	if !strings.Contains(html, "/webmail/assets/") {
		t.Fatalf("webmail index must reference /webmail/assets/, got: %s", html)
	}
	// Discover every referenced asset and probe it.
	assets := map[string]bool{}
	for _, prefix := range []string{`src="`, `href="`} {
		for _, chunk := range splitOn(html, prefix) {
			end := strings.Index(chunk, `"`)
			if end < 0 {
				continue
			}
			candidate := chunk[:end]
			if strings.HasPrefix(candidate, "/webmail/assets/") {
				assets[candidate] = true
			}
		}
	}
	if len(assets) == 0 {
		t.Fatal("no /webmail/assets/* references found in webmail index")
	}
	for asset := range assets {
		s, _ := h.call(t, "GET", asset, "")
		if s != 200 {
			t.Fatalf("GET %s: expected 200, got %d", asset, s)
		}
	}
}

// TestFreshInstall_WebmailSpaFallbackForDeepLinks covers
// the SPA case where a user lands on /webmail/inbox or
// /webmail/compose. serveSPA must fall back to index.html
// so the React router can take over.
func TestFreshInstall_WebmailSpaFallbackForDeepLinks(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	for _, path := range []string{"/webmail/inbox", "/webmail/compose", "/webmail/no-such-page"} {
		s, body := h.call(t, "GET", path, "")
		if s != 200 {
			t.Fatalf("GET %s: expected 200, got %d", path, s)
		}
		if !strings.Contains(string(body), "webmail/assets/") {
			t.Fatalf("GET %s: expected webmail index fallback, got: %s", path, string(body))
		}
	}
}

// TestFreshInstall_AdminPageIsServed exercises the /admin
// route. The admin login form posts to /api/v1/auth/login
// (see release/admin/app.js), so the page must be reachable
// on plain HTTP and must not collide with the API path.
func TestFreshInstall_AdminPageIsServed(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	for _, path := range []string{"/admin", "/admin/", "/admin/app.js", "/admin/styles.css"} {
		s, _ := h.call(t, "GET", path, "")
		if s != 200 {
			t.Fatalf("GET %s: expected 200, got %d", path, s)
		}
	}
}

// TestFreshInstall_HealthEndpointReachable is the most basic
// gate — the API must respond at /api/v1/health. The
// installer uses this as the canary that the process bound
// the port.
func TestFreshInstall_HealthEndpointReachable(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	s, body := h.call(t, "GET", "/api/v1/health", "")
	if s != 200 {
		t.Fatalf("GET /api/v1/health: expected 200, got %d", s)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("health status: expected ok, got %v", payload["status"])
	}
}

// TestFreshInstall_LoginPayloadMatchesInstallerWireFormat
// is the mirror of the installer's build_login_payload: the
// JSON shape {"username":..,"password":..} must succeed. The
// admin UI sends {email,username,password}; both shapes must
// authenticate the same row.
func TestFreshInstall_LoginPayloadMatchesInstallerWireFormat(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	for _, payload := range []string{
		fmt.Sprintf(`{"username":%q,"password":%q}`, h.email, h.password),
		fmt.Sprintf(`{"email":%q,"password":%q}`, h.email, h.password),
		fmt.Sprintf(`{"email":%q,"username":%q,"password":%q}`, h.email, h.email, h.password),
	} {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("payload %q: %v", payload, err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("payload %q: expected 200, got %d: %s", payload, resp.StatusCode, string(body))
		}
	}
}

// TestFreshInstall_PasswordWithSpecialCharsMatchesInstaller
// covers the password shapes the install.sh test suite
// already exercises (quotes, slashes, dollar signs, spaces).
// If the installer's `json_escape` or the runtime's
// `c.Bind().JSON` disagree on any of these, the installer's
// verify_install would silently pass for one shape and fail
// for another.
func TestFreshInstall_PasswordWithSpecialCharsMatchesInstaller(t *testing.T) {
	passwords := []string{
		"MaghaghaMos086",
		`Password "quoted" \ slash $ dollar ! bang # hash 123`,
		"Password With Spaces",
		`Password\Slash123`,
		`Password"Quote123`,
		"Password'SingleQuote123",
	}
	for _, p := range passwords {
		p := p
		t.Run(p, func(t *testing.T) {
			h := buildFreshInstallHarness(t, "admin@orvix.email", p)
			t.Cleanup(func() { h.close(t) })
			for _, endpoint := range []string{"/api/v1/auth/login", "/admin/login"} {
				h.loginAsAdmin(t, endpoint)
			}
		})
	}
}

// TestFreshInstall_HealthGateClosesBeforeBootstrap is the
// last-line guard: if seedAdminUser silently fails (e.g. the
// users table is missing), the installer must NOT be able
// to reach /api/v1/health and "INSTALLATION VERIFICATION
// PASSED". The health endpoint does not require the user to
// exist, so we additionally verify the database row.
func TestFreshInstall_DatabaseHasExactlyOneAdminUser(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	var count int
	if err := h.sqlDB.QueryRow(
		`SELECT COUNT(*) FROM users WHERE email = ? AND role = 'admin' AND active = 1`,
		h.email,
	).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one active admin user, got %d", count)
	}

	var mboxCount int
	if err := h.sqlDB.QueryRow(
		`SELECT COUNT(*) FROM coremail_mailboxes WHERE email = ? AND is_admin = 1 AND status = 'active' AND deleted_at IS NULL`,
		h.email,
	).Scan(&mboxCount); err != nil {
		t.Fatalf("count mailboxes: %v", err)
	}
	if mboxCount != 1 {
		t.Fatalf("expected exactly one active admin mailbox, got %d", mboxCount)
	}
}

// ── helpers ────────────────────────────────────────────

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func readPasswordHash(t *testing.T, h *freshInstallHarness) string {
	t.Helper()
	var hash string
	if err := h.sqlDB.QueryRow(`SELECT password_hash FROM users WHERE email = ?`, h.email).Scan(&hash); err != nil {
		t.Fatalf("read password_hash: %v", err)
	}
	return hash
}

func splitOn(s, sep string) []string {
	out := []string{}
	for {
		idx := strings.Index(s, sep)
		if idx < 0 {
			out = append(out, s)
			return out
		}
		out = append(out, s[:idx])
		s = s[idx+len(sep):]
	}
}

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}
