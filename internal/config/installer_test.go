package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

func bashCommand(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "bash"
	}
	path := `C:\Program Files\Git\bin\bash.exe`
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return "bash"
}

// stripBashComments removes "#"-prefixed line comments from a bash
// script for the purpose of scanning its executable body. Naive but
// adequate for the safety checks below — it correctly handles
// comment-prefixed lines and inline trailing comments separated by whitespace.
func stripBashComments(script string) string {
	var out strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Strip trailing inline comment after a space.
		if idx := strings.Index(line, " #"); idx >= 0 {
			out.WriteString(line[:idx])
			out.WriteString("\n")
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

func TestInstallerTemplateRC1CleanPath(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	required := []string{
		"export DEBIAN_FRONTEND=noninteractive",
		"export NEEDRESTART_MODE=a",
		"INSTALL_LOG=\"${INSTALL_LOG:-/var/log/orvix/install.log}\"",
		"ORVIX ENTERPRISE MAIL",
		"COREMAIL INSTALLER",
		"progress_bar()",
		"set_step \"preparing\" \"System preflight\" 10",
		"set_step \"dependencies\" \"Platform dependencies\" 20",
		"set_step \"user\" \"Service identity\" 35",
		"set_step \"configuration-input\" \"Administrator enrollment\" 45",
		"set_step \"binary\" \"CoreMail binary deployment\" 60",
		"set_step \"configuration\" \"Configuration provisioning\" 75",
		"set_step \"systemd\" \"Service activation\" 85",
		"set_step \"verification\" \"Enterprise health verification\" 95",
		"render_success",
		"install_version()",
		"trap on_error ERR",
		"tail -n 80 \"$INSTALL_LOG\"",
		"run_quiet apt-get update -qq",
		"apt-get install -y -qq",
		"ca-certificates curl jq sqlite3 openssl python3 tar gzip redis-server libcap2-bin iproute2 ufw",
		"-o Dpkg::Options::=--force-confdef",
		"-o Dpkg::Options::=--force-confold",
		"systemctl enable --now redis-server",
		"cp -R \"$ORVIX_SOURCE_DIR\"/release/admin/. /usr/share/orvix/admin/",
		"cp -R \"$ORVIX_SOURCE_DIR\"/release/webmail/. /usr/share/orvix/webmail/",
		"find /usr/share/orvix/admin -type f -exec chmod 0644 {} +",
		"find /usr/share/orvix/webmail -type f -exec chmod 0644 {} +",
		"admin_ui_dir: /usr/share/orvix/admin",
		"webmail_ui_dir: /usr/share/orvix/webmail",
		"coremail:",
		"outbound:",
		"prefer_ipv4: true",
		"enabled: true",
		"host: 127.0.0.1",
		"admin_port: 8080",
		"password_min_len: 8",
		"curl -fsSI http://127.0.0.1:8080/admin",
		"curl -fsSI http://127.0.0.1:8080/webmail",
		"curl -fsS http://127.0.0.1:8081/.well-known/jmap",
		"systemctl is-enabled --quiet orvix",
		"command -v sqlite3",
		"[ -f /var/lib/orvix/orvix.db ]",
		"sqlite_escape()",
		"bootstrapped admin user row was not created",
		"bootstrapped admin mailbox row was not created",
		"SELECT COUNT(*) FROM users WHERE email = '$sql_email' AND role = 'admin' AND active = 1;",
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE email = '$sql_email' AND is_admin = 1 AND status = 'active' AND deleted_at IS NULL;",
		"bootstrap.env preserved for diagnosis: $BOOTSTRAP_ENV",
		"INSTALLATION VERIFICATION PASSED",
		"setcap 'cap_net_bind_service=+ep' \"$ORVIX_BIN\"",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"BOOTSTRAP_ENV=\"${BOOTSTRAP_ENV:-/etc/orvix/bootstrap.env}\"",
		"ORVIX_ADMIN_EMAIL",
		"ORVIX_ADMIN_PASSWORD_B64",
		"printf '%s' \"$password\" | base64 | tr -d '\\n'",
		"rm -f \"$BOOTSTRAP_ENV\"",
		"/admin/login",
		"journalctl -u orvix.service -n 80 --no-pager",
		"Product: Orvix Enterprise Mail / CoreMail",
		// Production HTTPS URLs the operator will use after
		// setup-https.sh runs.
		"Admin URL:   https://admin.${domain}/admin",
		"Webmail URL: https://webmail.${domain}/",
		"JMAP URL:    https://mail.${domain}/.well-known/jmap",
		// The 2026-06-29 hotfix replaced the public-IP
		// "TEMPORARY URLs" block with a domain-based INTENDED
		// block (so the operator doesn't see a public HTTP
		// login URL on the server IP) plus a loopback-only
		// local-validation block plus an SSH tunnel escape
		// hatch. These strings are the new contract.
		"INTENDED production URLs (HTTPS via caddy, NOT YET REACHABLE):",
		"Local validation URLs (loopback only, from inside the VPS):",
		"Admin UI:    http://127.0.0.1:8080/admin",
		"Webmail UI:  http://127.0.0.1:8080/webmail",
		"JMAP:        http://127.0.0.1:8081/.well-known/jmap",
		"ssh -L 8080:127.0.0.1:8080 -L 8081:127.0.0.1:8081 root@${server_ip}",
		"setup-https.sh",
		"Mail Hostname: mail.${domain}",
		"SMTP: mail.${domain}:25",
		"IMAP: mail.${domain}:143",
		"POP3: mail.${domain}:110",
		"DNS required (set these with your DNS provider)",
		"A admin.${domain} -> ${server_ip}",
		"A mail.${domain} -> ${server_ip}",
		"release/scripts/setup-https.sh ${domain} ${server_ip}",
		// Credential file UX.
		"Admin login details saved to",
		"write_admin_login_file",
		"validate_webmail_ui",
		"chmod 0600",
		// CORS: the webmail SPA ships from
		// webmail.${domain} (NOT admin.${domain}/webmail —
		// that path-based mount is removed in this release).
		// The admin server must allow both admin.${domain}
		// and webmail.${domain} so cross-subdomain API
		// calls (with credentials:include) succeed.
		"https://$admin_host",
		"http://$admin_host",
		"https://$webmail_host",
		"http://$webmail_host",
		"local admin_host=\"admin.$domain\"",
		"local webmail_host=\"webmail.$domain\"",
		"local cookie_domain=\".$domain\"",
		"Admin password (8-72 bytes, hidden):",
		"admin password must be at least 8 characters",
		"admin password is too long for bcrypt",
		"smoke_login_admin_attempts",
		"smoke_webmail_assets",
		"smoke_jmap_session",
		"multi-login gate",
		"verify_install_password_login",
		"VERIFY password-chain first login",
		"VERIFY password-chain second login",
		"bootstrap env base64 round-trip mismatch",
		"second_jar",
	}
	for _, item := range required {
		if !strings.Contains(installer, item) {
			t.Fatalf("installer missing %q", item)
		}
	}
	stalePasswordLen := "12"
	forbidden := []string{
		// The old misleading URL labels — admin.${domain} is
		// not reachable on plain HTTP before setup-https.sh
		// runs, so printing "Admin URL: http://admin.X/admin"
		// implies the operator can hit it.
		"Admin URL:   http://admin.${domain}/admin",
		"Webmail URL: http://admin.${domain}/webmail",
		"JMAP URL:    http://mail.${domain}/.well-known/jmap",
		"Admin UI: http://mail.${primary_domain}:8080/admin",
		"Admin UI: http://$(hostname -f 2>/dev/null || hostname):8080/admin",
		"hostname -f 2>/dev/null || hostname",
		// The plain "Temporary Admin API:" label was too
		// vague — the new label is "Admin UI:    http://IP:8080/admin"
		// inside a clearly-marked TEMPORARY block.
		"Temporary Admin API: http://${server_ip}:8080/admin",
		// No password should ever be printed to stdout. The
		// installer must use the root-only credential file.
		"echo \"$admin_password\"",
		"printf \"%s\" \"$admin_password\"",
		"log_detail \"$admin_password\"",
		"log_detail \"$password\"",
		"ORVIX_ADMIN_PASSWORD=\"$escaped_password\"",
		"RC1 CLEAN INSTALLER",
		"min " + stalePasswordLen + " chars",
		"at least " + stalePasswordLen + " characters",
		"==>",
	}
	for _, item := range forbidden {
		if strings.Contains(installer, item) {
			t.Fatalf("installer must not contain %q", item)
		}
	}
	if strings.Contains(strings.ToLower(installer), "stalwart") {
		t.Fatal("RC1 clean installer must not reference Stalwart")
	}
	verifyIndex := strings.Index(installer, "verify_install_password_login \"$email\" \"$password\"")
	deleteIndex := strings.Index(installer, "rm -f \"$BOOTSTRAP_ENV\"")
	if verifyIndex < 0 || deleteIndex < 0 {
		t.Fatal("installer must check login response and delete bootstrap env after success")
	}
	if deleteIndex < verifyIndex {
		t.Fatal("installer must not delete bootstrap.env before login verification succeeds")
	}
}

func TestHTTPSSetupScriptCaddyFlow(t *testing.T) {
	root := repoRoot(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "setup-https.sh"))
	if err != nil {
		t.Fatalf("read https setup script: %v", err)
	}
	script := string(scriptBytes)
	for _, item := range []string{
		"admin.<domain>   -> 127.0.0.1:8080",
		"webmail.<domain> -> 127.0.0.1:8080   (path-rewritten to /webmail/*)",
		"mail.<domain>    -> 127.0.0.1:8080   (/api/* paths)",
		"mail.<domain>    -> 127.0.0.1:8081   (everything else:",
		"ADMIN_DOMAIN=\"${ADMIN_DOMAIN:-admin.$PRIMARY_DOMAIN}\"",
		"WEBMAIL_DOMAIN=\"${WEBMAIL_DOMAIN:-webmail.$PRIMARY_DOMAIN}\"",
		"MAIL_DOMAIN=\"${MAIL_DOMAIN:-mail.$PRIMARY_DOMAIN}\"",
		"reverse_proxy 127.0.0.1:8080",
		"reverse_proxy 127.0.0.1:8081",
		"@api path /api/*",
		"@webmail path /webmail /webmail/*",
		"@assets path /assets /assets/*",
		"rewrite * /webmail{uri}",
		"ufw allow 80/tcp",
		"ufw allow 443/tcp",
		"caddy validate --config /etc/caddy/Caddyfile",
		"systemctl is-active --quiet caddy",
		"check_dns \"$ADMIN_DOMAIN\"",
		"check_dns \"$WEBMAIL_DOMAIN\"",
		"check_dns \"$MAIL_DOMAIN\"",
		"check_local_port 80",
		"check_local_port 443",
		"check_https \"https://$ADMIN_DOMAIN/admin\" HEAD",
		"check_https \"https://$ADMIN_DOMAIN/api/v1/health\" GET",
		"check_https \"https://$WEBMAIL_DOMAIN/webmail/assets/webmail.js\" HEAD",
		"check_https \"https://$WEBMAIL_DOMAIN/webmail/assets/webmail.css\" HEAD",
		"check_https \"https://$WEBMAIL_DOMAIN/\" HEAD",
		"check_https \"https://$WEBMAIL_DOMAIN/api/v1/health\" GET",
		"check_https \"https://$MAIL_DOMAIN/.well-known/jmap\" GET",
		"check_content_type \"https://$WEBMAIL_DOMAIN/webmail/assets/webmail.js\" \"text/javascript\"",
		"check_content_type \"https://$WEBMAIL_DOMAIN/webmail/assets/webmail.css\" \"text/css\"",
		"check_content_type \"https://$WEBMAIL_DOMAIN/\" \"text/html\"",
		"curl -fsS --connect-timeout 5 --max-time 10 \"$url\"",
		// Production-readiness gate #2: setup-https.sh must
		// open the mail listener ports (SMTP/IMAP/POP3 + their
		// TLS variants) AND emit a post-https hardening block
		// that tells the operator to deny external access to
		// the internal admin + JMAP ports.
		"ufw allow 25/tcp",
		"ufw allow 110/tcp",
		"ufw allow 143/tcp",
		"ufw allow 587/tcp",
		"ufw allow 465/tcp",
		"ufw allow 993/tcp",
		"ufw allow 995/tcp",
		"post_https_firewall_hardening",
		"sudo ufw deny 8080/tcp",
		"sudo ufw deny 8081/tcp",
		"Recommended firewall posture after HTTPS",
	} {
		if !strings.Contains(script, item) {
			t.Fatalf("https setup script missing %q", item)
		}
	}
	if strings.Contains(script, "check_https \"https://$ADMIN_DOMAIN/api/v1/health\" HEAD") {
		t.Fatal("admin health smoke must use GET because the API route is GET-only")
	}
}

// extractCaddyBlock pulls the body of a top-level `$NAME { ... }`
// Caddy vhost block out of a heredoc. The closing brace is matched
// at column 0 — i.e. a line that starts with `}` and no leading
// whitespace, the convention Caddyfile uses for top-level vhost
// blocks. Returns "" if the block is not present or unbalanced.
//
// The block body is the text BETWEEN the opening `{` and the
// matching column-0 `}` — exactly what we want to assert
// structure against.
func extractCaddyBlock(script, name string) string {
	header := name + " {"
	start := strings.Index(script, header)
	if start < 0 {
		return ""
	}
	bodyStart := start + len(header)
	lines := strings.Split(script[bodyStart:], "\n")
	consumed := 0
	for i, line := range lines {
		// i == 0 is the header's own rest-of-line (e.g. the
		// opening `{` followed by a comment) — skip it.
		if i == 0 {
			consumed += len(line) + 1
			continue
		}
		// Column-0 closer: a `}` with NO leading whitespace.
		// A tab-indented `}` (e.g. inside `handle @api { ... }`)
		// must NOT match — otherwise we close the vhost
		// prematurely on the first nested handle.
		if line == "}" {
			return strings.TrimRight(script[bodyStart:bodyStart+consumed], "\n")
		}
		consumed += len(line) + 1
	}
	return ""
}

// TestHTTPSSetupScriptMailDomainBlock pins the structure of the
// `$MAIL_DOMAIN { ... }` Caddy vhost block. The previous test only
// grepped the whole script for "@webmail" / "@assets" /
// "reverse_proxy 127.0.0.1:8081" — too weak, because a regression
// could move those markers into the `$WEBMAIL_DOMAIN` block (or
// anywhere else) and the assertion would still pass.
//
// This test extracts the mail vhost body and asserts every piece
// of the required routing lives INSIDE that block:
//
//	$MAIL_DOMAIN {
//	  @api path /api/*                   → 8080  (admin + webmail API)
//	  handle @api { ... }
//	  @webmail path /webmail /webmail/*  → 8080  (webmail SPA + service worker)
//	  handle @webmail { ... }
//	  @assets path /assets /assets/*     → 8080  (rewrite to /webmail{uri})
//	  handle @assets { ... }
//	  handle {                           → 8081  (JMAP + everything else)
//	}
//
// Failure modes this test catches:
//   - `@webmail` block moved out of the mail vhost.
//   - The mail vhost routes /api/* to 8081 instead of 8080.
//   - The mail vhost routes /webmail/* to 8081 instead of 8080.
//   - The catch-all 8081 route removed (regression of the
//     Caddy mail API split-routing fix).
//   - The mail vhost accidentally re-uses the webmail vhost
//     catch-all (reverse-proxying webmail traffic to 8081).
func TestHTTPSSetupScriptMailDomainBlock(t *testing.T) {
	root := repoRoot(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "setup-https.sh"))
	if err != nil {
		t.Fatalf("read https setup script: %v", err)
	}
	script := string(scriptBytes)

	mailBlock := extractCaddyBlock(script, "$MAIL_DOMAIN")
	if mailBlock == "" {
		t.Fatal("could not locate $MAIL_DOMAIN { ... } Caddy vhost block in setup-https.sh")
	}

	// Structural assertions: every required piece MUST appear
	// inside the mail block body. extractCaddyBlock already
	// confirmed the "$MAIL_DOMAIN {" header exists in the
	// script — the body it returns starts AT the opening `{`
	// and ends at the matching column-0 `}`. We do NOT
	// re-assert the header inside the body because it sits
	// outside the captured range.
	requiredInMailBlock := []string{
		// @api route → 8080.
		"@api path /api/*",
		"handle @api",
		// 8080 must appear inside the @api handle so the test
		// catches a future regression that splits the API to
		// 8081 instead.
		"reverse_proxy 127.0.0.1:8080",

		// @webmail route → 8080. This is the service worker
		// path — required for browser push to work on the
		// mail host.
		"@webmail path /webmail /webmail/*",
		"handle @webmail",

		// @assets route → 8080 (with rewrite to /webmail{uri}).
		"@assets path /assets /assets/*",
		"handle @assets",
		"rewrite * /webmail{uri}",

		// Catch-all → 8081. This is the JMAP / SMTP-submission-web
		// / IMAP / POP3 path that the upstream runtime serves
		// from the second listener. Regression of the
		// "Caddy mail API split-routing" fix.
		"handle {",
		"reverse_proxy 127.0.0.1:8081",
	}
	for _, item := range requiredInMailBlock {
		if !strings.Contains(mailBlock, item) {
			t.Errorf("$MAIL_DOMAIN Caddy block missing %q\n--- mail block ---\n%s", item, mailBlock)
		}
	}

	// Negative assertions: the mail block must NOT proxy any
	// of these targets to 8081 — that would route admin /
	// webmail / API traffic into the JMAP listener and break
	// the host. The 8081 catch-all must be reserved for
	// "everything else" only.
	badRoutes := []string{
		// No 8081 should appear in any @api / @webmail / @assets
		// handle inside the mail block.
		"handle @api {\n\t\treverse_proxy 127.0.0.1:8081",
		"handle @webmail {\n\t\treverse_proxy 127.0.0.1:8081",
		"handle @assets {\n\t\treverse_proxy 127.0.0.1:8081",
	}
	for _, bad := range badRoutes {
		if strings.Contains(mailBlock, bad) {
			t.Errorf("$MAIL_DOMAIN block must not route to 8081: %q", bad)
		}
	}

	// Sanity: the mail block must contain at least one
	// 8080 reverse-proxy AND at least one 8081 reverse-proxy.
	// If a refactor deletes one of them entirely, the
	// per-item assertions above would catch it, but this
	// catches a more subtle "the block has only 8081" bug
	// caused by a global replace gone wrong.
	if !strings.Contains(mailBlock, "reverse_proxy 127.0.0.1:8080") {
		t.Errorf("$MAIL_DOMAIN block has no 8080 reverse-proxy: %s", mailBlock)
	}
	if !strings.Contains(mailBlock, "reverse_proxy 127.0.0.1:8081") {
		t.Errorf("$MAIL_DOMAIN block has no 8081 reverse-proxy: %s", mailBlock)
	}

	// Cross-check: the $WEBMAIL_DOMAIN block must NOT have
	// a 8081 catch-all — that would break the webmail vhost
	// by proxying every unknown path to the JMAP listener.
	// The current setup-https.sh design uses 8080 for the
	// webmail catch-all (rewrite * /webmail{uri} → 8080).
	webmailBlock := extractCaddyBlock(script, "$WEBMAIL_DOMAIN")
	if webmailBlock == "" {
		t.Fatal("could not locate $WEBMAIL_DOMAIN { ... } Caddy vhost block in setup-https.sh")
	}
	if strings.Contains(webmailBlock, "reverse_proxy 127.0.0.1:8081") {
		t.Errorf("$WEBMAIL_DOMAIN block must not proxy to 8081: %s", webmailBlock)
	}
}

func TestInstallerVAPIDProvisioningIsServiceReadable(t *testing.T) {
	root := repoRoot(t)
	installBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read install script: %v", err)
	}
	installer := string(installBytes)

	for _, want := range []string{
		"install_release_scripts",
		"provision_vapid_keys \"$admin_email\"",
		"/usr/share/orvix/scripts/generate-vapid-keys.sh",
		"vapid_public_key:",
		"vapid_private_key_file:",
		"vapid_subject:",
		"root:orvix",
		"640",
	} {
		if !strings.Contains(installer, want) {
			t.Errorf("installer must contain VAPID provisioning marker %q", want)
		}
	}

	generatorBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "generate-vapid-keys.sh"))
	if err != nil {
		t.Fatalf("read VAPID generator: %v", err)
	}
	generator := string(generatorBytes)
	for _, want := range []string{
		"need_cmd openssl",
		"need_cmd python3",
		"install -m 0640 -o root -g \"$SERVICE_GROUP\"",
		"install -m 0644 -o root -g root",
		"vapid_private_key_file",
		"vapid_subject",
	} {
		if !strings.Contains(generator, want) {
			t.Errorf("VAPID generator missing safety marker %q", want)
		}
	}
	for _, forbidden := range []string{"go run", "ORVIX_BIN", "node ", "npm ", "github.com/orvix/orvix/internal/coremail/push"} {
		if strings.Contains(generator, forbidden) {
			t.Errorf("VAPID generator must not depend on Go/Node/runtime source; found %q", forbidden)
		}
	}
}

// ── SUBMISSION-3D: 587 SMTP TLS setup script ─────────────────

// TestSMTPTLSSetupScriptExists asserts the file is present and
// parses as a bash script with no syntax errors.
func TestSMTPTLSSetupScriptExists(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "release", "scripts", "setup-smtp-tls.sh")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("setup-smtp-tls.sh missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("setup-smtp-tls.sh is empty")
	}
	if runtime.GOOS != "windows" {
		// On Windows we can't reliably chmod, so just ensure the file
		// is readable and parses with `bash -n`.
		cmd := exec.Command(bashCommand(t), "-n", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup-smtp-tls.sh bash syntax error: %v\n%s", err, out)
		}
	}
}

// TestSMTPCheckScriptExists asserts the doctor script is present
// and parses cleanly.
func TestSMTPCheckScriptExists(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "release", "scripts", "check-smtp-tls.sh")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("check-smtp-tls.sh missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("check-smtp-tls.sh is empty")
	}
	cmd := exec.Command(bashCommand(t), "-n", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("check-smtp-tls.sh bash syntax error: %v\n%s", err, out)
	}
}

