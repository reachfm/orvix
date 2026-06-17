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
		"ca-certificates curl jq sqlite3 openssl tar gzip redis-server libcap2-bin iproute2 ufw",
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
		"Admin URL:   https://admin.${domain}/admin",
		"Webmail URL: https://webmail.${domain}/",
		"JMAP URL:    https://mail.${domain}/.well-known/jmap",
		// The TEMPORARY block is what the operator sees BEFORE
		// setup-https.sh runs. It must be clearly labelled and
		// bound to the server IP (since admin.${domain} is not
		// reachable without HTTPS+DNS at this stage).
		"TEMPORARY URLs (plain HTTP, no TLS",
		"Admin UI:    http://${server_ip}:8080/admin",
		"Webmail UI:  http://${server_ip}:8080/webmail",
		"JMAP:        http://${server_ip}:8081/.well-known/jmap",
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
		"mail.<domain>    -> 127.0.0.1:8081",
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
		"restrict external access to TCP 8080 and 8081",
		"keep mail protocol ports 25, 110, and 143 unchanged",
		"sudo ufw deny 8080/tcp",
		"sudo ufw deny 8081/tcp",
	} {
		if !strings.Contains(script, item) {
			t.Fatalf("https setup script missing %q", item)
		}
	}
	if strings.Contains(script, "check_https \"https://$ADMIN_DOMAIN/api/v1/health\" HEAD") {
		t.Fatal("admin health smoke must use GET because the API route is GET-only")
	}
}

func TestReleaseReferencedFilesExist(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		filepath.Join("release", "scripts", "setup-https.sh"),
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
		"/webmail/assets/",
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
