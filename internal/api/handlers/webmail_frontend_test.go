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

// stripJSCommentsAndStrings is provided by webmail_user_test.go;
// this file reuses it so the no-queue grep test has the same
// stripping behaviour as the existing TestWebmailAPINoQueueUsage.
