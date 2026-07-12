package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func setenv(t *testing.T, key, value string) {
	t.Helper()
	prev := os.Getenv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if prev == "" {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, prev)
		}
	})
}

func unsetenv(t *testing.T, key string) {
	t.Helper()
	prev := os.Getenv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if prev != "" {
			os.Setenv(key, prev)
		}
	})
}

func TestLoadHonorsORVIX_CONFIG(t *testing.T) {
	// Ensure ORVIX_CONFIG is not set from the environment so
	// we control exactly which file is loaded.
	unsetenv(t, "ORVIX_CONFIG")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "production.yaml")
	cfgContent := `
server:
  host: "127.0.0.1"
  admin_port: 8080

redis:
  host: "10.0.0.5"
  port: 6380

coremail:
  enabled: true
  smtp_host: 0.0.0.0
  smtp_port: 25
  imap_host: 0.0.0.0
  imap_port: 143
  pop3_host: 0.0.0.0
  pop3_port: 110
  jmap_host: 127.0.0.1
  jmap_port: 8081
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Set ORVIX_CONFIG to the temp file and verify Load() uses it.
	setenv(t, "ORVIX_CONFIG", cfgPath)

	cfg, err := Load(zap.NewNop())
	if err != nil {
		t.Fatalf("Load() with ORVIX_CONFIG: %v", err)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("server.host = %q, want 127.0.0.1", cfg.Server.Host)
	}
	if cfg.Server.AdminPort != 8080 {
		t.Errorf("server.admin_port = %d, want 8080", cfg.Server.AdminPort)
	}
	if cfg.Redis.Host != "10.0.0.5" {
		t.Errorf("redis.host = %q, want 10.0.0.5", cfg.Redis.Host)
	}
	if cfg.Redis.Port != 6380 {
		t.Errorf("redis.port = %d, want 6380", cfg.Redis.Port)
	}
	if !cfg.CoreMail.Enabled {
		t.Error("coremail.enabled = false, want true")
	}
	if cfg.CoreMail.SMTPHost != "0.0.0.0" {
		t.Errorf("coremail.smtp_host = %q, want 0.0.0.0", cfg.CoreMail.SMTPHost)
	}
	if cfg.CoreMail.JMAPHost != "127.0.0.1" {
		t.Errorf("coremail.jmap_host = %q, want 127.0.0.1", cfg.CoreMail.JMAPHost)
	}
}

func TestLoadORVIX_CONFIGMissingFileReturnsError(t *testing.T) {
	unsetenv(t, "ORVIX_CONFIG")

	missing := filepath.Join(t.TempDir(), "nonexistent.yaml")
	setenv(t, "ORVIX_CONFIG", missing)

	_, err := Load(zap.NewNop())
	if err == nil {
		t.Fatal("expected error when ORVIX_CONFIG points to missing file, got nil")
	}
	if !strings.Contains(err.Error(), "ORVIX_CONFIG="+missing) {
		t.Errorf("error must mention the missing ORVIX_CONFIG path, got: %v", err)
	}
}

func TestLoadORVIX_CONFIGMalformedReturnsError(t *testing.T) {
	unsetenv(t, "ORVIX_CONFIG")

	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badPath, []byte("server: [invalid yaml\n"), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	setenv(t, "ORVIX_CONFIG", badPath)

	_, err := Load(zap.NewNop())
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestLoadWithoutORVIX_CONFIGFallsBackToCWD(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "orvix.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
server:
  host: "127.0.0.1"
  admin_port: 8080
coremail:
  enabled: true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// Point ORVIX_CONFIG at our test file so viper does NOT search
	// system paths (/etc/orvix/orvix.yaml) that may exist from a
	// prior install on this host.
	setenv(t, "ORVIX_CONFIG", cfgPath)

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	cfg, err := Load(zap.NewNop())
	if err != nil {
		t.Fatalf("Load() with CWD config: %v", err)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("server.host = %q, want 127.0.0.1", cfg.Server.Host)
	}
	if !cfg.CoreMail.Enabled {
		t.Error("coremail.enabled = false, want true")
	}
}

func TestLoadWithoutConfigFileUsesDefaults(t *testing.T) {
	// Point ORVIX_CONFIG at an empty file in a temp dir so viper does
	// NOT search the system paths (/etc/orvix/orvix.yaml etc.) that
	// may exist from a real install on this host (e.g. after a
	// previous verification run on a VPS).
	dir := t.TempDir()
	emptyCfg := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(emptyCfg, []byte("---\n# intentionally empty\n"), 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	setenv(t, "ORVIX_CONFIG", emptyCfg)

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	cfg, err := Load(zap.NewNop())
	if err != nil {
		t.Fatalf("Load() with no config file: %v", err)
	}
	// Defaults should apply.
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("default server.host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.CoreMail.Enabled {
		t.Error("default coremail.enabled = true, want false")
	}
}

// ── TLS Policy Configuration Tests ─────────────────────────────

func TestTLSPolicyCanonicalKeySetsTLSPolicy(t *testing.T) {
	unsetenv(t, "ORVIX_CONFIG")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tls.yaml")
	content := `
