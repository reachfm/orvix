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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
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

// TestPublicInstallerMatchesReleaseBundleLayout pins the
// build-release-bundle.sh contract consumed by install-public.sh:
// VERSION and BUILDINFO live at the bundle root beside bin/orvix,
// while install.sh and runtime assets live under release/.
func TestPublicInstallerMatchesReleaseBundleLayout(t *testing.T) {
	root := repoRoot(t)
	publicInstaller := mustRead(t, filepath.Join(root, "release", "install-public.sh"))
	bundleBuilder := mustRead(t, filepath.Join(root, "release", "scripts", "build-release-bundle.sh"))

	validateStart := strings.Index(publicInstaller, "validate_bundle_layout()")
	if validateStart < 0 {
		t.Fatal("install-public.sh must define validate_bundle_layout")
	}
	validateEnd := strings.Index(publicInstaller[validateStart:], "\n}\n")
	if validateEnd < 0 {
		t.Fatal("could not isolate validate_bundle_layout body")
	}
	validateBody := publicInstaller[validateStart : validateStart+validateEnd]

	for _, required := range []string{
		"bin/orvix",
		"VERSION",
		"BUILDINFO",
		"release/install.sh",
		"release/admin/index.html",
		"release/webmail/index.html",
		"release/systemd/orvix.service",
	} {
		if !strings.Contains(validateBody, required) {
			t.Fatalf("install-public.sh validate_bundle_layout missing bundle path %q", required)
		}
		if !strings.Contains(bundleBuilder, required) {
			t.Fatalf("build-release-bundle.sh missing matching bundle path %q", required)
		}
	}
	if strings.Contains(validateBody, "release/VERSION") {
		t.Fatal("install-public.sh must not require release/VERSION; builder places VERSION at bundle root")
	}
}

func TestReleaseBundleCopiesAdminMjsSmokeScripts(t *testing.T) {
	root := repoRoot(t)
	bundleBuilder := mustRead(t, filepath.Join(root, "release", "scripts", "build-release-bundle.sh"))

	for _, required := range []string{
		"release/scripts/smoke-admin-import-graph.mjs",
		"release/scripts/smoke-admin-runtime.mjs",
	} {
		if !strings.Contains(bundleBuilder, required) {
			t.Fatalf("build-release-bundle.sh must require %s in the sealed bundle", required)
		}
	}
	if !strings.Contains(bundleBuilder, "release/scripts/*.mjs") {
		t.Fatal("build-release-bundle.sh must copy release/scripts/*.mjs into the bundle")
	}
}