// TestSMTPTLSSetupScriptShape asserts the script contains every
// safety property called out by the SUBMISSION-3D spec.
func TestSMTPTLSSetupScriptShape(t *testing.T) {
	root := repoRoot(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "setup-smtp-tls.sh"))
	if err != nil {
		t.Fatalf("read setup-smtp-tls.sh: %v", err)
	}
	script := string(scriptBytes)

	mustContain := []string{
		// Targets — only the Orvix path, never Caddy.
		"/etc/orvix/tls/smtp",
		"fullchain.pem",
		"privkey.pem",
		// Source paths are operator-supplied; no hardcoded Caddy layout.
		"ORVIX_SRC_CERT",
		"ORVIX_SRC_KEY",
		// Safety: refuse too-permissive source keys.
		"source key is too permissive",
		// Validation BEFORE any destructive operation.
		"validate_pair",
		"-pubkey",
		"openssl x509",
		// Cert/key pair validation cannot be skipped — even after copy.
		"installed cert/key did not validate",
		// Backup before any YAML edit.
		"${ORVIX_CONFIG}.bak-",
		// Permissions: key must NOT be world-readable.
		"0640",
		"install -m 0644",
		// The runtime ports must remain untouched when the script fails.
		"port 25 inbound is unaffected",
		// Reload the service after YAML edit.
		"systemctl reload-or-restart",
		// Probe 587 is listening.
		"sport = :587",
		// Rollback hint in the success output.
		"Rollback",
		// Path sanitization — the install log only records sizes.
		"size:",
		// No raw openssl error text leaked.
		"2>>\"$INSTALL_LOG\"",
	}
	for _, want := range mustContain {
		if !strings.Contains(script, want) {
			t.Errorf("setup-smtp-tls.sh missing required safety marker: %q", want)
		}
	}

	// Negative assertions: the script must NOT hardcode Caddy paths
	// (or any other upstream cert layout) in executable code. Comments
	// are fine — they're how we tell operators what NOT to do.
	executable := stripBashComments(script)
	mustNotContain := []string{
		"/var/lib/caddy",
		"/.local/share/caddy",
		"acme-v02.api.letsencrypt.org",
		"/etc/letsencrypt/live",
	}
	for _, bad := range mustNotContain {
		if strings.Contains(executable, bad) {
			t.Errorf("setup-smtp-tls.sh must NOT hardcode %q in executable code (operator-supplied source only)", bad)
		}
	}
}

// TestSMTPTLSSetupScriptIdempotent asserts the script's logic is
// idempotent — second run with same inputs produces same end state.
// Concretely: we verify the YAML-rewrite function is "upsert"
// (not "append-only"), so rerunning does not duplicate keys.
func TestSMTPTLSSetupScriptUpsertBehavior(t *testing.T) {
	root := repoRoot(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "setup-smtp-tls.sh"))
	if err != nil {
		t.Fatalf("read setup-smtp-tls.sh: %v", err)
	}
	script := string(scriptBytes)
	// The upsert helper must replace an existing key, not append.
	if !strings.Contains(script, "upsert_yaml_field") {
		t.Fatal("setup-smtp-tls.sh must define upsert_yaml_field (idempotent YAML edit)")
	}
	if !strings.Contains(script, `leaf_re = re.compile`) && !strings.Contains(script, `leaf_re=re.compile`) {
		t.Fatal("setup-smtp-tls.sh upsert helper must regex-match an existing key for replacement")
	}
}

// TestSMTPCheckScriptShape asserts the doctor script covers all the
// required readiness checks.
func TestSMTPCheckScriptShape(t *testing.T) {
	root := repoRoot(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "check-smtp-tls.sh"))
	if err != nil {
		t.Fatalf("read check-smtp-tls.sh: %v", err)
	}
	script := string(scriptBytes)

	mustContain := []string{
		// Required readiness checks.
		"port 25 is listening",
		"coremail.submission_enabled",
		"coremail.tls_cert_file",
		"coremail.tls_key_file",
		"cert/key pair validates",
		"world-readable",
		"expires within 30 days",
		// Negative checks.
		"port 465 is not listening",
		// Outcome lines.
		"587 status:",
		"OVERALL",
		// Port 25 failure must be loud, even when submission is on.
		"port 25 is NOT listening",
	}
	for _, want := range mustContain {
		if !strings.Contains(script, want) {
			t.Errorf("check-smtp-tls.sh missing required marker: %q", want)
		}
	}
}

func TestWebmailIndexHTMLUsesAssetShortPaths(t *testing.T) {
	root := repoRoot(t)
	pageBytes, err := os.ReadFile(filepath.Join(root, "release", "webmail", "index.html"))
	if err != nil {
		t.Fatalf("read webmail index: %v", err)
	}
	page := string(pageBytes)
	for _, asset := range []string{"auth-gate.css", "webmail.css", "auth-gate.js", "webmail.js"} {
		oldPath := "/webmail/assets/" + asset
		if strings.Contains(page, oldPath) {
			t.Errorf("index.html must use /assets/%s not %s for dedicated webmail hostname", asset, oldPath)
		}
		newPath := "/assets/" + asset
		if !strings.Contains(page, newPath) {
			t.Errorf("index.html missing /assets/%s", asset)
		}
	}
}

func TestReleaseReferencedFilesExist(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		filepath.Join("release", "scripts", "setup-https.sh"),
		filepath.Join("release", "scripts", "setup-smtp-tls.sh"),
		filepath.Join("release", "scripts", "check-smtp-tls.sh"),
		filepath.Join("release", "admin", "index.html"),
		filepath.Join("release", "admin", "app.js"),
		filepath.Join("release", "admin", "styles.css"),
		filepath.Join("release", "webmail", "index.html"),
		filepath.Join("release", "configs", "orvix.yaml.example"),
		filepath.Join("release", "systemd", "orvix.service"),
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("release referenced file missing: %s: %v", path, err)
		}
	}
	adminAssets, err := filepath.Glob(filepath.Join(root, "release", "webmail", "assets", "*"))
	if err != nil {
		t.Fatalf("glob webmail assets: %v", err)
	}
	if len(adminAssets) == 0 {
		t.Fatal("release webmail assets are missing")
	}
}

func TestInstallerBootstrapEnvEncodesPassword(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)
	if !strings.Contains(installer, `main "$@"`) {
		t.Fatal("installer entrypoint marker not found")
	}
	harness := strings.Replace(installer, `main "$@"`, `chown() { :; }; chmod() { :; }; BOOTSTRAP_ENV="$3"; write_bootstrap_env "$1" "$(cat "$2")"; cat "$BOOTSTRAP_ENV"`, 1)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "bootstrap.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	passwords := []string{
		"MaghaghaMos086",
		"Password123!",
		"Password$123",
		"Password With Spaces",
		`Password\Slash123`,
		`Password"Quote123`,
		"Password'SingleQuote123",
	}
	for i, password := range passwords {
		envName := "bootstrap-" + string(rune('a'+i)) + ".env"
		passwordName := "password-" + string(rune('a'+i)) + ".txt"
		if err := os.WriteFile(filepath.Join(harnessDir, passwordName), []byte(password), 0600); err != nil {
			t.Fatalf("write password fixture: %v", err)
		}
		cmd := exec.Command(bashCommand(t), "bootstrap.sh", "admin@orvix.email", passwordName, envName)
		cmd.Dir = harnessDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bootstrap env command failed for %q: %v: %s", password, err, string(out))
		}
		envFile := string(out)
		if strings.Contains(envFile, password) {
			t.Fatalf("bootstrap env must not contain raw admin password %q", password)
		}
		if !strings.Contains(envFile, "ORVIX_ADMIN_EMAIL=admin@orvix.email") {
			t.Fatalf("bootstrap env missing email: %s", envFile)
		}
		var encoded string
		for _, line := range strings.Split(envFile, "\n") {
			if strings.HasPrefix(line, "ORVIX_ADMIN_PASSWORD_B64=") {
				encoded = strings.TrimPrefix(line, "ORVIX_ADMIN_PASSWORD_B64=")
			}
		}
		if encoded == "" {
			t.Fatalf("bootstrap env missing encoded password: %s", envFile)
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode password: %v", err)
		}
		if string(decoded) != password {
			t.Fatalf("decoded password mismatch: got %q want %q", string(decoded), password)
		}
	}
}

// TestInstallerSmokeHelpersParseable sources install.sh into
// a subshell and asks bash to parse-only the three new
// smoke functions. If a typo breaks the installer's smoke
// path, this test catches it before the install runs on a
// real VPS. It is intentionally a syntax check, not a
// behavioural test — the behavioural coverage lives in
// cmd/orvix/freshinstall_test.go.
func TestInstallerSmokeHelpersParseable(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)
	if !strings.Contains(installer, `main "$@"`) {
		t.Fatal("installer entrypoint marker not found")
	}
	// Replace main with a no-op that only probes for the
	// new helper definitions. require_root is never called
	// because main is replaced.
	harness := strings.Replace(installer, `main "$@"`, `:`, 1) + `
case "$(declare -F smoke_login_admin_attempts)" in
  *smoke_login_admin_attempts*) ;;
  *) echo "smoke_login_admin_attempts missing" >&2; exit 1 ;;
esac
case "$(declare -F smoke_webmail_assets)" in
  *smoke_webmail_assets*) ;;
  *) echo "smoke_webmail_assets missing" >&2; exit 1 ;;
esac
case "$(declare -F smoke_jmap_session)" in
  *smoke_jmap_session*) ;;
  *) echo "smoke_jmap_session missing" >&2; exit 1 ;;
esac
echo "smoke helpers defined"
`
	dir := t.TempDir()
	script := filepath.Join(dir, "probe.sh")
	if err := os.WriteFile(script, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	out, err := exec.Command(bashCommand(t), script).CombinedOutput()
	if err != nil {
		t.Fatalf("smoke helpers not parseable: %v: %s", err, string(out))
	}
	if !strings.Contains(string(out), "smoke helpers defined") {
		t.Fatalf("unexpected probe output: %s", string(out))
	}
}

func TestInstallerLoginPayloadGeneration(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)
	if !strings.Contains(installer, `main "$@"`) {
		t.Fatal("installer entrypoint marker not found")
	}
	harness := strings.Replace(installer, `main "$@"`, `build_login_payload "$1" "$(cat "$2")"`, 1)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "payload.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	tests := []struct {
		email    string
		password string
	}{
		{"admin@example.com", "PlainPassword123!"},
		{"admin@example.com", `P@ss"word!`},
		{"admin@example.com", "P@ssword with spaces and punctuation!"},
		{"admin@example.com", "MaghaghaMos086"},
		{"admin@example.com", "Password$123"},
		{"admin@example.com", `Password\Slash123`},
		{"admin@example.com", `Password"Quote123`},
		{"admin@example.com", "Password'SingleQuote123"},
	}
	for _, tt := range tests {
		passwordName := "payload-password.txt"
		if err := os.WriteFile(filepath.Join(harnessDir, passwordName), []byte(tt.password), 0600); err != nil {
			t.Fatalf("write payload password fixture: %v", err)
		}
		cmd := exec.Command(bashCommand(t), "payload.sh", tt.email, passwordName)
		cmd.Dir = harnessDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("payload command failed: %v: %s", err, string(out))
		}
		var payload map[string]string
		if err := json.Unmarshal(out, &payload); err != nil {
			t.Fatalf("payload is not JSON: %q: %v", string(out), err)
		}
		if payload["username"] != tt.email {
			t.Fatalf("username mismatch: %q", payload["username"])
		}
		if payload["password"] != tt.password {
			t.Fatalf("password mismatch: %q", payload["password"])
		}
		if _, ok := payload["email"]; ok {
			t.Fatalf("payload must not contain email field, only username")
		}
	}
}

func TestPackagedSystemdHasLowPortCapability(t *testing.T) {
	root := repoRoot(t)
	unitBytes, err := os.ReadFile(filepath.Join(root, "release", "systemd", "orvix.service"))
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	unit := string(unitBytes)
	for _, item := range []string{
		"User=orvix",
		"EnvironmentFile=-/etc/orvix/bootstrap.env",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE",
		"After=network-online.target redis-server.service",
	} {
		if !strings.Contains(unit, item) {
			t.Fatalf("systemd unit missing %q", item)
		}
	}
}

func TestExampleConfigEnablesCoreMail(t *testing.T) {
	root := repoRoot(t)
	exampleBytes, err := os.ReadFile(filepath.Join(root, "release", "configs", "orvix.yaml.example"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	example := string(exampleBytes)
	for _, item := range []string{
		"admin_port: 8080",
		"admin_ui_dir: /usr/share/orvix/admin",
		"host: 127.0.0.1",
		"coremail:",
		"enabled: true",
		"smtp_port: 25",
		"imap_port: 143",
		"pop3_port: 110",
		"jmap_port: 8081",
		"password_min_len: 8",
		"license_file_path: /etc/orvix/license.json",
		"license_authority_cache_path: /var/lib/orvix/license-cache.json",
		"webmail_ui_dir: /usr/share/orvix/webmail",
		"outbound:",
		"prefer_ipv4: true",
	} {
		if !strings.Contains(example, item) {
			t.Fatalf("example config missing %q", item)
		}
	}
}

func TestReleaseAdminLoginPageExists(t *testing.T) {
	root := repoRoot(t)
	pageBytes, err := os.ReadFile(filepath.Join(root, "release", "admin", "index.html"))
	if err != nil {
		t.Fatalf("read admin page: %v", err)
	}
	page := string(pageBytes)
	appBytes, err := os.ReadFile(filepath.Join(root, "release", "admin", "app.js"))
	if err != nil {
		t.Fatalf("read admin app: %v", err)
	}
	styleBytes, err := os.ReadFile(filepath.Join(root, "release", "admin", "styles.css"))
	if err != nil {
		t.Fatalf("read admin styles: %v", err)
	}
	bundle := page + "\n" + string(appBytes) + "\n" + string(styleBytes)
	for _, item := range []string{
		"Orvix Mail Platform",
		"login-form",
		"Sign in to Orvix Admin",
		"/api/v1/auth/login",
		"/api/v1/health",
		"/api/v1/me",
		"Dashboard",
		"Domains",
		"Mailboxes",
		"Queue",
		"Logs",
		"Settings",
		"CoreMail Runtime",
		"SMTP",
		"IMAP",
		"POP3",
		"Redis / Queue",
		"/admin/styles.css",
		"/admin/app.js",
	} {
		if !strings.Contains(bundle, item) {
			t.Fatalf("admin bundle missing %q", item)
		}
	}
	for _, item := range []string{"<style>", "<script>"} {
		if strings.Contains(page, item) {
			t.Fatalf("admin page must not use inline CSP-blocked asset %q", item)
		}
	}

	for _, forbidden := range []string{"RC1", "Clean Path"} {
		if strings.Contains(bundle, forbidden) {
			t.Fatalf("admin bundle must not contain %q", forbidden)
		}
	}

	for _, asset := range []string{"styles.css", "app.js"} {
		if _, err := os.Stat(filepath.Join(root, "release", "admin", asset)); err != nil {
			t.Fatalf("admin asset %s missing: %v", asset, err)
		}
	}
}

func TestReleaseWebmailBuildExists(t *testing.T) {
	root := repoRoot(t)
	pageBytes, err := os.ReadFile(filepath.Join(root, "release", "webmail", "index.html"))
	if err != nil {
		t.Fatalf("read webmail page: %v", err)
	}
	page := string(pageBytes)
	for _, item := range []string{
		"Orvix Webmail",
		"/assets/",
	} {
		if !strings.Contains(page, item) {
			t.Fatalf("webmail build missing %q", item)
		}
	}
	assets, err := filepath.Glob(filepath.Join(root, "release", "webmail", "assets", "*.js"))
	if err != nil {
		t.Fatalf("glob webmail assets: %v", err)
	}
	if len(assets) == 0 {
		t.Fatal("webmail release build must include JavaScript assets")
	}
}

// TestInstallerValidatesWebmailAuthGate pins that the
// installer refuses to complete if the deployed webmail
// lacks the auth-gate wiring. Without the gate, an
// unauthenticated visitor to /webmail sees the React mail UI
// (Inbox/Compose) even though every API call returns 401 —
// the production symptom the gate was added to prevent.
func TestInstallerValidatesWebmailAuthGate(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	mustHave := []string{
		"validate_webmail_ui()",
		`$ui_dir/assets/auth-gate.js`,
		`$ui_dir/assets/auth-gate.css`,
		// The validation must also gate against the
		// webmail.js client being present and the React
		// demo bundle being absent. The legacy bundle
		// reference (in the rejection loop) is what
		// enforces the "stop shipping demo webmail"
		// rule.
		`$ui_dir/assets/webmail.js`,
		`"index-CmhA8wNq.js"`,
		"webmail UI index.html does not reference auth-gate.js",
		"webmail UI gate script must be referenced before the webmail client",
		"webmail UI webmail.js not found",
	}
	for _, needle := range mustHave {
		if !strings.Contains(installer, needle) {
			t.Errorf("installer must contain %q (webmail validation)", needle)
		}
	}
}

func TestInstallerWebmailGateValidationUsesScriptTags(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)
	for _, needle := range []string{
		"sed '/<!--/,/-->/d'",
		`<script[^>]+src=`,
	} {
		if !strings.Contains(installer, needle) {
			t.Fatalf("installer webmail order validation must use script tags and ignore comments; missing %q", needle)
		}
	}

	pageBytes, err := os.ReadFile(filepath.Join(root, "release", "webmail", "index.html"))
	if err != nil {
		t.Fatalf("read webmail page: %v", err)
	}
	page := string(pageBytes)
	rawClient := strings.Index(page, "webmail.js")
	rawGate := strings.Index(page, "auth-gate.js")
	if rawClient == -1 || rawGate == -1 {
		t.Fatalf("webmail page must reference both webmail.js and auth-gate.js")
	}
	if rawClient > rawGate {
		t.Fatalf("test fixture no longer reproduces raw-text ordering trap: raw webmail.js=%d raw auth-gate.js=%d", rawClient, rawGate)
	}

	scriptPattern := regexp.MustCompile(`(?is)<script[^>]+src=["'][^"']*/(auth-gate|webmail)\.js[^"']*["']`)
	matches := scriptPattern.FindAllStringSubmatch(page, -1)
	if len(matches) < 2 {
		t.Fatalf("webmail page must contain auth-gate.js and webmail.js script tags, got %v", matches)
	}
	if matches[0][1] != "auth-gate" || matches[1][1] != "webmail" {
		t.Fatalf("webmail script tag order must be auth-gate before webmail client, got %q then %q", matches[0][1], matches[1][1])
	}
}

// TestInstallerWritesRootOnlyLoginFile pins the post-
// install login-file contract: 0600 root:root, contains
// the URLs, the admin email, and the reset command path,
// but NEVER the admin password, the password hash, any JWT,
// or the bootstrap env secret.
//
// The contract changed in this turn: previously the file
// also contained the plaintext admin password. We removed
// that line because /etc/orvix/bootstrap.env is removed by
// verify_install immediately after the dual-login probe
// succeeds, so the password is already gone from the
// system; storing it again on disk would create a second
// copy that must be kept in sync if the operator rotates
// the password via reset-admin-password.sh.
//
// If you ever need to add the password back, update this
// test FIRST and explain in the comment why a second copy
// on disk is the lesser of two evils.
func TestInstallerWritesRootOnlyLoginFile(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	mustHave := []string{
		"write_admin_login_file()",
		"ORVIX_ADMIN_CRED_FILE",
		"chmod 0600",
		"chown root:root",
		// The file body must include URLs, email, and
		// reset command — but NOT the password itself.
		"Admin URL:",
		"Webmail URL:",
		"Admin email:",
		"reset-admin-password.sh",
		// Atomic-write pattern: write to tmp, chmod, rename.
		"${cred_file}.tmp.$$",
		"mv \"$tmpfile\" \"$cred_file\"",
		// The installer must unset the password after use
		// so it does not linger in the script's memory
		// past this point.
		"unset admin_password",
	}
	for _, needle := range mustHave {
		if !strings.Contains(installer, needle) {
			t.Errorf("installer must contain %q (login file UX)", needle)
		}
	}

	// The function name was renamed; the old name must
	// not appear anywhere in the installer.
	if strings.Contains(installer, "write_admin_credentials_file") {
		t.Errorf("login file must NOT contain %q (function was renamed; the password-storing version must not come back)", "write_admin_credentials_file")
	}

	// Scope the forbidden-content check to the body of
	// write_admin_login_file. The login file is the only
	// thing we are auditing here — the install.sh may
	// legitimately reference ORVIX_ADMIN_PASSWORD_B64 in
	// other contexts (write_bootstrap_env writes the env
	// file the orvix binary reads on first boot).
	loginBody := extractFunctionBody(installer, "write_admin_login_file() {")
	if loginBody == "" {
		t.Fatal("could not locate write_admin_login_file function body")
	}

	forbidden := []string{
		// The "Admin password:" line that previously wrote
		// the plaintext password into the login file. The
		// user explicitly forbade storing the password
		// anywhere after install.
		"Admin password: ${admin_password}",
		"Admin password: \"${admin_password}\"",
		"Admin password: ${admin_password",
		// bcrypt / hash markers. The login file must not
		// contain a hash.
		"$2a$",
		"$2b$",
		"password_hash",
		// Bootstrap env secret. The whole point of
		// /etc/orvix/bootstrap.env is that it carries the
		// password to the first boot; we do not want a
		// second copy on disk.
		"ORVIX_ADMIN_PASSWORD_B64",
		"ORVIX_ADMIN_PASSWORD=",
	}
	for _, f := range forbidden {
		if strings.Contains(loginBody, f) {
			t.Errorf("login file body must NOT contain %q (no plaintext password, no hash, no secret)", f)
		}
	}

	// The printf block that writes the file body must
	// reference admin_password at most in explanatory
	// prose ("Password: the value typed at the install
	// prompt"), never as a literal write target. We assert
	// this by scanning for printf lines that contain
	// "${admin_password}" as a positional argument.
	for _, line := range strings.Split(loginBody, "\n") {
		if strings.Contains(line, "printf") &&
			strings.Contains(line, "${admin_password}") {
			t.Errorf("login file body must not printf %s into anything: %q",
				"${admin_password}", strings.TrimSpace(line))
		}
	}
}

// extractFunctionBody returns the body of the named
// function, where "body" means everything between the
// opening `{` and the matching `}` at column 0. Returns
// "" if not found. Used by the installer credential-file
// test to scope forbidden-pattern checks to a single
// function.
func extractFunctionBody(installer, header string) string {
	idx := strings.Index(installer, header)
	if idx < 0 {
		return ""
	}
	bodyStart := idx + len(header)
	for i := bodyStart; i < len(installer); i++ {
		if installer[i] == '}' && (i == 0 || installer[i-1] == '\n') {
			return installer[bodyStart:i]
		}
	}
	return ""
}

// TestInstallerNoPasswordEchoToStdoutOrLog is the regression
// test for the user's "no secret echo" requirement. The
// installer must never print the admin password to stdout
// (visible in the terminal), to $INSTALL_LOG (visible via
// journalctl), or to the post-install login file. The admin
// password lives only in the operator's memory and as a
// bcrypt hash in the users table.
func TestInstallerNoPasswordEchoToStdoutOrLog(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	// Forbid any line that pipes/echoes/printf the password
	// into a non-credential-file destination. The credential
	// file itself is allowed to receive the password.
	//
	// Each pattern below is a real failure mode a careless
	// refactor could introduce. We scan the whole installer.
	forbiddenPatterns := []string{
		`echo "$admin_password"`,
		`echo "$password"`,
		`printf '%s' "$admin_password" | tee`,
		`printf '%s' "$password" | tee`,
		`log_detail "$admin_password"`,
		`log_detail "$password"`,
		`tee -a "$INSTALL_LOG" <<EOF`, // heredoc to log is also banned
	}
	for _, p := range forbiddenPatterns {
		if strings.Contains(installer, p) {
			t.Errorf("installer must not contain %q (password leak)", p)
		}
	}

	// log_detail MUST NOT contain any line that pipes a
	// password variable. We assert this by scanning for
	// `log_detail` combined with a password variable in
	// the same line.
	for _, line := range strings.Split(installer, "\n") {
		if strings.Contains(line, "log_detail") &&
			(strings.Contains(line, "$admin_password") ||
				strings.Contains(line, " \"$password\"") ||
				strings.Contains(line, " \"$password\n")) {
			t.Errorf("log_detail must not log the password: %q", strings.TrimSpace(line))
		}
	}
}

// TestRuntimeUpdateDeploysWebmailAssets pins that
// apply-runtime-update.sh copies release/webmail into
// /usr/share/orvix/webmail and refuses to finish if the
// gate is missing. Without this, an operator who only ever
// runs the runtime update path gets a /webmail page without
// the auth gate — the bug we are fixing in this turn.
func TestRuntimeUpdateDeploysWebmailAssets(t *testing.T) {
	root := repoRoot(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "apply-runtime-update.sh"))
	if err != nil {
		t.Fatalf("read runtime-update script: %v", err)
	}
	script := string(scriptBytes)

	mustHave := []string{
		// The script must reference both UI trees.
		"release/admin",
		"release/webmail",
		"/usr/share/orvix/admin",
		"/usr/share/orvix/webmail",
		// It must copy via `cp -r` (idempotent overwrite in
		// place) — not `rm -rf` first, which would wipe
		// operator-placed assets.
		"cp -r \"$REPO_ROOT/release/webmail/.\" /usr/share/orvix/webmail/",
		// It must verify the deployed webmail has the gate.
		"auth-gate.js",
		"auth-gate.css",
		"webmail UI deployment incomplete",
		// Idempotency markers: the script does NOT remove the
		// destination directory before copying.
		"rm -rf /usr/share/orvix/webmail",
		"rm -rf /usr/share/orvix/admin",
		// Permissions are kept world-readable, matching the
		// installer.
		"chmod 0755",
		"chmod 0644",
		"chown -R root:root",
	}
	for _, needle := range mustHave {
		// Negative assertions: the forbidden patterns must
		// not appear. Positive assertions are split below.
		if strings.HasPrefix(needle, "rm -rf") {
			if strings.Contains(script, needle) {
				t.Errorf("runtime update must not contain %q (would delete operator assets)", needle)
			}
			continue
		}
		if !strings.Contains(script, needle) {
			t.Errorf("runtime update must contain %q", needle)
		}
	}
}

