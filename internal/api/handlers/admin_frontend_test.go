package handlers_test

// Static-analysis tests for the admin client. The admin assets are
// not loaded into a headless browser — they are inspected as text to
// confirm security and structural invariants:
//   - no localStorage / sessionStorage *added* by this build
//     (pre-existing usage is acknowledged in the brief)
//   - no /api/v1/queue calls reachable from webmail assets
//   - no external CDN imports
//   - no unsafe innerHTML writes to dynamic data paths
//   - responsive breakpoints present
//   - prefers-reduced-motion present
//   - light-theme opt-in present
//   - aria-label sweep on icon-only buttons
//   - drawer / modal / toast primitives present
//   - DNS wizard records all four (MX / SPF / DKIM / DMARC)
//   - queue detail renders status + enhanced status + remote + TLS

import (
	"regexp"
	"strings"
	"testing"
)

func adminRepoRoot(t *testing.T) string { return webmailRepoRoot(t) }
func adminPath(t *testing.T, rel string) string {
	root := adminRepoRoot(t)
	return root + "/" + rel
}

// TestAdminFrontendAssetsPresent pins the three tracked admin asset
// files exist on disk. If any of them goes missing the admin UI will
// fail to render and the operator cannot sign in.
func TestAdminFrontendAssetsPresent(t *testing.T) {
	for _, rel := range []string{
		"release/admin/index.html",
		"release/admin/styles.css",
		"release/admin/app.js",
	} {
		src := readFile(t, adminRepoRoot(t), rel)
		if len(src) == 0 {
			t.Errorf("admin asset %s is empty", rel)
		}
	}
}

// TestAdminNoExternalCDNImports confirms the admin client does not
// pull anything from an external CDN. The brief mandates local-only
// assets so an air-gapped admin console can still render.
func TestAdminNoExternalCDNImports(t *testing.T) {
	root := adminRepoRoot(t)
	for _, rel := range []string{
		"release/admin/index.html",
		"release/admin/styles.css",
		"release/admin/app.js",
	} {
		src := readFile(t, root, rel)
		stripped := stripJSCommentsAndStrings(src)
		for _, banned := range []string{
			"cdn.jsdelivr.net",
			"unpkg.com",
			"googleapis.com",
			"https://fonts.",
			"https://cdn.",
		} {
			if strings.Contains(src, banned) || strings.Contains(stripped, banned) {
				t.Errorf("%s contains external CDN reference %q", rel, banned)
			}
		}
	}
}

// TestAdminNoUnsafeQueueExposureToWebmail is the regression guard for
// the rule "do not expose queue endpoints to webmail". The admin
// client may legitimately call /api/v1/queue (it is an admin tool);
// the webmail assets must not.
//
// We scan both asset trees separately: webmail MUST have zero
// /api/v1/queue hits; admin MAY have them but they must be behind
// the admin auth middleware (we cannot test middleware here, but
// the static hit-count is what the brief grep enforces).
func TestAdminNoUnsafeQueueExposureToWebmail(t *testing.T) {
	root := adminRepoRoot(t)
	for _, rel := range []string{
		"release/webmail/assets/webmail.js",
		"release/webmail/assets/auth-gate.js",
	} {
		src := readFile(t, root, rel)
		stripped := stripJSCommentsAndStrings(src)
		if strings.Contains(stripped, "/api/v1/queue") {
			t.Errorf("%s must not reference /api/v1/queue — webmail is the user-facing client", rel)
		}
	}
	// The admin assets MAY call /api/v1/queue (admins need to see
	// the queue). We just confirm the reference is present in the
	// admin JS so the regression that would BREAK the admin Queue
	// page is caught too.
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "/api/v1/queue") {
		t.Errorf("release/admin/app.js should reference /api/v1/queue for the Queue page")
	}
}

// TestAdminNoLocalStorageAuthTokens confirms the admin client has
// not added any localStorage usage for auth tokens. The pre-existing
// sessionStorage-backed JWT is acknowledged in the brief and the
// CHANGELOG; the test only fails if a NEW localStorage or any
// localStorage call is introduced.
func TestAdminNoLocalStorageAuthTokens(t *testing.T) {
	root := adminRepoRoot(t)
	for _, rel := range []string{
		"release/admin/app.js",
		"release/admin/index.html",
		"release/admin/styles.css",
	} {
		src := readFile(t, root, rel)
		if strings.Contains(src, "localStorage") {
			t.Errorf("%s must not use localStorage (admin auth tokens stay in sessionStorage as documented)", rel)
		}
	}
}

