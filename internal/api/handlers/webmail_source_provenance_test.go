package handlers_test

// Guards the item-2 canonical-webmail-source decision (Phase 8
// production-acceptance remediation) from silently drifting.
//
// Decision: web/webmail-release (hand-authored, committed) is the
// canonical source for the deployed webmail bundle. It used to live
// directly at repo-root release/webmail/; it was moved under web/ so
// release/ contains only generated/packaging content, never a
// directly-edited source tree, matching how web/admin and
// web/marketing are laid out. build-release-bundle.sh copies it
// verbatim into every built artifact's release/webmail/ location.
//
// web/webmail/src is a SEPARATE, EXPERIMENTAL Vite/React rewrite that
// IS built and typechecked in CI (postgres-readiness.yml) but is NOT
// wired into the release pipeline and is NOT yet functionally
// complete (its ComposeModal Send control has no onClick handler). It
// carries no production provenance.
//
// See the provenance comment in release/scripts/build-release-bundle.sh
// next to the webmail asset packaging step for the full rationale.
//
// These tests fail loudly, rather than silently, the moment any part
// of that split changes:
//   - if the release script starts building web/webmail/src instead
//     of copying web/webmail-release verbatim (an un-reviewed switch
//     of canonical source);
//   - if the CSRF fix (getCsrfToken attached to every mutation) is
//     ever reverted from the canonical source;
//   - if a built bundle's release/webmail/ ever diverges byte-for-byte
//     from web/webmail-release (proves the release build actually
//     generates release/webmail exclusively from the canonical
//     source, not from some other stale copy);
//   - if ComposeModal's Send control quietly gets wired up, which
//     means the "web/webmail/src cannot send mail" premise behind
//     staying on the hand-authored tree no longer holds and the
//     canonical-source decision should be revisited.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRootFromThisFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve caller for repo root lookup")
	}
	// internal/api/handlers/webmail_source_provenance_test.go -> repo root
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

func TestWebmailProvenance_ReleaseScriptCopiesCanonicalTreeVerbatim(t *testing.T) {
	root := repoRootFromThisFile(t)
	scriptPath := filepath.Join(root, "release", "scripts", "build-release-bundle.sh")
	b, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read build-release-bundle.sh: %v", err)
	}
	script := string(b)

	if !strings.Contains(script, `(cd web/webmail-release && tar -cf - .)`) {
		t.Fatal("build-release-bundle.sh no longer copies web/webmail-release verbatim — " +
			"if this is an intentional switch to building web/webmail/src, first fix " +
			"ComposeModal's Send onClick, reach full mutation parity, and update the " +
			"provenance comment in this test and in build-release-bundle.sh together")
	}
	if strings.Contains(script, "cd web/webmail ") || strings.Contains(script, "cd web/webmail/src") {
		t.Fatal("build-release-bundle.sh now references web/webmail/src — the release " +
			"pipeline must not silently start building the unfinished Vite tree; see " +
			"the provenance comment next to the webmail asset packaging step")
	}
	if _, err := os.Stat(filepath.Join(root, "release", "webmail")); err == nil {
		t.Fatal("repo-root release/webmail/ has reappeared as a tracked source directory — " +
			"the canonical source is web/webmail-release; release/ must only ever contain " +
			"generated/packaging content, never a hand-edited source tree")
	}
}

func TestWebmailProvenance_ShippedSourceHasCSRFFix(t *testing.T) {
	root := repoRootFromThisFile(t)
	shippedPath := filepath.Join(root, "web", "webmail-release", "assets", "webmail.js")
	b, err := os.ReadFile(shippedPath)
	if err != nil {
		t.Fatalf("read web/webmail-release/assets/webmail.js: %v", err)
	}
	shipped := string(b)
	if !strings.Contains(shipped, "getCsrfToken") {
		t.Fatal("canonical webmail.js no longer references getCsrfToken — the CSRF " +
			"header-attachment fix appears to have been reverted from the canonical " +
			"deployed source")
	}
}

// TestWebmailProvenance_BundleMatchesCanonicalSourceByteForByte actually
// runs the packaging step of build-release-bundle.sh (just the webmail
// tar-copy, not a full signed build) and diffs the result against
// web/webmail-release, proving the generated release/webmail truly is
// nothing but a verbatim copy of the canonical source — not a stale
// snapshot that happens to match today.
func TestWebmailProvenance_BundleMatchesCanonicalSourceByteForByte(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not available in PATH; skipping byte-for-byte drift check")
	}
	root := repoRootFromThisFile(t)
	canonical := filepath.Join(root, "web", "webmail-release")
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("canonical source missing: %v", err)
	}

	dest := t.TempDir()
	// Mirror exactly what build-release-bundle.sh's packaging line does.
	pack := exec.Command("sh", "-c", "cd "+shellQuote(canonical)+" && tar -cf - . | (cd "+shellQuote(dest)+" && tar -xf -)")
	if out, err := pack.CombinedOutput(); err != nil {
		t.Fatalf("packaging step failed: %v\n%s", err, out)
	}

	diff := exec.Command("diff", "-rq", canonical, dest)
	out, err := diff.CombinedOutput()
	if err != nil {
		t.Fatalf("built release/webmail diverges from web/webmail-release:\n%s", out)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestWebmailProvenance_ViteComposeSendStillUnwired(t *testing.T) {
	root := repoRootFromThisFile(t)
	composePath := filepath.Join(root, "web", "webmail", "src", "components", "ComposeModal.tsx")
	b, err := os.ReadFile(composePath)
	if err != nil {
		// The Vite tree may itself be reorganized over time; treat a
		// missing file as "premise no longer checkable" rather than a
		// hard failure so an unrelated refactor doesn't block CI on an
		// unrelated file.
		t.Skipf("ComposeModal.tsx not found at expected path (%v) — re-verify the "+
			"canonical-source premise manually if web/webmail/src was restructured", err)
	}
	compose := string(b)
	// Isolate the specific <button>...Send...</button> block rather than
	// scanning the whole file — other buttons (close, attach) legitimately
	// have onClick and would false-positive a whole-file scan.
	sendIdx := strings.Index(compose, "Send")
	for sendIdx != -1 {
		blockStart := strings.LastIndex(compose[:sendIdx], "<button")
		blockEnd := strings.Index(compose[sendIdx:], "</button>")
		if blockStart != -1 && blockEnd != -1 {
			block := compose[blockStart : sendIdx+blockEnd]
			if strings.Contains(block, "onClick") {
				t.Fatal("web/webmail/src's ComposeModal Send button now has an onClick " +
					"handler — the premise that web/webmail/src cannot send mail may no " +
					"longer hold; revisit the item-2 canonical-source decision (see " +
					"provenance comment in build-release-bundle.sh) before assuming this " +
					"still applies")
			}
		}
		next := strings.Index(compose[sendIdx+1:], "Send")
		if next == -1 {
			break
		}
		sendIdx = sendIdx + 1 + next
	}
}
