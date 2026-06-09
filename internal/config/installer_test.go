package config

import (
	"os"
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
		"apt-get install -y -qq ca-certificates curl tar gzip redis-server libcap2-bin iproute2",
		"systemctl enable --now redis-server",
		"install -m 0644 \"$ORVIX_SOURCE_DIR/release/admin/index.html\" /usr/share/orvix/admin/index.html",
		"admin_ui_dir: /usr/share/orvix/admin",
		"coremail:",
		"enabled: true",
		"host: 127.0.0.1",
		"admin_port: 8080",
		"curl -fsSI http://127.0.0.1:8080/admin",
		"setcap 'cap_net_bind_service=+ep' \"$ORVIX_BIN\"",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"ORVIX_ADMIN_EMAIL",
		"ORVIX_ADMIN_PASSWORD",
		"/api/v1/auth/login",
	}
	for _, item := range required {
		if !strings.Contains(installer, item) {
			t.Fatalf("installer missing %q", item)
		}
	}
	if strings.Contains(strings.ToLower(installer), "stalwart") {
		t.Fatal("RC1 clean installer must not reference Stalwart")
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
	for _, item := range []string{
		"Orvix Admin",
		"login-form",
		"/api/v1/auth/login",
		"/api/v1/me",
		"Dashboard",
	} {
		if !strings.Contains(page, item) {
			t.Fatalf("admin page missing %q", item)
		}
	}
}