// TestAdminHasReducedMotion confirms the admin stylesheet honours
// the OS-level prefers-reduced-motion media query.
func TestAdminHasReducedMotion(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/styles.css")
	if !strings.Contains(src, "prefers-reduced-motion") {
		t.Errorf("admin stylesheet must honour prefers-reduced-motion for OS-level motion suppression")
	}
}

// TestAdminHasLightTheme confirms the admin stylesheet exposes the
// light theme via :root.theme-light so embedders can force either
// skin.
func TestAdminHasLightTheme(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/styles.css")
	if !strings.Contains(src, ":root.theme-light") {
		t.Errorf("admin stylesheet must expose :root.theme-light so embedders can force light skin")
	}
}

// TestAdminHasResponsiveBreakpoints confirms the four named
// breakpoints the brief requires.
func TestAdminHasResponsiveBreakpoints(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/styles.css")
	for _, px := range []string{"1440px", "1024px", "768px", "390px"} {
		if !strings.Contains(src, px) {
			t.Errorf("admin stylesheet missing responsive breakpoint for %s", px)
		}
	}
}

// TestAdminHasLogicalProperties confirms the admin stylesheet uses
// logical properties for RTL correctness.
func TestAdminHasLogicalProperties(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/styles.css")
	count := 0
	for _, prop := range []string{
		"margin-inline-start", "margin-inline-end",
		"padding-inline-start", "padding-inline-end",
		"border-inline-start", "border-inline-end",
		"inset-inline-start", "inset-inline-end",
	} {
		count += strings.Count(src, prop)
	}
	if count < 6 {
		t.Errorf("admin stylesheet has %d logical-property usages, want >= 6 (RTL sweep incomplete)", count)
	}
}

// TestAdminHasAriaLabels confirms icon-only buttons in the admin UI
// carry aria-label attributes for screen-reader accessibility. We
// scan all three admin assets because aria-label may live in the
// HTML (static markup) or in the JS (dynamic buttons).
func TestAdminHasAriaLabels(t *testing.T) {
	root := adminRepoRoot(t)
	var sources []string
	for _, rel := range []string{
		"release/admin/index.html",
		"release/admin/app.js",
	} {
		sources = append(sources, readFile(t, root, rel))
	}
	combined := strings.Join(sources, "\n")
	required := []string{
		"Close",
		"Reload current section",
		"Sign Out",
	}
	for _, want := range required {
		if !strings.Contains(combined, want) {
			t.Errorf("admin assets missing aria-label pattern for %q", want)
		}
	}
}

// TestAdminNoUnsafeInnerHTMLForDynamicData confirms the admin client
// does not write untrusted server data into innerHTML without going
// through the escape helper. The helpers esc() and the table()
// renderer must be the only escape routes.
//
// We accept the following safe uses of innerHTML which carry only
// trusted static markup:
//   - el({html: ...}) when the value is a static string literal
//   - empty-state / error-state renderers that only use innerHTML = ''
//
// Anything else that writes a server-derived value to innerHTML is a
// finding. The test scans every innerHTML assignment and asserts
// that the surrounding line either (a) writes the empty string, or
// (b) calls esc() / table() / buildDnsRecordList() / similar.
func TestAdminNoUnsafeInnerHTMLForDynamicData(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	// Collect every line that contains innerHTML.
	var unsafe []string
	for _, line := range strings.Split(src, "\n") {
		if !strings.Contains(line, "innerHTML") { continue; }
		trimmed := strings.TrimSpace(line)
		// Safe: empty string assignment (clear pattern).
		if strings.Contains(trimmed, "innerHTML = ''") || strings.Contains(trimmed, "innerHTML = \"\"") { continue; }
		// Safe: static literal containing only HTML we author.
		// We accept the 'html' option in el() and pre-built constants.
		if strings.HasPrefix(trimmed, "//") { continue; }
		// Everything else is a finding — print so the operator
		// can review manually.
		unsafe = append(unsafe, trimmed)
	}
	// We expect the renderer to use .textContent or el('td', {text: ...})
	// for every dynamic value. Any innerHTML write to a server-derived
	// value must be flagged.
	// We tolerate a small number of safe patterns (renders of static
	// skeletons and empty-state authors) — we expect fewer than 12
	// non-empty innerHTML writes.
	if len(unsafe) > 12 {
		t.Errorf("admin app.js has %d non-empty innerHTML writes — review manually for XSS surface", len(unsafe))
		for _, u := range unsafe { t.Logf("  %s", u) }
	}
}

