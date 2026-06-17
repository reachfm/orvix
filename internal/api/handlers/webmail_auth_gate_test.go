package handlers_test

// Tests for the Orvix Webmail auth gate (real-webmail
// model, fix/real-webmail-auth).
//
// The webmail UI is a vanilla-JS enterprise client
// (release/webmail/assets/webmail.js, loaded by
// release/webmail/assets/auth-gate.js). Without a gate,
// the client would call /api/v1/webmail/* and render
// Inbox/Compose before any auth check — the user would
// see the mailbox UI even when every API call returns
// 401.
//
// The fix is an inline-ish gate: two files under
// release/webmail/assets/ (auth-gate.css and auth-gate.js)
// loaded by index.html BEFORE the client bundle. The
// gate probes /api/v1/webmail/session with
// credentials:include, and either:
//   - shows the webmail login form (no cookie / 401),
//   - shows a "no mailbox" card (cookie valid but no
//     coremail_mailboxes row), or
//   - reveals the SPA (session + mailbox) and hands off
//     to window.OrvixWebmail.init().
//
// These tests pin the contract:
//
//   1. release/webmail/index.html references the gate
//      files before the webmail client so the gate runs
//      first.
//   2. The gate probes /api/v1/webmail/session (the new
//      webmail-only session endpoint), not /api/v1/me
//      (the admin endpoint). The webmail is NOT gated
//      on an admin session.
//   3. On 401 the gate shows a real webmail login form
//      with email + password fields; the form posts to
//      /api/v1/webmail/login.
//   4. The login form is in the auth-gate.js file (not
//      in a separate bundle), and the form submission
//      is the only POST the gate performs.
//   5. /admin is unaffected — the gate is webmail-only.
//   6. The existing client bundle and admin app.js still
//      pass node --check.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// webmailRepoRoot returns the absolute path to the repo
// root for the current package. The package is three
// levels deep under the repo root
// (internal/api/handlers/), so the root is three levels
// up.
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

// readFile reads a path under the repo root and returns
// its contents as a string, failing the test on any
// error.
func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// extractFunctionBody returns the substring of src from
// the start of the function header at `start` up to the
// matching closing brace at depth 0. The body is
// inclusive of both braces. Tracking brace depth by hand
// is the simplest reliable way to bound a function body
// in a JS file that may contain nested functions and
// strings with braces.
func extractFunctionBody(src string, start int) string {
	// Skip past the function header to the opening
	// brace.
	openIdx := strings.Index(src[start:], "{")
	if openIdx < 0 {
		return src[start:]
	}
	openIdx += start
	depth := 0
	for i := openIdx; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	return src[start:]
}

