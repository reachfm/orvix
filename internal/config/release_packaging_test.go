// Package config — release-packaging tests.
//
// These tests pin the install-time and upgrade-time safety
// properties of the release artifacts. They are deliberately
// cross-cutting: they do not test config parsing, they test
// the SHELL scripts and systemd units that the production
// installer depends on. Each test corresponds to one finding
// in docs/PRODUCTION_READINESS_GATE.md §7.
package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bashPath returns the bash interpreter to use for `bash -n`
// syntax checks. On Linux this is `bash`; on Windows we look
// up Git Bash first (used by CI on Windows runners), falling
// back to whatever `bash` is on PATH.
func bashPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "bash"
	}
	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "bash"
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// TestReleaseInstallScriptsParse runs `bash -n` over every
// release shell script. Catches the LF/CRLF regression and
// any plain syntax error introduced by refactors.
func TestReleaseInstallScriptsParse(t *testing.T) {
	root := repoRoot(t)
	bash := bashPath(t)
	scripts := []string{
		"release/install.sh",
		"release/upgrade.sh",
		"release/uninstall.sh",
		"release/scripts/setup-https.sh",
		"release/scripts/healthcheck.sh",
		"release/scripts/diagnostics.sh",
		"release/scripts/reset-admin-password.sh",
		"release/scripts/generate-vapid-keys.sh",
		"release/scripts/setup-smtp-tls.sh",
		"release/scripts/check-smtp-tls.sh",
		"release/scripts/apply-runtime-update.sh",
		"release/scripts/smoke-health.sh",
		"release/scripts/smoke-runtime.sh",
		"release/scripts/smoke-upgrade.sh",
		"release/scripts/smoke-ports.sh",
	}
	for _, s := range scripts {
		full := filepath.Join(root, s)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("missing script: %s", s)
			continue
		}
		cmd := exec.Command(bash, "-n", full)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("%s: bash -n failed: %v\n%s", s, err, out)
		}
	}
}

// TestReleaseInstallScriptsUseLF enforces that release shell
// scripts are committed with LF line endings, not CRLF. This
// is a regression guard: CRLF breaks `bash -n` on Ubuntu and
// has slipped in twice already via the Windows editor.
func TestReleaseInstallScriptsUseLF(t *testing.T) {
	root := repoRoot(t)
	scripts := []string{
		"release/install.sh",
		"release/upgrade.sh",
		"release/uninstall.sh",
		"release/scripts/setup-https.sh",
		"release/scripts/healthcheck.sh",
		"release/scripts/diagnostics.sh",
		"release/scripts/smoke-health.sh",
		"release/scripts/smoke-runtime.sh",
		"release/scripts/smoke-upgrade.sh",
		"release/scripts/smoke-ports.sh",
	}
	for _, s := range scripts {
		data, err := os.ReadFile(filepath.Join(root, s))
		if err != nil {
			t.Errorf("read %s: %v", s, err)
			continue
		}
		if strings.Contains(string(data), "\r\n") {
			t.Errorf("%s contains CRLF line endings (must be LF)", s)
		}
	}
}

// TestInstallScriptInstallsApplyRuntimeUpdate pins that
// release/install.sh copies apply-runtime-update.sh to
// /usr/share/orvix/scripts/. Without this copy, the
// orvix-update.service ExecStart resolves to a path that
// does not exist on a fresh VPS install.
func TestInstallScriptInstallsApplyRuntimeUpdate(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "install.sh"))
	for _, needle := range []string{
		"/usr/share/orvix/scripts/apply-runtime-update.sh",
		"$ORVIX_SOURCE_DIR/release/scripts/apply-runtime-update.sh",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("install.sh missing install-time copy: %q", needle)
		}
	}
}

// TestOrvixUpdateServiceExecPath pins the production-readiness
// gate finding #1: the unit's ExecStart must resolve to a path
// the installer copies into place, not /opt/orvix/...
func TestOrvixUpdateServiceExecPath(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "systemd", "orvix-update.service"))
	var execStart string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ExecStart=") {
			execStart = trimmed
			break
		}
	}
	if execStart == "" {
		t.Fatal("orvix-update.service has no ExecStart line")
	}
	if strings.Contains(execStart, "/opt/orvix/release/scripts/apply-runtime-update.sh") {
		t.Errorf("orvix-update.service ExecStart still references /opt/orvix/... (path never copied by install.sh): %s", execStart)
	}
	if !strings.Contains(execStart, "/usr/share/orvix/scripts/apply-runtime-update.sh") {
		t.Errorf("orvix-update.service ExecStart must contain /usr/share/orvix/scripts/apply-runtime-update.sh, got: %s", execStart)
	}
}

// TestOrvixServiceRequiresRedis pins that the main service
// declares redis-server.service as a HARD dependency (Requires=),
// not just Wants=. Without Requires=, orvix may boot without
// Redis and only fail on the first request.
func TestOrvixServiceRequiresRedis(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "systemd", "orvix.service"))
	if !strings.Contains(body, "Requires=redis-server.service") {
		t.Error("orvix.service must declare Requires=redis-server.service (hard dep)")
	}
	if !strings.Contains(body, "RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX") {
		t.Error("orvix.service must declare RestrictAddressFamilies for hardening")
	}
	if !strings.Contains(body, "MemoryDenyWriteExecute=true") {
		t.Error("orvix.service must enable MemoryDenyWriteExecute hardening")
	}
}