func runInstallPromptHarness(t *testing.T, mode string, env map[string]string) (int, string) {
	t.Helper()
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	installer = strings.Replace(installer, "\nmain \"$@\"\n", "\n# main disabled by test harness\n", 1)

	dir := t.TempDir()
	harness := filepath.Join(dir, "prompt-harness.sh")
	body := installer + `
fail() { printf 'ERROR:%s\n' "$*" >&2; exit 1; }
log_detail() { :; }
render_failure() { :; }
CURRENT_STEP="prompt-test"
if [ -n "${TEST_INPUT_FILE:-}" ]; then
	exec 3<"$TEST_INPUT_FILE"
	export ORVIX_PROMPT_INPUT_FD=3
fi
case "${1:-}" in
	domain) prompt_domain ;;
	email) prompt_email ;;
	password) prompt_password ;;
	*) printf 'unknown mode\n' >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(harness, []byte(body), 0o700); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bashPath(t), harness, mode)
	cmd.Stdin = strings.NewReader("")
	cmd.Env = append(os.Environ(), "ORVIX_PROMPT_INPUT_FD=3")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("prompt harness timed out; possible infinite prompt loop\n%s", out)
	}
	if err == nil {
		return 0, string(out)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("run harness: %v\n%s", err, out)
	return 1, string(out)
}

func TestInstallPromptsFailClosedOnCurlPipeEOF(t *testing.T) {
	for _, mode := range []string{"domain", "email", "password"} {
		t.Run(mode, func(t *testing.T) {
			code, out := runInstallPromptHarness(t, mode, nil)
			if code == 0 {
				t.Fatalf("%s prompt must fail on EOF/no prompt input; output:\n%s", mode, out)
			}
			if !strings.Contains(out, "input ended") && !strings.Contains(out, "required") {
				t.Fatalf("%s prompt failure must be clear, got:\n%s", mode, out)
			}
		})
	}
}

func TestInstallPromptsSupportEnvNonInteractive(t *testing.T) {
	cases := []struct {
		mode string
		env  map[string]string
		want string
	}{
		{"domain", map[string]string{"ORVIX_PRIMARY_DOMAIN": "example.com"}, "example.com"},
		{"domain", map[string]string{"ORVIX_DOMAIN": "example.org"}, "example.org"},
		{"email", map[string]string{"ORVIX_ADMIN_EMAIL": "admin@example.com"}, "admin@example.com"},
		{"password", map[string]string{"ORVIX_ADMIN_PASSWORD": "StrongPass123!"}, "StrongPass123!"},
		{"password", map[string]string{"ORVIX_ADMIN_PASSWORD_B64": "U3Ryb25nUGFzczEyMyE="}, "StrongPass123!"},
	}
	for _, tc := range cases {
		t.Run(tc.mode+"_"+tc.want, func(t *testing.T) {
			code, out := runInstallPromptHarness(t, tc.mode, tc.env)
			if code != 0 {
				t.Fatalf("expected success, got exit %d\n%s", code, out)
			}
			if strings.TrimSpace(out) != tc.want {
				t.Fatalf("unexpected output: got %q want %q", strings.TrimSpace(out), tc.want)
			}
		})
	}
}

func TestInstallPasswordPromptReadsPromptFDAndDoesNotSpinOnConfirmationEOF(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "prompt-input")
	if err := os.WriteFile(input, []byte("StrongPass123!\nStrongPass123!\n"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	code, out := runInstallPromptHarness(t, "password", map[string]string{"TEST_INPUT_FILE": input})
	if code != 0 {
		t.Fatalf("expected prompt fd success, got exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "StrongPass123!") {
		t.Fatalf("password output missing from harness output: %q", strings.TrimSpace(out))
	}

	eofInput := filepath.Join(dir, "eof-input")
	if err := os.WriteFile(eofInput, []byte("StrongPass123!\n"), 0o600); err != nil {
		t.Fatalf("write eof input: %v", err)
	}
	code, out = runInstallPromptHarness(t, "password", map[string]string{"TEST_INPUT_FILE": eofInput})
	if code == 0 {
		t.Fatalf("confirmation EOF must fail, got success\n%s", out)
	}
	if !strings.Contains(out, "confirmation is required") {
		t.Fatalf("confirmation EOF failure must be clear, got:\n%s", out)
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
//   - BLOCKER 5: fail-closed checksum (verify_checksum_fail_closed)
//   - BLOCKER 5: --allow-unsigned-local-artifact refused for URL upgrades
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
		// BLOCKER 5
		"verify_checksum_fail_closed",
		"--allow-unsigned-local-artifact",
		// BLOCKER 5: refuse --allow-unsigned-local-artifact for URL.
		"refused for --from-url",
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
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "BOLD='\\033") {
			t.Errorf("upgrade.sh uses single-quoted ANSI escape (will print literal \\033): %s", line)
		}
	}
	// BLOCKER 5: the old warning-only function body must not
	// return 0 when no expected sha256 is supplied. The fail-
	// closed function must say "FAIL missing checksum" or
	// similar in that path.
	if strings.Contains(body, "no checksum supplied; skipping integrity verification") {
		t.Error("upgrade.sh still has the OLD warning-only checksum path; production-readiness BLOCKER 5 requires fail-closed")
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

// TestInstallUsesShippedOrvixService pins production-readiness
// gate BLOCKER 1: install.sh must copy release/systemd/orvix.service
// into /etc/systemd/system/orvix.service, not write a divergent
// inline heredoc. A fresh VPS install must end up with the SAME
// unit the reviewer audited.
func TestInstallUsesShippedOrvixService(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "install.sh"))
	// The shipped unit must be installed via install(1) / cp, not
	// emitted via a heredoc. Pin the call to the new write_service
	// function as an install -m 0644 ... line referencing
	// release/systemd/orvix.service.
	if !strings.Contains(body, "release/systemd/orvix.service") {
		t.Error("install.sh must reference release/systemd/orvix.service (BLOCKER 1: install the shipped unit)")
	}
	if !strings.Contains(body, "install -m 0644") || !strings.Contains(body, "/etc/systemd/system/orvix.service") {
		t.Error("install.sh must install the shipped unit file to /etc/systemd/system/orvix.service")
	}
	// The OLD inline heredoc must be gone. Specifically, write_service
	// must not begin with `cat > /etc/systemd/system/orvix.service <<'UNIT'`.
	if strings.Contains(body, "cat > /etc/systemd/system/orvix.service") {
		t.Error("install.sh still writes orvix.service inline via heredoc (BLOCKER 1: must install shipped unit instead)")
	}
	// And the shipped unit itself must contain Requires=redis,
	// AmbientCapabilities=CAP_NET_BIND_SERVICE, etc. — install.sh
	// must NOT install a unit that lacks these properties.
	shipped := mustRead(t, filepath.Join(root, "release", "systemd", "orvix.service"))
	for _, needle := range []string{
		"Requires=redis-server.service",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"NoNewPrivileges=true",
		"ProtectSystem=full",
		"ReadWritePaths=/var/lib/orvix /var/log/orvix /etc/orvix",
		"[Install]",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(shipped, needle) {
			t.Errorf("shipped release/systemd/orvix.service missing %q — install.sh sanity-checks the shipped unit before installing it, so a regression here blocks the install", needle)
		}
	}
	// install.sh must sanity-check the shipped unit before installing it
	// (so a future refactor that drops Requires=redis-server.service
	// causes the install to FAIL rather than ship a degraded unit).
	if !strings.Contains(body, "shipped systemd unit is missing required directive") &&
		!strings.Contains(body, "shipped systemd unit $src is missing required directive") {
		t.Error("install.sh must sanity-check the shipped unit before installing it (BLOCKER 1 guard)")
	}
}

// TestOrvixUpdateServiceOperatorOnly pins production-readiness
// gate BLOCKER 2: orvix-update.service must NOT be enabled at
// boot. The unit file must not contain an executable [Install]
// section (so `systemctl enable` will refuse), and install.sh
// must not call `systemctl enable orvix-update.service`.
func TestOrvixUpdateServiceOperatorOnly(t *testing.T) {
	root := repoRoot(t)
	// Unit file: strip comments, then check that no executable
	// line declares [Install] or WantedBy=. The comments may
	// mention those strings for documentation purposes.
	unit := mustRead(t, filepath.Join(root, "release", "systemd", "orvix-update.service"))
	var executable strings.Builder
	for _, line := range strings.Split(unit, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		executable.WriteString(line)
		executable.WriteString("\n")
	}
	if strings.Contains(executable.String(), "[Install]") {
		t.Error("orvix-update.service has executable [Install] section (BLOCKER 2: must be operator-only; remove [Install] so systemctl enable refuses)")
	}
	if strings.Contains(executable.String(), "WantedBy=") {
		t.Error("orvix-update.service executable lines reference WantedBy= (BLOCKER 2: operator-only)")
	}
	// install.sh must not enable it.
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	for _, line := range strings.Split(installer, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		if strings.Contains(line, "systemctl enable orvix-update") {
			t.Errorf("install.sh calls `systemctl enable orvix-update.service` (BLOCKER 2: must not enable): %s", line)
		}
	}
	// install.sh must actively ASSERT the update unit is not enabled
	// after install, so a future refactor that re-enables it fails
	// the install loudly. The check must capture the exact state
	// string (not --quiet, which would treat "static" as enabled).
	if !strings.Contains(installer, "systemctl is-enabled orvix-update.service") {
		t.Error("install.sh must guard against orvix-update.service being enabled (BLOCKER 2)")
	}
}

// TestApplyRuntimeUpdateUsesRepoDirEnv pins production-readiness
// gate BLOCKER 3: the script must derive its repo root from
// ORVIX_REPO_DIR (default /opt/orvix), validate .git exists,
// and fail clearly if the repo is missing.
func TestApplyRuntimeUpdateUsesRepoDirEnv(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "scripts", "apply-runtime-update.sh"))
	for _, needle := range []string{
		"ORVIX_REPO_DIR",
		// Must validate the repo exists AND .git exists.
		"does not exist",
		"not a git worktree",
		// Must NOT derive repo root from the script's own path,
		// because the installed script lives at
		// /usr/share/orvix/scripts/ which is not a git worktree.
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("apply-runtime-update.sh missing BLOCKER 3 marker: %q", needle)
		}
	}
	// The OLD line `REPO_ROOT="$(cd "$(dirname "$0")/.."...`
	// must NOT exist; it would resolve to /usr/share/orvix
	// (the parent of the install path) which is not a worktree.
	if strings.Contains(body, `cd "$(dirname "$0")/.." && git rev-parse`) {
		t.Error("apply-runtime-update.sh still derives repo root from its own install path (BLOCKER 3)")
	}
	// The systemd unit must export ORVIX_REPO_DIR=/opt/orvix.
	unit := mustRead(t, filepath.Join(root, "release", "systemd", "orvix-update.service"))
	if !strings.Contains(unit, "Environment=ORVIX_REPO_DIR=/opt/orvix") {
		t.Error("orvix-update.service must export ORVIX_REPO_DIR=/opt/orvix (BLOCKER 3)")
	}
}