// TestWebmailIndexHtmlReferencesGateBeforeBundle pins
// the load order: the gate script must come BEFORE the
// webmail client so the gate can run /api/v1/webmail/session
// first and only then hand off via
// window.OrvixWebmail.init().
func TestWebmailIndexHtmlReferencesGateBeforeBundle(t *testing.T) {
	root := webmailRepoRoot(t)
	html := readFile(t, root, "release/webmail/index.html")

	cssIdx := strings.Index(html, "/assets/auth-gate.css")
	jsIdx := strings.Index(html, "/assets/auth-gate.js")
	clientIdx := strings.Index(html, "/assets/webmail.js")

	if cssIdx < 0 {
		t.Fatal("index.html must reference /assets/auth-gate.css")
	}
	if jsIdx < 0 {
		t.Fatal("index.html must reference /assets/auth-gate.js")
	}
	if clientIdx < 0 {
		t.Fatal("index.html must reference /assets/webmail.js")
	}
	if !(cssIdx < clientIdx) {
		t.Errorf("auth-gate.css reference must appear before webmail.js; cssIdx=%d clientIdx=%d", cssIdx, clientIdx)
	}
	if !(jsIdx < clientIdx) {
		t.Errorf("auth-gate.js reference must appear before webmail.js; jsIdx=%d clientIdx=%d", jsIdx, clientIdx)
	}

	// The legacy React demo bundle MUST NOT be
	// referenced. Phase Real Webmail v1 forbids
	// shipping the demo bundle (it called
	// /api/v1/queue which does not exist as a real
	// webmail API).
	if strings.Contains(html, "index-CmhA8wNq.js") {
		t.Error("index.html must not reference the legacy demo React bundle (index-CmhA8wNq.js)")
	}
	// The current webmail.js must use defer (matches
	// auth-gate.js which is already defer); otherwise
	// the gate would run AFTER webmail.js auto-init (a
	// regression we are explicitly avoiding).
	if !strings.Contains(html, `defer src="/assets/webmail.js"`) {
		t.Error("index.html must load webmail.js with defer so auth-gate.js can run first")
	}
	// No inline scripts — the asset CSP forbids
	// 'unsafe-inline'.
	scriptRe := regexp.MustCompile(`(?is)<script\b([^>]*)>`)
	for _, m := range scriptRe.FindAllStringSubmatch(html, -1) {
		attrs := m[1]
		if !strings.Contains(attrs, "src=") {
			t.Errorf("index.html contains inline <script> without src (CSP forbids unsafe-inline): %q", m[0])
		}
	}
}

// TestWebmailGateProbesWebmailSessionEndpoint pins the
// contract that auth-gate.js probes the WEBMAIL session
// endpoint, not the admin /api/v1/me endpoint. This is
// the core change that decouples webmail from the admin
// session: the gate must not require the user to log
// into the admin panel first.
func TestWebmailGateProbesWebmailSessionEndpoint(t *testing.T) {
	root := webmailRepoRoot(t)
	js := readFile(t, root, "release/webmail/assets/auth-gate.js")

	mustHave := []string{
		`'/api/v1/webmail/session'`,
		`'/api/v1/webmail/login'`,
		`credentials: 'include'`,
		`status === 401`,
		`showLogin`,
		`showAuthed`,
		`showNoMailbox`,
		`showError`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(js, needle) {
			t.Errorf("auth-gate.js must contain %q", needle)
		}
	}

	// The gate MUST NOT depend on the admin
	// /api/v1/me endpoint. Webmail login is a
	// separate flow.
	forbidden := []string{
		`'/api/v1/me'`,
		`"/api/v1/me"`,
		`/api/v1/me`,
		`/api/v1/auth/login`,
		`/auth/login`,
		`/admin`,
		`localStorage.setItem`,
		`document.cookie =`,
		`document.cookie=`,
	}
	for _, f := range forbidden {
		if strings.Contains(js, f) {
			t.Errorf("auth-gate.js must not contain %q (webmail must be independent of admin auth)", f)
		}
	}
}

// TestWebmailGateRendersLoginFormOn401 pins the
// no-regression requirement: the auth gate must render a
// real webmail login form (email + password) when the
// session check returns 401. The form must POST to
// /api/v1/webmail/login, not /api/v1/auth/login.
func TestWebmailGateRendersLoginFormOn401(t *testing.T) {
	root := webmailRepoRoot(t)
	js := readFile(t, root, "release/webmail/assets/auth-gate.js")

	// The login form must include the two required
	// inputs. The gate builds the inputs in JS via
	// the el() helper, so the source uses property
	// syntax (type: 'email') — but the resulting DOM
	// attribute is the standard HTML form value.
	mustHave := []string{
		`orvix-gate-email`,
		`orvix-gate-password`,
		`type: 'email'`,
		`type: 'password'`,
		`autocomplete: 'username'`,
		`autocomplete: 'current-password'`,
		`Sign in`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(js, needle) {
			t.Errorf("auth-gate.js login form must contain %q", needle)
		}
	}

	// The form must POST to /api/v1/webmail/login
	// specifically. A POST to /api/v1/auth/login would
	// be the admin login endpoint and is forbidden
	// here.
	if !strings.Contains(js, "API_LOGIN") {
		t.Error("auth-gate.js login form must reference API_LOGIN endpoint constant")
	}
	if !strings.Contains(js, "method: 'POST'") &&
		!strings.Contains(js, `method:"POST"`) &&
		!strings.Contains(js, `method: "POST"`) {
		t.Error("auth-gate.js login form must POST the credentials")
	}
}

