package handlers_test

// Guards the item-2 canonical-webmail-source decision (Phase 8
// production-acceptance remediation) from silently drifting.
//
// Decision: release/webmail/assets/webmail.js (hand-authored,
// committed) is the canonical, deployed webmail source. web/webmail/src
// is a parallel Vite/React rewrite that IS built and typechecked in CI
// (postgres-readiness.yml) but is NOT wired into the release pipeline
// and is NOT yet functionally complete (its ComposeModal Send control
// has no onClick handler). See the provenance comment in
// release/scripts/build-release-bundle.sh next to the webmail asset
// packaging step for the full rationale.
//
// These tests fail loudly, rather than silently, the moment either
// half of that split changes:
//   - if the release script starts building web/webmail/src instead
//     of copying release/webmail verbatim (an un-reviewed switch of
//     canonical source);
//   - if the CSRF fix (getCsrfToken attached to every mutation) is
//     ever reverted from the shipped file;
//   - if ComposeModal's Send control quietly gets wired up, which
//     means the "web/webmail/src cannot send mail" premise behind
//     staying on the hand-authored tree no longer holds and the
//     canonical-source decision should be revisited.

import (
	"os"
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

	if !strings.Contains(script, `(cd release/webmail && tar -cf - .)`) {
		t.Fatal("build-release-bundle.sh no longer copies release/webmail verbatim — " +
			"if this is an intentional switch to building web/webmail/src, first fix " +
			"ComposeModal's Send onClick, reach full mutation parity, and update the " +
			"provenance comment in this test and in build-release-bundle.sh together")
	}
	if strings.Contains(script, "cd web/webmail") {
		t.Fatal("build-release-bundle.sh now references web/webmail — the release " +
			"pipeline must not silently start building the unfinished Vite tree; see " +
			"the provenance comment next to the webmail asset packaging step")
	}
}

func TestWebmailProvenance_ShippedSourceHasCSRFFix(t *testing.T) {
	root := repoRootFromThisFile(t)
	shippedPath := filepath.Join(root, "release", "webmail", "assets", "webmail.js")
	b, err := os.ReadFile(shippedPath)
	if err != nil {
		t.Fatalf("read release/webmail/assets/webmail.js: %v", err)
	}
	shipped := string(b)
	if !strings.Contains(shipped, "getCsrfToken") {
		t.Fatal("shipped webmail.js no longer references getCsrfToken — the CSRF " +
			"header-attachment fix appears to have been reverted from the canonical " +
			"deployed source")
	}
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