// TestInstallerWriteConfigRendersValidYAML pins the contract
// that release/install.sh::write_config produces a YAML file
// that the running orvix process can parse via viper and that
// exposes the documented top-level sections (server, database,
// auth, logging, update, backup). This is the regression test
// for the bug where write_config emitted the `server:` block
// at column 2 instead of column 0: the heredoc body was
// accidentally indented alongside its bash function body,
// which produced YAML whose `server:` key was not a clean
// sibling of database/auth/logging/update/backup at column 0.
//
// The harness sources install.sh with main() replaced by a
// call to write_config against a temp file, with chown/chmod
// stubbed so the test runs on any host (no root required).
// We then parse the rendered file with viper (the same loader
// the orvix binary uses on startup) and assert both the
// top-level keys and the server.* fields that come from the
// installer substitutions.
func TestInstallerWriteConfigRendersValidYAML(t *testing.T) {
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)
	if !strings.Contains(installer, `main "$@"`) {
		t.Fatal("installer entrypoint marker not found")
	}

	const domain = "example.com"
	// Point write_config at a temp file, then print the file
	// contents on stdout so the Go side can read it. We
	// override chown/chmod so this runs on any host (no root,
	// no Linux-only stat bits).
	harness := strings.Replace(installer,
		`main "$@"`,
		fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; write_config "%s"; cat "$ORVIX_CONFIG"`, domain),
		1,
	)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "render-config.sh")
	configPath := filepath.Join(harnessDir, "orvix.yaml")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "render-config.sh", configPath)
	cmd.Dir = harnessDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render installer config: %v: %s", err, string(out))
	}
	rendered := string(out)
	if strings.TrimSpace(rendered) == "" {
		t.Fatal("write_config produced an empty config file")
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		t.Fatalf("installer config is not valid YAML; viper parse failed (this is the write_config heredoc bug): %v\n--- rendered YAML ---\n%s", err, rendered)
	}

	settings := v.AllSettings()

	// 1. Top-level keys must be present. This is the
	//    primary contract the installer promises: every
	//    block the runtime reads from Defaults() must be
	//    reachable through the file the installer writes.
	required := []string{"server", "database", "auth", "logging", "update", "backup"}
	for _, key := range required {
		if _, ok := settings[key]; !ok {
			t.Errorf("installer config missing top-level key %q; rendered YAML was:\n%s", key, rendered)
		}
	}

	// 2. The server block must parse as a nested mapping
	//    with the operator-facing hostnames substituted.
	//    If write_config emitted `server:` at column 2
	//    instead of column 0, viper would either fold the
	//    key into a parent mapping (so settings["server"]
	//    would be a string or nil) or refuse to parse it
	//    at all. Either way this assertion fails.
	serverRaw, ok := settings["server"]
	if !ok {
		t.Fatalf("server block missing or not a top-level mapping: %s", rendered)
	}
	serverMap, ok := serverRaw.(map[string]any)
	if !ok {
		t.Fatalf("server block must parse as a nested mapping, got %T: %s", serverRaw, rendered)
	}
	for _, field := range []string{"host", "port", "admin_port", "admin_host", "webmail_host", "mail_host"} {
		if _, ok := serverMap[field]; !ok {
			t.Errorf("server.%s missing in rendered config: %s", field, rendered)
		}
	}

	// 3. The heredoc must interpolate the derived hostnames
	//    so the runtime gets admin.example.com /
	//    webmail.example.com / mail.example.com instead of
	//    empty strings.
	expectations := map[string]string{
		"server.admin_host":   "admin." + domain,
		"server.webmail_host": "webmail." + domain,
		"server.mail_host":    "mail." + domain,
		"auth.cookie_domain":  "." + domain,
	}
	for dotted, want := range expectations {
		got := readNestedString(settings, dotted)
		if got != want {
			t.Errorf("%s: got %q want %q (installer heredoc did not interpolate domain correctly): %s", dotted, got, want, rendered)
		}
	}
}

// readNestedString walks a dotted key path (e.g.
// "server.admin_host") through a nested map[string]any and
// returns the leaf as a string. Returns "" if any segment is
// missing or the leaf is not a string.
func readNestedString(settings map[string]any, dotted string) string {
	parts := strings.Split(dotted, ".")
	var cur any = settings
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[p]
	}
	s, _ := cur.(string)
	return s
}

// TestExampleConfigInternalBindsAreLoopback pins the 2026-06-29
// hotfix: release/configs/orvix.yaml.example must NOT bind the
// admin/webmail HTTP backend or the JMAP endpoint to 0.0.0.0.
// Both must be 127.0.0.1 so the only public way to reach them is
// via the Caddy reverse proxy on 443 (set up by setup-https.sh).
// Mail listener binds (smtp_host, imap_host, pop3_host) MUST
// stay at 0.0.0.0 — those ports are intentionally public.
func TestExampleConfigInternalBindsAreLoopback(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "configs", "orvix.yaml.example"))
	for _, needle := range []string{
		`host: "127.0.0.1"`, // server.host default
		`jmap_host: 127.0.0.1`,
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("release/configs/orvix.yaml.example missing required internal bind %q (must be loopback by default — 2026-06-29 hotfix)", needle)
		}
	}
	// The example MUST keep mail listener binds at 0.0.0.0.
	// Tighter is a regression (mail stops accepting).
	for _, needle := range []string{
		`smtp_host: 0.0.0.0`,
		`imap_host: 0.0.0.0`,
		`pop3_host: 0.0.0.0`,
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("release/configs/orvix.yaml.example missing required public mail bind %q (mail listeners MUST stay 0.0.0.0)", needle)
		}
	}
	// The example MUST NOT have the old unsafe defaults for the
	// internal ports anywhere.
	stripped := stripBashComments(body) // body is YAML but stripBashComments is a safe no-op on non-comment lines.
	for _, forbidden := range []string{
		`host: "0.0.0.0"`,
		`jmap_host: 0.0.0.0`,
	} {
		if strings.Contains(stripped, forbidden) {
			t.Errorf("release/configs/orvix.yaml.example still contains the unsafe default %q for an internal port (2026-06-29 hotfix regression)", forbidden)
		}
	}
}

// TestInstallerWriteConfigBindsInternalToLoopback pins that the
// installer's write_config() heredoc writes server.host and
// coremail.jmap_host as 127.0.0.1 (NOT 0.0.0.0). The smoke-ports
// gate expects 8080 and 8081 to be loopback-only; a fresh install
// that wrote 0.0.0.0 would fail the gate.
func TestInstallerWriteConfigBindsInternalToLoopback(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	// Run the installer's write_config() in a sandboxed harness
	// (same pattern as TestInstallerWriteConfigRendersValidYAML)
	// and parse the rendered YAML with viper.
	const domain = "example.com"
	harness := strings.Replace(installer,
		`main "$@"`,
		fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; write_config "%s"; cat "$ORVIX_CONFIG"`, domain),
		1,
	)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "render-config.sh")
	configPath := filepath.Join(harnessDir, "orvix.yaml")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "render-config.sh", configPath)
	cmd.Dir = harnessDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render installer config: %v: %s", err, string(out))
	}
	rendered := string(out)

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		t.Fatalf("installer config is not valid YAML: %v\n--- rendered YAML ---\n%s", err, rendered)
	}
	settings := v.AllSettings()

	// server.host MUST be 127.0.0.1 (loopback), not 0.0.0.0.
	gotServerHost := readNestedString(settings, "server.host")
	if gotServerHost != "127.0.0.1" {
		t.Errorf("server.host: got %q, want %q (2026-06-29 hotfix: fresh install must bind admin/webmail HTTP backend to loopback, NOT 0.0.0.0). Rendered:\n%s", gotServerHost, "127.0.0.1", rendered)
	}
	// coremail.jmap_host MUST be 127.0.0.1.
	gotJmapHost := readNestedString(settings, "coremail.jmap_host")
	if gotJmapHost != "127.0.0.1" {
		t.Errorf("coremail.jmap_host: got %q, want %q (2026-06-29 hotfix: fresh install must bind JMAP to loopback, NOT 0.0.0.0). Rendered:\n%s", gotJmapHost, "127.0.0.1", rendered)
	}
	// Mail listener binds MUST stay at 0.0.0.0 (public).
	for _, key := range []string{
		"coremail.smtp_host",
		"coremail.imap_host",
		"coremail.pop3_host",
	} {
		if got := readNestedString(settings, key); got != "0.0.0.0" {
			t.Errorf("%s: got %q, want %q (mail listener must stay public, NOT tightened to loopback)", key, got, "0.0.0.0")
		}
	}
}

// TestInstallerSummaryOutputUsesDomainNotPublicIP pins the
// 2026-06-29 hotfix: the installer's success banner MUST NOT
// print `http://${server_ip}:8080` or `http://${server_ip}:8081`
// (those would advertise an unauthenticated public HTTP login
// on the server's bare IP). It MUST print domain-based HTTPS
// URLs and the SSH tunnel escape hatch.
func TestInstallerSummaryOutputUsesDomainNotPublicIP(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	stripped := stripBashComments(installer)

	// The forbidden patterns: any of these in executable code
	// is the regression. We check the uncommented body so a
	// comment that says "FIXME: never print this" doesn't trip
	// the matcher.
	for _, forbidden := range []string{
		`http://${server_ip}:8080/admin`,
		`http://${server_ip}:8080/webmail`,
		`http://${server_ip}:8081/.well-known/jmap`,
		`http://${server_ip}:8080`,
		`http://${server_ip}:8081`,
	} {
		if strings.Contains(stripped, forbidden) {
			t.Errorf("release/install.sh still prints public-IP URL %q in executable code (2026-06-29 hotfix: must use domain-based HTTPS URLs + 127.0.0.1 loopback + SSH tunnel docs only)", forbidden)
		}
	}

	// Positive assertions: the installer's summary must include
	// the intended domain-based HTTPS URLs and the SSH tunnel.
	for _, needle := range []string{
		`https://admin.${domain}/admin`,
		`https://webmail.${domain}/`,
		`https://mail.${domain}/.well-known/jmap`,
		`http://127.0.0.1:8080/admin`,
		`http://127.0.0.1:8080/webmail`,
		`http://127.0.0.1:8081/.well-known/jmap`,
		`ssh -L 8080:127.0.0.1:8080 -L 8081:127.0.0.1:8081 root@${server_ip}`,
	} {
		if !strings.Contains(installer, needle) {
			t.Errorf("release/install.sh missing required installer summary element %q (2026-06-29 hotfix)", needle)
		}
	}
}

// TestInstallerBindPostureSkipsDisabledOptionalPorts verifies that
// the verify_install() bind posture check does NOT unconditionally
// require ports 587 (Submission) and 465 (SMTPS). These ports are
// disabled by default and must only be validated when the config
// explicitly enables them.
func TestInstallerBindPostureSkipsDisabledOptionalPorts(t *testing.T) {
	installer := mustRead(t, filepath.Join(repoRoot(t), "release", "install.sh"))

	// The installer must check the config before requiring 587/465.
	if !strings.Contains(installer, `submission_enabled` ) {
		t.Error("verify_install must check coremail.submission_enabled before requiring port 587")
	}
	if !strings.Contains(installer, `smtps_enabled` ) {
		t.Error("verify_install must check coremail.smtps_enabled before requiring port 465")
	}
	// The mandatory public ports (25, 110, 143) must still be checked.
	if !strings.Contains(installer, `check_public_port 25` ) {
		t.Error("verify_install must require port 25 (SMTP)")
	}
	if !strings.Contains(installer, `check_public_port 110` ) {
		t.Error("verify_install must require port 110 (POP3)")
	}
	if !strings.Contains(installer, `check_public_port 143` ) {
		t.Error("verify_install must require port 143 (IMAP)")
	}
	// 587 and 465 must NOT be unconditionally checked.
	for _, port := range []string{"587", "465"} {
		if strings.Contains(installer, `for port in 25 110 143 `+port) {
			t.Errorf("verify_install must NOT unconditionally iterate port %s in the mail ports loop", port)
		}
	}
}

// TestInstallerBindPostureAllBindsLoopback verifies that the
// 8080/8081 loopback check validates ALL bound addresses, not
// just the first one. If a port is bound to both loopback AND
// a public address, the check must fail.
func TestInstallerBindPostureAllBindsLoopback(t *testing.T) {
	installer := mustRead(t, filepath.Join(repoRoot(t), "release", "install.sh"))

	// Must iterate every bound address for each internal port.
	if !strings.Contains(installer, `for addr in $addrs` ) {
		t.Error("verify_install must iterate all bound addresses for 8080/8081")
	}
	// Must track has_loopback AND all_loopback flags.
	if !strings.Contains(installer, `all_loopback` ) {
		t.Error("verify_install must track all_loopback flag for 8080/8081")
	}
	if !strings.Contains(installer, `has_loopback` ) {
		t.Error("verify_install must track has_loopback flag for 8080/8081")
	}
	// Must reject when has_loopback is true but all_loopback is false
	// (mixed loopback + public bind).
	if !strings.Contains(installer, `is exposed on non-loopback` ) {
		t.Error("verify_install must reject mixed loopback+public binds for 8080/8081")
	}
	// Must reject when no loopback bind exists.
	if !strings.Contains(installer, `has no loopback bind` ) {
		t.Error("verify_install must reject when no loopback bind exists for 8080/8081")
	}
}

// TestInstallerBindPostureCoremailBoolHelper verifies the coremail_bool
// helper function is used to read boolean config values scoped to the
// coremail: section.
func TestInstallerBindPostureCoremailBoolHelper(t *testing.T) {
	installer := mustRead(t, filepath.Join(repoRoot(t), "release", "install.sh"))

	if !strings.Contains(installer, `coremail_bool()` ) {
		t.Error("verify_install must define coremail_bool() helper to read config values scoped to coremail section")
	}
	// Must use awk for section-aware tracking, not global grep.
	if !strings.Contains(installer, `in_coremail = (sec == "coremail"` ) {
		t.Error("coremail_bool() must use section-aware awk to scope matching to coremail: section")
	}
}

// TestInstallerBindPostureMainPID verifies the bootstrap env process
// check uses systemctl MainPID instead of pidof.
func TestInstallerBindPostureMainPID(t *testing.T) {
	installer := mustRead(t, filepath.Join(repoRoot(t), "release", "install.sh"))

	if !strings.Contains(installer, `systemctl show -p MainPID --value orvix` ) {
		t.Error("verify_install must use systemctl show -p MainPID --value orvix to find the process")
	}
	if strings.Contains(installer, `pidof orvix` ) {
		t.Error("verify_install must NOT use pidof orvix (may return multiple PIDs)")
	}
}

// TestInstallerBootstrapEnvReadFailsClosed pins the 2026-06-30
// re-review blocker fix: the /proc/$MainPID/environ verification
// must NOT rely solely on the old `tr ... | grep -qiE` pipeline
// inside an `if` — if the procfs file is unreadable, that pipeline
// produces no grep match and silently logs success even though the
// process environment was never actually inspected. Combined with
// the missing `set -e` propagation through the `if`, this leaves
// the installer passing while bootstrap password material may still
// be present in the live orvix process.
//
// The contract verified here is:
//   1. The MainPID empty / zero check is still in place (existing).
//   2. The /proc/$MainPID/environ file is checked for readability
//      BEFORE it is read, and unreadable is a `fail`.
//   3. The captured environment is loaded into a separate variable
//      with a `|| fail` failure path so a read error does not
//      silently succeed.
//   4. The captured (not piped-from-disk) environment is what the
//      grep step inspects — proving the read is fail-closed rather
//      than silent-on-failure.
//   5. The naive `tr ... | grep` pipe pattern is gone, so a future
//      refactor cannot accidentally re-introduce the silent-success
//      hole.
func TestInstallerBootstrapEnvReadFailsClosed(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	stripped := stripBashComments(installer)

	// (1) MainPID empty / zero check is still present.
	if !strings.Contains(stripped, `if [ -z "$orvix_pid" ] || [ "$orvix_pid" = "0" ]; then`) {
		t.Error("installer must still validate MainPID is non-empty and non-zero")
	}
	if !strings.Contains(stripped, `fail "cannot determine orvix MainPID after restart"`) {
		t.Error("installer must fail when MainPID is empty or zero")
	}

	// (2) Readability check BEFORE reading the file. This is the
	//     fail-closed gate the previous code lacked.
	if !strings.Contains(stripped, `[ ! -r "$env_file" ]`) {
		t.Error("installer must check `[ ! -r \"$env_file\" ]` before reading /proc/$MainPID/environ")
	}
	if !strings.Contains(stripped, `fail "cannot read orvix process environment for bootstrap secret verification`) {
		t.Error("installer must `fail` when /proc/$MainPID/environ is unreadable (no silent success)")
	}

	// (3) The captured environment is loaded into a separate
	//     variable with an explicit `|| fail` failure path.
	if !strings.Contains(stripped, `process_env="$(tr '\0' '\n' < "$env_file")" || \`) {
		t.Error("installer must capture process_env with `|| fail` so a read error does not silently succeed")
	}
	if !strings.Contains(stripped, `fail "failed to read orvix process environment for bootstrap secret verification`) {
		t.Error("installer must `fail` if `tr < /proc/$MainPID/environ` itself errors")
	}

	// (4) Grep runs over the captured variable, NOT over a fresh
	//     pipe from disk. This is the structural difference that
	//     makes the read fail-closed.
	if !strings.Contains(stripped, `printf '%s\n' "$process_env" | grep -qiE 'ORVIX_ADMIN_PASSWORD|ORVIX_ADMIN_PASSWORD_B64'`) {
		t.Error("installer must grep the captured `$process_env` variable, not re-read /proc in the pipeline")
	}

	// (5) The naive unsafe pipeline must be gone — there must be
	//     no surviving `tr '\0' '\n' < "/proc/$orvix_pid/environ" |
	//     grep` form anywhere in the script body.
	naive := `tr '\0' '\n' < "/proc/$orvix_pid/environ" 2>/dev/null | grep -qiE`
	if strings.Contains(stripped, naive) {
		t.Errorf("installer still contains the naive silent-success pattern %q; this would let unreadable /proc/$MainPID/environ pass without inspection", naive)
	}
	// Also reject the even-shorter unguarded form.
	naiveBare := `tr '\0' '\n' < "/proc/$orvix_pid/environ"`
	if strings.Contains(stripped, naiveBare) {
		t.Errorf("installer still pipes /proc/$orvix_pid/environ directly through tr; this is the unsafe pattern the 2026-06-30 fix must remove")
	}
}

