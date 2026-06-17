package handlers_test

// Tests for the Orvix Webmail auth gate.
//
// The webmail UI is a React SPA bundled into
// release/webmail/assets/index-CmhA8wNq.js. Without a gate,
// the bundle mounts #root and renders Inbox/Compose before
// any auth check — the user sees the mailbox UI even when
// every API call returns 401.
//
// The fix is an inline-ish gate: two files under
// release/webmail/assets/ (auth-gate.css and auth-gate.js)
// loaded by index.html BEFORE the React bundle. The gate
// hides #root, probes /api/v1/me with credentials:include,
// and either shows the React app (auth OK) or a visible
// "Please sign in" card (auth missing/failed).
//
// These tests pin the contract:
//
//   1. release/webmail/index.html references the gate files
//      before the React bundle so the gate runs first.
//   2. The gate hides #root until auth resolves.
//   3. The gate fetches /api/v1/me (the existing session
//      endpoint) with credentials:include so the admin
//      session cookie is sent.
//   4. The gate handles 200, 401, and other statuses
//      visibly. No silent failure mode.
//   5. /admin is unaffected — the gate is webmail-only.
//   6. The existing React bundle and admin app.js still
//      pass node --check.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// webmailRepoRoot returns the absolute path to the repo root
// for the current package. The package is three levels deep
// under the repo root (internal/api/handlers/), so the root
// is three levels up.
func webmailRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root sanity check failed at %s: %v", root, err)
	}
	return root
}

// readFile reads a path under the repo root and returns its
// contents as a string, failing the test on any error.
func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// TestWebmailIndexHtmlReferencesGateBeforeBundle pins the
// load order: the gate script must come BEFORE the webmail
// client so it can hide #root before the client mounts.
func TestWebmailIndexHtmlReferencesGateBeforeBundle(t *testing.T) {
	root := webmailRepoRoot(t)
	html := readFile(t, root, "release/webmail/index.html")

	cssIdx := strings.Index(html, "/webmail/assets/auth-gate.css")
	jsIdx := strings.Index(html, "/webmail/assets/auth-gate.js")
	clientIdx := strings.Index(html, "/webmail/assets/webmail.js")

	if cssIdx < 0 {
		t.Fatal("index.html must reference /webmail/assets/auth-gate.css")
	}
	if jsIdx < 0 {
		t.Fatal("index.html must reference /webmail/assets/auth-gate.js")
	}
	if clientIdx < 0 {
		t.Fatal("index.html must reference /webmail/assets/webmail.js")
	}
	if !(cssIdx < clientIdx) {
		t.Errorf("auth-gate.css reference must appear before webmail.js; cssIdx=%d clientIdx=%d", cssIdx, clientIdx)
	}
	if !(jsIdx < clientIdx) {
		t.Errorf("auth-gate.js reference must appear before webmail.js; jsIdx=%d clientIdx=%d", jsIdx, clientIdx)
	}

	// The legacy React demo bundle MUST NOT be referenced.
	// Phase Real Webmail v1 forbids shipping the demo bundle
	// (it calls /api/v1/queue which does not exist as a real
	// webmail API).
	if strings.Contains(html, "index-CmhA8wNq.js") {
		t.Error("index.html must not reference the legacy demo React bundle (index-CmhA8wNq.js)")
	}
	// #root must still be present so the gate can hide it.
	if !strings.Contains(html, `<div id="root">`) {
		t.Error("index.html must keep <div id=\"root\"> as the gate's hidden mount point")
	}
}

// TestWebmailGateJsProbesMeEndpoint pins the contract that
// auth-gate.js probes /api/v1/me and reacts to 200/401/other.
func TestWebmailGateJsProbesMeEndpoint(t *testing.T) {
	root := webmailRepoRoot(t)
	js := readFile(t, root, "release/webmail/assets/auth-gate.js")

	mustHave := []string{
		`'/api/v1/me'`,
		`credentials: 'include'`,
		`status === 200`,
		`status === 401`,
		`hideRoot`,
		`showAuthed`,
		`showUnauth`,
		`showError`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(js, needle) {
			t.Errorf("auth-gate.js must contain %q", needle)
		}
	}

	// The gate must NOT make any POST/PUT/DELETE/auth/login
	// calls. It is read-only and does not modify state.
	forbidden := []string{
		`method: 'POST'`,
		`method:"POST"`,
		`method: "POST"`,
		`/api/v1/auth/login`,
		`/auth/login`,
		`/api/v1/auth/refresh`,
		`localStorage.setItem`,
		`document.cookie =`,
		`document.cookie=`,
	}
	for _, f := range forbidden {
		if strings.Contains(js, f) {
			t.Errorf("auth-gate.js must not contain %q (gate is read-only)", f)
		}
	}
}

