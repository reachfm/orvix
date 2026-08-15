package releasebundle

// Tests for item 4 (Phase 8 production-acceptance remediation):
// release/upgrade.sh must reject an accidental version downgrade and
// permit one only through an explicit, audited --rollback operation.
//
// upgrade.sh is bash, not Go, so these tests extract its
// parse_semverish/version_compare/binary_version/
// enforce_version_monotonicity/record_version_audit function
// definitions into a standalone script (stubbing report/fail/log, the
// only outside symbols those functions call) and execute real bash
// against real fake "orvix" binaries that answer --version. This
// exercises the actual shipped logic, not a Go reimplementation of it
// that could silently drift from the real script.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRootForVersionTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve caller for repo root lookup")
	}
	// internal/releasebundle/version_monotonicity_test.go -> repo root
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// extractVersionFuncs pulls the version-monotonicity function block out
// of the real upgrade.sh (between parse_semverish() and
// install_and_restart()) into its own file, so tests run the exact
// shipped bash rather than a copy that can drift.
func extractVersionFuncs(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available; skipping upgrade.sh version-monotonicity test")
	}
	root := repoRootForVersionTest(t)
	src, err := os.ReadFile(filepath.Join(root, "release", "upgrade.sh"))
	if err != nil {
		t.Fatalf("read upgrade.sh: %v", err)
	}
	content := string(src)
	start := strings.Index(content, "parse_semverish() {")
	end := strings.Index(content, "install_and_restart() {")
	if start == -1 || end == -1 || end <= start {
		t.Fatal("could not locate parse_semverish()..install_and_restart() block in upgrade.sh — " +
			"has the function been renamed or moved? Update this test's markers to match.")
	}
	block := content[start:end]
	for _, want := range []string{"version_compare()", "binary_version()", "enforce_version_monotonicity()", "record_version_audit()"} {
		if !strings.Contains(block, want) {
			t.Fatalf("extracted block is missing %s — extraction markers may be stale", want)
		}
	}

	dir := t.TempDir()
	funcsPath := filepath.Join(dir, "version_funcs.sh")
	if err := os.WriteFile(funcsPath, []byte(block), 0755); err != nil {
		t.Fatalf("write extracted functions: %v", err)
	}
	return funcsPath
}

// writeFakeBinary writes a tiny shell script that answers `--version`
// with the given version token, mimicking `orvix --version`'s first
// line (buildinfo.Short(): "<version> commit: ... channel: ... ").
func writeFakeBinary(t *testing.T, dir, name, version string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"" + version + " commit: deadbeef channel: rc\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(p, []byte(script), 0755); err != nil {
		t.Fatalf("write fake binary %s: %v", name, err)
	}
	return p
}