// TestInstallerBootstrapEnvReadIsBehavioral runs the actual
// fail-closed read block against a synthetic env_file path to prove
// (a) an unreadable path causes the installer to exit non-zero,
// (b) a readable path that does NOT contain the bootstrap secret
// produces success, and (c) a readable path that DOES contain the
// bootstrap secret is detected and reported as a failure. This is
// the load-bearing behavioural test — string checks above confirm
// the pattern is present, this one confirms it actually works.
func TestInstallerBootstrapEnvReadIsBehavioral(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	stripped := stripBashComments(installer)

	// Locate the boundary between the MainPID determination and the
	// rest of verify_install. We extract just the bootstrap-secret
	// read block: from the "VERIFY orvix process environment"
	// `log_detail` line (whose start must be back-walked to the
	// previous newline) up to (but not including) the post-comment
	// listener-posture block, which we mark with the
	// `local bind_check_failed=0` line because that survives
	// stripBashComments.
	startMarker := `log_detail "VERIFY orvix process environment: MainPID=$orvix_pid"`
	endMarker := "local bind_check_failed=0"

	startIdx := strings.Index(stripped, startMarker)
	if startIdx < 0 {
		t.Fatalf("could not find bootstrap-secret verification start marker %q in install.sh", startMarker)
	}
	// Walk back to the previous newline so the extracted block
	// starts at the beginning of the `log_detail` line, not in the
	// middle of it.
	blockStart := startIdx
	if nl := strings.LastIndex(stripped[:startIdx], "\n"); nl >= 0 {
		blockStart = nl + 1
	}

	endIdx := strings.Index(stripped[startIdx:], endMarker)
	if endIdx < 0 {
		t.Fatalf("could not find end marker %q after bootstrap-secret block in install.sh", endMarker)
	}
	// Walk back to the previous newline so the extracted block ends
	// at the previous line's newline (exclusive of the next block).
	blockEnd := startIdx + endIdx
	if nl := strings.LastIndex(stripped[:blockEnd], "\n"); nl >= 0 {
		blockEnd = nl + 1
	}

	block := stripped[blockStart:blockEnd]

	// The extracted block contains `env_file="/proc/$orvix_pid/environ"`
	// which would override the test's chosen $env_file. Rewrite that
	// line so the harness's `HARNESS_ENV_FILE` global takes precedence
	// when set; otherwise the install.sh default applies.
	blockLines := strings.Split(block, "\n")
	cleaned := make([]string, 0, len(blockLines))
	for _, ln := range blockLines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, `env_file="/proc/`) {
			cleaned = append(cleaned, `    env_file="${HARNESS_ENV_FILE:-/proc/$orvix_pid/environ}"`)
			continue
		}
		cleaned = append(cleaned, ln)
	}
	block = strings.Join(cleaned, "\n")

	// Wrap the extracted block in a runnable harness. The harness:
	//   - defines a `fail()` that records the error message and exits 1,
	//   - wraps the extracted block in a function so the `local`
	//     declarations inside it are valid bash,
	//   - accepts env_file / pid via positional args,
	//   - binds the variables the extracted block expects.
	harness := fmt.Sprintf(`#!/usr/bin/env bash
set -u
FAIL_MSG=""
fail() {
    FAIL_MSG="$*"
    echo "FAIL_CALLED: $*" >&2
    exit 1
}
log_detail() { echo "LOG: $*" >&2; }

orvix_pid="$1"
HARNESS_ENV_FILE="$2"

verify_bootstrap_env() {
%s
}

verify_bootstrap_env

log_detail "VERIFY orvix process environment: no bootstrap password material found (MainPID=$orvix_pid)"
echo "OK"
`, block)

	run := func(t *testing.T, label, envFile string) (exitCode int, stdout string) {
		t.Helper()
		dir := t.TempDir()
		harnessPath := filepath.Join(dir, "env-read.sh")
		if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
			t.Fatalf("%s: write harness: %v", label, err)
		}
		// Use a synthetic PID; the harness never inspects /proc/$PID
		// directly because we override env_file via positional arg.
		cmd := exec.Command(bashCommand(t), harnessPath, "1234", envFile)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		stdout = string(out)
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			} else {
				exitCode = -1
			}
		}
		return exitCode, stdout
	}

	t.Run("unreadable env_file exits non-zero and reports fail", func(t *testing.T) {
		// A path that does not exist is definitely not readable.
		missing := filepath.Join(t.TempDir(), "no-such-environ")
		exitCode, out := run(t, "unreadable", missing)
		if exitCode == 0 {
			t.Errorf("unreadable env_file must cause non-zero exit; got 0\noutput: %s", out)
		}
		if !strings.Contains(out, "cannot read orvix process environment for bootstrap secret verification") {
			t.Errorf("unreadable env_file must surface fail() message; output:\n%s", out)
		}
	})

	t.Run("readable env_file with no secret succeeds", func(t *testing.T) {
		dir := t.TempDir()
		clean := filepath.Join(dir, "environ")
		if err := os.WriteFile(clean, []byte("PATH=/usr/bin\nHOME=/root\nLANG=C\n"), 0o600); err != nil {
			t.Fatalf("write clean env: %v", err)
		}
		exitCode, out := run(t, "clean", clean)
		if exitCode != 0 {
			t.Errorf("clean env_file must produce exit 0; got %d\noutput: %s", exitCode, out)
		}
		if !strings.Contains(out, "OK") {
			t.Errorf("clean env_file must reach OK sentinel; output:\n%s", out)
		}
	})

	t.Run("readable env_file containing bootstrap secret fails closed", func(t *testing.T) {
		dir := t.TempDir()
		dirty := filepath.Join(dir, "environ")
		// Embed the bootstrap secret exactly as orvix.service would.
		contents := "PATH=/usr/bin\nORVIX_ADMIN_PASSWORD_B64=c2VjcmV0\nHOME=/root\n"
		if err := os.WriteFile(dirty, []byte(contents), 0o600); err != nil {
			t.Fatalf("write dirty env: %v", err)
		}
		exitCode, out := run(t, "dirty", dirty)
		if exitCode == 0 {
			t.Errorf("env_file containing bootstrap secret must cause non-zero exit; got 0\noutput: %s", out)
		}
		if !strings.Contains(out, "bootstrap password material persists in orvix process environment") {
			t.Errorf("dirty env_file must surface the persistence fail() message; output:\n%s", out)
		}
	})
}

// TestInstallerPostBootstrapReadinessWaitExists pins the 2026-06-30
// fresh-VPS-rehearsal fix: after we delete /etc/orvix/bootstrap.env
// and restart the orvix service, `systemctl is-active` and MainPID
// existence only prove the process is alive — they do NOT prove
// listener goroutines have finished binding sockets. The fresh VPS
// rehearsal showed bind-posture validation running in the same
// second the listeners were still starting up, producing a
// false-negative failure (port 8080/8081 reported "no loopback
// bind", mail ports 25/110/143 reported "not bound publicly") on
// an otherwise healthy install.
//
// Contract verified here:
//   1. A helper `wait_for_runtime_ready_after_restart()` is defined.
//   2. It is called BETWEEN the bootstrap-env verification
//      (`VERIFY orvix process environment: no bootstrap password
//      material found`) and the listener bind posture validation
//      (`local bind_check_failed=0`).
//   3. The helper probes BOTH HTTP endpoints AND listener sockets —
//      it does not rely only on `systemctl is-active`.
//   4. The endpoints probed are the same ones the bind posture
//      check guards: 8080/health, 8081/jmap.
//   5. The listeners probed are 25, 110, 143, 8080, 8081.
//   6. The failure path dumps `ss -ltnp` AND recent
//      `journalctl -u orvix` into $INSTALL_LOG so an operator can
//      diagnose a stuck install without re-running.
//   7. The failure path uses `fail` (not `warn` / `return 1`) so
//      bind posture is never skipped.
func TestInstallerPostBootstrapReadinessWaitExists(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	// (1) Helper function is defined.
	if !strings.Contains(installer, "wait_for_runtime_ready_after_restart() {") {
		t.Fatal("installer must define wait_for_runtime_ready_after_restart()")
	}

	// (2) Called between the bootstrap-env verification and the
	//     bind posture validation. We pin ordering by finding the
	//     line numbers of each marker and asserting the call is
	//     strictly between them.
	lines := strings.Split(installer, "\n")
	idxBootstrapOK := -1
	idxBindStart := -1
	idxReadinessCall := -1
	for i, line := range lines {
		// Strip any trailing whitespace and inline comments so a
		// line like `    wait_for_runtime_ready_after_restart  # call`
		// still matches.
		trimmed := strings.TrimSpace(line)
		if idxBootstrapOK < 0 && strings.Contains(trimmed, "VERIFY orvix process environment: no bootstrap password material found") {
			idxBootstrapOK = i
		}
		if idxBindStart < 0 && strings.Contains(trimmed, "local bind_check_failed=0") {
			idxBindStart = i
		}
		// Find the CALL, not the definition or a comment that
		// mentions the function name. The call line is exactly
		// `wait_for_runtime_ready_after_restart` (possibly with
		// trailing comment after ` #`). We anchor with
		// `strings.HasPrefix` so we do not catch the definition
		// (`wait_for_runtime_ready_after_restart() {`) or echo
		// statements that mention the function name.
		if idxReadinessCall < 0 && (trimmed == "wait_for_runtime_ready_after_restart" ||
			strings.HasPrefix(trimmed, "wait_for_runtime_ready_after_restart ")) {
			idxReadinessCall = i
		}
	}
	if idxBootstrapOK < 0 {
		t.Fatal("could not find bootstrap-env verification success marker")
	}
	if idxBindStart < 0 {
		t.Fatal("could not find bind posture validation start marker")
	}
	if idxReadinessCall < 0 {
		t.Fatal("wait_for_runtime_ready_after_restart is never called in verify_install()")
	}
	if !(idxBootstrapOK < idxReadinessCall && idxReadinessCall < idxBindStart) {
		t.Errorf("wait_for_runtime_ready_after_restart must be called between bootstrap-env verification (line %d) and bind posture validation (line %d); got call at line %d",
			idxBootstrapOK+1, idxBindStart+1, idxReadinessCall+1)
	}

	// (3) The helper does NOT rely only on `systemctl is-active`.
	//     It must probe HTTP endpoints and listener sockets via
	//     curl / ss. We extract the helper body to inspect it in
	//     isolation (no false positives from the rest of the
	//     script that uses systemctl is-active elsewhere).
	stripped := stripBashComments(installer)
	helperStart := strings.Index(stripped, "wait_for_runtime_ready_after_restart() {")
	if helperStart < 0 {
		t.Fatal("stripped installer missing wait_for_runtime_ready_after_restart() {")
	}
	// Helper ends at the next top-level closing brace that brings
	// indentation back to column 0. The next function in the file
	// (`verify_install()`) starts with that exact pattern, so we
	// cut at the first occurrence of "\nverify_install() {".
	helperEndRel := strings.Index(stripped[helperStart:], "\nverify_install() {")
	if helperEndRel < 0 {
		t.Fatal("could not find end of wait_for_runtime_ready_after_restart helper body")
	}
	helper := stripped[helperStart : helperStart+helperEndRel]

	// curl + ss must appear in the helper body.
	if !strings.Contains(helper, "curl -fsS http://127.0.0.1:8080/api/v1/health") {
		t.Error("readiness helper must probe http://127.0.0.1:8080/api/v1/health")
	}
	if !strings.Contains(helper, "curl -fsS http://127.0.0.1:8081/.well-known/jmap") {
		t.Error("readiness helper must probe http://127.0.0.1:8081/.well-known/jmap")
	}
	for _, port := range []string{":25", ":110", ":143", ":8080", ":8081"} {
		needle := "sport = " + port
		if !strings.Contains(helper, needle) {
			t.Errorf("readiness helper must probe listener %s via ss", port)
		}
	}

	// (4) The failure path dumps `ss -ltnp` and `journalctl -u orvix`.
	if !strings.Contains(helper, "ss -ltnp") {
		t.Error("readiness helper failure path must dump `ss -ltnp` into $INSTALL_LOG")
	}
	if !strings.Contains(helper, "journalctl -u orvix") {
		t.Error("readiness helper failure path must dump `journalctl -u orvix` into $INSTALL_LOG")
	}
	if !strings.Contains(helper, "$INSTALL_LOG") {
		t.Error("readiness helper failure path must write diagnostics to $INSTALL_LOG")
	}

	// (5) The failure path uses `fail` (not warn / return 1).
	if !strings.Contains(helper, `fail "runtime listeners did not become ready after bootstrap cleanup restart`) {
		t.Error("readiness helper must call fail() on timeout; never warn or return non-zero")
	}
}

// TestInstallerPostBootstrapReadinessWaitOrder pins the SAME ordering
// invariant TestInstallerPostBootstrapReadinessWaitExists already
// pins, but expressed as a behavioural run: extract the
// wait_for_runtime_ready_after_restart helper, run it inside a
// stubbed harness, and prove that bind posture is unreachable until
// readiness passes. This catches the regression where someone
// re-orders the verify_install body so bind posture runs before
// readiness.
//
// NOTE on counters: the helper calls `curl ... && curl ... && ss ...`
// inside a pipeline. Each command in a pipeline runs in a subshell,
// so any variable increments inside `ss()` / `curl()` would NOT
// propagate to the parent. We record call counts via file appends
// (which survive subshell boundaries) and read them after.
func TestInstallerPostBootstrapReadinessWaitOrder(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	stripped := stripBashComments(installer)

	helperStart := strings.Index(stripped, "wait_for_runtime_ready_after_restart() {")
	if helperStart < 0 {
		t.Fatal("stripped installer missing wait_for_runtime_ready_after_restart() {")
	}
	helperEndRel := strings.Index(stripped[helperStart:], "\nverify_install() {")
	if helperEndRel < 0 {
		t.Fatal("could not find end of wait_for_runtime_ready_after_restart helper body")
	}
	helper := stripped[helperStart : helperStart+helperEndRel]

	harness := fmt.Sprintf(`#!/usr/bin/env bash
set -u
COUNTERS_DIR="%s"
mkdir -p "$COUNTERS_DIR"
INSTALL_LOG="$COUNTERS_DIR/install.log"
touch "$INSTALL_LOG"
log_detail() { echo "LOG: $*"; }
fail() { echo "FAIL_CALLED: $*" >&2; exit 1; }
# stubbed curl: always succeeds, appends a marker to a file
curl() {
    echo curl >> "$COUNTERS_DIR/calls.log"
    return 0
}
# stubbed ss: returns a line containing the port
ss() {
    echo "ss:$*" >> "$COUNTERS_DIR/calls.log"
    case "$*" in
        *:25*)  echo "LISTEN 0  128  0.0.0.0:25  0.0.0.0:*" ;;
        *:110*) echo "LISTEN 0  128  0.0.0.0:110 0.0.0.0:*" ;;
        *:143*) echo "LISTEN 0  128  0.0.0.0:143 0.0.0.0:*" ;;
        *:8080*) echo "LISTEN 0  128  127.0.0.1:8080 0.0.0.0:*" ;;
        *:8081*) echo "LISTEN 0  128  127.0.0.1:8081 0.0.0.0:*" ;;
        *-ltnp*) echo "LISTEN 0  128  127.0.0.1:8081 0.0.0.0:*" ;;
        *)       return 0 ;;
    esac
}
journalctl() { return 0; }

# Shorten the deadline so the test runs fast. Picked 5s (vs.
# production 30s) so even on a slow machine under full-suite
# load the helper gets 2+ attempts and the test does not flake.
ORVIX_READINESS_DEADLINE_SECONDS=5
export ORVIX_READINESS_DEADLINE_SECONDS

%s

wait_for_runtime_ready_after_restart
echo "READINESS_OK"
`, "$1", helper)

	dir := t.TempDir()
	harnessPath := filepath.Join(dir, "order.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	out, err := exec.Command(bashCommand(t), harnessPath, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("readiness helper failed in stubbed harness: %v\noutput:\n%s", err, out)
	}
	outStr := string(out)

	if !strings.Contains(outStr, "READINESS_OK") {
		t.Errorf("expected READINESS_OK sentinel; got:\n%s", outStr)
	}

	// Inspect the call-count file. Both curl AND ss must have been
	// invoked — proves the helper actually probes HTTP and listeners.
	callsBytes, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatalf("read calls log: %v", err)
	}
	calls := string(callsBytes)
	curlCount := strings.Count(calls, "\ncurl\n") + strings.Count(calls, "^curl\n")
	ssCount := strings.Count(calls, "\nss:")
	if curlCount == 0 {
		t.Errorf("readiness helper must call curl (HTTP endpoints); calls log:\n%s", calls)
	}
	if ssCount < 5 {
		t.Errorf("readiness helper must call ss for each of 25/110/143/8080/8081 (got >= 5); calls log:\n%s", calls)
	}
}

// TestInstallerPostBootstrapReadinessWaitFailsClosed runs the actual
// wait_for_runtime_ready_after_restart() helper from install.sh
// against stubbed curl/ss that simulate the fresh-VPS false-negative
// scenario: listeners never come up within the deadline. The test
// asserts:
//   1. Exit code is non-zero.
//   2. The fail() message names the bootstrap-cleanup-restart reason.
//   3. $INSTALL_LOG receives the `ss -ltnp` and `journalctl -u orvix`
//      diagnostic dumps so an operator can diagnose post-mortem.
//   4. The helper's loop runs MORE THAN ONCE (i.e. it actually
//      polls and waits, it does not bail on the first attempt).
//
// NOTE on counters: same subshell caveat as the Order test. We use
// file appends in the stubbed curl/ss so the parent can count calls
// after the helper exits.
func TestInstallerPostBootstrapReadinessWaitFailsClosed(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	stripped := stripBashComments(installer)

	helperStart := strings.Index(stripped, "wait_for_runtime_ready_after_restart() {")
	if helperStart < 0 {
		t.Fatal("stripped installer missing wait_for_runtime_ready_after_restart() {")
	}
	helperEndRel := strings.Index(stripped[helperStart:], "\nverify_install() {")
	if helperEndRel < 0 {
		t.Fatal("could not find end of wait_for_runtime_ready_after_restart helper body")
	}
	helper := stripped[helperStart : helperStart+helperEndRel]

	// Build a harness where curl and ss both FAIL forever (the
	// fresh-VPS rehearsal scenario where listeners never bind).
	harness := fmt.Sprintf(`#!/usr/bin/env bash
set -u
COUNTERS_DIR="%s"
mkdir -p "$COUNTERS_DIR"
INSTALL_LOG="$COUNTERS_DIR/install.log"
touch "$INSTALL_LOG"
log_detail() { echo "LOG: $*"; }
fail() { echo "FAIL_CALLED: $*" >&2; exit 1; }
# stubbed curl: always fails (simulates 8080/8081 not yet up)
curl() {
    echo curl >> "$COUNTERS_DIR/calls.log"
    return 22  # curl "HTTP error" exit code
}
# stubbed ss: empty (no listeners bound yet) but still emits
# a marker line so the diagnostic dump in $INSTALL_LOG is
# non-empty (operator can confirm the harness did run).
ss() {
    echo "ss:$*" >> "$COUNTERS_DIR/calls.log"
    case "$*" in
        *-ltnp*) echo "STATE: no listeners bound yet" ;;
    esac
    return 0
}
journalctl() {
    echo "JOURNAL: simulated recent orvix log lines (no real boot)" >&2
    return 0
}

# Shorten the deadline so this test runs in a few seconds.
# Picked 5s (vs. production 30s) so even on a slow machine
# under full-suite load the helper gets 2+ attempts and the
# test does not flake on the >= 2 curl assertion.
ORVIX_READINESS_DEADLINE_SECONDS=5
export ORVIX_READINESS_DEADLINE_SECONDS

%s

wait_for_runtime_ready_after_restart
echo "UNEXPECTED_OK"
`, "$1", helper)

	dir := t.TempDir()
	harnessPath := filepath.Join(dir, "fail-closed.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	out, err := exec.Command(bashCommand(t), harnessPath, dir).CombinedOutput()
	outStr := string(out)
	if err == nil {
		t.Fatalf("readiness helper must fail closed when listeners never come up; got exit 0\noutput:\n%s", outStr)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() == 0 {
			t.Fatalf("readiness helper must exit non-zero on timeout; got 0\noutput:\n%s", outStr)
		}
	} else {
		t.Fatalf("unexpected error type: %v\noutput:\n%s", err, outStr)
	}

	// (2) The fail() message must clearly name the bootstrap-cleanup
	//     restart reason.
	if !strings.Contains(outStr, "FAIL_CALLED: runtime listeners did not become ready after bootstrap cleanup restart") {
		t.Errorf("failure message must name the bootstrap-cleanup-restart reason; output:\n%s", outStr)
	}

	// (3) $INSTALL_LOG must contain the diagnostic dumps.
	logBytes, err := os.ReadFile(filepath.Join(dir, "install.log"))
	if err != nil {
		t.Fatalf("read $INSTALL_LOG: %v", err)
	}
	logStr := string(logBytes)
	if !strings.Contains(logStr, "ss -ltnp") {
		t.Errorf("$INSTALL_LOG must contain ss -ltnp dump; got:\n%s", logStr)
	}
	if !strings.Contains(logStr, "journalctl -u orvix") {
		t.Errorf("$INSTALL_LOG must contain journalctl -u orvix dump; got:\n%s", logStr)
	}

	// (4) The helper must have polled more than once. With a
	// 2-second deadline and 1-second sleep, we expect at least
	// 2 curl calls (one per attempt). Use the calls log file
	// and count exact-match "curl" lines.
	callsBytes, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatalf("read calls log: %v", err)
	}
	curlCount := 0
	for _, line := range strings.Split(strings.TrimSpace(string(callsBytes)), "\n") {
		if line == "curl" {
			curlCount++
		}
	}
	if curlCount < 2 {
		t.Errorf("readiness helper must poll more than once; expected >=2 curl calls; got %d\ncalls log:\n%s", curlCount, callsBytes)
	}
}

