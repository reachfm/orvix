package handlers_test

// Guards the item-2 canonical-webmail-source architecture (Phase 8
// production-acceptance remediation) from silently drifting.
//
// Architecture: web/webmail-release is the canonical, ONLY-EDITABLE
// source. release/webmail is a GENERATED staging copy of it, always
// produced by the shared stage_webmail() function in
// release/scripts/lib-webmail-stage.sh, and — like release/admin's
// legacy fallback tree — committed to git rather than merely
// git-ignored build output: internal/config's WebmailUIDir test
// default and several router tests (internal/api/router_test.go)
// read release/webmail directly off a fixed relative path on disk and
// must not depend on a build step having run first in this checkout.
// A prior attempt at this remediation deleted release/webmail
// entirely on the theory that release/ should only ever contain
// generated content never checked in — that broke
// TestMarketingSPADeepLinksAndExistingUISurvive and the production
// router's default webmail path in CI (GET /webmail and
// GET /assets/webmail.js both 404'd). The fix is not "release/webmail
// must not exist" but "release/webmail must never be hand-edited
// directly — it must always be byte-identical to what stage_webmail()
// produces from web/webmail-release".
//
// web/webmail/src is a SEPARATE, EXPERIMENTAL Vite/React rewrite that
// IS built and typechecked in CI (postgres-readiness.yml) but is NOT
// wired into the release pipeline and is NOT yet functionally
// complete (its ComposeModal Send control has no onClick handler). It
// carries no production provenance.
//
// See the provenance comment in release/scripts/build-release-bundle.sh
// next to the webmail asset packaging step for the full rationale.

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

// TestWebmailProvenance_ReleaseWebmailExistsAndIsRuntimeReady proves the
// exact regression this test file was rewritten over: release/webmail
// must exist, checked in, with the files the production router and
// the direct repository tests expect — not conditionally present only
// after a build step runs.
func TestWebmailProvenance_ReleaseWebmailExistsAndIsRuntimeReady(t *testing.T) {
	root := repoRootFromThisFile(t)
	releaseWebmail := filepath.Join(root, "release", "webmail")
	for _, rel := range []string{"index.html", "sw.js", filepath.Join("assets", "webmail.js")} {
		p := filepath.Join(releaseWebmail, rel)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("release/webmail/%s missing: %v — the production router's default "+
				"WebmailUIDir and the router tests read this path directly off disk; it "+
				"must be a committed, always-present staged copy of web/webmail-release, "+
				"not something only present after a build step runs", rel, err)
		}
	}
}

// TestWebmailProvenance_StagedOutputByteIdenticalToCanonicalSource runs
// the real stage_webmail() function (not a reimplementation of it)
// against a temp destination and diffs it against the canonical
// source AND against the currently-committed release/webmail —
// proving both that staging is deterministic and that the committed
// copy is not stale relative to the canonical source.
func TestWebmailProvenance_StagedOutputByteIdenticalToCanonicalSource(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available; skipping staging script test")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not available; skipping staging script test")
	}
	root := repoRootFromThisFile(t)
	canonical := filepath.Join(root, "web", "webmail-release")
	committed := filepath.Join(root, "release", "webmail")
	stageScript := filepath.Join(root, "release", "scripts", "stage-webmail.sh")

	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("canonical source missing: %v", err)
	}
	if _, err := os.Stat(stageScript); err != nil {
		t.Fatalf("stage-webmail.sh missing: %v", err)
	}

	dest := t.TempDir()
	stageDest := filepath.Join(dest, "staged")
	// The script's default destination is release/webmail relative to
	// the passed repo root; to stage into an isolated temp dir instead
	// (so this test never mutates the working tree), invoke
	// lib-webmail-stage.sh's stage_webmail() directly.
	cmd := exec.Command("bash", "-c",
		"source "+shellQuote(filepath.Join(root, "release", "scripts", "lib-webmail-stage.sh"))+
			" && stage_webmail "+shellQuote(canonical)+" "+shellQuote(stageDest))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage_webmail failed: %v\n%s", err, out)
	}

	if out, err := exec.Command("diff", "-rq", canonical, stageDest).CombinedOutput(); err != nil {
		t.Fatalf("staged output diverges from canonical source:\n%s", out)
	}
	if out, err := exec.Command("diff", "-rq", canonical, committed).CombinedOutput(); err != nil {
		t.Fatalf("committed release/webmail diverges from web/webmail-release — it is "+
			"stale relative to the canonical source; run "+
			"'bash release/scripts/stage-webmail.sh' and commit the result:\n%s", out)
	}
}

// TestWebmailProvenance_StagingFailsClosedOnMissingSource proves
// stage_webmail() refuses to produce output from an incomplete
// source rather than silently shipping a partial/empty tree.
func TestWebmailProvenance_StagingFailsClosedOnMissingSource(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available; skipping staging script test")
	}
	root := repoRootFromThisFile(t)
	libScript := filepath.Join(root, "release", "scripts", "lib-webmail-stage.sh")

	emptySrc := t.TempDir()
	dest := t.TempDir()
	destPath := filepath.Join(dest, "out")

	cmd := exec.Command("bash", "-c",
		"source "+shellQuote(libScript)+" && stage_webmail "+shellQuote(emptySrc)+" "+shellQuote(destPath))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("stage_webmail must fail closed on a source missing index.html/assets/webmail.js, but it succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "index.html") && !strings.Contains(string(out), "webmail.js") {
		t.Fatalf("expected a clear missing-file error, got:\n%s", out)
	}
	if _, statErr := os.Stat(destPath); statErr == nil {
		t.Fatal("stage_webmail must not create a partial destination directory on failure")
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestWebmailProvenance_ReleaseScriptUsesSharedStagingFunction(t *testing.T) {
	root := repoRootFromThisFile(t)
	scriptPath := filepath.Join(root, "release", "scripts", "build-release-bundle.sh")
	b, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read build-release-bundle.sh: %v", err)
	}
	script := string(b)

	if !strings.Contains(script, "lib-webmail-stage.sh") {
		t.Fatal("build-release-bundle.sh no longer sources lib-webmail-stage.sh — the " +
			"release bundle and the committed release/webmail must be produced by the " +
			"exact same staging function, not two independently-maintained copy steps")
	}
	if !strings.Contains(script, "stage_webmail ") {
		t.Fatal("build-release-bundle.sh no longer calls stage_webmail() for the webmail " +
			"asset tree")
	}
	if strings.Contains(script, "cd web/webmail ") || strings.Contains(script, "cd web/webmail/src") {
		t.Fatal("build-release-bundle.sh now references web/webmail/src — the release " +
			"pipeline must not silently start building the unfinished Vite tree; see " +
			"the provenance comment next to the webmail asset packaging step")
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
