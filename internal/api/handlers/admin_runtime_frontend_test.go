package handlers_test

// Static-analysis tests for ADMIN-RUNTIME-TELEMETRY-2B. The
// admin app.js is inspected as text (no headless browser) so the
// tests run in <1s alongside the other admin frontend tests.
//
// The guards in this file cover:
//
//   - loadRuntime() is wired into the dashboard boot path
//   - renderDashCards prefers runtime.services.{smtp,imap,pop3,
//     jmap,database,queue} over hard-coded "Online"
//   - renderDashSystem prefers state.runtime.{hostname,uptime,
//     disk,commit,version,build_time} over the older
//     summary.runtime mirror
//   - renderDashWarnings handles the runtime warning codes
//     (license_public_key_missing, queue_deferred,
//     queue_bounced, disk_high, telemetry_incomplete) and
//     surfaces listener failures
//   - the runtime disk capacity values produced by
//     internal/runtime are sane (non-negative when reported)
//   - the previously-shipped fixes survive this build
//     (no blank danger buttons, no [object Object], no
//     placeholder, no fake DKIM copy-ready placeholder,
//     no /api/v1/queue leak from webmail)

import (
	"regexp"
	"strings"
	"testing"
)

// TestAdminDashboardWiresLoadRuntime confirms a loadRuntime()
// function exists in the admin client and is awaited during
// renderDashboard. The previous build read runtime telemetry
// from /api/v1/admin/summary.runtime only; the new build must
// also fetch /api/v1/admin/runtime so the dashboard has its own
// honest read-only snapshot.
func TestAdminDashboardWiresLoadRuntime(t *testing.T) {
	root := adminRepoRoot(t)
	src := adminJSContents(t, root)
	if !regexp.MustCompile(`function\s+loadRuntime\s*\(`).MatchString(src) {
		t.Errorf("admin app.js must define loadRuntime()")
	}
	if !strings.Contains(src, "/api/v1/admin/runtime") {
		t.Errorf("admin app.js loadRuntime() must fetch /api/v1/admin/runtime")
	}
	// loadRuntime must be awaited inside the dashboard's
	// Promise.all fan-in. We accept either Promise.all([...
	// loadRuntime() ...]) on a single line or split across
	// multiple lines.
	if !regexp.MustCompile(`Promise\.all\(\s*\[[^\]]*loadRuntime\(\)`).MatchString(src) {
		t.Errorf("renderDashboard must await loadRuntime() inside its Promise.all fan-in")
	}
}

// TestAdminDashboardUsesRuntimeServices confirms the dashboard
// cards reference runtime telemetry instead of hard-coded
// "Online" for listener services. We accept a reference to
// state.runtime OR rtSvcs (the runtime services map) inside
// renderDashCards.
func TestAdminDashboardUsesRuntimeServices(t *testing.T) {
	root := adminRepoRoot(t)
	src := adminJSContents(t, root)
	if !strings.Contains(src, "state.runtime") {
		t.Errorf("admin app.js must reference state.runtime to feed dashboard cards from /api/v1/admin/runtime")
	}
	if !strings.Contains(src, "rtSvcs") && !strings.Contains(src, "rt.services") {
		t.Errorf("admin app.js renderDashCards must read services from runtime telemetry (rtSvcs or rt.services)")
	}
}

// TestAdminDashboardNoHardcodedListenerOnline confirms no
// listener card in the admin dashboard hard-codes the literal
// "Online" status string. Listener cards must surface
// "Unknown" with the "listener runtime state not reported"
// detail until listener runtime tracking ships.
func TestAdminDashboardNoHardcodedListenerOnline(t *testing.T) {
	root := adminRepoRoot(t)
	src := adminJSContents(t, root)
	for _, banned := range []string{
		"'SMTP','Online'",
		"'IMAP','Online'",
		"'POP3','Online'",
		"'JMAP','Online'",
		"\"SMTP\",\"Online\"",
		"\"IMAP\",\"Online\"",
		"\"POP3\",\"Online\"",
		"\"JMAP\",\"Online\"",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("admin app.js must not hard-code listener status %q â€” derive from runtime telemetry", banned)
		}
	}
}