// TestWebmailGateJsHidesRootUntilAuthed pins the core
// behaviour: the React mount point is hidden by default and
// only revealed after /api/v1/me returns 200.
func TestWebmailGateJsHidesRootUntilAuthed(t *testing.T) {
	root := webmailRepoRoot(t)
	js := readFile(t, root, "release/webmail/assets/auth-gate.js")

	// hideRoot sets display:none on #root.
	if !strings.Contains(js, `root.style.display = 'none'`) {
		t.Error("auth-gate.js must hide #root via display:none")
	}
	// showAuthed clears the gate AND reveals #root.
	if !strings.Contains(js, `root.style.display = ''`) {
		t.Error("auth-gate.js must reveal #root only after a successful auth check")
	}
	// The "hide" call must run BEFORE the fetch so the bundle
	// cannot briefly render Compose/Inbox before the gate
	// resolves.
	hideIdx := strings.Index(js, "hideRoot();")
	fetchIdx := strings.Index(js, "fetch(API_ME")
	if hideIdx < 0 || fetchIdx < 0 || fetchIdx < hideIdx {
		t.Errorf("hideRoot() must run before the fetch; hideIdx=%d fetchIdx=%d", hideIdx, fetchIdx)
	}
}

// TestWebmailGateJsHandlesUnauth pins the unauth path: 401
// from /api/v1/me must produce a visible "Please sign in"
// card with a link, not silently fall through.
func TestWebmailGateJsHandlesUnauth(t *testing.T) {
	root := webmailRepoRoot(t)
	js := readFile(t, root, "release/webmail/assets/auth-gate.js")

	// The 401 branch must call showUnauth.
	if !strings.Contains(js, `showUnauth()`) {
		t.Error("auth-gate.js must call showUnauth() on 401")
	}
	// The sign-in card must include the link target /admin.
	if !strings.Contains(js, `SIGN_IN_HREF = '/admin'`) {
		t.Error("auth-gate.js sign-in card must link to /admin")
	}
	// The sign-in card text must be visible and not empty.
	if !strings.Contains(js, "Please sign in") {
		t.Error("auth-gate.js sign-in card must include visible 'Please sign in' copy")
	}
	// showUnauth must NOT reveal #root.
	// We assert this indirectly: the showUnauth function
	// body must not touch root.style.display.
	unauthStart := strings.Index(js, "function showUnauth()")
	if unauthStart < 0 {
		t.Fatal("showUnauth function not found")
	}
	unauthBody := js[unauthStart:]
	if strings.Contains(unauthBody, "root.style.display") {
		t.Error("showUnauth must not touch #root — the React app stays hidden on 401")
	}
}

// TestWebmailGateCssContract pins the gate styles file. The
// styles must use class names that the JS targets, and the
// file must be served as a static asset.
func TestWebmailGateCssContract(t *testing.T) {
	root := webmailRepoRoot(t)
	css := readFile(t, root, "release/webmail/assets/auth-gate.css")

	mustHave := []string{
		`#orvix-auth-gate`,
		`.orvix-gate-card`,
		`.orvix-gate-title`,
		`.orvix-gate-text`,
		`.orvix-gate-button`,
		`.orvix-gate-spinner`,
		`@keyframes orvix-gate-spin`,
		// Visible text comes from JS; CSS must define the
		// visual presentation.
		`background: #0C0E12`,
		`color: #E8EAF0`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(css, needle) {
			t.Errorf("auth-gate.css must contain %q", needle)
		}
	}
}

// TestAdminUIUnaffectedByWebmailGate pins the no-regression
// requirement: the /admin bundle must NOT load the webmail
// auth gate. /admin has its own login form already.
func TestAdminUIUnaffectedByWebmailGate(t *testing.T) {
	root := webmailRepoRoot(t)
	admin := readFile(t, root, "release/admin/index.html")
	for _, forbidden := range []string{
		"auth-gate.js",
		"auth-gate.css",
	} {
		if strings.Contains(admin, forbidden) {
			t.Errorf("admin/index.html must not reference %q", forbidden)
		}
	}

	// And admin/app.js must still pass node --check. This
	// is the regression gate the user explicitly listed
	// (`node --check release/admin/app.js`). On Windows we
	// rely on the test runner to detect the JS file but
	// node is required for the syntax check; skip if absent.
	if _, err := exec.LookPath("node"); err != nil {
		t.Logf("node not available; skipping node --check for admin/app.js")
	} else {
		out, err := exec.Command("node", "--check", filepath.Join(root, "release/admin/app.js")).CombinedOutput()
		if err != nil {
			t.Fatalf("node --check release/admin/app.js failed: %v\n%s", err, string(out))
		}
	}
}