// TestOrvixUpdateServiceHasSetFcapCapability pins production-
// readiness gate BLOCKER 4: the apply-runtime-update.sh script
// runs `setcap cap_net_bind_service=+ep`, which requires
// CAP_SETFCAP. The unit's CapabilityBoundingSet and
// AmbientCapabilities must include CAP_SETFCAP; otherwise
// setcap fails silently and the binary loses its low-port bind
// capability on next restart.
func TestOrvixUpdateServiceHasSetFcapCapability(t *testing.T) {
	root := repoRoot(t)
	unit := mustRead(t, filepath.Join(root, "release", "systemd", "orvix-update.service"))
	if !strings.Contains(unit, "CapabilityBoundingSet=CAP_SETFCAP") {
		t.Error("orvix-update.service CapabilityBoundingSet must include CAP_SETFCAP (BLOCKER 4: setcap requires it)")
	}
	if !strings.Contains(unit, "AmbientCapabilities=CAP_SETFCAP") {
		t.Error("orvix-update.service AmbientCapabilities must include CAP_SETFCAP (BLOCKER 4)")
	}
	// The script itself must still run setcap (otherwise the
	// binary loses its CAP_NET_BIND_SERVICE after `install -m
	// 0755` strips xattrs on each restart).
	script := mustRead(t, filepath.Join(root, "release", "scripts", "apply-runtime-update.sh"))
	if !strings.Contains(script, "setcap") || !strings.Contains(script, "cap_net_bind_service") {
		t.Error("apply-runtime-update.sh must keep the setcap call for cap_net_bind_service (BLOCKER 4)")
	}
}