// runEnforce sources the extracted functions plus test stubs for
// report/fail/log, sets the relevant globals, and calls
// enforce_version_monotonicity. Returns (exit code, combined output).
// fail() is stubbed to exit 9 with a recognizable marker so tests can
// distinguish "refused" from a real bash error.
func runEnforce(t *testing.T, funcsPath string, env map[string]string) (int, string) {
	t.Helper()
	script := `set -uo pipefail
source "` + funcsPath + `"
report() { echo "REPORT[$1]: $2"; }
fail() { echo "FAIL_CALLED: $1"; exit 9; }
log() { echo "LOG: $1"; }
enforce_version_monotonicity
echo "ENFORCE_RETURNED_ZERO"
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("running enforce_version_monotonicity: %v\n%s", err, out)
		}
	}
	return exitCode, string(out)
}

func TestUpgradeVersionMonotonicity_ForwardUpgradeAllowed(t *testing.T) {
	funcsPath := extractVersionFuncs(t)
	dir := t.TempDir()
	oldBin := writeFakeBinary(t, dir, "old", "1.0.4-rc2")
	newBin := writeFakeBinary(t, dir, "new", "1.0.5")
	auditLog := filepath.Join(dir, "audit.log")

	code, out := runEnforce(t, funcsPath, map[string]string{
		"ORVIX_BIN": oldBin, "NEW_BIN": newBin,
		"ROLLBACK": "0", "ROLLBACK_REASON": "", "DRY_RUN": "0",
		"VERSION_AUDIT_LOG": auditLog,
	})
	if code != 0 {
		t.Fatalf("expected exit 0 for a forward upgrade, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "ENFORCE_RETURNED_ZERO") {
		t.Fatalf("expected enforce_version_monotonicity to return normally:\n%s", out)
	}
	auditBytes, err := os.ReadFile(auditLog)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	audit := string(auditBytes)
	if !strings.Contains(audit, "kind=upgrade") || !strings.Contains(audit, "from=1.0.4-rc2") || !strings.Contains(audit, "to=1.0.5") {
		t.Fatalf("audit log missing expected upgrade record:\n%s", audit)
	}
}

func TestUpgradeVersionMonotonicity_DowngradeRefusedWithoutRollback(t *testing.T) {
	funcsPath := extractVersionFuncs(t)
	dir := t.TempDir()
	// The exact regression this item exists to catch: 1.0.4-rc2 -> 1.0.3-rc4.
	oldBin := writeFakeBinary(t, dir, "old", "1.0.4-rc2")
	newBin := writeFakeBinary(t, dir, "new", "1.0.3-rc4")
	auditLog := filepath.Join(dir, "audit.log")

	code, out := runEnforce(t, funcsPath, map[string]string{
		"ORVIX_BIN": oldBin, "NEW_BIN": newBin,
		"ROLLBACK": "0", "ROLLBACK_REASON": "", "DRY_RUN": "0",
		"VERSION_AUDIT_LOG": auditLog,
	})
	if code != 9 {
		t.Fatalf("expected fail() to be called (exit 9), got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "FAIL_CALLED") || !strings.Contains(out, "version regression refused") {
		t.Fatalf("expected a version-regression refusal, got:\n%s", out)
	}
	if _, err := os.Stat(auditLog); err == nil {
		t.Fatal("a refused downgrade must not write an audit entry")
	}
}

func TestUpgradeVersionMonotonicity_ExplicitRollbackAllowedAndAudited(t *testing.T) {
	funcsPath := extractVersionFuncs(t)
	dir := t.TempDir()
	oldBin := writeFakeBinary(t, dir, "old", "1.0.4-rc2")
	newBin := writeFakeBinary(t, dir, "new", "1.0.3-rc4")
	auditLog := filepath.Join(dir, "audit.log")

	code, out := runEnforce(t, funcsPath, map[string]string{
		"ORVIX_BIN": oldBin, "NEW_BIN": newBin,
		"ROLLBACK": "1", "ROLLBACK_REASON": "reverting a bad rc", "DRY_RUN": "0",
		"VERSION_AUDIT_LOG": auditLog,
	})
	if code != 0 {
		t.Fatalf("expected exit 0 for an explicit rollback, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "explicit rollback") {
		t.Fatalf("expected a rollback report line, got:\n%s", out)
	}
	auditBytes, err := os.ReadFile(auditLog)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	audit := string(auditBytes)
	if !strings.Contains(audit, "kind=rollback") ||
		!strings.Contains(audit, "from=1.0.4-rc2") ||
		!strings.Contains(audit, "to=1.0.3-rc4") ||
		!strings.Contains(audit, "reason=reverting a bad rc") {
		t.Fatalf("audit log missing expected rollback record:\n%s", audit)
	}
}

func TestUpgradeVersionMonotonicity_RollbackReasonRequired(t *testing.T) {
	// This is enforced in main()'s arg-parsing validation, not inside
	// enforce_version_monotonicity itself; assert the validation logic
	// directly by grepping for it, since main() isn't unit-testable in
	// isolation without a full root-required run.
	root := repoRootForVersionTest(t)
	src, err := os.ReadFile(filepath.Join(root, "release", "upgrade.sh"))
	if err != nil {
		t.Fatalf("read upgrade.sh: %v", err)
	}
	content := string(src)
	if !strings.Contains(content, `--rollback requires --rollback-reason`) {
		t.Fatal("upgrade.sh no longer validates that --rollback requires --rollback-reason")
	}
}

func TestUpgradeVersionMonotonicity_SkippedWhenNoPriorInstall(t *testing.T) {
	funcsPath := extractVersionFuncs(t)
	dir := t.TempDir()
	newBin := writeFakeBinary(t, dir, "new", "1.0.5")
	auditLog := filepath.Join(dir, "audit.log")

	code, out := runEnforce(t, funcsPath, map[string]string{
		"ORVIX_BIN": filepath.Join(dir, "does-not-exist"), "NEW_BIN": newBin,
		"ROLLBACK": "0", "ROLLBACK_REASON": "", "DRY_RUN": "0",
		"VERSION_AUDIT_LOG": auditLog,
	})
	if code != 0 {
		t.Fatalf("expected exit 0 (nothing to compare against on a fresh install), got %d:\n%s", code, out)
	}
	if _, err := os.Stat(auditLog); err == nil {
		t.Fatal("a skipped comparison (no prior binary) must not write an audit entry")
	}
}