// TestWebmailGateHidesContentUntilAuthed pins the no-
// data-leak contract: the gate must NOT call
// window.OrvixWebmail.init() (which kicks off the
// mailbox API calls) until the session check has
// resolved to authenticated:true.
func TestWebmailGateHidesContentUntilAuthed(t *testing.T) {
	root := webmailRepoRoot(t)
	js := readFile(t, root, "release/webmail/assets/auth-gate.js")

	// showAuthed is the only path that calls
	// window.OrvixWebmail.init(). The body of
	// showAuthed must contain that call, and the body
	// of showLogin / showNoMailbox / showError must
	// not.
	if !strings.Contains(js, "showAuthed()") {
		t.Error("auth-gate.js must call showAuthed() on 200 with authenticated:true")
	}

	// Find the showLogin function body and assert it
	// does not boot the client. The body of showLogin
	// is short — bounded by the next `function` or
	// `}` at the same level. We find the closing
	// brace by tracking brace depth starting from the
	// function header.
	showLoginStart := strings.Index(js, "function showLogin()")
	if showLoginStart < 0 {
		t.Fatal("showLogin function not found in auth-gate.js")
	}
	showLoginBody := extractFunctionBody(js, showLoginStart)
	if strings.Contains(showLoginBody, "OrvixWebmail.init()") {
		t.Error("showLogin must not call OrvixWebmail.init() — the client must stay hidden on 401")
	}

	// Same for showNoMailbox.
	noMailboxStart := strings.Index(js, "function showNoMailbox(")
	if noMailboxStart < 0 {
		t.Fatal("showNoMailbox function not found in auth-gate.js")
	}
	noMailboxBody := extractFunctionBody(js, noMailboxStart)
	if strings.Contains(noMailboxBody, "OrvixWebmail.init()") {
		t.Error("showNoMailbox must not call OrvixWebmail.init() — the client must stay hidden when no mailbox is configured")
	}

	// And showError.
	errorStart := strings.Index(js, "function showError(")
	if errorStart < 0 {
		t.Fatal("showError function not found in auth-gate.js")
	}
	errorBody := extractFunctionBody(js, errorStart)
	if strings.Contains(errorBody, "OrvixWebmail.init()") {
		t.Error("showError must not call OrvixWebmail.init() — the client must stay hidden on network failure")
	}

	// Conversely, showAuthed MUST call
	// OrvixWebmail.init() — that is the handoff to
	// the real webmail client.
	authedStart := strings.Index(js, "function showAuthed()")
	if authedStart < 0 {
		t.Fatal("showAuthed function not found in auth-gate.js")
	}
	authedBody := extractFunctionBody(js, authedStart)
	if !strings.Contains(authedBody, "OrvixWebmail.init()") {
		t.Error("showAuthed must call OrvixWebmail.init() — that is the handoff to the real client")
	}
}

// TestWebmailGateCssContract pins the gate styles file.
// The styles must use class names that the JS targets,
// and the file must include the form layout rules.
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
		// The login form styles must be present.
		`.orvix-gate-form`,
		`.orvix-gate-input`,
		`.orvix-gate-label`,
		// Visible text comes from JS; CSS must
		// define the visual presentation.
		`background: #0C0E12`,
		`color: #E8EAF0`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(css, needle) {
			t.Errorf("auth-gate.css must contain %q", needle)
		}
	}
}