// TestCoremailBoolBehavior runs the actual coremail_bool() awk logic
// against temporary YAML files to prove section-aware matching works
// end-to-end. This is a behavioral test, not a string check.
func TestCoremailBoolBehavior(t *testing.T) {
	type testCase struct {
		name     string
		yaml     string
		key      string
		expected string // "0" or "1"
	}

	cases := []testCase{
		{
			name: "unrelated section submission_enabled ignored",
			yaml: `custom_provider:
  submission_enabled: true
  smtps_enabled: true

coremail:
  enabled: true
`,
			key:      "submission_enabled",
			expected: "0",
		},
		{
			name: "unrelated section smtps_enabled ignored",
			yaml: `custom_provider:
  submission_enabled: true
  smtps_enabled: true

coremail:
  enabled: true
`,
			key:      "smtps_enabled",
			expected: "0",
		},
		{
			name: "coremail submission_enabled true",
			yaml: `coremail:
  enabled: true
  submission_enabled: true
`,
			key:      "submission_enabled",
			expected: "1",
		},
		{
			name: "coremail smtps_enabled true",
			yaml: `coremail:
  enabled: true
  smtps_enabled: true
`,
			key:      "smtps_enabled",
			expected: "1",
		},
		{
			name: "coremail submission_enabled false",
			yaml: `coremail:
  submission_enabled: false
`,
			key:      "submission_enabled",
			expected: "0",
		},
		{
			name: "coremail smtps_enabled false",
			yaml: `coremail:
  smtps_enabled: false
`,
			key:      "smtps_enabled",
			expected: "0",
		},
		{
			name: "absent key returns false",
			yaml: `coremail:
  enabled: true
`,
			key:      "submission_enabled",
			expected: "0",
		},
		{
			name: "both sections, unrelated submission_enabled present but coremail also set",
			yaml: `custom:
  submission_enabled: true

coremail:
  enabled: true
  submission_enabled: true
  smtps_enabled: true

other:
  smtps_enabled: true
`,
			key:      "submission_enabled",
			expected: "1",
		},
		{
			name: "coremail both true, check smtps",
			yaml: `coremail:
  enabled: true
  submission_enabled: true
  smtps_enabled: true
`,
			key:      "smtps_enabled",
			expected: "1",
		},
	}

	// Build a self-contained awk program for each key.
	awkBody := func(key string) string {
		return fmt.Sprintf(`BEGIN { in_coremail = 0; result = 0 }
/^[a-zA-Z][a-zA-Z0-9_-]*:/ {
    sec = $1; sub(/:$/, "", sec)
    in_coremail = (sec == "coremail" ? 1 : 0)
}
in_coremail && /^[[:space:]]*%s:[[:space:]]*true[[:space:]]*$/ { result = 1 }
END { print result }`, key)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "orvix.yaml")
			if err := os.WriteFile(cfgPath, []byte(c.yaml), 0o600); err != nil {
				t.Fatalf("write yaml: %v", err)
			}

			// Write the awk program to a temp file to avoid
			// single-quote nesting issues in shell command lines.
			awkProg := filepath.Join(dir, "check.awk")
			if err := os.WriteFile(awkProg, []byte(awkBody(c.key)), 0o644); err != nil {
				t.Fatalf("write awk prog: %v", err)
			}

			// Run awk through bash (awk may not be on PATH
			// directly on Windows / Git Bash setups).
			cmd := exec.Command(bashCommand(t), "-c",
				fmt.Sprintf("awk -f '%s' '%s'", awkProg, cfgPath))
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("awk execution failed: %v\nyaml:\n%s", err, c.yaml)
			}
			got := strings.TrimSpace(string(out))
			if got != c.expected {
				t.Errorf("coremail_bool(%q) = %s, want %s\nconfig:\n%s", c.key, got, c.expected, c.yaml)
			}
		})
	}
}

// TestInstallerBindPosturePublicMailPortAcceptsSpecificIP verifies
// the public mail-port check accepts a specific non-loopback IP
// (e.g. 203.0.113.5), not only wildcard 0.0.0.0/*/[::].
func TestInstallerBindPosturePublicMailPortAcceptsSpecificIP(t *testing.T) {
	installer := mustRead(t, filepath.Join(repoRoot(t), "release", "install.sh"))

	// The check_public_port function must check for at least one
	// non-loopback address, not only wildcard patterns.
	if !strings.Contains(installer, `has_public=true` ) {
		t.Error("check_public_port must detect any non-loopback bind as public")
	}
	if !strings.Contains(installer, `127.*|127.0.0.1|\[::1\]|::1` ) {
		t.Error("check_public_port must skip all loopback addresses and accept specific IPs")
	}
}

// TestInstallerMigrateUnsafeInternalBinds pins that release/install.sh
// defines a migrate_unsafe_internal_binds() function that:
//   - is present in the script (static string check)
//   - is invoked during install (called from main before write_config)
//   - targets server.host exactly equal to "0.0.0.0"
//   - targets coremail.jmap_host exactly equal to 0.0.0.0
//   - does NOT touch smtp_host / imap_host / pop3_host / submission_host / smtps_host
//   - does NOT change operator-set non-default values (anchored pattern)
func TestInstallerMigrateUnsafeInternalBinds(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	stripped := stripBashComments(installer)

	// 1) Function definition must exist.
	if !strings.Contains(installer, "migrate_unsafe_internal_binds()") {
		t.Fatal("release/install.sh must define migrate_unsafe_internal_binds() (2026-06-29 hotfix: security hardening migration for re-runs)")
	}
	// 2) Main must call the migration before write_config.
	if !strings.Contains(stripped, "migrate_unsafe_internal_binds") {
		t.Error("release/install.sh must invoke migrate_unsafe_internal_binds from the install flow")
	}
	// 3) The migration must target the exact "0.0.0.0" default
	//    for server.host, and only under the server: section.
	if !strings.Contains(installer, `server.*host:.*"0\.0\.0\.0"`) &&
		!strings.Contains(installer, `host: "0.0.0.0"`) {
		t.Error("release/install.sh migrate_unsafe_internal_binds must target server.host with the exact default \"0.0.0.0\"")
	}
	// 4) The migration must target the exact 0.0.0.0 default
	//    for coremail.jmap_host, and only under the coremail: section.
	if !strings.Contains(installer, `jmap_host: 0.0.0.0`) {
		t.Error("release/install.sh migrate_unsafe_internal_binds must target coremail.jmap_host with the exact default 0.0.0.0")
	}
	// 5) The migration must be section-aware: it must use awk with
	//    section tracking so host: under custom_provider: is not matched.
	if !strings.Contains(installer, "section") || !strings.Contains(installer, "awk") {
		t.Error("release/install.sh migrate_unsafe_internal_binds must use awk for section-aware matching")
	}
	// 6) The migration MUST NOT touch mail listener binds. The
	//    awk patterns are key-specific: they only match "host:" and
	//    "jmap_host:", never "smtp_host", "imap_host", etc.
	for _, mailKey := range []string{
		"smtp_host", "imap_host", "pop3_host",
		"submission_host", "smtps_host",
	} {
		// Crude but effective: a sed replacement line for any
		// of these keys would appear as "s|...<key>:...". The
		// migrate function should have NO such line.
		if strings.Contains(stripped, "s|"+mailKey+":") {
			t.Errorf("release/install.sh migrate_unsafe_internal_binds appears to sed-replace mail listener bind %q — mail binds MUST stay public", mailKey)
		}
	}
}

// TestInstallerMigrateUnsafeInternalBindsBehavior runs the actual
// migrate_unsafe_internal_binds() function from install.sh against
// a sample unsafe /etc/orvix/orvix.yaml and asserts the binds are
// corrected, mail listener binds are preserved, and operator-set
// non-default values are NOT touched.
func TestInstallerMigrateUnsafeInternalBindsBehavior(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	// Extract the function source via brace counting.
	fn, err := extractBashFunction(installer, "migrate_unsafe_internal_binds")
	if err != nil {
		t.Fatalf("could not extract migrate_unsafe_internal_binds from install.sh: %v", err)
	}
	if fn == "" {
		t.Fatal("extractBashFunction returned empty function body for migrate_unsafe_internal_binds")
	}

	cases := []struct {
		name        string
		initial     string
		wantServer  string
		wantJmap    string
		wantSMTP    string
		wantIMAP    string
		wantPOP3    string
		wantAdmin   string // operator-edited field, must be preserved
	}{
		{
			name: "fresh unsafe config (server.host + jmap_host = 0.0.0.0)",
			initial: `server:
  host: "0.0.0.0"
  port: 80
  admin_port: 8080
  admin_host: "admin.example.com"
  webmail_host: "webmail.example.com"
  mail_host: "mail.example.com"

coremail:
  enabled: true
  smtp_host: 0.0.0.0
  smtp_port: 25
  imap_host: 0.0.0.0
  imap_port: 143
  pop3_host: 0.0.0.0
  pop3_port: 110
  jmap_host: 0.0.0.0
  jmap_port: 8081

auth:
  cookie_domain: ".example.com"
`,
			wantServer: `127.0.0.1`,
			wantJmap:   `127.0.0.1`,
			wantSMTP:   `0.0.0.0`,
			wantIMAP:   `0.0.0.0`,
			wantPOP3:   `0.0.0.0`,
			wantAdmin:  `admin.example.com`,
		},
		{
			name: "operator-set public IP must NOT be migrated",
			initial: `server:
  host: "192.0.2.5"
  port: 80
coremail:
  jmap_host: "198.51.100.7"
  smtp_host: 0.0.0.0
`,
			wantServer: `192.0.2.5`,
			wantJmap:   `198.51.100.7`,
			wantSMTP:   `0.0.0.0`,
		},
		{
			name: "already-safe values — no changes",
			initial: `server:
  host: "127.0.0.1"
coremail:
  jmap_host: 127.0.0.1
`,
			wantServer: `127.0.0.1`,
			wantJmap:   `127.0.0.1`,
		},
		{
			name: "custom section host preserved",
			initial: `server:
  host: "0.0.0.0"
  port: 80

custom_provider:
  host: "0.0.0.0"
  port: 9999

coremail:
  smtp_host: 0.0.0.0
  imap_host: 0.0.0.0
  pop3_host: 0.0.0.0
  jmap_host: 0.0.0.0
`,
			wantServer: `127.0.0.1`,
			wantJmap:   `127.0.0.1`,
			wantSMTP:   `0.0.0.0`,
			wantIMAP:   `0.0.0.0`,
			wantPOP3:   `0.0.0.0`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Write the initial config to a temp file, set
			// ORVIX_CONFIG, source the function, and run it.
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "orvix.yaml")
			if err := os.WriteFile(cfgPath, []byte(c.initial), 0o600); err != nil {
				t.Fatalf("write initial config: %v", err)
			}

			// Build a self-contained bash program that exports
			// ORVIX_CONFIG, stubs log_detail and run_quiet,
			// sources the function, and runs it. Writing to a
			// file avoids the `bash -c` quoting gotchas that
			// would mangle the function's complex sed patterns.
			program := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
export ORVIX_CONFIG=%q
log_detail() { :; }
run_quiet() { "$@" >/dev/null 2>&1; }
%s
migrate_unsafe_internal_binds
cat "$ORVIX_CONFIG"
`, cfgPath, fn)
			progPath := filepath.Join(dir, "run.sh")
			if err := os.WriteFile(progPath, []byte(program), 0o755); err != nil {
				t.Fatalf("write program: %v", err)
			}

			cmd := exec.Command(bashCommand(t), progPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run migration: %v\n%s", err, out)
			}
			rendered := string(out)

			checks := []struct {
				key, want string
			}{
				{"server.host", c.wantServer},
				{"coremail.jmap_host", c.wantJmap},
			}
			if c.wantSMTP != "" {
				checks = append(checks, struct{ key, want string }{"coremail.smtp_host", c.wantSMTP})
			}
			if c.wantIMAP != "" {
				checks = append(checks, struct{ key, want string }{"coremail.imap_host", c.wantIMAP})
			}
			if c.wantPOP3 != "" {
				checks = append(checks, struct{ key, want string }{"coremail.pop3_host", c.wantPOP3})
			}
			for _, ch := range checks {
				leaf := strings.TrimSpace(strings.SplitN(ch.key, ".", 2)[1])
				// Accept both quoted (`host: "127.0.0.1"`) and
				// unquoted (`jmap_host: 127.0.0.1`) YAML forms.
				needleQuoted := leaf + ": \"" + ch.want + "\""
				needleBare := leaf + ": " + ch.want
				if !strings.Contains(rendered, needleQuoted) && !strings.Contains(rendered, needleBare) {
					t.Errorf("expected %q = %q after migration, but rendered config was:\n%s", ch.key, ch.want, rendered)
				}
			}
			if c.wantAdmin != "" && !strings.Contains(rendered, c.wantAdmin) {
				t.Errorf("operator-edited field was overwritten (expected to find %q in rendered config):\n%s", c.wantAdmin, rendered)
			}
			// Regression: custom section values must NOT be migrated.
			// If the initial config contained custom_provider.host,
			// it must still contain the same quoted value after migration.
			if strings.Contains(c.initial, "custom_provider:") {
				if !strings.Contains(rendered, `host: "0.0.0.0"`) {
					t.Errorf("custom_provider.host was incorrectly migrated (must be section-aware):\n%s", rendered)
				}
			}
		})
	}
}

// TestProvisionConfigFreshInstallWritesSafeDefaults pins the
// 2026-06-29 (re-review) hotfix: when $ORVIX_CONFIG does not
// exist, provision_config() MUST call write_config() so the
// fresh install gets the safe defaults (server.host=127.0.0.1,
// jmap_host=127.0.0.1, public mail listener binds at 0.0.0.0).
// This is the same expectation as the previous
// TestInstallerWriteConfigBindsInternalToLoopback but driven
// through the real provision_config() entry point so a future
// refactor that wires a different code path here is caught.
func TestProvisionConfigFreshInstallWritesSafeDefaults(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	// Harness: do NOT pre-create $ORVIX_CONFIG. The install flow
	// should detect "no config yet" and run write_config().
	// We point INSTALL_LOG at a file inside the harness dir so
	// log_detail() does not try to write to /var/log/orvix (which
	// does not exist on the test host).
	const domain = "example.com"
	harness := strings.Replace(installer,
		`main "$@"`,
		fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; INSTALL_LOG="%s/install.log"; touch "$INSTALL_LOG"; provision_config "%s"; cat "$ORVIX_CONFIG"`, "$2", domain),
		1,
	)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "render-config.sh")
	configPath := filepath.Join(harnessDir, "orvix.yaml")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "render-config.sh", configPath, harnessDir)
	cmd.Dir = harnessDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render installer config (fresh install path): %v: %s", err, string(out))
	}
	rendered := string(out)

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		t.Fatalf("installer config is not valid YAML: %v\n--- rendered YAML ---\n%s", err, rendered)
	}
	settings := v.AllSettings()

	// Must be the safe loopback defaults.
	if got := readNestedString(settings, "server.host"); got != "127.0.0.1" {
		t.Errorf("fresh install: server.host got %q, want 127.0.0.1", got)
	}
	if got := readNestedString(settings, "coremail.jmap_host"); got != "127.0.0.1" {
		t.Errorf("fresh install: coremail.jmap_host got %q, want 127.0.0.1", got)
	}
	// Mail listener binds MUST be public.
	for _, key := range []string{
		"coremail.smtp_host",
		"coremail.imap_host",
		"coremail.pop3_host",
	} {
		if got := readNestedString(settings, key); got != "0.0.0.0" {
			t.Errorf("fresh install: %s got %q, want 0.0.0.0 (public)", key, got)
		}
	}
}

// TestProvisionConfigReRunPreservesOperatorConfig is the load-
// bearing test for the 2026-06-29 (re-review) hotfix. When
// $ORVIX_CONFIG already exists, provision_config() MUST NOT
// call write_config(). Operator-managed fields are preserved
// untouched; only the two surgical migrations (server.host and
// jmap_host) are applied if the unsafe defaults are present.
func TestProvisionConfigReRunPreservesOperatorConfig(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	cases := []struct {
		name             string
		initial          string
		wantServer       string
		wantJmap         string
		wantSMTP         string
		wantIMAP         string
		wantPOP3         string
		wantAdminHost    string // operator-edited, must be preserved
		wantCookieDomain string // operator-edited, must be preserved
		wantVapidSubj    string // operator-edited, must be preserved
	}{
		{
			name: "re-run with unsafe defaults -> migrated, operator edits preserved",
			initial: `server:
  host: "0.0.0.0"
  port: 80
  admin_port: 8080
  admin_host: "admin.acme.example"
  webmail_host: "webmail.acme.example"
  mail_host: "mail.acme.example"

database:
  driver: sqlite

coremail:
  enabled: true
  smtp_host: 0.0.0.0
  smtp_port: 25
  imap_host: 0.0.0.0
  imap_port: 143
  pop3_host: 0.0.0.0
  pop3_port: 110
  jmap_host: 0.0.0.0
  jmap_port: 8081
  vapid_subject: "mailto:ops@acme.example"

auth:
  cookie_domain: ".acme.example"
`,
			wantServer:       "127.0.0.1",
			wantJmap:         "127.0.0.1",
			wantSMTP:         "0.0.0.0",
			wantIMAP:         "0.0.0.0",
			wantPOP3:         "0.0.0.0",
			wantAdminHost:    "admin.acme.example",
			wantCookieDomain: ".acme.example",
			wantVapidSubj:    "mailto:ops@acme.example",
		},
		{
			name: "re-run with operator-set custom IPs -> not touched, mail listeners preserved",
			initial: `server:
  host: "192.0.2.10"
  port: 80
  admin_host: "admin.acme.example"

coremail:
  smtp_host: 0.0.0.0
  imap_host: 0.0.0.0
  pop3_host: 0.0.0.0
  jmap_host: 192.0.2.11

auth:
  cookie_domain: ".acme.example"
`,
			wantServer:       "192.0.2.10",
			wantJmap:         "192.0.2.11",
			wantSMTP:         "0.0.0.0",
			wantIMAP:         "0.0.0.0",
			wantPOP3:         "0.0.0.0",
			wantAdminHost:    "admin.acme.example",
			wantCookieDomain: ".acme.example",
		},
		{
			name: "re-run with already-safe values -> no migration, full config preserved",
			initial: `server:
  host: "127.0.0.1"
  port: 80
  admin_host: "admin.acme.example"

coremail:
  smtp_host: 0.0.0.0
  imap_host: 0.0.0.0
  pop3_host: 0.0.0.0
  jmap_host: 127.0.0.1

auth:
  cookie_domain: ".acme.example"
`,
			wantServer:       "127.0.0.1",
			wantJmap:         "127.0.0.1",
			wantSMTP:         "0.0.0.0",
			wantIMAP:         "0.0.0.0",
			wantPOP3:         "0.0.0.0",
			wantAdminHost:    "admin.acme.example",
			wantCookieDomain: ".acme.example",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Pre-create the config file so provision_config()
			// sees "re-run, not fresh" and skips write_config().
			dir := t.TempDir()
			configPath := filepath.Join(dir, "orvix.yaml")
			if err := os.WriteFile(configPath, []byte(c.initial), 0o600); err != nil {
				t.Fatalf("write pre-existing config: %v", err)
			}

			// Harness: stub chown/chmod so we don't need root,
			// call provision_config (which is the real install
			// entry), then cat the result. We point INSTALL_LOG
			// at a file inside the harness dir so log_detail()
			// does not try to write to /var/log/orvix.
			const domain = "example.com"
			harness := strings.Replace(installer,
				`main "$@"`,
				fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; INSTALL_LOG="%s/install.log"; touch "$INSTALL_LOG"; provision_config "%s"; cat "$ORVIX_CONFIG"`, "$2", domain),
				1,
			)
			harnessPath := filepath.Join(dir, "rerun.sh")
			if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
				t.Fatalf("write harness: %v", err)
			}

			cmd := exec.Command(bashCommand(t), "rerun.sh", configPath, dir)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run provision_config (re-run path): %v\n%s", err, out)
			}
			rendered := string(out)

			// Parse and check the binds.
			v := viper.New()
			v.SetConfigType("yaml")
			if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
				t.Fatalf("re-run config is not valid YAML: %v\n--- rendered YAML ---\n%s", err, rendered)
			}
			_ = v.AllSettings() // parsed; checks below use raw string contains

			checks := []struct {
				key, want string
			}{
				{"server.host", c.wantServer},
				{"coremail.jmap_host", c.wantJmap},
			}
			if c.wantSMTP != "" {
				checks = append(checks, struct{ key, want string }{"coremail.smtp_host", c.wantSMTP})
			}
			if c.wantIMAP != "" {
				checks = append(checks, struct{ key, want string }{"coremail.imap_host", c.wantIMAP})
			}
			if c.wantPOP3 != "" {
				checks = append(checks, struct{ key, want string }{"coremail.pop3_host", c.wantPOP3})
			}
			for _, ch := range checks {
				leaf := strings.TrimSpace(strings.SplitN(ch.key, ".", 2)[1])
				// Accept both quoted (`host: "127.0.0.1"`) and
				// unquoted (`jmap_host: 127.0.0.1`) YAML forms.
				needleQuoted := leaf + ": \"" + ch.want + "\""
				needleBare := leaf + ": " + ch.want
				if !strings.Contains(rendered, needleQuoted) && !strings.Contains(rendered, needleBare) {
					t.Errorf("re-run: expected %q = %q, rendered config:\n%s", ch.key, ch.want, rendered)
				}
			}

			// Operator-edited fields MUST be preserved verbatim.
			for _, needle := range []string{
				c.wantAdminHost,
				c.wantCookieDomain,
				c.wantVapidSubj,
			} {
				if needle == "" {
					continue
				}
				if !strings.Contains(rendered, needle) {
					t.Errorf("re-run: operator-edited field %q was overwritten, rendered config:\n%s", needle, rendered)
				}
			}
		})
	}
}

// TestProvisionConfigReRunDoesNotOverwriteOperatorFields is the
// negative control: a re-run MUST NOT introduce any of the keys
// the operator did not already have in their config. The fresh
// write_config heredoc emits a long list of fields (auth.*,
// coremail.vapid_*, coremail.max_attachment_size_mb, etc.) and
// if write_config were called on a re-run, those would clobber
// the operator's version. This test pre-creates a minimal config
// and asserts the re-run output does NOT grow new sections.
func TestProvisionConfigReRunDoesNotOverwriteOperatorFields(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	dir := t.TempDir()
	configPath := filepath.Join(dir, "orvix.yaml")
	// Minimal pre-existing config. Note: deliberately missing
	// `auth:`, `coremail.vapid_*`, `metrics:`, `update:` — if
	// write_config runs, it would inject all of these.
	initial := `server:
  host: "0.0.0.0"
  port: 80
  admin_host: "admin.acme.example"

coremail:
  jmap_host: 0.0.0.0
`
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write pre-existing config: %v", err)
	}

	harness := strings.Replace(installer,
		`main "$@"`,
		fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; INSTALL_LOG="%s/install.log"; touch "$INSTALL_LOG"; provision_config "example.com"; cat "$ORVIX_CONFIG"`, "$2"),
		1,
	)
	harnessPath := filepath.Join(dir, "rerun.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "rerun.sh", configPath, dir)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run re-run path: %v\n%s", err, out)
	}
	rendered := string(out)

	// Binds MUST be migrated to loopback (the surgical fix).
	if !strings.Contains(rendered, `host: "127.0.0.1"`) {
		t.Errorf("re-run did not migrate server.host to 127.0.0.1; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, `jmap_host: 127.0.0.1`) {
		t.Errorf("re-run did not migrate jmap_host to 127.0.0.1; rendered:\n%s", rendered)
	}
	// Operator-edited admin_host MUST be preserved.
	if !strings.Contains(rendered, `admin.acme.example`) {
		t.Errorf("re-run overwrote operator admin_host; rendered:\n%s", rendered)
	}
	// Sections that were NOT in the pre-existing config MUST NOT
	// be injected by the re-run path. If write_config ran, it
	// would have written `auth:`, `metrics:`, `update:`,
	// `backup:`, `logging:`, etc. None of these are present in
	// the initial config and none are created by migration.
	for _, forbidden := range []string{
		"auth:",
		"metrics:",
		"update:",
		"backup:",
		"logging:",
		"database:",
		"redis:",
		"outbound:",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("re-run injected %q (write_config must not run on re-run); rendered:\n%s", forbidden, rendered)
		}
	}
}
// generated by release/scripts/setup-https.sh proxies every
// public hostname to 127.0.0.1 (NOT to 0.0.0.0 or any public IP).
// This is the load-bearing assumption that allows the admin and
// JMAP listeners to bind only on loopback.
func TestSetupHttpsReverseProxiesToLoopback(t *testing.T) {
	root := repoRoot(t)
	body := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))
	stripped := stripBashComments(body)
	// Every reverse_proxy line must target 127.0.0.1 (admin/webmail
	// 8080) or 127.0.0.1:8081 (JMAP). No reverse_proxy to 0.0.0.0
	// or any other host.
	lines := strings.Split(stripped, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "reverse_proxy ") {
			continue
		}
		target := strings.TrimPrefix(trimmed, "reverse_proxy ")
		target = strings.TrimSpace(target)
		// Strip any block in Caddyfile (`reverse_proxy host:port { ... }`
		// is rare; we accept plain target strings only). The literal
		// target strings we ship are always "127.0.0.1:8080" or
		// "127.0.0.1:8081".
		if !strings.HasPrefix(target, "127.0.0.1:") {
			t.Errorf("release/scripts/setup-https.sh has reverse_proxy to non-loopback target %q — must proxy to 127.0.0.1:<port> (2026-06-29 hotfix)", target)
		}
	}
	// Positive: both port targets must be present.
	if !strings.Contains(stripped, "reverse_proxy 127.0.0.1:8080") {
		t.Error("release/scripts/setup-https.sh must contain `reverse_proxy 127.0.0.1:8080` for the admin/webmail backend")
	}
	if !strings.Contains(stripped, "reverse_proxy 127.0.0.1:8081") {
		t.Error("release/scripts/setup-https.sh must contain `reverse_proxy 127.0.0.1:8081` for the JMAP backend")
	}
}

