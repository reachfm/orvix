package config

import (
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultsPasswordMinLengthIsInstallerPolicy(t *testing.T) {
	cfg := Defaults()
	if cfg.Auth.PasswordMinLen != 8 {
		t.Fatalf("default password minimum length must be 8, got %d", cfg.Auth.PasswordMinLen)
	}
}

func TestReleaseExampleCoreMailConfigIsDeploymentReady(t *testing.T) {
	v := viper.New()
	v.SetConfigFile(filepath.Join("..", "..", "release", "configs", "orvix.yaml.example"))
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("read release example config: %v", err)
	}

	cfg := Defaults()
	if err := v.Unmarshal(cfg); err != nil {
		t.Fatalf("parse release example config: %v", err)
	}
	cfg.validate()

	if !cfg.CoreMail.Enabled {
		t.Fatal("coremail.enabled must be true in deployment example")
	}
	if cfg.CoreMail.SMTPPort == 0 {
		t.Fatal("coremail.smtp_port must be non-zero")
	}
	if cfg.CoreMail.IMAPPort == 0 {
		t.Fatal("coremail.imap_port must be non-zero")
	}
	if cfg.CoreMail.POP3Port == 0 {
		t.Fatal("coremail.pop3_port must be non-zero")
	}
	if cfg.CoreMail.JMAPPort == 0 {
		t.Fatal("coremail.jmap_port must be non-zero")
	}
	if cfg.CoreMail.DataPath == "" {
		t.Fatal("coremail.data_path must be present")
	}
	if cfg.CoreMail.MailStorePath != cfg.CoreMail.DataPath {
		t.Fatalf("coremail.data_path must map to runtime mailstore path, got %q want %q", cfg.CoreMail.MailStorePath, cfg.CoreMail.DataPath)
	}
	if cfg.CoreMail.LicenseFilePath == "" {
		t.Fatal("coremail.license_file_path must be present")
	}
	if cfg.CoreMail.LicenseAuthorityCachePath == "" {
		t.Fatal("coremail.license_authority_cache_path must be present")
	}
	if cfg.Auth.PasswordMinLen != 8 {
		t.Fatalf("auth.password_min_len must be 8 in deployment example, got %d", cfg.Auth.PasswordMinLen)
	}
}

// TestNamecheapEnvOverridesWired checks that the Namecheap env
// override function correctly maps flat env vars to config fields.
func TestNamecheapEnvOverridesWired(t *testing.T) {
	v := viper.New()
	v.Set("NAMECHEAP_API_USER", "test-user")
	v.Set("NAMECHEAP_API_KEY", "test-key-12345")
	v.Set("NAMECHEAP_USERNAME", "test-username")
	v.Set("NAMECHEAP_CLIENT_IP", "1.2.3.4")
	v.Set("NAMECHEAP_SANDBOX", "true")
	v.Set("NAMECHEAP_ENABLE_APPLY", "true")

	cfg := Defaults()
	applyEnvOverrides(v, cfg)

	if cfg.DNS.NamecheapAPIUser != "test-user" {
		t.Errorf("NamecheapAPIUser: got %q want %q", cfg.DNS.NamecheapAPIUser, "test-user")
	}
	if cfg.DNS.NamecheapAPIKey != "test-key-12345" {
		t.Errorf("NamecheapAPIKey: got %q want %q", cfg.DNS.NamecheapAPIKey, "test-key-12345")
	}
	if cfg.DNS.NamecheapUsername != "test-username" {
		t.Errorf("NamecheapUsername: got %q want %q", cfg.DNS.NamecheapUsername, "test-username")
	}
	if cfg.DNS.NamecheapClientIP != "1.2.3.4" {
		t.Errorf("NamecheapClientIP: got %q want %q", cfg.DNS.NamecheapClientIP, "1.2.3.4")
	}
	if !cfg.DNS.NamecheapSandbox {
		t.Errorf("NamecheapSandbox must be true")
	}
	if !cfg.DNS.NamecheapEnableApply {
		t.Errorf("NamecheapEnableApply must be true")
	}
}

// TestNamecheapEnvOverridesDefaults confirms that missing env vars
// leave the default values (false for bools, empty for strings).
func TestNamecheapEnvOverridesDefaults(t *testing.T) {
	v := viper.New()
	cfg := Defaults()
	// Ensure the defaults are safe before applying overrides.
	if cfg.DNS.NamecheapEnableApply {
		t.Errorf("default NamecheapEnableApply must be false")
	}
	if cfg.DNS.NamecheapSandbox {
		t.Errorf("default NamecheapSandbox must be false")
	}
	// Apply overrides with an empty viper (no env set).
	applyEnvOverrides(v, cfg)
	// Verify defaults are preserved after overrides.
	if cfg.DNS.NamecheapEnableApply {
		t.Errorf("NamecheapEnableApply must remain false when not set")
	}
	if cfg.DNS.NamecheapSandbox {
		t.Errorf("NamecheapSandbox must remain false when not set")
	}
	if cfg.DNS.NamecheapAPIUser != "" {
		t.Errorf("NamecheapAPIUser must remain empty when not set")
	}
}