// TestDocsRejectInvalidUFWMultiPort pins production-readiness
// gate BLOCKER 6: docs/PRODUCTION_READINESS_GATE.md must NOT
// contain invalid `ufw allow <p1>/tcp <p2>/tcp ...` syntax.
// ufw rejects multi-port syntax on the command line. Each port
// must be its own rule. Acceptable forms:
//   - one `ufw allow <port>/tcp` per port (chained with `&&`
//     or `;` between separate invocations)
//   - a `for port in ...; do ufw allow "${port}/tcp"; done` loop
//
// We split each line at shell command separators (`&&`, `||`,
// `;`) and assert that no command fragment contains more than
// one port argument. We skip Markdown table rows (`| ... |`)
// and code-block fences so audit-finding prose doesn't trip
// the matcher.
func TestDocsRejectInvalidUFWMultiPort(t *testing.T) {
	root := repoRoot(t)
	doc := mustRead(t, filepath.Join(root, "docs", "PRODUCTION_READINESS_GATE.md"))
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		// Skip Markdown table rows.
		if strings.HasPrefix(trimmed, "|") {
			continue
		}
		// Normalise line continuations.
		scanned := strings.ReplaceAll(line, "\\\n", " ")
		// Split on shell command separators. Each resulting
		// fragment is one logical command.
		fragments := regexp.MustCompile(`&&|\|\||;`).Split(scanned, -1)
		for _, frag := range fragments {
			f := strings.TrimSpace(frag)
			if !strings.Contains(f, "ufw ") {
				continue
			}
			ufwCmd := regexp.MustCompile(`(?m)ufw\s+(allow|deny)\s+(.+?)\s*$`)
			m := ufwCmd.FindStringSubmatch(f)
			if m == nil {
				continue
			}
			args := strings.TrimSpace(m[2])
			args = regexp.MustCompile(`^(sudo\s+)?`).ReplaceAllString(args, "")
			fields := strings.Fields(args)
			portFields := 0
			for _, field := range fields {
				if strings.Contains(field, "/tcp") || strings.Contains(field, "/udp") {
					portFields++
				}
			}
			if portFields > 1 {
				t.Errorf("docs/PRODUCTION_READINESS_GATE.md contains invalid multi-port ufw syntax (%d ports in one rule): %q", portFields, f)
			}
		}
	}
	// Positive assertion: the doc must include the loop form.
	if !strings.Contains(doc, `for port in`) || !strings.Contains(doc, `sudo ufw allow "${port}/tcp"`) {
		t.Error("docs/PRODUCTION_READINESS_GATE.md must demonstrate the per-port ufw loop form (BLOCKER 6)")
	}
}