server:
  host: "127.0.0.1"
  admin_port: 8080
coremail:
  enabled: true
outbound:
  tls_policy: strict
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	setenv(t, "ORVIX_CONFIG", cfgPath)
	oldWD, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	cfg, err := Load(zap.NewNop())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Outbound.TLSPolicy != "strict" {
		t.Errorf("TLSPolicy = %q, want strict", cfg.Outbound.TLSPolicy)
	}
	if cfg.Outbound.TLSPolicyLegacy != "" {
		t.Errorf("TLSPolicyLegacy = %q, want empty", cfg.Outbound.TLSPolicyLegacy)
	}
	if cfg.Outbound.ResolvedTLSPolicy() != "strict" {
		t.Errorf("ResolvedTLSPolicy = %q, want strict", cfg.Outbound.ResolvedTLSPolicy())
	}
}

func TestTLSPolicyCanonicalKeyAcceptsOpportunistic(t *testing.T) {
	unsetenv(t, "ORVIX_CONFIG")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tls.yaml")
	content := `
server:
  host: "127.0.0.1"
  admin_port: 8080
coremail:
  enabled: true
outbound:
  tls_policy: opportunistic
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	setenv(t, "ORVIX_CONFIG", cfgPath)
	oldWD, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	cfg, err := Load(zap.NewNop())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Outbound.TLSPolicy != "opportunistic" {
		t.Errorf("TLSPolicy = %q, want opportunistic", cfg.Outbound.TLSPolicy)
	}
}

func TestTLSPolicyLegacyKeyStillWorks(t *testing.T) {
	unsetenv(t, "ORVIX_CONFIG")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tls.yaml")
	content := `
server:
  host: "127.0.0.1"
  admin_port: 8080
coremail:
  enabled: true
outbound:
  outbound_tls_policy: strict
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	setenv(t, "ORVIX_CONFIG", cfgPath)
	oldWD, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	cfg, err := Load(zap.NewNop())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Outbound.TLSPolicy != "strict" {
		t.Errorf("TLSPolicy (resolved from legacy) = %q, want strict", cfg.Outbound.TLSPolicy)
	}
	if cfg.Outbound.TLSPolicyLegacy != "" {
		t.Errorf("TLSPolicyLegacy = %q, want empty after resolution", cfg.Outbound.TLSPolicyLegacy)
	}
}

func TestTLSPolicyCanonicalWinsWhenBothKeysSet(t *testing.T) {
	unsetenv(t, "ORVIX_CONFIG")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tls.yaml")
	content := `
server:
  host: "127.0.0.1"
  admin_port: 8080
coremail:
  enabled: true
outbound:
  tls_policy: strict
  outbound_tls_policy: opportunistic
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	setenv(t, "ORVIX_CONFIG", cfgPath)
	oldWD, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	cfg, err := Load(zap.NewNop())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Outbound.TLSPolicy != "strict" {
		t.Errorf("canonical TLSPolicy = %q, want strict (canonical wins)", cfg.Outbound.TLSPolicy)
	}
	if cfg.Outbound.TLSPolicyLegacy != "" {
		t.Errorf("TLSPolicyLegacy = %q, want empty after resolution", cfg.Outbound.TLSPolicyLegacy)
	}
}

func TestTLSPolicyMissingKeyUsesEmptyDefault(t *testing.T) {
	unsetenv(t, "ORVIX_CONFIG")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tls.yaml")
	content := `
server:
  host: "127.0.0.1"
  admin_port: 8080
coremail:
  enabled: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	setenv(t, "ORVIX_CONFIG", cfgPath)
	oldWD, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	cfg, err := Load(zap.NewNop())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Outbound.TLSPolicy != "" {
		t.Errorf("TLSPolicy = %q, want empty (defaults to opportunistic at runtime)", cfg.Outbound.TLSPolicy)
	}
	if cfg.Outbound.ResolvedTLSPolicy() != "" {
		t.Errorf("ResolvedTLSPolicy = %q, want empty", cfg.Outbound.ResolvedTLSPolicy())
	}
}
