package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMinimal_NoControlCharacters(t *testing.T) {
	// Create a temporary config file with control characters (simulating the VPS bug)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "orvix.yaml")

	// Write a config with a null byte (actual control character that YAML rejects)
	badConfig := "server:\x00\n  listen: \":8080\"\n"
	if err := os.WriteFile(configPath, []byte(badConfig), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Set config dir to temp dir
	origDir := os.Getenv("ORVIX_CONFIG_DIR")
	os.Setenv("ORVIX_CONFIG_DIR", tmpDir)
	defer os.Setenv("ORVIX_CONFIG_DIR", origDir)

	// This should fail with control character error
	_, err := LoadMinimal()
	if err == nil {
		t.Error("Expected error for config with control characters, got nil")
	}
}

func TestLoadMinimal_ValidConfig(t *testing.T) {
	// Create a temporary valid config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "orvix.yaml")

	validConfig := `server:
  listen: ":8080"
  debug: false

database:
  driver: "sqlite"
  dsn: "orvix.db"

stalwart:
  binary_path: "/usr/local/bin/stalwart"
  admin_port: 8081
  smtp_ports: [25, 587, 465]
  imap_ports: [143, 993]
  pop3_ports: [110, 995]
  jmap_ports: [80, 443]
`
	if err := os.WriteFile(configPath, []byte(validConfig), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Set config dir to temp dir
	origDir := os.Getenv("ORVIX_CONFIG_DIR")
	os.Setenv("ORVIX_CONFIG_DIR", tmpDir)
	defer os.Setenv("ORVIX_CONFIG_DIR", origDir)

	// This should succeed
	cfg, err := LoadMinimal()
	if err != nil {
		t.Fatalf("LoadMinimal failed: %v", err)
	}

	// Verify config was loaded
	if cfg.Server.Listen != ":8080" {
		t.Errorf("Expected server.listen = :8080, got %s", cfg.Server.Listen)
	}
	if cfg.Stalwart.AdminPort != 8081 {
		t.Errorf("Expected stalwart.admin_port = 8081, got %d", cfg.Stalwart.AdminPort)
	}
}
