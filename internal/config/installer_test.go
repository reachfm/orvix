package config

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
		"Admin URL: http://admin.${domain}/admin",
		"Webmail URL: http://admin.${domain}/webmail",
		"JMAP URL: http://mail.${domain}/.well-known/jmap",
		"Mail Hostname: mail.${domain}",
		"SMTP: mail.${domain}:25",
		"IMAP: mail.${domain}:143",
		"POP3: mail.${domain}:110",
		"DNS required: A admin.${domain} -> ${server_ip}",
		"DNS required: A mail.${domain} -> ${server_ip}",
		"Temporary Admin API: http://${server_ip}:8080/admin",
		"release/scripts/setup-https.sh ${domain} ${server_ip}",
		"Admin password (min 8 chars):",
		"admin password must be at least 8 characters",
	}
	for _, item := range required {
		if !strings.Contains(installer, item) {
			t.Fatalf("installer missing %q", item)
		}
	}
	forbidden := []string{
		"Admin UI: http://mail.${primary_domain}:8080/admin",
		"Admin UI: http://$(hostname -f 2>/dev/null || hostname):8080/admin",
		"hostname -f 2>/dev/null || hostname",
		"ORVIX_ADMIN_PASSWORD=\"$escaped_password\"",
		"RC1 CLEAN INSTALLER",
		"min 12 chars",
		"at least 12 characters",
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
	verifyIndex := strings.Index(installer, "if [ \"$http_code\" != \"200\" ]; then")
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
		"admin.<domain> -> 127.0.0.1:8080",
		"mail.<domain>  -> 127.0.0.1:8081",
		"ADMIN_DOMAIN=\"${ADMIN_DOMAIN:-admin.$PRIMARY_DOMAIN}\"",
		"MAIL_DOMAIN=\"${MAIL_DOMAIN:-mail.$PRIMARY_DOMAIN}\"",
		"reverse_proxy 127.0.0.1:8080",
		"reverse_proxy 127.0.0.1:8081",
		"ufw allow 80/tcp",
		"ufw allow 443/tcp",
		"caddy validate --config /etc/caddy/Caddyfile",
		"systemctl is-active --quiet caddy",
		"check_dns \"$ADMIN_DOMAIN\"",
		"check_dns \"$MAIL_DOMAIN\"",
		"check_local_port 80",
		"check_local_port 443",
		"check_https \"https://$ADMIN_DOMAIN/admin\" HEAD",
		"check_https \"https://$ADMIN_DOMAIN/api/v1/health\" GET",
		"check_https \"https://$MAIL_DOMAIN/.well-known/jmap\" GET",
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