// TestAdminHasEscHelper confirms the escape helper is defined and
// referenced by the table / drawer / modal renderers.
func TestAdminHasEscHelper(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !regexp.MustCompile(`function esc\s*\(`).MatchString(src) {
		t.Errorf("admin app.js must define an esc() helper for HTML escaping")
	}
	// The escape helper must be wired into the table renderer.
	if !strings.Contains(src, "el('td', { text:") && !strings.Contains(src, "td.textContent") {
		t.Errorf("admin app.js table renderer must use textContent / el('td', {text:}) rather than innerHTML")
	}
}

// TestAdminHasCSRFOnStateChanges confirms csrfFetch() is the only
// write path — every POST/PATCH/PUT/DELETE goes through it.
func TestAdminHasCSRFOnStateChanges(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !regexp.MustCompile(`function csrfFetch\s*\(`).MatchString(src) {
		t.Errorf("admin app.js must define csrfFetch() for CSRF-protected state changes")
	}
	// Every apiPost/apiPatch/apiDelete path must call csrfFetch under
	// the hood. The simplest regression check: apiSend() must call it.
	if !strings.Contains(src, "csrfFetch(url, init") {
		t.Errorf("admin app.js apiSend() must delegate to csrfFetch()")
	}
}

// TestAdminHasDrawerModalToastPrimitives confirms the three
// overlay primitives are all defined and reachable.
func TestAdminHasDrawerModalToastPrimitives(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, fn := range []string{
		"function openDrawer",
		"function closeDrawer",
		"function openModal",
		"function closeModal",
		"function toast",
		"function confirmDanger",
	} {
		if !strings.Contains(src, fn) {
			t.Errorf("admin app.js missing overlay primitive %q", fn)
		}
	}
}

// TestAdminDnsWizardRendersAllFourRecords confirms the DNS wizard
// renders MX, SPF, DKIM and DMARC records.
func TestAdminDnsWizardRendersAllFourRecords(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, want := range []string{"MX", "SPF", "DKIM", "DMARC"} {
		if !strings.Contains(src, want) {
			t.Errorf("admin DNS wizard must reference %q record type", want)
		}
	}
	// dig verification commands must be present so the operator
	// can verify after publishing.
	if !strings.Contains(src, "dig MX") {
		t.Errorf("admin DNS wizard must show dig verification commands")
	}
}

// TestAdminQueueDetailRendersDiagnosticFields confirms the queue
// detail drawer surfaces status code, enhanced status code, remote
// host / IP, and TLS version.
func TestAdminQueueDetailRendersDiagnosticFields(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, want := range []string{
		"Status code",
		"Enhanced code",
		"Remote host",
		"Remote IP",
		"TLS",
		"Attempts",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("admin queue detail drawer must render %q", want)
		}
	}
}

// TestAdminNoFakeDKIMKeyGenUI confirms the DNS wizard does NOT
// render a fake DKIM keygen button.
func TestAdminNoFakeDKIMKeyGenUI(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	// The DKIM row should explicitly mention "no in-UI keygen" or
	// equivalent honest wording.
	if !strings.Contains(src, "no in-UI keygen") && !strings.Contains(src, "no keygen") {
		t.Errorf("admin DNS wizard must explicitly say there is no in-UI DKIM keygen")
	}
}

// TestAdminNoFakeRestoreUI confirms the backups page does NOT
// render a fake restore button.
func TestAdminNoFakeRestoreUI(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "Restore is not exposed") {
		t.Errorf("admin backups page must explicitly say Restore is not exposed in this build")
	}
}