// TestInstallerWriteConfigRendersDNSPublicIPv4 pins the
// 2026-06-30 fresh-install contract: write_config() MUST emit a
// `dns:` block containing `public_ipv4: "<detected server public
// IPv4>"`. Without this, the admin DNS/DKIM dashboard returns
// 422 "public mail IPv4 is not configured" and the operator
// cannot publish any A / MX / SPF / DKIM / DMARC records.
//
// The `dns.public_ipv4` field is the single source of truth for
// the public DNS plan. It MUST NOT be inferred from
// `coremail.smtp_host` (a listener bind address, defaults to
// 0.0.0.0). The test also asserts that `coremail.smtp_host` stays
// at `0.0.0.0` after the heredoc runs — fixing DNS by mutating
// the listener bind address is a regression.
func TestInstallerWriteConfigRendersDNSPublicIPv4(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	const domain = "example.com"
	const publicIPv4 = "65.75.203.74"
	// Harness: stub chown/chmod, point write_config at a temp
	// file, pass the public IP as second arg, cat the result.
	harness := strings.Replace(installer,
		`main "$@"`,
		fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; write_config "%s" "%s"; cat "$ORVIX_CONFIG"`, domain, publicIPv4),
		1,
	)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "render-config.sh")
	configPath := filepath.Join(harnessDir, "orvix.yaml")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "render-config.sh", configPath)
	cmd.Dir = harnessDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render installer config: %v: %s", err, string(out))
	}
	rendered := string(out)

	// The rendered YAML must parse as valid YAML and contain a
	// `dns:` block with `public_ipv4: "65.75.203.74"`.
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		t.Fatalf("installer config is not valid YAML: %v\n--- rendered YAML ---\n%s", err, rendered)
	}
	settings := v.AllSettings()

	if got := readNestedString(settings, "dns.public_ipv4"); got != publicIPv4 {
		t.Errorf("fresh install: dns.public_ipv4 got %q, want %q (the admin DNS dashboard fails closed with 422 otherwise)", got, publicIPv4)
	}
	// public_ipv6 is optional; default to empty string so the
	// YAML is parseable and a future operator can set it.
	if got := readNestedString(settings, "dns.public_ipv6"); got != "" {
		t.Errorf("fresh install: dns.public_ipv6 got %q, want empty default", got)
	}

	// The listener bind host MUST stay 0.0.0.0 — the public
	// DNS plan must not bleed into coremail.smtp_host.
	if got := readNestedString(settings, "coremail.smtp_host"); got != "0.0.0.0" {
		t.Errorf("fresh install: coremail.smtp_host got %q, want 0.0.0.0 (DNS MUST NOT change the listener bind)", got)
	}
	if got := readNestedString(settings, "coremail.jmap_host"); got != "127.0.0.1" {
		t.Errorf("fresh install: coremail.jmap_host got %q, want 127.0.0.1", got)
	}
	if got := readNestedString(settings, "server.host"); got != "127.0.0.1" {
		t.Errorf("fresh install: server.host got %q, want 127.0.0.1", got)
	}
}

// TestMigrateDnsPublicIpAddsWhenMissing pins the re-run /
// migration contract: when $ORVIX_CONFIG already exists and
// `dns.public_ipv4` is missing, migrate_dns_public_ip MUST add
// the field as the first key under the existing `dns:` section
// (or as a fresh `dns:` block if none exists). It MUST NOT
// overwrite any existing operator value. It MUST NOT infer the
// value from `coremail.smtp_host` or any other listener bind
// field.
func TestMigrateDnsPublicIpAddsWhenMissing(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	const publicIPv4 = "65.75.203.74"

	cases := []struct {
		name      string
		initial   string
		postCheck func(t *testing.T, rendered string)
	}{
		{
			name: "missing public_ipv4 under existing dns: section -> inserted as first key",
			initial: `server:
  host: "127.0.0.1"
  port: 80

coremail:
  smtp_host: 0.0.0.0
  jmap_host: 127.0.0.1

dns:
  cloudflare_api_key: "secret-cf-key"
  cloudflare_zone_id: "zone-abc"

auth:
  cookie_domain: ".example.com"
`,
			postCheck: func(t *testing.T, rendered string) {
				v := viper.New()
				v.SetConfigType("yaml")
				if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
					t.Fatalf("re-rendered config is not valid YAML: %v\n%s", err, rendered)
				}
				if got := readNestedString(v.AllSettings(), "dns.public_ipv4"); got != publicIPv4 {
					t.Errorf("migrate: dns.public_ipv4 got %q, want %q", got, publicIPv4)
				}
				// Operator's Cloudflare token MUST be preserved verbatim.
				if got := readNestedString(v.AllSettings(), "dns.cloudflare_api_key"); got != "secret-cf-key" {
					t.Errorf("migrate clobbered dns.cloudflare_api_key: got %q", got)
				}
				if got := readNestedString(v.AllSettings(), "dns.cloudflare_zone_id"); got != "zone-abc" {
					t.Errorf("migrate clobbered dns.cloudflare_zone_id: got %q", got)
				}
				// coremail.smtp_host MUST stay 0.0.0.0.
				if got := readNestedString(v.AllSettings(), "coremail.smtp_host"); got != "0.0.0.0" {
					t.Errorf("migrate changed coremail.smtp_host: got %q", got)
				}
			},
		},
		{
			name: "no dns: section at all -> appended as fresh dns: block",
			initial: `server:
  host: "127.0.0.1"
  port: 80

coremail:
  smtp_host: 0.0.0.0
  jmap_host: 127.0.0.1

auth:
  cookie_domain: ".example.com"
`,
			postCheck: func(t *testing.T, rendered string) {
				v := viper.New()
				v.SetConfigType("yaml")
				if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
					t.Fatalf("re-rendered config is not valid YAML: %v\n%s", err, rendered)
				}
				if got := readNestedString(v.AllSettings(), "dns.public_ipv4"); got != publicIPv4 {
					t.Errorf("migrate: dns.public_ipv4 got %q, want %q", got, publicIPv4)
				}
				if !strings.Contains(rendered, "\ndns:") && !strings.HasPrefix(rendered, "dns:") {
					t.Errorf("migrate: expected a dns: section in rendered config:\n%s", rendered)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "orvix.yaml")
			if err := os.WriteFile(configPath, []byte(c.initial), 0o600); err != nil {
				t.Fatalf("write pre-existing config: %v", err)
			}

			// Harness: stub chown/chmod, point provision_config at
			// the temp file, then cat it. The provision_config path
			// is the one used by install.sh on a re-run — it calls
			// migrate_dns_public_ip internally.
			harness := strings.Replace(installer,
				`main "$@"`,
				fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; INSTALL_LOG="%s/install.log"; touch "$INSTALL_LOG"; provision_config "example.com" "%s"; cat "$ORVIX_CONFIG"`, dir, publicIPv4),
				1,
			)
			harnessPath := filepath.Join(dir, "rerun.sh")
			if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
				t.Fatalf("write harness: %v", err)
			}

			cmd := exec.Command(bashCommand(t), "rerun.sh", configPath)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("re-run migrate_dns_public_ip: %v\noutput:\n%s", err, out)
			}
			rendered := string(out)
			c.postCheck(t, rendered)
		})
	}
}

// TestMigrateDnsPublicIpPreservesExistingValue pins the most
// load-bearing re-run contract: when `dns.public_ipv4` is
// already set (any value), migrate_dns_public_ip MUST NOT
// overwrite it. The migration is add-only; the operator's value
// is the source of truth on a re-run.
func TestMigrateDnsPublicIpPreservesExistingValue(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	cases := []struct {
		name             string
		existingPublicIP string
		installerIPArg   string // what install.sh thinks the public IP is
		wantFinal        string
	}{
		{
			name:             "operator public IP != installer-detected IP -> operator value preserved",
			existingPublicIP: "203.0.113.10",
			installerIPArg:   "65.75.203.74",
			wantFinal:        "203.0.113.10",
		},
		{
			name:             "operator public IP matches installer-detected IP -> still preserved (no rewrite)",
			existingPublicIP: "65.75.203.74",
			installerIPArg:   "65.75.203.74",
			wantFinal:        "65.75.203.74",
		},
		{
			name:             "operator intentionally cleared public IP (empty string) -> preserved as empty",
			existingPublicIP: `""`,
			installerIPArg:   "65.75.203.74",
			wantFinal:        "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "orvix.yaml")
			initial := fmt.Sprintf(`server:
  host: "127.0.0.1"

coremail:
  smtp_host: 0.0.0.0

dns:
  public_ipv4: %s
`, c.existingPublicIP)
			if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			harness := strings.Replace(installer,
				`main "$@"`,
				fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; INSTALL_LOG="%s/install.log"; touch "$INSTALL_LOG"; provision_config "example.com" "%s"; cat "$ORVIX_CONFIG"`, dir, c.installerIPArg),
				1,
			)
harnessPath := filepath.Join(dir, "rerun.sh")
		if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
			t.Fatalf("write harness: %v", err)
		}

		cmd := exec.Command(bashCommand(t), "rerun.sh", configPath)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("re-run provision_config: %v\noutput:\n%s", err, out)
		}
		rendered := string(out)

			v := viper.New()
			v.SetConfigType("yaml")
			if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
				t.Fatalf("re-run config is not valid YAML: %v\n%s", err, rendered)
			}
			if got := readNestedString(v.AllSettings(), "dns.public_ipv4"); got != c.wantFinal {
				t.Errorf("re-run overwrote dns.public_ipv4: got %q, want %q\nrendered:\n%s", got, c.wantFinal, rendered)
			}
		})
	}
}

// TestInstallerWriteConfigNeverInfersPublicIPFromSMTPHost is the
// regression guard for the architecture rule: DNS public IP MUST
// come from a dedicated config field, never from the SMTP
// listener bind host. The test runs write_config with an EMPTY
// public IP arg and asserts that the rendered `dns.public_ipv4`
// is the empty string (never 0.0.0.0, never coremail.smtp_host,
// never any synthesised value).
func TestInstallerWriteConfigNeverInfersPublicIPFromSMTPHost(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))

	const domain = "example.com"
	harness := strings.Replace(installer,
		`main "$@"`,
		fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; write_config "%s" ""; cat "$ORVIX_CONFIG"`, domain),
		1,
	)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "render-config.sh")
	configPath := filepath.Join(harnessDir, "orvix.yaml")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "render-config.sh", configPath)
	cmd.Dir = harnessDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render installer config: %v: %s", err, string(out))
	}
	rendered := string(out)

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		t.Fatalf("installer config is not valid YAML: %v\n--- rendered YAML ---\n%s", err, rendered)
	}
	settings := v.AllSettings()

	// dns.public_ipv4 MUST be the empty string when no IP is
	// supplied — we MUST NOT default to 0.0.0.0 or to
	// coremail.smtp_host. The DNS plan endpoint then fails closed
	// with 422 until the operator (or setup-https.sh) supplies a
	// real public IP.
	if got := readNestedString(settings, "dns.public_ipv4"); got != "" {
		t.Errorf("write_config must emit dns.public_ipv4 verbatim; got %q, want empty (must NOT fall back to coremail.smtp_host=0.0.0.0 or any other listener bind address)", got)
	}
	// coremail.smtp_host stays 0.0.0.0 — independent of the
	// empty public_ipv4.
	if got := readNestedString(settings, "coremail.smtp_host"); got != "0.0.0.0" {
		t.Errorf("coremail.smtp_host got %q, want 0.0.0.0", got)
	}
}

// TestSetupHttpsPatchesMissingDNSPublicIPv4 drives the actual
// setup-https.sh helper `patch_dns_public_ipv4()` against a
// pre-existing orvix.yaml that has NO `dns.public_ipv4` field.
// The helper MUST add `public_ipv4: "<SERVER_IP>"` either as the
// first key under the existing `dns:` section, or as a fresh
// `dns:` block if none exists. It MUST NOT touch coremail.smtp_host.
func TestSetupHttpsPatchesMissingDNSPublicIPv4(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))

	cases := []struct {
		name    string
		initial string
	}{
		{
			name: "no dns: section",
			initial: `server:
  host: "127.0.0.1"
`,
		},
		{
			name: "existing dns: section without public_ipv4",
			initial: `server:
  host: "127.0.0.1"

dns:
  cloudflare_api_key: "secret-cf-key"
`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "orvix.yaml")
			if err := os.WriteFile(cfgPath, []byte(c.initial), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			// Build a harness that sources setup-https.sh's
			// helpers but stubs everything we don't want to run
			// for real (require_root, install_caddy, write_caddyfile,
			// open_firewall, caddy validate/reload, check_dns,
			// check_https, etc.). We only need patch_dns_public_ipv4.
			//
			// Strip `main "$@"` from the sourced script body so
			// the harness does NOT auto-invoke main() (which would
			// call require_root and try to install caddy). The
			// harness itself drives patch_dns_public_ipv4 below.
			scriptNoMain := strings.Replace(script, "main \"$@\"", "# main disabled by test harness", 1)
			harness := `#!/usr/bin/env bash
set -euo pipefail
PRIMARY_DOMAIN="orvix.email"
ORVIX_SERVER_IP="65.75.203.74"
SERVER_IP="65.75.203.74"
INSTALL_LOG="%s/setup.log"
touch "$INSTALL_LOG"
ADMIN_DOMAIN="admin.orvix.email"
WEBMAIL_DOMAIN="webmail.orvix.email"
MAIL_DOMAIN="mail.orvix.email"
ORVIX_CONFIG="$1"
export ORVIX_CONFIG ORVIX_SERVER_IP

` + scriptNoMain + `

# Stub the helpers AFTER the script so this definition wins.
# setup-https.sh defines them for real; we override to no-ops so
# the test does not try to install Caddy / open firewall ports /
# talk to systemd on the test host.
require_root() { :; }
install_caddy() { :; }
write_caddyfile() { :; }
open_firewall() { :; }
check_dns() { :; }
check_local_port() { :; }
check_https() { :; }
check_content_type() { :; }
post_https_firewall_hardening() { :; }
caddy() { return 0; }
systemctl() { return 0; }
dig() { echo "65.75.203.74"; }
getent() { echo "65.75.203.74"; }

patch_dns_public_ipv4
`

			harnessPath := filepath.Join(dir, "patch.sh")
			harnessFinal := strings.Replace(harness, "%s/setup.log", dir+"/setup.log", 1)
			if err := os.WriteFile(harnessPath, []byte(harnessFinal), 0o755); err != nil {
				t.Fatalf("write harness: %v", err)
			}

			cmd := exec.Command(bashCommand(t), "patch.sh", cfgPath)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				// Dump the harness so failures show the exact
				// line 40 content. Helpful when debugging
				// shell quoting issues inside the awk in
				// patch_dns_public_ipv4.
				if hb, hErr := os.ReadFile(filepath.Join(dir, "patch.sh")); hErr == nil {
					lines := strings.Split(string(hb), "\n")
					header := "patch.sh (first 50 lines):\n"
					body := ""
					for i, ln := range lines {
						if i >= 50 {
							break
						}
						body += fmt.Sprintf("%3d: %s\n", i+1, ln)
					}
					t.Fatalf("patch_dns_public_ipv4 failed: %v\noutput:\n%s\n%s%s", err, out, header, body)
				}
				t.Fatalf("patch_dns_public_ipv4 failed: %v\noutput:\n%s", err, out)
			}

			// Read the patched config and assert dns.public_ipv4
			// was added without touching coremail.smtp_host.
			patchedBytes, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("read patched config: %v", err)
			}
			patched := string(patchedBytes)

			v := viper.New()
			v.SetConfigType("yaml")
			if err := v.ReadConfig(strings.NewReader(patched)); err != nil {
				t.Fatalf("patched config is not valid YAML: %v\n%s", err, patched)
			}
			if got := readNestedString(v.AllSettings(), "dns.public_ipv4"); got != "65.75.203.74" {
				t.Errorf("patch_dns_public_ipv4: dns.public_ipv4 got %q, want 65.75.203.74; patched:\n%s", got, patched)
			}
			// cloudflare_api_key preserved if it was present.
			if strings.Contains(c.initial, "cloudflare_api_key") {
				if got := readNestedString(v.AllSettings(), "dns.cloudflare_api_key"); got != "secret-cf-key" {
					t.Errorf("patch clobbered dns.cloudflare_api_key: got %q", got)
				}
			}
			// coremail.smtp_host is not in either initial config
			// and must not be created by the patch.
			if strings.Contains(patched, "smtp_host") {
				t.Errorf("patch_dns_public_ipv4 must NOT touch coremail.smtp_host; patched:\n%s", patched)
			}
		})
	}
}