// TestSetupHttpsOpensMailPorts pins that setup-https.sh
// opens the mail listener ports (25, 110, 143, 587, 465, 993,
// 995) on ufw. This is gate finding #2 — without these rules,
// a VPS with ufw active silently rejects SMTP/IMAP/POP3.
func TestSetupHttpsOpensMailPorts(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))
	for _, port := range []string{"25", "110", "143", "587", "465", "993", "995"} {
		// Look for `ufw allow <port>/tcp` pattern.
		needle := "ufw allow " + port + "/tcp"
		if !strings.Contains(body, needle) {
			t.Errorf("setup-https.sh missing firewall rule %q", needle)
		}
	}
	// The post-https hardening block must exist.
	for _, needle := range []string{
		"post_https_firewall_hardening",
		"ufw deny 8080/tcp",
		"ufw deny 8081/tcp",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("setup-https.sh missing hardening output %q", needle)
		}
	}
}

// TestUpgradeScriptSafetyProperties pins that upgrade.sh
// retains every safety property the production-readiness gate
// requires (gate finding #3):
//   - set -euo pipefail
//   - SHA256 verification of the new binary
//   - backup of orvix.db
//   - backup of jwt_key.pem
//   - backup of vapid_private.key
//   - health-endpoint polling (verify_health)
//   - rollback path on health failure
//   - --dry-run support
//   - no hardcoded https://releases.orvix.email URL (in
//     executable code, not in explanatory comments)
func TestUpgradeScriptSafetyProperties(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "upgrade.sh"))
	for _, needle := range []string{
		"set -euo pipefail",
		"sha256sum",
		"orvix.db",
		"jwt_key.pem",
		"vapid_private.key",
		"verify_health",
		"rolling back",
		"--dry-run",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("upgrade.sh missing safety property %q", needle)
		}
	}
	// Walk the body looking for the URL outside of comments.
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "releases.orvix.email") {
			t.Errorf("upgrade.sh hardcodes https://releases.orvix.email in executable code (line: %s)", trimmed)
		}
	}
	// Single-quoted ANSI escape regressions (BOLD='\\033[1m').
	// We tolerate them in comments and string LITERALS but
	// must catch the runtime BOLD variable declaration.
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "BOLD='\\033") {
			t.Errorf("upgrade.sh uses single-quoted ANSI escape (will print literal \\033): %s", line)
		}
	}
}

// TestUninstallScriptDefaultPreservesData pins gate finding
// #4: the default uninstall path must NOT delete /var/lib/orvix
// or /etc/orvix. The destructive path must require a typed
// confirmation phrase.
func TestUninstallScriptDefaultPreservesData(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "uninstall.sh"))
	// Stale stalwart references must be gone.
	if strings.Contains(body, "stalwart") {
		t.Error("uninstall.sh still references stalwart (legacy RC3 artifact)")
	}
	// --purge-data must require a confirmation phrase.
	if !strings.Contains(body, "CONFIRM_PHRASE") && !strings.Contains(body, "purge all orvix data") {
		t.Error("uninstall.sh must require a confirmation phrase for --purge-data")
	}
	// The default path must copy /var/lib/orvix into a backup
	// before doing anything destructive.
	if !strings.Contains(body, "/var/backups/orvix-uninstall") {
		t.Error("uninstall.sh must preserve data into /var/backups/orvix-uninstall/ by default")
	}
	// userdel -r is forbidden — that would delete /var/lib/orvix.
	if strings.Contains(body, "userdel -r orvix") || strings.Contains(body, "userdel -r  orvix") {
		t.Error("uninstall.sh uses userdel -r which deletes /var/lib/orvix (forbidden)")
	}
}

// TestHealthcheckScriptNoStalwart pins gate finding #6: the
// legacy Stalwart Mail Server references must be removed.
func TestHealthcheckScriptNoStalwart(t *testing.T) {
	root := repoRoot(t)
	for _, s := range []string{
		"release/scripts/healthcheck.sh",
		"release/scripts/diagnostics.sh",
	} {
		body := mustRead(t, filepath.Join(root, s))
		if strings.Contains(body, "stalwart") {
			t.Errorf("%s still references stalwart", s)
		}
	}
}

// TestSmokeScriptsShipped pins that every smoke script the
// production-readiness gate promises is actually present and
// syntactically valid. A future refactor that drops one must
// fail CI before the gate doc goes stale.
func TestSmokeScriptsShipped(t *testing.T) {
	root := repoRoot(t)
	bash := bashPath(t)
	for _, name := range []string{
		"smoke-health.sh",
		"smoke-runtime.sh",
		"smoke-upgrade.sh",
		"smoke-ports.sh",
	} {
		full := filepath.Join(root, "release", "scripts", name)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("missing smoke script: %s", name)
			continue
		}
		cmd := exec.Command(bash, "-n", full)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("%s: bash -n failed: %v\n%s", name, err, out)
		}
	}
}

// TestProductionReadinessDocExists pins that the gate doc is
// committed alongside the code it audits. The doc is the
// primary delivery artefact of this branch.
func TestProductionReadinessDocExists(t *testing.T) {
	root := repoRoot(t)
	full := filepath.Join(root, "docs", "PRODUCTION_READINESS_GATE.md")
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("production-readiness gate doc missing: %v", err)
	}
	body := string(data)
	for _, needle := range []string{
		"Install architecture",
		"Ports",
		"systemd units",
		"Backup / rollback",
		"Smoke tests",
		"Audit findings & fixes",
		"VPS deploy instructions",
		"Verdict",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("PRODUCTION_READINESS_GATE.md missing section: %q", needle)
		}
	}
}