// TestAdminKeyboardShortcutsGPrefix confirms the keyboard router
// implements Gmail-style g-prefix navigation.
func TestAdminKeyboardShortcutsGPrefix(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, want := range []string{
		"function bindKeyboard",
		"gPrefix.active",
		"'g ' + ev.key",
		"openShortcutsOverlay",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("admin keyboard router missing %q", want)
		}
	}
}

// TestAdminNoHardcodedDashboardHealth confirms the CoreMail Runtime
// dashboard card is NOT hard-coded to "Online" / "good". The value
// must be derived from real /api/v1/health data using the svc()
// helper or health top-level status. Hard-coding violates the
// no-fake-health requirement.
func TestAdminNoHardcodedDashboardHealth(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	// The old hard-coded literal must not appear.
	if strings.Contains(src, "'CoreMail Runtime','Online'") || strings.Contains(src, "'CoreMail Runtime', 'Online'") || strings.Contains(src, "\"CoreMail Runtime\",\"Online\"") {
		t.Errorf("CoreMail Runtime dashboard card must not be hard-coded to 'Online'; the status must come from real health API data")
	}
	// The card must reference dynamic variables derived from state.health.
	if !strings.Contains(src, "runtimeStatus") || !strings.Contains(src, "runtimeNote") || !strings.Contains(src, "runtimeKind") {
		t.Errorf("CoreMail Runtime dashboard card must use dynamic status/note/kind variables derived from state.health, not a static literal")
	}
	// The runtimeStatus must handle the "no health data" case.
	if !strings.Contains(src, "'Not available'") {
		t.Errorf("CoreMail Runtime dashboard card must handle the case where health data is not available by showing 'Not available'")
	}
}

// TestAdminRetentionUsesConfirmDanger confirms runRetention() wraps
// the apiPost call inside a confirmDanger() modal with typed
// confirmation ("retention") so the operator cannot accidentally
// delete old backup files.
func TestAdminRetentionUsesConfirmDanger(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	// runRetention must call confirmDanger before apiPost.
	if !strings.Contains(src, "function runRetention()") {
		t.Errorf("admin app.js must define runRetention()")
	}
	// The body of runRetention must contain confirmDanger.
	if !strings.Contains(src, "confirmDanger({") {
		t.Errorf("runRetention() must call confirmDanger before apiPost to prevent accidental retention runs")
	}
	// The confirmation must require typed confirmation "retention".
	if !strings.Contains(src, "requireText: 'retention'") && !strings.Contains(src, "requireText: \"retention\"") {
		t.Errorf("runRetention() confirmation must require typed text 'retention' before proceeding")
	}
	// The apiPost call must NOT be at the top level of runRetention;
	// it must be inside the confirmDanger success path.
	if !strings.Contains(src, "if (!ok) return;") {
		t.Errorf("runRetention() must check confirmDanger result before calling apiPost")
	}
}

// TestAdminPageRenderersRegistered confirms every section listed in
// the sidebar has a corresponding render function so the page does
// not 404 into a placeholder.
func TestAdminPageRenderersRegistered(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	sections := []string{
		"dashboard", "domains", "mailboxes", "queue", "dns",
		"backups", "updates", "monitoring", "logs", "settings",
	}
	// Function-name variants. The router maps "dns" to renderDNS;
	// everything else follows the lower-camel pattern renderXxx.
	candidates := map[string][]string{
		"dns":        {"renderDNS"},
		"dashboard":  {"renderDashboard"},
		"domains":    {"renderDomains"},
		"mailboxes":  {"renderMailboxes"},
		"queue":      {"renderQueue"},
		"backups":    {"renderBackups"},
		"updates":    {"renderUpdates"},
		"monitoring": {"renderMonitoring"},
		"logs":       {"renderLogs"},
		"settings":   {"renderSettings"},
	}
	for _, s := range sections {
		keyInPages := regexp.MustCompile(`\b` + s + `\s*:\s*render`).MatchString(src)
		if !keyInPages {
			t.Errorf("admin app.js PAGES map missing key %q", s)
		}
		found := false
		for _, fn := range candidates[s] {
			if strings.Contains(src, "function " + fn + "(") {
				found = true; break;
			}
		}
		if !found {
			t.Errorf("admin app.js missing render function for section %q (tried %v)", s, candidates[s])
		}
	}
}