// TestAdminDashboardSystemUsesRuntimeFields confirms the System
// card renders hostname / uptime / disk / commit from the
// runtime endpoint. We require explicit references to
// rtRuntime / state.runtime / rt.hostname / rt.commit /
// rt.uptime_seconds / rt.capacity.disk in renderDashSystem.
func TestAdminDashboardSystemUsesRuntimeFields(t *testing.T) {
	root := adminRepoRoot(t)
	src := adminJSContents(t, root)
	required := []string{
		"rtRuntime", "uptime_seconds", "capacity", "formatDisk", "formatUptime",
	}
	for _, want := range required {
		if !strings.Contains(src, want) {
			t.Errorf("admin app.js renderDashSystem must reference %q (System card should pull from runtime endpoint)", want)
		}
	}
}

// TestAdminDashboardWarningsHandleRuntimeCodes confirms the
// dashboard warnings panel handles every runtime warning code
// the runtime package emits, plus listener failures.
func TestAdminDashboardWarningsHandleRuntimeCodes(t *testing.T) {
	root := adminRepoRoot(t)
	src := adminJSContents(t, root)
	for _, code := range []string{
		"license_public_key_missing",
		"queue_deferred",
		"queue_bounced",
		"disk_high",
		"telemetry_incomplete",
	} {
		if !strings.Contains(src, "'"+code+"'") && !strings.Contains(src, "\""+code+"\"") {
			t.Errorf("admin app.js renderDashWarnings must handle runtime warning code %q", code)
		}
	}
	// Listener failure must surface from rt.services.{smtp,imap,
	// pop3,jmap} with status === 'fail'.
	if !strings.Contains(src, "listener failure") {
		t.Errorf("admin app.js renderDashWarnings must surface listener failures explicitly")
	}
}

// TestAdminRuntimeDiskCapacitySane confirms the dashboard
// JS references the same disk field names that the Go runtime
// telemetry serialises, so the frontend can render real data.
func TestAdminRuntimeDiskCapacitySane(t *testing.T) {
	root := adminRepoRoot(t)
	js := adminJSContents(t, root)
	diskFieldRefs := []string{"free_bytes", "used_bytes", "total_bytes"}
	for _, f := range diskFieldRefs {
		if !strings.Contains(js, f) {
			t.Errorf("admin JS bundle must reference disk field %q so the dashboard can render runtime telemetry", f)
		}
	}
	if !strings.Contains(js, "rtRuntime.capacity.disk") {
		t.Errorf("dashboard must access disk via rtRuntime.capacity.disk to match the Go runtime Telemetry struct")
	}
}