// TestWebmailGateFilesAreServedByRouter is a runtime test:
// build a real Fiber router with the webmail assets dir
// pointing at the repo's release/webmail/, then confirm:
//   - GET /webmail returns the index.html with the gate.
//   - GET /webmail/assets/auth-gate.js returns the JS file.
//   - GET /webmail/assets/auth-gate.css returns the CSS file.
//   - GET /webmail/inbox (SPA fallback) also returns the
//     same index.html with the gate.
func TestWebmailGateFilesAreServedByRouter(t *testing.T) {
	router, _, cleanup := buildWebmailRouter(t)
	defer cleanup()

	app := router.App()

	get := func(t *testing.T, path string) (int, string) {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	// /webmail — index.html with gate references.
	status, body := get(t, "/webmail")
	if status != 200 {
		t.Fatalf("GET /webmail: expected 200, got %d", status)
	}
	if !strings.Contains(body, "/webmail/assets/auth-gate.js") {
		t.Fatalf("/webmail HTML missing auth-gate.js reference: %s", body)
	}
	if !strings.Contains(body, "/webmail/assets/auth-gate.css") {
		t.Fatalf("/webmail HTML missing auth-gate.css reference: %s", body)
	}
	if !strings.Contains(body, `<div id="root">`) {
		t.Fatalf("/webmail HTML missing #root mount point: %s", body)
	}

	// /webmail/inbox — SPA fallback to index.html, must
	// also include the gate.
	status, body = get(t, "/webmail/inbox")
	if status != 200 {
		t.Fatalf("GET /webmail/inbox: expected 200, got %d", status)
	}
	if !strings.Contains(body, "/webmail/assets/auth-gate.js") {
		t.Fatalf("/webmail/inbox HTML missing auth-gate.js reference")
	}

	// /webmail/compose — same SPA fallback.
	status, body = get(t, "/webmail/compose")
	if status != 200 {
		t.Fatalf("GET /webmail/compose: expected 200, got %d", status)
	}
	if !strings.Contains(body, "/webmail/assets/auth-gate.js") {
		t.Fatalf("/webmail/compose HTML missing auth-gate.js reference")
	}

	// /webmail/assets/auth-gate.js — must return 200 with
	// the JS content (not 404, not the SPA fallback).
	status, body = get(t, "/webmail/assets/auth-gate.js")
	if status != 200 {
		t.Fatalf("GET /webmail/assets/auth-gate.js: expected 200, got %d", status)
	}
	if !strings.Contains(body, "Orvix Webmail") || !strings.Contains(body, "/api/v1/me") {
		t.Fatalf("auth-gate.js content unexpected: %s", body)
	}

	// /webmail/assets/auth-gate.css — same.
	status, body = get(t, "/webmail/assets/auth-gate.css")
	if status != 200 {
		t.Fatalf("GET /webmail/assets/auth-gate.css: expected 200, got %d", status)
	}
	if !strings.Contains(body, "#orvix-auth-gate") {
		t.Fatalf("auth-gate.css content unexpected: %s", body)
	}

	// /webmail/assets/webmail.js — the real webmail client
	// (Phase Real Webmail v1). Must be served; this is
	// the replacement for the legacy demo bundle.
	status, body = get(t, "/webmail/assets/webmail.js")
	if status != 200 {
		t.Fatalf("GET /webmail/assets/webmail.js: expected 200, got %d", status)
	}
	if strings.Contains(body, "<!doctype html") {
		t.Fatalf("webmail.js returned the SPA fallback (index.html); the file is missing on disk. body[:200]=%q", body[:min(200, len(body))])
	}
	if !strings.Contains(body, "/api/v1/webmail/me") {
		t.Fatalf("webmail.js must reference /api/v1/webmail/me: %s", body[:min(200, len(body))])
	}
	if len(body) < 1000 {
		t.Fatalf("webmail.js suspiciously small: %d bytes", len(body))
	}

	// /webmail/assets/webmail.css — webmail styles.
	status, body = get(t, "/webmail/assets/webmail.css")
	if status != 200 {
		t.Fatalf("GET /webmail/assets/webmail.css: expected 200, got %d", status)
	}
	if !strings.Contains(body, ".webmail") {
		t.Fatalf("webmail.css content unexpected: %s", body)
	}

	// The legacy demo bundle MUST NOT be served. The
	// Fiber SPA fallback returns the index.html for any
	// unknown asset, so a 200 response is expected but
	// the body must be the index.html (SPA fallback),
	// not a real JS bundle. This proves the demo bundle
	// file does not exist on disk anymore.
	status, body = get(t, "/webmail/assets/index-CmhA8wNq.js")
	if status == 200 && !strings.Contains(body, "<!doctype html") {
		t.Fatalf("legacy demo bundle (index-CmhA8wNq.js) appears to be served as a real file; body[:200]=%q", body[:min(200, len(body))])
	}
	// The body must be the SPA fallback (index.html)
	// and must not contain the React mount point that
	// was in the original demo bundle.
	if status == 200 && !strings.Contains(body, "auth-gate.js") {
		t.Fatalf("GET /webmail/assets/index-CmhA8wNq.js: expected index.html fallback, got %q", body[:min(200, len(body))])
	}
}

// TestWebmailGateProbesActualAPIEndpoint exercises the gate
// end-to-end against a running Fiber router. We boot the
// router, hit /webmail, and confirm that the embedded
// /api/v1/me reference matches a real, auth-protected
// endpoint in the running API. If a future refactor renames
// or removes /api/v1/me without updating the gate, this
// test fails before the user sees a blank webmail.
//
// We do NOT execute the JS in a browser; we assert the JS
// string and the live endpoint agree.
func TestWebmailGateProbesActualAPIEndpoint(t *testing.T) {
	root := webmailRepoRoot(t)
	js := readFile(t, root, "release/webmail/assets/auth-gate.js")

	// Extract the path literal between the outermost
	// single quotes after `var API_ME = `.
	idx := strings.Index(js, "var API_ME = '")
	if idx < 0 {
		t.Fatal("API_ME assignment not found in auth-gate.js")
	}
	start := idx + len("var API_ME = '")
	end := strings.Index(js[start:], "'")
	if end < 0 {
		t.Fatal("API_ME closing quote not found")
	}
	apiMePath := js[start : start+end]
	if apiMePath == "" || !strings.HasPrefix(apiMePath, "/") {
		t.Fatalf("API_ME path looks wrong: %q", apiMePath)
	}

	router, _, cleanup := buildWebmailRouter(t)
	defer cleanup()

	// Without auth, the API endpoint must return 401 (not
	// 404, not 200). That is the gate's "unauth" signal.
	req := httptest.NewRequest("GET", apiMePath, nil)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("GET %s: %v", apiMePath, err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("gate endpoint %s: expected 401 for unauthenticated, got %d", apiMePath, resp.StatusCode)
	}
}

// TestAdminAppJSPassesNodeSyntaxCheck is the explicit gate
// from the user's acceptance criteria. We skip if node is
// not installed (e.g. minimal CI container).
func TestAdminAppJSPassesNodeSyntaxCheck(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Logf("node not available; skipping node --check for admin/app.js")
		return
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("skipping on %s", runtime.GOOS)
	}
	root := webmailRepoRoot(t)
	out, err := exec.Command("node", "--check", filepath.Join(root, "release/admin/app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("node --check release/admin/app.js failed: %v\n%s", err, string(out))
	}
}

// TestWebmailGateJsPassesNodeSyntaxCheck is the equivalent
// for the new gate file. Skipped if node is not installed.
func TestWebmailGateJsPassesNodeSyntaxCheck(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Logf("node not available; skipping node --check for auth-gate.js")
		return
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("skipping on %s", runtime.GOOS)
	}
	root := webmailRepoRoot(t)
	out, err := exec.Command("node", "--check", filepath.Join(root, "release/webmail/assets/auth-gate.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("node --check release/webmail/assets/auth-gate.js failed: %v\n%s", err, string(out))
	}
}

// ── helpers ─────────────────────────────────────────────

// buildWebmailRouter wires a real Fiber router with the
// repo's release/webmail/ as the webmail dir and a staged
// admin dir. It returns the router, the scratch dir (for
// callers that want it), and a cleanup function that must
// be called via defer to release the sqlite file handle
// before t.TempDir tries to RemoveAll on Windows.
func buildWebmailRouter(t *testing.T) (*api.Router, string, func()) {
	t.Helper()
	root := webmailRepoRoot(t)
	webmailDir := filepath.Join(root, "release", "webmail")

	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	mkAllWebmail(t, adminDir)
	writeFileWebmail(t, filepath.Join(adminDir, "index.html"), "<html><body>admin</body></html>")
	writeFileWebmail(t, filepath.Join(adminDir, "app.js"), "")
	writeFileWebmail(t, filepath.Join(adminDir, "styles.css"), "")

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = scratchDir + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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
	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	cleanup := func() {
		// Shutdown Fiber first so it stops accepting
		// requests, then close the underlying sql.DB so
		// the sqlite file handle is released before
		// t.TempDir's RemoveAll runs (Windows holds the
		// file open otherwise and the cleanup fails with
		// "process cannot access the file because it is
		// being used by another process").
		_ = router.App().Shutdown()
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	return router, scratchDir, cleanup
}

func mkAllWebmail(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFileWebmail(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