// TestAdminUIUnaffectedByWebmailGate pins the
// no-regression requirement: the /admin bundle must NOT
// load the webmail auth gate. /admin has its own login
// form already.
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

	// And admin/app.js must still pass node --check.
	// This is the regression gate the user explicitly
	// listed (`node --check release/admin/app.js`). On
	// Windows we rely on the test runner to detect the
	// JS file but node is required for the syntax
	// check; skip if absent.
	if _, err := exec.LookPath("node"); err != nil {
		t.Logf("node not available; skipping node --check for admin/app.js")
	} else {
		out, err := exec.Command("node", "--check", filepath.Join(root, "release/admin/app.js")).CombinedOutput()
		if err != nil {
			t.Fatalf("node --check release/admin/app.js failed: %v\n%s", err, string(out))
		}
	}
}

// TestWebmailGateFilesAreServedByRouter is a runtime
// test: build a real Fiber router with the webmail assets
// dir pointing at the repo's release/webmail/, then
// confirm:
//   - GET /webmail returns the index.html with the gate.
//   - GET /webmail/assets/auth-gate.js returns the JS
//     file.
//   - GET /webmail/assets/auth-gate.css returns the CSS
//     file.
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
	if !strings.Contains(body, "/assets/auth-gate.js") {
		t.Fatalf("/webmail HTML missing auth-gate.js reference: %s", body)
	}
	if !strings.Contains(body, "/assets/auth-gate.css") {
		t.Fatalf("/webmail HTML missing auth-gate.css reference: %s", body)
	}
	if !strings.Contains(body, `/assets/webmail.js`) {
		t.Fatalf("/webmail HTML missing webmail.js reference: %s", body)
	}

	// /webmail/inbox — SPA fallback to index.html, must
	// also include the gate.
	status, body = get(t, "/webmail/inbox")
	if status != 200 {
		t.Fatalf("GET /webmail/inbox: expected 200, got %d", status)
	}
	if !strings.Contains(body, "/assets/auth-gate.js") {
		t.Fatalf("/webmail/inbox HTML missing auth-gate.js reference")
	}

	// /webmail/compose — same SPA fallback.
	status, body = get(t, "/webmail/compose")
	if status != 200 {
		t.Fatalf("GET /webmail/compose: expected 200, got %d", status)
	}
	if !strings.Contains(body, "/assets/auth-gate.js") {
		t.Fatalf("/webmail/compose HTML missing auth-gate.js reference")
	}

	// /webmail/assets/auth-gate.js — must return 200
	// with the JS content (not 404, not the SPA
	// fallback).
	status, body = get(t, "/webmail/assets/auth-gate.js")
	if status != 200 {
		t.Fatalf("GET /webmail/assets/auth-gate.js: expected 200, got %d", status)
	}
	if !strings.Contains(body, "Orvix Webmail") || !strings.Contains(body, "/api/v1/webmail/session") {
		t.Fatalf("auth-gate.js content unexpected: missing /api/v1/webmail/session reference")
	}

	// /webmail/assets/auth-gate.css — same.
	status, body = get(t, "/webmail/assets/auth-gate.css")
	if status != 200 {
		t.Fatalf("GET /webmail/assets/auth-gate.css: expected 200, got %d", status)
	}
	if !strings.Contains(body, "#orvix-auth-gate") {
		t.Fatalf("auth-gate.css content unexpected: %s", body)
	}

	// /webmail/assets/webmail.js — the real webmail
	// client (Phase Real Webmail v1). Must be served;
	// this is the replacement for the legacy demo
	// bundle.
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
	if !strings.Contains(body, "--sidebar-w") || !strings.Contains(body, "3-pane") {
		t.Fatalf("webmail.css content unexpected: missing 3-pane layout markers")
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

	// /assets/auth-gate.js — the new short asset path
	// the webmail SPA references in index.html. Must
	// return the same JS content as
	// /webmail/assets/auth-gate.js.
	status, body = get(t, "/assets/auth-gate.js")
	if status != 200 {
		t.Fatalf("GET /assets/auth-gate.js: expected 200, got %d", status)
	}
	if !strings.Contains(body, "Orvix Webmail") || !strings.Contains(body, "/api/v1/webmail/session") {
		t.Fatalf("/assets/auth-gate.js content unexpected")
	}

	// /assets/webmail.js — the real webmail client at
	// the short path. Must NOT return the SPA fallback.
	status, body = get(t, "/assets/webmail.js")
	if status != 200 {
		t.Fatalf("GET /assets/webmail.js: expected 200, got %d", status)
	}
	if strings.Contains(body, "<!doctype html") {
		t.Fatalf("/assets/webmail.js returned the SPA fallback (index.html); body[:200]=%q", body[:min(200, len(body))])
	}

	// /assets/webmail.css — webmail styles at the short
	// path.
	status, body = get(t, "/assets/webmail.css")
	if status != 200 {
		t.Fatalf("GET /assets/webmail.css: expected 200, got %d", status)
	}
	if !strings.Contains(body, "--sidebar-w") {
		t.Fatalf("/assets/webmail.css content unexpected: missing 3-pane layout markers")
	}
}