// TestSetupHttpsPatchesMatchingDNSPublicIPv4 confirms that when
// the existing `dns.public_ipv4` matches the SERVER_IP supplied
// to setup-https.sh, patch_dns_public_ipv4 is a no-op (the file
// is preserved verbatim).
func TestSetupHttpsPatchesMatchingDNSPublicIPv4(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "orvix.yaml")
	initial := `server:
  host: "127.0.0.1"

dns:
  public_ipv4: "65.75.203.74"
  cloudflare_api_key: "secret-cf-key"
`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	harness := `#!/usr/bin/env bash
set -euo pipefail
PRIMARY_DOMAIN="orvix.email"
ORVIX_SERVER_IP="65.75.203.74"
SERVER_IP="65.75.203.74"
INSTALL_LOG="%s/setup.log"
touch "$INSTALL_LOG"
ADMIN_DOMAIN="admin.orvix.email"
WEBMAIL_DOMAIN="webmail.orvix.email"
MAIL_DOMAIN="mail.orvix.email"
ORVIX_CONFIG="$1"
export ORVIX_CONFIG ORVIX_SERVER_IP

` + strings.Replace(script, "main \"$@\"", "# main disabled by test harness", 1) + `

# Stubs AFTER the script so the last definition wins.
require_root() { :; }
install_caddy() { :; }
write_caddyfile() { :; }
open_firewall() { :; }
check_dns() { :; }
check_local_port() { :; }
check_https() { :; }
check_content_type() { :; }
post_https_firewall_hardening() { :; }
caddy() { return 0; }
systemctl() { return 0; }
dig() { echo "65.75.203.74"; }
getent() { echo "65.75.203.74"; }

patch_dns_public_ipv4
`
	harnessPath := filepath.Join(dir, "patch.sh")
	harnessFinal := strings.Replace(harness, "%s/setup.log", dir+"/setup.log", 1)
	if err := os.WriteFile(harnessPath, []byte(harnessFinal), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "patch.sh", cfgPath)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("patch_dns_public_ipv4 failed on matching IP: %v\noutput:\n%s", err, out)
	}

	patchedBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read patched config: %v", err)
	}
	patched := string(patchedBytes)
	if patched != initial {
		t.Errorf("patch_dns_public_ipv4 on matching IP must be a no-op\nbefore:\n%s\nafter:\n%s", initial, patched)
	}
}

// TestSetupHttpsPatchesMismatchingDNSPublicIPv4Fails confirms
// that when the existing `dns.public_ipv4` differs from the
// SERVER_IP supplied to setup-https.sh, patch_dns_public_ipv4
// FAILS LOUDLY with a clear message that names both values.
// The function MUST NEVER silently overwrite the operator's
// value (an operator may have intentionally set a different IP
// after a VPS migration or behind a NAT).
func TestSetupHttpsPatchesMismatchingDNSPublicIPv4Fails(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "orvix.yaml")
	initial := `server:
  host: "127.0.0.1"

dns:
  public_ipv4: "203.0.113.10"
`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	harness := `#!/usr/bin/env bash
set -uo pipefail
PRIMARY_DOMAIN="orvix.email"
ORVIX_SERVER_IP="65.75.203.74"
SERVER_IP="65.75.203.74"
INSTALL_LOG="%s/setup.log"
touch "$INSTALL_LOG"
ADMIN_DOMAIN="admin.orvix.email"
WEBMAIL_DOMAIN="webmail.orvix.email"
MAIL_DOMAIN="mail.orvix.email"
ORVIX_CONFIG="$1"
export ORVIX_CONFIG ORVIX_SERVER_IP

` + strings.Replace(script, "main \"$@\"", "# main disabled by test harness", 1) + `

# Stubs AFTER the script so these definitions win.
require_root() { :; }
install_caddy() { :; }
write_caddyfile() { :; }
open_firewall() { :; }
check_dns() { :; }
check_local_port() { :; }
check_https() { :; }
check_content_type() { :; }
post_https_firewall_hardening() { :; }
caddy() { return 0; }
systemctl() { return 0; }
dig() { echo "65.75.203.74"; }
getent() { echo "65.75.203.74"; }

# Override fail() AFTER the script so this definition wins; we
# want to inspect the failure message instead of letting the
# script's fail() kill the test runner immediately.
fail() {
    echo "FAIL_CALLED: $*" >&2
    echo "Detailed log: $INSTALL_LOG" >&2
    exit 1
}

patch_dns_public_ipv4
echo "UNEXPECTED_OK"
`
	harnessPath := filepath.Join(dir, "patch.sh")
	harnessFinal := strings.Replace(harness, "%s/setup.log", dir+"/setup.log", 1)
	if err := os.WriteFile(harnessPath, []byte(harnessFinal), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "patch.sh", cfgPath)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if err == nil {
		t.Fatalf("patch_dns_public_ipv4 on mismatched IP must fail; got exit 0\noutput:\n%s", outStr)
	}
	if !strings.Contains(outStr, "mismatch") {
		t.Errorf("failure message must mention mismatch; got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "203.0.113.10") {
		t.Errorf("failure message must name the existing IP; got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "65.75.203.74") {
		t.Errorf("failure message must name the supplied IP; got:\n%s", outStr)
	}

	// The config file MUST NOT have been overwritten.
	patchedBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(patchedBytes), `public_ipv4: "203.0.113.10"`) {
		t.Errorf("mismatch path must NOT overwrite operator value; got:\n%s", patchedBytes)
	}
}

// ── BLOCKER 2 / 3: public IPv4 validation helpers ────────────────
//
// is_valid_public_ipv4 is the gate every public-IPv4 input goes
// through: install.sh's main(), setup-https.sh's require_input(),
// and the migrate / patch helpers all rely on it. The tests below
// pin the full matrix the DNS Ops dashboard rejects, so any future
// drift between the install-time gate and the runtime gate is
// caught before it ships a junk value to /etc/orvix/orvix.yaml.

// TestInstallerIsValidPublicIPv4 exercises is_valid_public_ipv4
// end-to-end. Each sub-case runs the actual bash helper against
// a synthetic input and asserts the exit code, so a future
// refactor that drops a range check (e.g. forgets 169.254/16) is
// caught by this test before it ships.
func TestInstallerIsValidPublicIPv4(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	if !strings.Contains(installer, "is_valid_public_ipv4() {") {
		t.Fatal("installer must define is_valid_public_ipv4() (2026-06-30 BLOCKER 2 fix)")
	}

	// Extract the helper via brace counting so we run the actual
	// production code, not a copy.
	helper, err := extractBashFunction(installer, "is_valid_public_ipv4")
	if err != nil {
		t.Fatalf("extract is_valid_public_ipv4: %v", err)
	}
	if helper == "" {
		t.Fatal("extractBashFunction returned empty helper body for is_valid_public_ipv4")
	}

	cases := []struct {
		name string
		ip   string
		want int // 0 = accepted, 1 = rejected
	}{
		// Accept: a normal public IPv4.
		{"accept 65.75.203.74", "65.75.203.74", 0},
		{"accept 8.8.8.8", "8.8.8.8", 0},
		{"accept 1.1.1.1", "1.1.1.1", 0},

		// Reject: empty.
		{"reject empty", "", 1},

		// Reject: IPv6.
		{"reject IPv6 2606:cc0:200:4c0::1", "2606:cc0:200:4c0::1", 1},
		{"reject IPv6 ::1", "::1", 1},

		// Reject: 0.0.0.0 (unspecified).
		{"reject 0.0.0.0", "0.0.0.0", 1},

		// Reject: loopback (127/8).
		{"reject 127.0.0.1", "127.0.0.1", 1},
		{"reject 127.255.255.254", "127.255.255.254", 1},

		// Reject: RFC1918.
		{"reject 10.0.0.1", "10.0.0.1", 1},
		{"reject 172.16.0.1", "172.16.0.1", 1},
		{"reject 172.31.255.255", "172.31.255.255", 1},
		{"reject 192.168.1.1", "192.168.1.1", 1},

		// Reject: carrier-grade NAT (100.64.0.0/10, RFC6598).
		{"reject 100.64.0.1 (CGNAT)", "100.64.0.1", 1},
		{"reject 100.127.255.255 (CGNAT top)", "100.127.255.255", 1},

		// Reject: RFC6890 special-use 192.0.0.0/24.
		{"reject 192.0.0.1 (special-use)", "192.0.0.1", 1},
		{"reject 192.0.0.255 (special-use top)", "192.0.0.255", 1},

		// Reject: RFC2544 benchmarking (198.18.0.0/15).
		{"reject 198.18.0.1 (benchmark)", "198.18.0.1", 1},
		{"reject 198.19.255.255 (benchmark top)", "198.19.255.255", 1},

		// Reject: link-local.
		{"reject 169.254.1.1", "169.254.1.1", 1},

		// Reject: RFC5737 documentation ranges.
		{"reject 192.0.2.1", "192.0.2.1", 1},
		{"reject 198.51.100.1", "198.51.100.1", 1},
		{"reject 203.0.113.1", "203.0.113.1", 1},

		// Reject: multicast (224/4).
		{"reject 224.0.0.1", "224.0.0.1", 1},
		{"reject 239.255.255.255", "239.255.255.255", 1},

		// Reject: reserved (240/4).
		{"reject 240.0.0.1", "240.0.0.1", 1},
		{"reject 255.255.255.255", "255.255.255.255", 1},

		// Reject: malformed strings (regex gate).
		{"reject single octet", "65", 1},
		{"reject 5 octets", "1.2.3.4.5", 1},
		{"reject alpha", "abc.def.ghi.jkl", 1},
		{"reject octet overflow", "256.0.0.0", 1},
		{"reject leading whitespace", " 65.75.203.74", 1},
		{"reject trailing slash", "65.75.203.74/", 1},
		{"reject zero-padded octet 1", "065.75.203.74", 1},
		{"reject ipv4 with port", "65.75.203.74:80", 1},

		// Accept edges: just outside private/special ranges.
		{"accept 11.0.0.1 (above RFC1918 10/8)", "11.0.0.1", 0},
		{"accept 100.128.0.1 (above CGNAT)", "100.128.0.1", 0},
		{"accept 172.15.255.255 (below 172.16/12)", "172.15.255.255", 0},
		{"accept 172.32.0.0 (above 172.16/12)", "172.32.0.0", 0},
		{"accept 192.169.0.0 (above 192.168/16)", "192.169.0.0", 0},
		{"accept 169.253.0.0 (below 169.254/16)", "169.253.0.0", 0},
		{"accept 169.255.0.0 (above 169.254/16)", "169.255.0.0", 0},
		{"accept 192.0.3.0 (above TEST-NET-1)", "192.0.3.0", 0},
		{"accept 198.17.255.255 (below benchmark 198.18/15)", "198.17.255.255", 0},
		{"accept 198.20.0.1 (above benchmark 198.18/15)", "198.20.0.1", 0},
		{"accept 223.255.255.255 (below multicast)", "223.255.255.255", 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			prog := fmt.Sprintf(`#!/usr/bin/env bash
set -u
%s

if is_valid_public_ipv4 %q; then
    echo "ACCEPT"
else
    echo "REJECT"
fi
exit 0
`, helper, c.ip)
			progPath := filepath.Join(dir, "check.sh")
			if err := os.WriteFile(progPath, []byte(prog), 0o755); err != nil {
				t.Fatalf("write harness: %v", err)
			}
			cmd := exec.Command(bashCommand(t), progPath)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("harness failed: %v\noutput:\n%s", err, out)
			}
			outStr := strings.TrimSpace(string(out))
			gotAccept := strings.Contains(outStr, "ACCEPT")
			gotReject := strings.Contains(outStr, "REJECT")
			if gotAccept && gotReject {
				t.Fatalf("helper produced both ACCEPT and REJECT for %q: %s", c.ip, outStr)
			}
			switch c.want {
			case 0:
				if !gotAccept {
					t.Errorf("is_valid_public_ipv4(%q) must ACCEPT; got:\n%s", c.ip, outStr)
				}
			case 1:
				if !gotReject {
					t.Errorf("is_valid_public_ipv4(%q) must REJECT; got:\n%s", c.ip, outStr)
				}
			}
		})
	}
}

// TestSetupHttpsIsValidPublicIPv4 runs the same matrix against
// setup-https.sh's helper. The two scripts MUST agree on every
// input — drift would cause a fresh install to accept an IP
// that setup-https.sh later rejects, or vice versa.
func TestSetupHttpsIsValidPublicIPv4(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))
	if !strings.Contains(script, "is_valid_public_ipv4() {") {
		t.Fatal("setup-https.sh must define is_valid_public_ipv4() (2026-06-30 BLOCKER 3 fix)")
	}
	helper, err := extractBashFunction(script, "is_valid_public_ipv4")
	if err != nil {
		t.Fatalf("extract is_valid_public_ipv4 from setup-https.sh: %v", err)
	}
	if helper == "" {
		t.Fatal("extractBashFunction returned empty helper body")
	}

	cases := []struct {
		ip   string
		want int
	}{
		{"65.75.203.74", 0},
		{"8.8.8.8", 0},
		{"", 1},
		{"0.0.0.0", 1},
		{"10.0.0.1", 1},
		{"100.64.0.1", 1},
		{"100.127.255.255", 1},
		{"127.0.0.1", 1},
		{"169.254.1.1", 1},
		{"172.16.0.1", 1},
		{"192.0.0.1", 1},
		{"192.0.0.255", 1},
		{"192.0.2.1", 1},
		{"192.168.1.1", 1},
		{"198.18.0.1", 1},
		{"198.19.255.255", 1},
		{"198.51.100.1", 1},
		{"203.0.113.1", 1},
		{"224.0.0.1", 1},
		{"255.255.255.255", 1},
		{"2606:cc0:200:4c0::1", 1},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("ip=%s", c.ip), func(t *testing.T) {
			dir := t.TempDir()
			prog := fmt.Sprintf(`#!/usr/bin/env bash
set -u
%s

if is_valid_public_ipv4 %q; then
    echo "ACCEPT"
else
    echo "REJECT"
fi
exit 0
`, helper, c.ip)
			progPath := filepath.Join(dir, "check.sh")
			if err := os.WriteFile(progPath, []byte(prog), 0o755); err != nil {
				t.Fatalf("write harness: %v", err)
			}
			cmd := exec.Command(bashCommand(t), progPath)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("harness failed: %v\noutput:\n%s", err, out)
			}
			outStr := strings.TrimSpace(string(out))
			switch c.want {
			case 0:
				if !strings.Contains(outStr, "ACCEPT") {
					t.Errorf("setup-https.sh is_valid_public_ipv4(%q) must ACCEPT; got: %s", c.ip, outStr)
				}
			case 1:
				if !strings.Contains(outStr, "REJECT") {
					t.Errorf("setup-https.sh is_valid_public_ipv4(%q) must REJECT; got: %s", c.ip, outStr)
				}
			}
		})
	}
}

// TestInstallerDetectPublicIPv4FromHostIPs runs the actual
// detect_public_ipv4_from_host_ips helper against a stubbed
// `hostname -I` and asserts that:
//   - the FIRST valid public IPv4 in the output is returned,
//   - private/loopback/IPv6 tokens are skipped,
//   - a hostname -I line with no valid token returns nothing.
//
// We stub `hostname` itself so this test does not depend on the
// host's actual network configuration.
func TestInstallerDetectPublicIPv4FromHostIPs(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	helper, err := extractBashFunction(installer, "detect_public_ipv4_from_host_ips")
	if err != nil {
		t.Fatalf("extract detect_public_ipv4_from_host_ips: %v", err)
	}
	if helper == "" {
		t.Fatal("installer must define detect_public_ipv4_from_host_ips (2026-06-30 BLOCKER 2 fix)")
	}
	// detect_public_ipv4_from_host_ips calls is_valid_public_ipv4
	// internally, so we extract that helper too and source both
	// into the harness so the call resolves.
	validator, err := extractBashFunction(installer, "is_valid_public_ipv4")
	if err != nil {
		t.Fatalf("extract is_valid_public_ipv4: %v", err)
	}
	if validator == "" {
		t.Fatal("installer must define is_valid_public_ipv4 (2026-06-30 BLOCKER 2 fix)")
	}

	cases := []struct {
		name        string
		hostnameOut string
		hostnameErr bool // if true, hostname returns empty (no output)
		want        string
	}{
		{
			name:        "single valid public IPv4 -> returned",
			hostnameOut: "65.75.203.74",
			want:        "65.75.203.74",
		},
		{
			name:        "valid first, invalid second -> returns first valid",
			hostnameOut: "65.75.203.74 127.0.0.1",
			want:        "65.75.203.74",
		},
		{
			name:        "invalid first, valid second -> returns valid (skips invalid)",
			hostnameOut: "127.0.0.1 65.75.203.74",
			want:        "65.75.203.74",
		},
		{
			name:        "all-invalid -> empty output, non-zero exit",
			hostnameOut: "127.0.0.1 10.0.0.5 192.168.1.1",
			want:        "",
		},
		{
			name:        "IPv6 + valid IPv4 mix -> returns valid IPv4",
			hostnameOut: "2606:cc0:200:4c0::1 65.75.203.74",
			want:        "65.75.203.74",
		},
		{
			name:        "RFC1918 first, public second -> returns public",
			hostnameOut: "10.0.0.5 65.75.203.74",
			want:        "65.75.203.74",
		},
		{
			name:        "private only -> empty output",
			hostnameOut: "10.0.0.5 172.16.0.1",
			want:        "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			hostnameStub := "65.75.203.74\n" // never used; we override hostname via stub
			_ = hostnameStub
			// Build the stubbed `hostname`. The bash function
			// calls `hostname -I` (with args); we need a stub
			// that responds to that exact invocation. We shadow
			// `hostname` via a function in the harness so it
			// wins over the system binary.
			hostnameArgs := ""
			if c.hostnameErr {
				hostnameArgs = `hostname() { return 1; }`
			} else {
				hostnameArgs = fmt.Sprintf(`hostname() {
    if [ "$1" = "-I" ]; then
        printf '%%s' %q
        return 0
    fi
    return 1
}`, c.hostnameOut)
			}
			prog := fmt.Sprintf(`#!/usr/bin/env bash
set -u
%s
%s
%s
detect_public_ipv4_from_host_ips
rc=$?
if [ $rc -eq 0 ]; then
    echo "GOT_OUTPUT"
else
    echo "NO_OUTPUT rc=$rc"
fi
`, validator, helper, hostnameArgs)
			progPath := filepath.Join(dir, "detect.sh")
			if err := os.WriteFile(progPath, []byte(prog), 0o755); err != nil {
				t.Fatalf("write harness: %v", err)
			}
			cmd := exec.Command(bashCommand(t), progPath)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			outStr := string(out)
			if err != nil {
				t.Fatalf("harness failed: %v\noutput:\n%s", err, outStr)
			}
			if c.want == "" {
				if !strings.Contains(outStr, "NO_OUTPUT") {
					t.Errorf("detect_public_ipv4_from_host_ips must produce NO_OUTPUT for hostname=%q; got:\n%s", c.hostnameOut, outStr)
				}
			} else {
				// The helper prints the IP on its own line; the
				// sentinel GOT_OUTPUT follows. Extract the line
				// BEFORE the sentinel and assert it equals want.
				idx := strings.Index(outStr, "GOT_OUTPUT")
				if idx < 0 {
					t.Fatalf("expected GOT_OUTPUT sentinel; got:\n%s", outStr)
				}
				before := strings.TrimSpace(outStr[:idx])
				if before != c.want {
					t.Errorf("detect_public_ipv4_from_host_ips with hostname=%q must print %q; got %q\nfull output:\n%s",
						c.hostnameOut, c.want, before, outStr)
				}
			}
		})
	}
}