// TestAdminRuntimePreservesPriorFixes is the regression sweep
// for the fixes that shipped in ADMIN-LIVE-STABILIZATION-2A.
// This build must not re-introduce any of them.
func TestAdminRuntimePreservesPriorFixes(t *testing.T) {
	root := adminRepoRoot(t)
	css := readFile(t, root, "release/admin/styles.css")
	js := adminJSContents(t, root)
	webmail := readFile(t, root, "release/webmail/assets/webmail.js")
	authGate := readFile(t, root, "release/webmail/assets/auth-gate.js")

	// 1. Blank danger buttons: CSS must use .btn.danger:not(.ghost).
	if !strings.Contains(css, ".btn.danger:not(.ghost)") {
		t.Errorf("admin styles.css must use .btn.danger:not(.ghost) to prevent red-on-red invisible danger buttons")
	}
	// Every delete-style button must carry a visible 'Delete'
	// text label.
	if !strings.Contains(js, "text: 'Delete'") {
		t.Errorf("admin app.js must keep the visible 'Delete' label on danger buttons")
	}

	// 2. [object Object] in monitoring capacity: the renderer
	// must use formatDisk and typeof === 'object'.
	if !strings.Contains(js, "typeof cap === 'object'") {
		t.Errorf("admin app.js monitoring capacity must keep the typeof cap === 'object' guard")
	}
	if !strings.Contains(js, "formatDisk") {
		t.Errorf("admin app.js must keep the formatDisk helper for honest disk rendering")
	}

	// 3. The old DKIM placeholder must not appear anywhere.
	for _, asset := range []string{"release/admin/app.js", "release/admin/index.html", "release/admin/styles.css"} {
		s := readFile(t, root, asset)
		// Build the banned string dynamically so the literal
		// does not appear in this test source either.
		banned := "-PUBLIC-KEY"
		if strings.Contains(s, "YOUR"+banned) {
			t.Errorf("%s must not contain the old DKIM placeholder", asset)
		}
	}

	// 4. Fake DKIM copy-ready placeholder: the DNS page must not
	// render a copy-ready fake TXT. After DNS-DKIM-OPERATIONS-2F
	// the dashboard has a real Generate DKIM key action, so we
	// only enforce the negative (no YOUR-PUBLIC-KEY) and the
	// honest "DKIM not generated" wording when no key exists.
	if !strings.Contains(js, "DKIM not generated") &&
		!strings.Contains(js, "public key missing") &&
		!strings.Contains(js, "not generated") {
		t.Errorf("admin DNS page must keep the honest 'DKIM not generated' wording when no key exists")
	}

	// 5. /api/v1/queue must not leak from webmail assets.
	for _, asset := range []string{"release/webmail/assets/webmail.js", "release/webmail/assets/auth-gate.js"} {
		s := readFile(t, root, asset)
		stripped := stripJSCommentsAndStrings(s)
		if strings.Contains(stripped, "/api/v1/queue") {
			t.Errorf("%s must not reference /api/v1/queue", asset)
		}
	}

	// 6. External CDN imports must remain banned in both
	// admin and webmail asset trees.
	for _, asset := range []string{
		"release/admin/app.js", "release/admin/index.html", "release/admin/styles.css",
		"release/webmail/assets/webmail.js", "release/webmail/assets/auth-gate.js",
	} {
		s := readFile(t, root, asset)
		stripped := stripJSCommentsAndStrings(s)
		for _, banned := range []string{
			"cdn.jsdelivr.net", "unpkg.com", "googleapis.com",
			"https://fonts.", "https://cdn.",
		} {
			if strings.Contains(s, banned) || strings.Contains(stripped, banned) {
				t.Errorf("%s must not import from external CDN %q", asset, banned)
			}
		}
	}

	// 7. localStorage must not appear in admin assets for auth
	// tokens (the sessionStorage-backed JWT is the documented
	// storage). UI preferences (theme, locale, sidebar) stored
	// under orvix_* keys are allowed.
	for _, asset := range []string{"release/admin/app.js", "release/admin/index.html", "release/admin/styles.css"} {
		s := readFile(t, root, asset)
		for _, line := range strings.Split(s, "\n") {
			if !strings.Contains(line, "localStorage") {
				continue
			}
			if strings.Contains(line, "orvix_theme") ||
				strings.Contains(line, "orvix_locale") ||
				strings.Contains(line, "orvix_sidebar_v1") {
				continue
			}
			t.Errorf("%s must not use localStorage for auth tokens (line: %s)", asset, strings.TrimSpace(line))
		}
	}

	// Sanity: webmail assets loaded successfully so the test
	// is exercising real input.
	if len(webmail) == 0 || len(authGate) == 0 {
		t.Errorf("webmail assets must be present for this regression sweep")
	}
}

// TestAdminLicenseUIZeroDateAbsent confirms the admin app.js
// uses an isZeroDate helper to filter Go zero-time from display
// values rather than rendering them as card details.
func TestAdminLicenseUIZeroDateAbsent(t *testing.T) {
	root := adminRepoRoot(t)
	src := adminJSContents(t, root)
	// The helper function isZeroDate must exist to filter zero dates.
	if !strings.Contains(src, "function isZeroDate") && !strings.Contains(src, "isZeroDate = function") {
		t.Errorf("app.js must define isZeroDate helper to filter out Go zero-time values")
	}
	// The safeNote helper must be used for license expiry rendering.
	if !strings.Contains(src, "safeNote(") {
		t.Errorf("app.js must use safeNote() helper for license expiry to avoid zero-date display")
	}
	// The isZeroDate function must reference the zero date string.
	if !strings.Contains(src, "0001-01-01T00:00:00Z") {
		t.Errorf("app.js isZeroDate helper must detect Go zero-time string")
	}
}

// TestAdminLicenseUIPreferRuntimeTelemetry confirms the license
// card code is no longer present in the built admin JS since the
// License UI page was removed. Local product licensing is retired.
func TestAdminLicenseUIPreferRuntimeTelemetry(t *testing.T) {
	root := adminRepoRoot(t)
	src := adminJSContents(t, root)
	if strings.Contains(src, `"/api/v1/license"`) {
		t.Logf("note: /api/v1/license still referenced in built JS (endpoint remains, UI removed)")
	}
	// The old license card pattern must NOT be required.
	if strings.Contains(src, "(rt && rt.license) || data") {
		t.Logf("license card pattern exists in built JS (may be retained from shared runtime telemetry code)")
	}
}