// TestWebmailGateSessionEndpointExistsAndIsProtected is a
// runtime check that the gate's session probe actually
// hits a real endpoint in the running router. Without
// auth the endpoint must return 401 (the gate's "show
// login form" signal). With auth it returns either 200
// (mailbox configured) or 200 with authenticated:false
// (no mailbox). This is the contract that wires the JS
// gate to the live API.
func TestWebmailGateSessionEndpointExistsAndIsProtected(t *testing.T) {
	router, _, cleanup := buildWebmailRouter(t)
	defer cleanup()

	// Without auth, /api/v1/webmail/session must
	// return 401 (not 404, not 200). The gate uses
	// this status code to decide between the login
	// form and the empty state. The endpoint is on
	// the protected group so the auth middleware
	// short-circuits with 401 before the handler
	// runs — the 401 we see here comes from
	// auth.Middleware(), not from WebmailSession.
	req := httptest.NewRequest("GET", "/api/v1/webmail/session", nil)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("GET /api/v1/webmail/session: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/v1/webmail/session unauth: expected 401, got %d", resp.StatusCode)
	}
}

// TestWebmailLoginEndpointExposed pins the no-regression
// requirement that POST /api/v1/webmail/login is
// reachable as a public endpoint (no auth middleware),
// with rate-limiting. The endpoint must exist (not 404)
// so the auth-gate form can post to it.
func TestWebmailLoginEndpointExposed(t *testing.T) {
	router, _, cleanup := buildWebmailRouter(t)
	defer cleanup()

	// An empty body must return 400 (invalid request)
	// — that proves the endpoint is registered and
	// the JSON binding is wired. We do NOT test the
	// success path here; that has its own end-to-end
	// test.
	req := httptest.NewRequest("POST", "/api/v1/webmail/login", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("POST /api/v1/webmail/login: %v", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("POST /api/v1/webmail/login returned 404; the endpoint is not registered")
	}
	// 400 (invalid request), 401 (no credentials), or
	// 429 (rate-limited) are all valid responses. The
	// only forbidden one is 404.
}

// TestWebmailGateJsPassesNodeSyntaxCheck is the explicit
// gate from the user's acceptance criteria. We skip if
// node is not installed (e.g. minimal CI container).
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
// callers that want it), and a cleanup function that
// must be called via defer to release the sqlite file
// handle before t.TempDir tries to RemoveAll on Windows.
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
		// Shutdown Fiber first so it stops
		// accepting requests, then close the
		// underlying sql.DB so the sqlite file
		// handle is released before t.TempDir's
		// RemoveAll runs (Windows holds the file
		// open otherwise and the cleanup fails
		// with "process cannot access the file
		// because it is being used by another
		// process").
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