// TestSmokePortsAcceptsRedisIPv6Loopback pins the 2026-06-29
// smoke-ports.sh hotfix: Redis bound on IPv6 loopback [::1]:6379
// (and the unbracketed ss/netstat variant ::1:6379) MUST be
// classified as a loopback bind, not as public exposure.
//
// The bug it guards against: bash case patterns interpret [::1]
// as a character class (matching `:` or `1`), so a pattern like
// `internal:[::1]:*` silently mis-matches the bracketed form and
// the script flags Redis as POSTURE WRONG. The fix is to extract
// a `is_loopback_bind` helper with QUOTED case patterns so the
// brackets stay literal. This test pins:
//   - the script defines is_loopback_bind
//   - the case patterns are quoted (the brackets are escaped)
//   - the function classifies the documented loopback forms as
//     loopback
//   - the function REJECTS the documented public-exposure forms
//   - 127.0.0.0/8 is also accepted (entire IPv4 loopback block)
//   - the bind-posture loop in the main script USES the helper
func TestSmokePortsAcceptsRedisIPv6Loopback(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "smoke-ports.sh"))
	bash := bashPath(t)

	// 1) Static pin: helper exists and uses quoted patterns.
	if !strings.Contains(script, "is_loopback_bind()") {
		t.Fatal("smoke-ports.sh must define is_loopback_bind helper (2026-06-29 hotfix)")
	}
	// The case patterns must keep the IPv6 brackets literal. The
	// pattern `"[::1]:"*` (with quotes) escapes the brackets;
	// the unquoted pattern `[::1]:*` would be interpreted as a
	// character class by bash.
	for _, needle := range []string{
		`"[::1]:"*`,
		`"::1:"*`,
		`"127.0.0.1:"*`,
	} {
		if !strings.Contains(script, needle) {
			t.Errorf("smoke-ports.sh is_loopback_bind is missing quoted loopback pattern %q (without quotes, bash interprets [::1] as a character class)", needle)
		}
	}
	// The bind-posture loop in the main script must call the
	// helper, not a buggy inline case statement.
	if !strings.Contains(script, "is_loopback_bind \"$addr\"") {
		t.Error("smoke-ports.sh bind-posture loop must call is_loopback_bind on each address")
	}
	// The OLD buggy pattern `internal:[::1]:*` (unquoted) must
	// not appear in the executable part of the script.
	stripped := stripBashComments(script)
	if strings.Contains(stripped, "internal:[::1]:*") ||
		strings.Contains(stripped, "public:[::1]:*") {
		t.Error("smoke-ports.sh still contains an unquoted `[::1]:*` case pattern (would be silently mis-matched as a bash character class)")
	}

	// 2) Behavioural pin: extract is_loopback_bind from the
	//    shipped script and run it under bash against the
	//    documented accept/reject table.
	function, err := extractBashFunction(script, "is_loopback_bind")
	if err != nil {
		t.Fatalf("extract is_loopback_bind from script: %v", err)
	}
	cases := []struct {
		addr     string
		accepted bool
		why      string
	}{
		// ACCEPTED — loopback binds.
		{"127.0.0.1:6379", true, "IPv4 loopback, ss default form"},
		{"[::1]:6379", true, "IPv6 loopback, bracketed ss default form"},
		{"::1:6379", true, "IPv6 loopback, unbracketed ss/netstat variant"},
		{"127.0.0.2:6379", true, "127/8 IPv4 loopback block (on principle)"},
		{"127.255.255.254:6379", true, "127/8 IPv4 loopback block, upper end"},

		// REJECTED — public exposure.
		{"0.0.0.0:6379", false, "IPv4 any (wildcard)"},
		{"[::]:6379", false, "IPv6 any (wildcard)"},
		{"*:6379", false, "explicit wildcard"},
		{"10.0.0.5:6379", false, "private RFC1918 address"},
		{"192.0.2.1:6379", false, "TEST-NET-1 documentation address"},
		{"198.51.100.7:6379", false, "TEST-NET-2 documentation address"},
		{"203.0.113.42:6379", false, "TEST-NET-3 documentation address"},
	}
	for _, c := range cases {
		// Build a small bash program that defines the function
		// (extracted from the shipped script) and then prints
		// "yes" if the function says it's loopback, else "no".
		program := function + "\nif is_loopback_bind " + shellQuote(c.addr) + "; then echo yes; else echo no; fi\n"
		cmd := exec.Command(bash, "-c", program)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("is_loopback_bind %s: bash error: %v\n%s", c.addr, err, out)
		}
		got := strings.TrimSpace(string(out)) == "yes"
		if got != c.accepted {
			t.Errorf("is_loopback_bind(%q) = %v, want %v — %s", c.addr, got, c.accepted, c.why)
		}
	}
}

