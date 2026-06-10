package config

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
		"ORVIX MAIL PLATFORM",
		"RC1 CLEAN INSTALLER",
		"progress_bar()",
		"set_step \"preparing\" \"Preparing system\" 10",
		"set_step \"dependencies\" \"Installing dependencies\" 20",
		"set_step \"user\" \"Creating service account\" 35",
		"set_step \"configuration-input\" \"Collecting admin settings\" 45",
		"set_step \"binary\" \"Installing Orvix binary\" 60",
		"set_step \"configuration\" \"Writing configuration\" 75",
		"set_step \"systemd\" \"Starting services\" 85",
		"set_step \"verification\" \"Verifying install\" 95",
		"render_success",
		"trap on_error ERR",
		"tail -n 80 \"$INSTALL_LOG\"",
		"run_quiet apt-get update -qq",
		"apt-get install -y -qq",
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
		"curl -fsSI http://127.0.0.1:8080/admin",
		"curl -fsSI http://127.0.0.1:8080/webmail",
		"curl -fsS http://127.0.0.1:8081/.well-known/jmap",
		"setcap 'cap_net_bind_service=+ep' \"$ORVIX_BIN\"",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"BOOTSTRAP_ENV=\"${BOOTSTRAP_ENV:-/etc/orvix/bootstrap.env}\"",
		"ORVIX_ADMIN_EMAIL",
		"ORVIX_ADMIN_PASSWORD_B64",
		"printf '%s' \"$password\" | base64 | tr -d '\\n'",
		"rm -f \"$BOOTSTRAP_ENV\"",
		"/api/v1/auth/login",
		"journalctl -u orvix.service -n 80 --no-pager",
		"Admin UI: http://admin.${domain}",
		"Mail Hostname: mail.${domain}",
		"SMTP: mail.${domain}:25",
		"IMAP: mail.${domain}:143",
		"POP3: mail.${domain}:110",
		"DNS required: A admin.${domain} -> ${server_ip}",
		"DNS required: A mail.${domain} -> ${server_ip}",
		"Temporary Admin API: http://${server_ip}:8080/admin",
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
	harness := strings.Replace(installer, `main "$@"`, `chown() { :; }; chmod() { :; }; BOOTSTRAP_ENV="$3"; write_bootstrap_env "$1" "$2"; cat "$BOOTSTRAP_ENV"`, 1)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "bootstrap.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	password := `Admin "quoted" \ slash $ dollar ! bang # hash 123`
	envPath := filepath.Join(harnessDir, "bootstrap.env")
	cmd := exec.Command("bash", "bootstrap.sh", "admin@orvix.email", password, envPath)
	cmd.Dir = harnessDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap env command failed: %v: %s", err, string(out))
	}
	envFile := string(out)
	if strings.Contains(envFile, password) {
		t.Fatal("bootstrap env must not contain raw admin password")
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
	harness := strings.Replace(installer, `main "$@"`, `build_login_payload "$1" "$2"`, 1)
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
	}
	for _, tt := range tests {
		cmd := exec.Command("bash", "payload.sh", tt.email, tt.password)
		cmd.Dir = harnessDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("payload command failed: %v: %s", err, string(out))
		}
		var payload map[string]string
		if err := json.Unmarshal(out, &payload); err != nil {
			t.Fatalf("payload is not JSON: %q: %v", string(out), err)
		}
		if payload["email"] != tt.email {
			t.Fatalf("email mismatch: %q", payload["email"])
		}
		if payload["password"] != tt.password {
			t.Fatalf("password mismatch: %q", payload["password"])
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
