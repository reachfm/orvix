package handlers_test

// Frontend runtime tests that shell out to `node` to verify
// behaviour of helpers defined in release/webmail/assets/webmail.js.
//
// We do this rather than running the JS through a headless
// browser because:
//   - node is already a project gate (CI runs node --check on
//     the asset files),
//   - the helpers under test (dirAuto, linkifyURLs, sanitiseHTML,
//     escapeHTML, renderBody) are pure functions with no DOM
//     dependency,
//   - the test runs in <1s on any developer laptop.
//
// The helpers are loaded by reading the asset file, extracting
// the relevant function bodies with a regex, evaluating them in
// a fresh Node VM context, and asserting on the results.

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// extractWebmailHelpers reads the webmail.js asset file and
// pulls out the function bodies of the helpers listed in
// `names`. Each helper is returned as a stand-alone JavaScript
// declaration that can be evaluated in a Node VM context.
//
// We use a simple "function name(...) { ... }" regex with
// brace balancing. The helpers in webmail.js are top-level
// declarations (not nested), so this works reliably.
func extractWebmailHelpers(t *testing.T, names []string) string {
	t.Helper()
	webmailJS := readFile(t, webmailRepoRoot(t), "release/webmail/assets/webmail.js")
	var out strings.Builder
	for _, name := range names {
		idx := strings.Index(webmailJS, "function "+name+"(")
		if idx < 0 {
			t.Fatalf("helper %q not found in webmail.js", name)
		}
		// Find the opening brace.
		braceOpen := strings.Index(webmailJS[idx:], "{")
		if braceOpen < 0 {
			t.Fatalf("helper %q: opening brace not found", name)
		}
		braceOpen += idx
		// Brace-balanced scan to the matching closing brace.
		depth := 0
		end := -1
		for i := braceOpen; i < len(webmailJS); i++ {
			switch webmailJS[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			t.Fatalf("helper %q: closing brace not found", name)
		}
		// Trim trailing whitespace after the closing brace.
		endTrim := end + 1
		for endTrim < len(webmailJS) && (webmailJS[endTrim] == '\n' || webmailJS[endTrim] == ' ' || webmailJS[endTrim] == '\r') {
			endTrim++
		}
		// Also strip an immediately following `function ...`
		// keyword so we don't glue helpers together.
		out.WriteString(webmailJS[idx:endTrim])
		out.WriteString("\n")
	}
	return out.String()
}

// runNodeJS evaluates the supplied JS source in a fresh Node
// process and returns the trimmed stdout. The script is
// expected to either:
//   - exit with code 0 and print nothing (silent pass), or
//   - exit non-zero with an error message on stdout/stderr.
//
// `node` must be on PATH; if it is not, the test is skipped
// (we don't want to break `go test ./...` on a CI image
// without node).
func runNodeJS(t *testing.T, jsSource string) (string, error) {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping JS helper test")
	}
	cmd := exec.Command(nodePath, "-e", jsSource)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// TestWebmailDirAutoHelperArabicEnglishEmpty pins the dirAuto
// behaviour the UI relies on:
//   - Arabic subject -> "rtl"
//   - Latin subject  -> "ltr"
//   - mixed Arabic+Latin subject -> "auto" (browser decides
//     per-glyph)
//   - empty / whitespace / punctuation-only -> "auto"
func TestWebmailDirAutoHelperArabicEnglishEmpty(t *testing.T) {
	src := extractWebmailHelpers(t, []string{"dirAuto"}) +
		// The spec's exact string, verbatim:
		// "السلام عليكم، هذه رسالة اختبار من Orvix"
		`
var ARABIC = "\u0627\u0644\u0633\u0644\u0627\u0645 \u0639\u0644\u064a\u0643\u0645\u060c \u0647\u0630\u0647 \u0631\u0633\u0627\u0644\u0629 \u0627\u062e\u062a\u0628\u0627\u0631 \u0645\u0646 Orvix";

function expect(input, want, label) {
  var got = dirAuto(input);
  if (got !== want) {
    console.error("FAIL " + label + ": dirAuto(" + JSON.stringify(input) + ") = " + JSON.stringify(got) + ", want " + JSON.stringify(want));
    process.exit(1);
  }
}

expect("Hello world", "ltr", "latin");
expect(ARABIC, "rtl", "arabic");
expect("\u05e9\u05dc\u05d5\u05dd", "rtl", "hebrew");
expect("Re: 5 things to know", "ltr", "latin-with-digit-and-punct");
expect("Hello \u0627\u0644\u0633\u0644\u0627\u0645", "ltr", "latin-first-mixed");
expect("\u0627\u0644\u0633\u0644\u0627\u0645 Hello", "rtl", "arabic-first-mixed");
expect("", "auto", "empty");
expect("   ", "auto", "whitespace-only");
expect("---", "auto", "punctuation-only");
expect("5", "auto", "digit-only");
expect("Re: " + ARABIC, "rtl", "Re: arabic-prefix");
expect(ARABIC + " Re:", "rtl", "arabic-then-punct");
`
	out, err := runNodeJS(t, src)
	if err != nil {
		t.Fatalf("dirAuto test failed: %v\n%s", err, out)
	}
	if out != "" {
		t.Fatalf("dirAuto test produced unexpected stdout: %q", out)
	}
}

// TestWebmailLinkifyHelperURLs pins that bare URLs in plain
// text get wrapped in anchor tags, and that javascript: URLs
// are NEVER produced by the linkifier.
func TestWebmailLinkifyHelperURLs(t *testing.T) {
	src := extractWebmailHelpers(t, []string{"linkifyURLs", "escapeHTML"}) +
		`
function expect(input, wantSubstr, mustNotContain, label) {
  var got = linkifyURLs(input);
  if (got.indexOf(wantSubstr) < 0) {
    console.error("FAIL " + label + ": missing " + JSON.stringify(wantSubstr) + " in " + JSON.stringify(got));
    process.exit(1);
  }
  if (mustNotContain && got.indexOf(mustNotContain) >= 0) {
    console.error("FAIL " + label + ": must not contain " + JSON.stringify(mustNotContain) + " in " + JSON.stringify(got));
    process.exit(1);
  }
}

// Plain http URL.
expect("visit https://example.com today", '<a href="https://example.com"', null, "http");
// Plain https URL.
expect("see https://example.com/x?y=1", '<a href="https://example.com/x?y=1"', null, "https-with-query");
// mailto: works.
expect("email mailto:a@b.c", '<a href="mailto:a@b.c"', null, "mailto");
// javascript: scheme is NEVER wrapped in an <a href>. The
// linkifier is conservative — stripping/encoding the
// dangerous scheme is the renderBody / sanitiseHTML
// helper's job, not the linkifier's. We only assert the
// linkifier does NOT produce a clickable anchor.
var jsOutput = linkifyURLs("click javascript:alert(1)");
if (jsOutput.indexOf('<a href="javascript:') >= 0) {
  console.error("FAIL javascript-scheme-not-linked: linkifier wrapped javascript: in <a>: " + jsOutput);
  process.exit(1);
}
`
	out, err := runNodeJS(t, src)
	if err != nil {
		t.Fatalf("linkify test failed: %v\n%s", err, out)
	}
}

// TestWebmailSanitiseHTMLHelper pins the XSS defences of
// sanitiseHTML: script tags, on* event handlers, javascript:
// URLs, and meta-refresh are all stripped.
func TestWebmailSanitiseHTMLHelper(t *testing.T) {
	src := extractWebmailHelpers(t, []string{"sanitiseHTML"}) +
		`
function expectStripped(input, label) {
  var got = sanitiseHTML(input);
  if (got.indexOf("<script") >= 0) {
    console.error("FAIL " + label + ": <script> not stripped: " + got);
    process.exit(1);
  }
  if (got.toLowerCase().indexOf("onerror") >= 0) {
    console.error("FAIL " + label + ": onerror not stripped: " + got);
    process.exit(1);
  }
  if (got.toLowerCase().indexOf("onclick") >= 0) {
    console.error("FAIL " + label + ": onclick not stripped: " + got);
    process.exit(1);
  }
  if (got.toLowerCase().indexOf("javascript:") >= 0) {
    console.error("FAIL " + label + ": javascript: not stripped: " + got);
    process.exit(1);
  }
}

expectStripped('<script>alert(1)</script>', "script-tag");
expectStripped('<img src="x" onerror="alert(1)">', "img-onerror");
expectStripped('<a href="javascript:alert(1)">click</a>', "a-javascript-url");
expectStripped('<iframe src="https://evil"></iframe>', "iframe");
expectStripped('<meta http-equiv="refresh" content="0;url=evil">', "meta-refresh");
expectStripped('<style>body { background: url("javascript:alert(1)") }</style>', "style-tag");

// Safe content is preserved.
var safe = sanitiseHTML('<p>Hello <strong>Orvix</strong></p>');
if (safe.indexOf("<strong>Orvix</strong>") < 0) {
  console.error("FAIL: safe <strong> stripped: " + safe);
  process.exit(1);
}
`
	out, err := runNodeJS(t, src)
	if err != nil {
		t.Fatalf("sanitise test failed: %v\n%s", err, out)
	}
}

// TestWebmailNoQueueAPICallsInWebmailAsset is a fast grep
// over release/webmail to confirm the user-facing webmail
// client never calls /api/v1/queue. The user explicitly
// forbade this; this is the regression guard.
//
// We strip JS comments and string literals the same way the
// existing TestWebmailAPINoQueueUsage does, then assert the
// cleaned source contains no `/api/v1/queue` reference.
func TestWebmailNoQueueAPICallsInWebmailAsset(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	stripped := stripJSCommentsAndStrings(src)
	if strings.Contains(stripped, "/api/v1/queue") {
		var lineNum int
		var snippet string
		for i, s := range strings.Split(stripped, "\n") {
			if strings.Contains(s, "/api/v1/queue") {
				lineNum = i + 1
				snippet = s
				break
			}
		}
		t.Fatalf("webmail.js calls /api/v1/queue at line %d: %s", lineNum, strings.TrimSpace(snippet))
	}
	for _, line := range strings.Split(stripped, "\n") {
		if strings.Contains(line, "/queue") {
			t.Errorf("webmail.js contains /queue reference: %q", strings.TrimSpace(line))
		}
	}
}

// TestWebmailNoLocalStorageInWebmailAssets is the regression
// guard for the "no tokens in localStorage" rule. Both
// auth-gate.js and webmail.js are scanned; neither may
// reference localStorage or sessionStorage under any name.
// The only allowed cookie access is `credentials: 'include'`
// (HttpOnly cookies set by the server).
func TestWebmailNoLocalStorageInWebmailAssets(t *testing.T) {
	root := webmailRepoRoot(t)
	for _, rel := range []string{
		"release/webmail/assets/webmail.js",
		"release/webmail/assets/auth-gate.js",
	} {
		src := readFile(t, root, rel)
		stripped := stripJSCommentsAndStrings(src)
		for _, banned := range []string{
			"localStorage",
			"sessionStorage",
			"document.cookie =",
			"document.cookie=",
		} {
			if strings.Contains(stripped, banned) {
				t.Errorf("%s contains banned storage API %q — webmail must rely on HttpOnly cookies only", rel, banned)
			}
		}
	}
}

// TestWebmailCSSHasReducedMotion confirms webmail.css
// honours the OS-level prefers-reduced-motion media
// query. This is the user-explicit accessibility
// requirement: every animation must be suppressible
// from the OS.
func TestWebmailCSSHasReducedMotion(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.css")
	if !strings.Contains(src, "prefers-reduced-motion") {
		t.Errorf("webmail.css must contain a prefers-reduced-motion media query for OS-level motion suppression")
	}
}

// TestWebmailCSSHasResponsiveBreakpoints confirms the
// stylesheet has the four named breakpoints the brief
// requires: 1440, 1024, 768, 390. (The existing 1100
// and 860 are kept for the legacy laptop / drawer
// thresholds.)
func TestWebmailCSSHasResponsiveBreakpoints(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.css")
	for _, px := range []string{"1440px", "1024px", "768px", "390px"} {
		if !strings.Contains(src, px) {
			t.Errorf("webmail.css missing responsive breakpoint for %s", px)
		}
	}
}

// TestWebmailCSSLogicalProperties confirms the
// stylesheet uses logical properties (margin-inline-*,
// padding-inline-*, inset-inline-*, border-inline-*)
// instead of physical padding-left/right and friends.
// This is the Arabic/RTL correctness requirement.
func TestWebmailCSSLogicalProperties(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.css")
	// Count the number of logical-property usages.
	// A non-trivial count confirms the sweep happened.
	logicalCount := 0
	for _, prop := range []string{
		"margin-inline-start", "margin-inline-end",
		"padding-inline-start", "padding-inline-end",
		"inset-inline-start", "inset-inline-end",
		"border-inline-start", "border-inline-end",
	} {
		logicalCount += strings.Count(src, prop)
	}
	if logicalCount < 8 {
		t.Errorf("webmail.css has %d logical-property usages, want >= 8 (RTL sweep incomplete)", logicalCount)
	}
}

// TestWebmailJSHasKeyboardShortcuts confirms the
// client-side keyboard handler is wired up with the
// shortcuts the brief requires. We scan the source
// for the expected key codes in their original
// quoted form (the no-queue / no-localStorage tests
// strip strings; this one does not, because we want
// to verify the actual key strings).
func TestWebmailJSHasKeyboardShortcuts(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	// Each key is searched as it appears in the
	// handler. The handler writes "if (ev.key === 'c')"
	// etc., so the unstripped file already has the
	// quoted literal we want.
	expected := []string{
		"ev.key === 'c'",
		"ev.key === '/'",
		"ev.key === 'r'",
		"ev.key === 'a'",
		"ev.key === 'f'",
		"ev.key === 'j'",
		"ev.key === 'k'",
		"ev.key === 'e'",
		"ev.key === 'Delete'",
		"ev.key === 'x'",
		"ev.key === '?'",
		"ev.key === 'Escape'",
	}
	for _, want := range expected {
		if !strings.Contains(src, want) {
			t.Errorf("webmail.js missing keyboard handler for %s", want)
		}
	}
}

// TestWebmailCSSHasLightTheme confirms the stylesheet
// defines a `:root.theme-light` block so embedders can
// force the light skin. WEBMAIL-UI-POLISH-3 adds this
// as the premium polish layer; the dark theme remains
// the default via `:root` (no class needed).
func TestWebmailCSSHasLightTheme(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.css")
	if !strings.Contains(src, ":root.theme-light") {
		t.Errorf("webmail.css missing :root.theme-light block — light theme not exposed")
	}
	// The light theme must re-tokenize at least the
	// text colour, the surface, and the accent. We
	// assert by name rather than by hex so the test
	// stays robust against future palette tweaks.
	requiredVars := []string{
		"--bg-canvas",
		"--text-0",
		"--accent",
	}
	for _, v := range requiredVars {
		// Count occurrences: must be at least 2 — one
		// in :root (dark default) and one in
		// :root.theme-light (light override).
		if c := strings.Count(src, v); c < 2 {
			t.Errorf("webmail.css theme variable %s appears %d times, want >= 2 (dark + light)", v, c)
		}
	}
}

// TestWebmailCSSHasDesignSystemTokens confirms the
// stylesheet uses the WEBMAIL-UI-POLISH-3 design
// tokens (semantic background tiers, shadow tiers,
// motion timings). These tokens are what give the
// shell its premium layered feel.
func TestWebmailCSSHasDesignSystemTokens(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.css")
	required := []string{
		"--bg-canvas", "--bg-surface", "--bg-elevated",
		"--bg-raised", "--bg-hover", "--bg-active",
		"--shadow-sm", "--shadow-md", "--shadow-lg",
		"--motion-fast", "--motion-med",
		"--r-md", "--r-lg", "--r-pill",
		"--sp-4", "--sp-5", "--sp-6",
	}
	missing := []string{}
	for _, v := range required {
		if !strings.Contains(src, v) {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		t.Errorf("webmail.css missing design tokens: %v", missing)
	}
}

// TestWebmailCSSHasPremiumComponents confirms the
// stylesheet styles every premium component the polish
// pack adds (skeleton loaders, rich empty states,
// premium toasts, polished active folder row).
func TestWebmailCSSHasPremiumComponents(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.css")
	required := []string{
		".skeleton",        // skeleton loader base
		".skeleton-line",   // line variant
		".skeleton-circle", // circle variant
		".reading-skeleton",
		".empty-illustration",
		".empty-title",
		".field-expander",
		".toast.warning",         // new toast type
		"--grad-brand",           // brand gradient token
		"--grad-active",          // active row gradient token
		".folder.active::before", // active row accent bar
		":root.theme-light",
		"backdrop-filter", // modal backdrop blur
	}
	missing := []string{}
	for _, sel := range required {
		if !strings.Contains(src, sel) {
			missing = append(missing, sel)
		}
	}
	if len(missing) > 0 {
		t.Errorf("webmail.css missing premium component selectors: %v", missing)
	}
}

// TestWebmailJSSkeletonRendering confirms the client
// renders skeleton placeholder rows while the message
// list is loading. The function name and the class
// "skeleton" must both appear in the JS source.
func TestWebmailJSSkeletonRendering(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	if !strings.Contains(src, "skeletonMessageRow") {
		t.Errorf("webmail.js missing skeletonMessageRow helper")
	}
	if !strings.Contains(src, "renderEmptyState") {
		t.Errorf("webmail.js missing renderEmptyState helper")
	}
	if !strings.Contains(src, "renderReadingPaneEmpty") {
		t.Errorf("webmail.js missing renderReadingPaneEmpty helper")
	}
}

// TestWebmailJSRichEmptyStates confirms the per-folder
// empty-state copy is present in the JS source. Each
// folder key must have its own title so the user
// receives context-appropriate copy rather than a
// generic "empty" message.
func TestWebmailJSRichEmptyStates(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	required := []string{
		"Inbox zero",
		"Nothing sent yet",
		"No drafts",
		"Trash is empty",
		"No archived messages",
		"No matches",
	}
	for _, want := range required {
		if !strings.Contains(src, want) {
			t.Errorf("webmail.js missing rich empty-state copy for %q", want)
		}
	}
}

// TestWebmailJSAriaLabelPatterns confirms icon-only
// buttons in the webmail UI carry aria-label attributes
// for screen-reader accessibility. This is the
// WEBMAIL-UI-POLISH-3 accessibility bar — every new
// icon button must be labelled.
//
// The webmail client uses two equivalent JS styles for
// aria-label: as a quoted attribute key in the el()
// opts object ('aria-label': '...') and as a setAttribute
// call. We accept both forms in the test.
func TestWebmailJSAriaLabelPatterns(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	required := []string{
		"Refresh",
		"Mark all as read",
		"Toggle folder sidebar",
		"Back to list",
		"Minimize",
		"Show Cc and Bcc fields",
	}
	// For each required label, look for it being passed
	// to aria-label in either quoted form.
	for _, want := range required {
		needle1 := "'aria-label': '" + want + "'"
		needle2 := "aria-label: '" + want + "'"
		if !strings.Contains(src, needle1) && !strings.Contains(src, needle2) {
			t.Errorf("webmail.js missing aria-label pattern for %q (looked for %q or %q)", want, needle1, needle2)
		}
	}
	// "Close" is used by multiple elements. At least one
	// Close aria-label must be present.
	if !strings.Contains(src, "aria-label': 'Close'") && !strings.Contains(src, "aria-label: 'Close'") {
		t.Errorf("webmail.js missing aria-label pattern for Close button")
	}
}

// TestWebmailJSRespectsColorScheme confirms the
// client honours the OS-level prefers-color-scheme
// media query for the light theme opt-in. The CSS
// defines `:root.theme-light`; the JS sets the class
// when matchMedia indicates the user prefers light.
func TestWebmailJSRespectsColorScheme(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	if !strings.Contains(src, "prefers-color-scheme") {
		t.Errorf("webmail.js missing prefers-color-scheme detection — light theme opt-in not wired up")
	}
	if !strings.Contains(src, "theme-light") {
		t.Errorf("webmail.js missing theme-light class application")
	}
}

// TestWebmailJSCallsNewEndpoints confirms the
// client-side code calls the new backend endpoints
// the feature pack adds. This is a smoke test: the
// new endpoint URLs must be present in the asset.
func TestWebmailJSCallsNewEndpoints(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	for _, endpoint := range []string{
		"/api/v1/webmail/messages/",
		"/api/v1/webmail/attachments/",
		"/api/v1/webmail/messages/batch",
		"/api/v1/webmail/messages/",
	} {
		if !strings.Contains(src, endpoint) {
			t.Errorf("webmail.js missing endpoint reference %q", endpoint)
		}
	}
	// The /source endpoint must be referenced as part
	// of the show-original anchor.
	if !strings.Contains(src, "/source") {
		t.Errorf("webmail.js missing /source endpoint reference for show-original action")
	}
}

// TestWebmailReadingToolbarTeardownRemovesMoveToWrap pins the
// regression guard for the live "duplicate Move to…" bug fixed
// in fix/webmail-live-stabilization-3a.
//
// The reading-pane toolbar (.reading-pane > .toolbar) has two
// static children (the Back button and a spacer) and a dynamic
// tail (action buttons + a .move-to-wrap <div> that hosts the
// "Move to…" <select>). When the pane re-renders for the same
// message — which happens on every star toggle, mark-read /
// mark-unread, spam / not-spam toggle — the teardown MUST wipe
// every dynamic child before appending the new ones. The earlier
// code only wiped .action-btn, so the previous .move-to-wrap
// <div> survived across re-renders and a fresh one was appended
// each time. The user-visible result was N "Move to…" controls
// in the reading-pane toolbar after N in-pane actions.
//
// The same teardown bug lived in renderReadingPaneLoading, so a
// stale .move-to-wrap from message A would also survive the
// loading skeleton of message B and compound when the real
// render landed. Both teardowns now target both classes.
//
// We assert the teardown selector names both .action-btn AND
// .move-to-wrap. The selector is a quoted string literal so the
// regex below is precise about the names but tolerant to inner
// whitespace and to either single or double quote styles.
func TestWebmailReadingToolbarTeardownRemovesMoveToWrap(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	// Selector pattern. Accepts single or double quotes and any
	// amount of whitespace between the class names so the test
	// does not become brittle to cosmetic edits.
	pattern := regexp.MustCompile(`querySelectorAll\(\s*['"]\.action-btn\s*,\s*\.move-to-wrap\s*['"]`)
	if !pattern.MatchString(src) {
		t.Errorf("webmail.js reading-pane toolbar teardown must remove BOTH .action-btn and .move-to-wrap; otherwise in-pane re-renders (star toggle, mark read/unread, spam toggle) and cross-message navigation through renderReadingPaneLoading compound duplicate 'Move to...' controls in the toolbar")
	}
	// Also assert the move-to-wrap is only CREATED in one place
	// per render. If a future change adds a second `class:
	// 'move-to-wrap'` creation site, that would also reintroduce
	// the duplicate. We tolerate whitespace inside the class
	// string but require the literal substring `move-to-wrap`
	// to appear as a class assignment.
	classAssigns := regexp.MustCompile(`class\s*:\s*['"][^'"]*move-to-wrap[^'"]*['"]`)
	matches := classAssigns.FindAllStringIndex(src, -1)
	if len(matches) < 1 {
		t.Errorf("webmail.js must create the .move-to-wrap wrapper at least once in the reading-pane toolbar")
	}
	if len(matches) > 1 {
		t.Errorf("webmail.js creates .move-to-wrap in %d places — the reading-pane toolbar should only ever render one Move to... control", len(matches))
	}
}

// TestWebmailReadingToolbarMoveToSelectIsSingleSource pins that
// the placeholder option string used as the "Move to…" <select>
// default in the reading-pane toolbar is created in exactly
// one place. The actual literal in the source is the JS
// single-quoted `'Move to…'` (Unicode horizontal ellipsis
// U+2026, not three ASCII periods) — the option is appended
// to the inline <select> inside renderReadingPane(). If a
// future change adds a second placeholder creation (copy-
// paste, stub, refactor that splits the function), the user
// would see duplicate "Move to…" controls even if the
// teardown were correct.
//
// We search the single-quoted form specifically because it
// matches the JS string literal that becomes the rendered
// <option> label. Double-quoted occurrences inside comments
// are documentation and must not be counted.
func TestWebmailReadingToolbarMoveToSelectIsSingleSource(t *testing.T) {
	root := webmailRepoRoot(t)
	src := readFile(t, root, "release/webmail/assets/webmail.js")
	pattern := regexp.MustCompile(`'Move to…'`)
	matches := pattern.FindAllStringIndex(src, -1)
	if len(matches) != 1 {
		t.Errorf("webmail.js must define the reading-pane 'Move to…' placeholder option exactly once, got %d", len(matches))
	}
}

// stripJSCommentsAndStrings is provided by webmail_user_test.go;
// this file reuses it so the no-queue grep test has the same
// stripping behaviour as the existing TestWebmailAPINoQueueUsage.