// TestSmokePortsRedisWording pins that smoke-ports.sh explains
// the IPv4/IPv6 loopback posture is OK for Redis, so operators
// reading the --verbose output see why [::1] is not flagged.
func TestSmokePortsRedisWording(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "smoke-ports.sh"))
	for _, needle := range []string{
		"redis",
		"loopback (IPv4/IPv6)",
		"is_loopback_bind",
	} {
		if !strings.Contains(script, needle) {
			t.Errorf("smoke-ports.sh missing Redis IPv4/IPv6 wording marker %q", needle)
		}
	}
}

// stripBashComments is defined in installer_test.go (same package).
// We re-use it here to scan smoke-ports.sh for unquoted IPv6
// patterns that would silently regress to a bash character class.

// extractBashFunction returns the body of <name>() { ... } from
// a bash script (single-function extraction, no nested-function
// support — sufficient for smoke-ports.sh's flat helper layout).
func extractBashFunction(script, name string) (string, error) {
	lines := strings.Split(script, "\n")
	var b strings.Builder
	inFn := false
	depth := 0
	header := name + "()"
	for _, line := range lines {
		if !inFn {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, header) {
				// Find the `{` on this line (or the next).
				if !strings.Contains(line, "{") {
					continue
				}
				inFn = true
				depth = 1
				b.WriteString(line)
				b.WriteString("\n")
				continue
			}
			continue
		}
		// Inside the function: count braces, ignore those inside
		// quotes. Best-effort: count both `{` and `}` regardless.
		for _, ch := range line {
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
		if depth == 0 {
			return b.String(), nil
		}
	}
	return "", fmt.Errorf("function %q not found or unterminated", name)
}

// shellQuote single-quotes a string for safe use as a single
// argument to bash -c. Used by the loopback test to inject the
// test address into the bash program.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