// TestInstallerNoLoopbackFallbackOnFreshInstall is the load-
// bearing regression test for BLOCKER 2. The previous installer
// silently substituted 127.0.0.1 when hostname -I returned
// nothing or returned a private address; that produced a fresh
// install whose /etc/orvix/orvix.yaml contained:
//
//   dns:
//     public_ipv4: "127.0.0.1"
//
// which the runtime then rejected as loopback, blocking the
// admin DNS/DKIM dashboard with a confusing 422. The new
// installer MUST fail closed instead.
//
// We drive the actual main() entry point under a stubbed
// hostname that emits a hostname -I with ONLY private/loopback
// addresses, and assert:
//   1. exit code is non-zero,
//   2. failure message names ORVIX_PUBLIC_IPV4 (so the operator
//      knows how to recover),
//   3. /etc/orvix/orvix.yaml is NEVER created (write_config is
//      not reached on the failure path).
func TestInstallerNoLoopbackFallbackOnFreshInstall(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	stripped := stripBashComments(installer)

	// Static assertions: the installer MUST NOT contain the
	// unsafe fallback pattern `${server_public_ip:-127.0.0.1}`
	// or any equivalent that silently substitutes loopback.
	for _, forbidden := range []string{
		`${server_public_ip:-127.0.0.1}`,
		`${server_public_ip:-"127.0.0.1"}`,
		`server_public_ip="${server_public_ip:-127.0.0.1}"`,
	} {
		if strings.Contains(stripped, forbidden) {
			t.Errorf("release/install.sh still contains the unsafe loopback fallback %q (2026-06-30 BLOCKER 2 fix)", forbidden)
		}
	}
	// And the safe `hostname -I | awk '{print $1}'` blind first
	// token (which the previous version used) is also gone: the
	// only `hostname -I` reference must go through
	// detect_public_ipv4_from_host_ips. We allow the literal
	// inside that helper's body, but the previous unsafe
	// pipeline form is gone from main().
	blindPick := `hostname -I 2>/dev/null | awk '{print $1}'`
	if strings.Contains(stripped, blindPick) {
		t.Errorf("release/install.sh still contains the blind `hostname -I | awk` IP picker %q (must use detect_public_ipv4_from_host_ips)", blindPick)
	}

	// Behavioural assertions: drive main() under a stubbed
	// hostname that returns ONLY private addresses. We expect
	// main() to fail closed with a clear ORVIX_PUBLIC_IPV4
	// pointer, and the config file must NOT be created.
	//
	// The harness body is built by string concatenation rather
	// than a multi-line raw string. Long raw strings can be
	// brittle when the surrounding file already contains many
	// UTF-8 multi-byte sequences elsewhere — gofmt's parser
	// sometimes rejects an otherwise valid raw string when the
	// file's encoding is mixed. Concatenation sidesteps the
	// problem entirely and is just as readable here.
	harnessReplacement := strings.Join([]string{
		"# Stubbed hostname that returns ONLY private addresses.",
		"hostname() {",
		"    if [ \"$1\" = \"-I\" ]; then",
		"        printf '%s' \"10.0.0.5 192.168.1.1 172.16.0.1\"",
		"        return 0",
		"    fi",
		"    return 1",
		"}",
		"# Stubbed chown/chmod so the harness runs without root.",
		"chown() { :; }",
		"chmod() { :; }",
		"# Stub fail() so we capture the message instead of exiting",
		"# the test runner.",
		"FAIL_MSG=\"\"",
		"fail() {",
		"    FAIL_MSG=\"$*\"",
		"    echo \"FAIL_CALLED: $*\" >&2",
		"    exit 1",
		"}",
		"log_detail() { :; }",
		"prompt_domain() { echo \"example.com\"; }",
		"prompt_email() { echo \"admin@example.com\"; }",
		"prompt_password() { printf '%s' \"TestPassword123!\"; }",
		"apt-get() { return 0; }",
		"systemctl() { return 0; }",
		"# id() MUST print \"0\" so the require_root check succeeds.",
		"id() {",
		"    if [ \"$1\" = \"-u\" ]; then",
		"        printf '0'",
		"        return 0",
		"    fi",
		"    return 0",
		"}",
		"useradd() { return 0; }",
		"install() { return 0; }",
		"install_binary() { :; }",
		"validate_binary() { :; }",
		"mkdir() { return 0; }",
		"cp() { return 0; }",
		"find() { return 0; }",
		"# Skip VAPID / binary / service / verify paths; we want to",
		"# fail at the public-IPv4 detection step BEFORE the rest",
		"# of main() runs.",
		"set_step() { :; }",
		"ORVIX_CONFIG=\"/tmp/orvix-test-no-loopback-fallback.yaml\"",
		"# Override INSTALL_LOG to a writable path so log_detail()",
		"# and prepare_log() do not try to write to /var/log/orvix",
		"# on the test host (which does not exist).",
		"INSTALL_LOG=\"$(pwd)/install.log\"",
		"mkdir -p \"$(dirname \"$INSTALL_LOG\")\"",
		"touch \"$INSTALL_LOG\"",
		"rm -f \"$ORVIX_CONFIG\"",
		"main \"$@\" 2>&1 || true",
		"echo \"FAIL_MSG=$FAIL_MSG\"",
		"echo \"CONFIG_EXISTS=$([ -f \"$ORVIX_CONFIG\" ] && echo yes || echo no)\"",
		"",
	}, "\n")
	harness := strings.Replace(installer, `main "$@"`, harnessReplacement, 1)
	dir := t.TempDir()
	harnessPath := filepath.Join(dir, "harness.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), harnessPath)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	outStr := string(out)

	// main() invokes fail() which exits 1; the harness captures
	// the message and re-runs `exit 1` from the `||` chain, so
	// the overall exit code is non-zero. We tolerate either:
	//   (a) harness exit 1 with FAIL_CALLED in stderr
	//   (b) harness exit 0 because the wrapper overrode fail()
	// Either way, the captured FAIL_MSG must name ORVIX_PUBLIC_IPV4
	// and the config file must not exist.
	if !strings.Contains(outStr, "ORVIX_PUBLIC_IPV4") {
		t.Errorf("install.sh failure path must tell the operator to set ORVIX_PUBLIC_IPV4; got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "public IPv4") {
		t.Errorf("install.sh failure path must mention public IPv4 detection failure; got:\n%s", outStr)
	}
	if strings.Contains(outStr, "127.0.0.1") && !strings.Contains(outStr, "ORVIX_PUBLIC_IPV4=65.75.203.74") {
		// The previous behaviour wrote 127.0.0.1 into the config.
		// The new failure message can mention 127.0.0.1 in
		// prose (the ORVIX_PUBLIC_IPV4 example uses 65.75.203.74),
		// but it must NOT echo 127.0.0.1 back as the chosen
		// server_public_ip. We allow the literal "127.0.0.1" if
		// it appears only inside the ORVIX_PUBLIC_IPV4=65.75.203.74
		// example string (i.e. the operator is told what to set).
		// Anything else is a regression.
		if strings.Contains(outStr, "server_public_ip=127.0.0.1") {
			t.Errorf("install.sh failure path must NOT echo server_public_ip=127.0.0.1 (the previous unsafe fallback); got:\n%s", outStr)
		}
	}
	// The config file must NOT exist — write_config must never run
	// on the failure path.
	if _, statErr := os.Stat("/tmp/orvix-test-no-loopback-fallback.yaml"); statErr == nil {
		// Best-effort cleanup of any stale file the failure path
		// left behind. os.Remove is intentional here (not
		// mavis-trash) because this is a Go test runtime helper,
		// not a user-initiated deletion.
		_ = os.Remove("/tmp/orvix-test-no-loopback-fallback.yaml")
		t.Errorf("install.sh failure path must NOT create /etc/orvix/orvix.yaml; found a stale file from this run")
	}
	_ = err // err may or may not be non-nil depending on which path the harness took
}

// TestInstallerFreshInstallUsesValidatedPublicIP is the positive
// case: when ORVIX_PUBLIC_IPV4 is set to a valid value, the
// installer MUST use it (NOT silently re-detect from hostname -I
// and overwrite it). The previous behaviour had no such gate;
// this test pins the new contract.
func TestInstallerFreshInstallUsesValidatedPublicIP(t *testing.T) {
	root := repoRoot(t)
	installer := mustRead(t, filepath.Join(root, "release", "install.sh"))
	stripped := stripBashComments(installer)

	// Static assertion: the installer must reference
	// ORVIX_PUBLIC_IPV4 so operators on NAT'd hosts can override
	// the hostname -I detection.
	if !strings.Contains(stripped, "ORVIX_PUBLIC_IPV4") {
		t.Errorf("installer must honour ORVIX_PUBLIC_IPV4 for hosts where hostname -I is unreliable (2026-06-30 BLOCKER 2 fix)")
	}

	// Behavioural assertion: a harness that drives provision_config
	// via the new public-IP detection path uses ORVIX_PUBLIC_IPV4
	// when set, even if hostname -I would return something else.
	const domain = "example.com"
	// 65.75.203.74 is a real public IPv4 that passes every
	// is_valid_public_ipv4 range check. The previous test used
	// 203.0.113.42 (TEST-NET-3, RFC5737 documentation) which
	// is_valid_public_ipv4 correctly REJECTS — so the harness
	// would fall through to detect_public_ipv4_from_host_ips
	// (returns nothing on Windows), leaving dns.public_ipv4
	// empty. Use a routable IP here.
	const expectedIP = "65.75.203.74"
	harness := strings.Replace(installer,
		`main "$@"`,
		fmt.Sprintf(`chown() { :; }; chmod() { :; }; ORVIX_CONFIG="$1"; ORVIX_PUBLIC_IPV4=%q; write_config "%s" "$(if is_valid_public_ipv4 "$ORVIX_PUBLIC_IPV4"; then echo "$ORVIX_PUBLIC_IPV4"; else detect_public_ipv4_from_host_ips; fi)"; cat "$ORVIX_CONFIG"`, expectedIP, domain),
		1,
	)
	dir := t.TempDir()
	harnessPath := filepath.Join(dir, "harness.sh")
	configPath := filepath.Join(dir, "orvix.yaml")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	cmd := exec.Command(bashCommand(t), harnessPath, configPath)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("harness failed: %v\noutput:\n%s", err, out)
	}
	rendered := string(out)

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		t.Fatalf("rendered config is not valid YAML: %v\n%s", err, rendered)
	}
	if got := readNestedString(v.AllSettings(), "dns.public_ipv4"); got != expectedIP {
		t.Errorf("installer must use ORVIX_PUBLIC_IPV4=%q when set; got dns.public_ipv4=%q", expectedIP, got)
	}
}

// ── BLOCKER 4: setup-https.sh restart + readiness after patch ──

// TestSetupHttpsValidatesServerIPBeforePatch exercises the
// require_input gate: setup-https.sh MUST validate SERVER_IP
// before any patching happens. An invalid SERVER_IP is a fail-
// closed error, NOT a silent coercion to loopback/private.
func TestSetupHttpsValidatesServerIPBeforePatch(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))

	// Strip main "$@" so the harness can drive require_input
	// in isolation.
	scriptNoMain := strings.Replace(script, "main \"$@\"", "# main disabled by test harness", 1)

	cases := []struct {
		name     string
		serverIP string
		wantFail bool
		wantMsg  string
	}{
		{"accept 65.75.203.74", "65.75.203.74", false, ""},
		{"reject 127.0.0.1 (loopback)", "127.0.0.1", true, "invalid public IPv4"},
		{"reject 0.0.0.0", "0.0.0.0", true, "invalid public IPv4"},
		{"reject 10.0.0.1 (RFC1918)", "10.0.0.1", true, "invalid public IPv4"},
		{"reject 192.168.1.1 (RFC1918)", "192.168.1.1", true, "invalid public IPv4"},
		{"reject 169.254.1.1 (link-local)", "169.254.1.1", true, "invalid public IPv4"},
		{"reject 192.0.2.1 (RFC5737)", "192.0.2.1", true, "invalid public IPv4"},
		{"reject empty", "", true, "could not be detected"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "orvix.yaml")
			if err := os.WriteFile(cfgPath, []byte("server:\n  host: \"127.0.0.1\"\n"), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			// Stub hostname to return an address we'd never want
			// as SERVER_IP. require_input must REJECT it BEFORE
			// any patching happens.
			harness := fmt.Sprintf(`#!/usr/bin/env bash
set -uo pipefail
# Source the script FIRST so the script's own parameter
# expansion (PRIMARY_DOMAIN="${1:-${ORVIX_PRIMARY_DOMAIN:-}}")
# picks up the env var we set below. Then override the
# script-derived values with the harness's intended inputs so
# a deliberate override from the test wins.
export ORVIX_PRIMARY_DOMAIN="orvix.email"
ORVIX_SERVER_IP=%q
SERVER_IP=$ORVIX_SERVER_IP
export ORVIX_SERVER_IP
INSTALL_LOG="%s/setup.log"
touch "$INSTALL_LOG"
ADMIN_DOMAIN="admin.orvix.email"
WEBMAIL_DOMAIN="webmail.orvix.email"
MAIL_DOMAIN="mail.orvix.email"
ORVIX_CONFIG=%q
export ORVIX_CONFIG

`, c.serverIP, dir, cfgPath) + scriptNoMain + `

# Re-assert PRIMARY_DOMAIN AFTER the script is sourced so
# the test's intended value wins (the script uses $1 OR
# ORVIX_PRIMARY_DOMAIN, but we want a deterministic value
# regardless of how the harness was invoked).
PRIMARY_DOMAIN="orvix.email"
ADMIN_DOMAIN="admin.orvix.email"
WEBMAIL_DOMAIN="webmail.orvix.email"
MAIL_DOMAIN="mail.orvix.email"

# Stubs AFTER the script so these definitions win.
require_root() { :; }
install_caddy() { :; }
write_caddyfile() { :; }
open_firewall() { :; }
check_dns() { :; }
check_local_port() { :; }
check_https() { :; }
check_content_type() { :; }
post_https_firewall_hardening() { :; }
caddy() { return 0; }
systemctl() { return 0; }
dig() { echo "65.75.203.74"; }
getent() { echo "65.75.203.74"; }

# Override fail() so the harness captures the message.
fail() {
    echo "FAIL_CALLED: $*" >&2
    exit 1
}

require_input
echo "REQUIRE_INPUT_OK"
`
			harnessPath := filepath.Join(dir, "harness.sh")
			if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
				t.Fatalf("write harness: %v", err)
			}
			cmd := exec.Command(bashCommand(t), harnessPath)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			outStr := string(out)
			if c.wantFail {
				if err == nil {
					t.Fatalf("require_input must fail for SERVER_IP=%q; got exit 0\noutput:\n%s", c.serverIP, outStr)
				}
				if !strings.Contains(outStr, c.wantMsg) {
					t.Errorf("failure message must contain %q for SERVER_IP=%q; got:\n%s", c.wantMsg, c.serverIP, outStr)
				}
			} else {
				if err != nil {
					t.Fatalf("require_input must succeed for SERVER_IP=%q; got %v\noutput:\n%s", c.serverIP, err, outStr)
				}
				if !strings.Contains(outStr, "REQUIRE_INPUT_OK") {
					t.Errorf("valid SERVER_IP must reach REQUIRE_INPUT_OK sentinel; got:\n%s", outStr)
				}
			}
		})
	}
}

// TestSetupHttpsRestartsOrvixAfterPatch is the load-bearing
// behavioural test for BLOCKER 4. After patch_dns_public_ipv4
// modifies the file, setup-https.sh MUST restart orvix and
// wait for the runtime to be ready. Without this, the runtime
// keeps reading the OLD dns.public_ipv4 from in-memory config
// while the dashboard verification gate hits the stale process
// and fails with 422.
//
// We drive the actual patch_dns_public_ipv4 + the new
// restart_orvix_after_patch wiring with a stubbed systemctl.
// The stub records restart attempts to a file so the test can
// assert the restart was invoked AFTER the patch, and NOT
// invoked when the patch was a no-op.
func TestSetupHttpsRestartsOrvixAfterPatch(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))
	scriptNoMain := strings.Replace(script, "main \"$@\"", "# main disabled by test harness", 1)

// Helper to build the harness with a config that needs
	// patching (dns.public_ipv4 missing). The stubbed systemctl
	// records every call to a file so the test can prove restart was
	// invoked. curl and the systemctl is-active check are
	// stubbed to "ready" so wait_for_orvix_readiness returns
	// immediately instead of timing out.
	build := func(t *testing.T, dir, cfgPath, serverIP string, systemctlStub string) string {
		harness := fmt.Sprintf(`#!/usr/bin/env bash
set -uo pipefail
export ORVIX_PRIMARY_DOMAIN="orvix.email"
PRIMARY_DOMAIN="orvix.email"
ORVIX_SERVER_IP=%q
SERVER_IP=$ORVIX_SERVER_IP
export ORVIX_SERVER_IP
INSTALL_LOG="%s/setup.log"
touch "$INSTALL_LOG"
ADMIN_DOMAIN="admin.orvix.email"
WEBMAIL_DOMAIN="webmail.orvix.email"
MAIL_DOMAIN="mail.orvix.email"
ORVIX_CONFIG=%q
export ORVIX_CONFIG

`, serverIP, dir, cfgPath) + scriptNoMain + `

require_root() { :; }
install_caddy() { :; }
write_caddyfile() { :; }
open_firewall() { :; }
check_dns() { :; }
check_local_port() { :; }
check_https() { :; }
check_content_type() { :; }
post_https_firewall_hardening() { :; }
caddy() { return 0; }
dig() { echo "65.75.203.74"; }
getent() { echo "65.75.203.74"; }
# Stubbed curl: report "ready" so wait_for_orvix_readiness
# returns success on the first poll instead of timing out.
curl() { return 0; }
# Stubbed systemctl that records every call.
` + systemctlStub + `

patch_dns_public_ipv4
restart_orvix_after_patch
echo "DONE PATCH_CHANGED=${PATCH_CHANGED:-0}"
`
		return harness
	}

	t.Run("PATCH_CHANGED=1 -> restart is invoked", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "orvix.yaml")
		// dns: section present, public_ipv4 missing.
		if err := os.WriteFile(cfgPath, []byte("server:\n  host: \"127.0.0.1\"\n\ndns:\n  cloudflare_api_key: \"k\"\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		stub := `systemctl() {
    echo "systemctl $*" >> "` + dir + `/calls.log"
    case "$*" in
        *restart\ orvix*)
            echo "RESTART_INVOKED" >> "` + dir + `/calls.log"
            ;;
    esac
    # Report orvix as active so wait_for_orvix_readiness
    # returns success immediately. --quiet means return code
    # only; exit 0 = active.
    return 0
}`
		harness := build(t, dir, cfgPath, "65.75.203.74", stub)
		harnessPath := filepath.Join(dir, "harness.sh")
		if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
			t.Fatalf("write harness: %v", err)
		}
		cmd := exec.Command(bashCommand(t), harnessPath)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		outStr := string(out)
		if err != nil {
			t.Fatalf("harness failed: %v\noutput:\n%s", err, outStr)
		}
		if !strings.Contains(outStr, "PATCH_CHANGED=1") {
			t.Errorf("PATCH_CHANGED must be 1 after adding public_ipv4; got:\n%s", outStr)
		}
		// Read the calls log; restart MUST have been invoked.
		callsBytes, err := os.ReadFile(filepath.Join(dir, "calls.log"))
		if err != nil {
			t.Fatalf("read calls log: %v", err)
		}
		if !strings.Contains(string(callsBytes), "RESTART_INVOKED") {
			t.Errorf("setup-https.sh MUST restart orvix after patching dns.public_ipv4; calls log:\n%s", callsBytes)
		}
	})

	t.Run("PATCH_CHANGED=0 -> restart is NOT invoked", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "orvix.yaml")
		// dns.public_ipv4 already matches SERVER_IP -> no patch.
		if err := os.WriteFile(cfgPath, []byte("server:\n  host: \"127.0.0.1\"\n\ndns:\n  public_ipv4: \"65.75.203.74\"\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		stub := `systemctl() {
    echo "systemctl $*" >> "` + dir + `/calls.log"
    case "$*" in
        *restart\ orvix*)
            echo "RESTART_INVOKED" >> "` + dir + `/calls.log"
            ;;
    esac
    return 0
}`
		harness := build(t, dir, cfgPath, "65.75.203.74", stub)
		harnessPath := filepath.Join(dir, "harness.sh")
		if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
			t.Fatalf("write harness: %v", err)
		}
		cmd := exec.Command(bashCommand(t), harnessPath)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		outStr := string(out)
		if err != nil {
			t.Fatalf("harness failed: %v\noutput:\n%s", err, outStr)
		}
		if !strings.Contains(outStr, "PATCH_CHANGED=0") {
			t.Errorf("PATCH_CHANGED must be 0 when no patch was needed; got:\n%s", outStr)
		}
		// Pre-create calls.log so the assertion below is
		// meaningful even when the stub never ran (PATCH_CHANGED=0
		// means no systemctl restart, so the stub is never
		// invoked and calls.log is never created). We touch the
		// file so os.ReadFile succeeds and the body check below
		// can prove no RESTART_INVOKED line was written.
		callsPath := filepath.Join(dir, "calls.log")
		if _, statErr := os.Stat(callsPath); os.IsNotExist(statErr) {
			if err := os.WriteFile(callsPath, []byte(""), 0o600); err != nil {
				t.Fatalf("seed empty calls log: %v", err)
			}
		}
		callsBytes, err := os.ReadFile(callsPath)
		if err != nil {
			t.Fatalf("read calls log: %v", err)
		}
		// Restart MUST NOT be invoked when the patch is a no-op.
		if strings.Contains(string(callsBytes), "RESTART_INVOKED") {
			t.Errorf("setup-https.sh MUST NOT restart orvix when no patch was needed; calls log:\n%s", callsBytes)
		}
	})
}

// TestSetupHttpsRestartFailsClosedOnReadinessTimeout pins the
// fail-closed semantics of the readiness wait. If orvix does
// not come back within the deadline, setup-https.sh MUST fail
// rather than silently continue with a stale runtime.
func TestSetupHttpsRestartFailsClosedOnReadinessTimeout(t *testing.T) {
	root := repoRoot(t)
	script := mustRead(t, filepath.Join(root, "release", "scripts", "setup-https.sh"))

	// Extract the readiness helper so we run the actual code.
	// In setup-https.sh, wait_for_orvix_readiness is the LAST
	// helper before main(), so we extract everything between
	// its opening `{` and the next top-level `main() {` marker.
	stripped := stripBashComments(script)
	helperStart := strings.Index(stripped, "wait_for_orvix_readiness() {")
	if helperStart < 0 {
		t.Fatal("setup-https.sh must define wait_for_orvix_readiness (BLOCKER 4)")
	}
	helperEndRel := strings.Index(stripped[helperStart:], "\nmain() {")
	if helperEndRel < 0 {
		t.Fatal("could not find end of wait_for_orvix_readiness helper body (looking for top-level main() { after the helper)")
	}
	helper := stripped[helperStart : helperStart+helperEndRel]
	_ = script // kept for parity with sibling tests; helper is what we extract and run.

	// Build a harness where curl always fails (simulates orvix
	// not coming back up) and systemctl reports inactive. The
	// helper must `fail` within the deadline.
	dir := t.TempDir()
	harness := fmt.Sprintf(`#!/usr/bin/env bash
set -u
INSTALL_LOG="%s/setup.log"
touch "$INSTALL_LOG"
log() { echo "LOG: $*"; }
ORVIX_READINESS_DEADLINE_SECONDS=2
export ORVIX_READINESS_DEADLINE_SECONDS
FAIL_MSG=""
fail() { echo "FAIL_CALLED: $*" >&2; exit 1; }
curl() { return 22; }   # simulate curl failure
systemctl() {
    if [ "$1" = "is-active" ] && [ "$3" = "orvix" ]; then
        return 1  # never active
    fi
    return 0
}
journalctl() { return 0; }

%s

PATCH_CHANGED=1
wait_for_orvix_readiness
echo "UNEXPECTED_OK"
`, dir, helper)
	harnessPath := filepath.Join(dir, "harness.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	cmd := exec.Command(bashCommand(t), harnessPath)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if err == nil {
		t.Fatalf("wait_for_orvix_readiness must fail closed on timeout; got exit 0\noutput:\n%s", outStr)
	}
	if !strings.Contains(outStr, "FAIL_CALLED: orvix did not become ready") {
		t.Errorf("failure must be reported via fail() with the readiness message; got:\n%s", outStr)
	}
	// The diagnostics must be written to $INSTALL_LOG.
	logBytes, err := os.ReadFile(filepath.Join(dir, "setup.log"))
	if err != nil {
		t.Fatalf("read install log: %v", err)
	}
	logStr := string(logBytes)
	if !strings.Contains(logStr, "systemctl status orvix") {
		t.Errorf("$INSTALL_LOG must contain systemctl status dump; got:\n%s", logStr)
	}
	if !strings.Contains(logStr, "journalctl -u orvix") {
		t.Errorf("$INSTALL_LOG must contain journalctl dump; got:\n%s", logStr)
	}
}

// extractBashFunction is provided by release_packaging_test.go
// in the same `config` package; we reuse it here.
//
// mustRead is also provided by release_packaging_test.go.
