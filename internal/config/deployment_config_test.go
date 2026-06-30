package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultsPasswordMinLengthIsInstallerPolicy(t *testing.T) {
	cfg := Defaults()
	if cfg.Auth.PasswordMinLen != 8 {
		t.Fatalf("default password minimum length must be 8, got %d", cfg.Auth.PasswordMinLen)
	}
}

func TestDefaultsCoreMailInboundDoesNotRequireSubmissionAuth(t *testing.T) {
	cfg := Defaults()
	// require_auth_for_submission controls port 587 submission only, not port 25 inbound.
	// It must default true so submission never accepts unauthenticated relay.
	if !cfg.CoreMail.RequireAuthForSubmission {
		t.Fatal("coremail.require_auth_for_submission must default true so port 587 submission requires AUTH")
	}
}

func TestCoreMailRequireAuthForSubmissionExplicitOverride(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(`
coremail:
  require_auth_for_submission: true
`)); err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg := Defaults()
	if err := v.Unmarshal(cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if !cfg.CoreMail.RequireAuthForSubmission {
		t.Fatal("explicit coremail.require_auth_for_submission=true must be honored")
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
	if !cfg.CoreMail.RequireAuthForSubmission {
		t.Fatal("deployment example must keep coremail.require_auth_for_submission true so submission requires AUTH")
	}
	// SUBMISSION-3C: submission defaults must be safe (disabled by default,
	// port 587) and the example must declare the TLS cert/key fields so an
	// operator cannot enable submission without seeing the gating config.
	if cfg.CoreMail.SubmissionEnabled {
		t.Fatal("deployment example must keep coremail.submission_enabled false (requires TLS)")
	}
	if cfg.CoreMail.SubmissionPort != 587 {
		t.Fatalf("deployment example submission_port must be 587, got %d", cfg.CoreMail.SubmissionPort)
	}
	if cfg.CoreMail.SubmissionHost != "0.0.0.0" {
		t.Fatalf("deployment example submission_host must be 0.0.0.0, got %q", cfg.CoreMail.SubmissionHost)
	}
	// SMTPS still disabled/honest.
	if cfg.CoreMail.SMTPsEnabled {
		t.Fatal("deployment example must keep coremail.smtps_enabled false (SMTPS not implemented)")
	}
	// Raw example must declare the TLS fields so an operator sees them
	// when flipping submission_enabled=true.
	raw, err := os.ReadFile(filepath.Join("..", "..", "release", "configs", "orvix.yaml.example"))
	if err != nil {
		t.Fatalf("read raw example: %v", err)
	}
	example := string(raw)
	for _, want := range []string{"tls_cert_file", "tls_key_file", "submission_enabled", "submission_host", "submission_port", "smtps_enabled", "imaps_enabled", "pop3s_enabled"} {
		if !strings.Contains(example, want) {
			t.Errorf("example config must declare %q so operators can see the TLS gating fields", want)
		}
	}
}

// TestNamecheapEnvOverridesFlatAliases checks that the flat
// alias env keys (ORVIX_NAMECHEAP_* → "NAMECHEAP_*") still work.
func TestNamecheapEnvOverridesFlatAliases(t *testing.T) {
	v := viper.New()
	v.Set("NAMECHEAP_API_USER", "flat-user")
	v.Set("NAMECHEAP_API_KEY", "flat-key")
	v.Set("NAMECHEAP_USERNAME", "flat-username")
	v.Set("NAMECHEAP_CLIENT_IP", "1.2.3.4")
	v.Set("NAMECHEAP_SANDBOX", "true")
	v.Set("NAMECHEAP_ENABLE_APPLY", "true")

	cfg := Defaults()
	applyEnvOverrides(v, cfg)

	if cfg.DNS.NamecheapAPIUser != "flat-user" {
		t.Errorf("flat NamecheapAPIUser: got %q want %q", cfg.DNS.NamecheapAPIUser, "flat-user")
	}
	if cfg.DNS.NamecheapAPIKey != "flat-key" {
		t.Errorf("flat NamecheapAPIKey: got %q want %q", cfg.DNS.NamecheapAPIKey, "flat-key")
	}
	if cfg.DNS.NamecheapUsername != "flat-username" {
		t.Errorf("flat NamecheapUsername: got %q want %q", cfg.DNS.NamecheapUsername, "flat-username")
	}
	if cfg.DNS.NamecheapClientIP != "1.2.3.4" {
		t.Errorf("flat NamecheapClientIP: got %q want %q", cfg.DNS.NamecheapClientIP, "1.2.3.4")
	}
	if !cfg.DNS.NamecheapSandbox {
		t.Errorf("flat NamecheapSandbox must be true")
	}
	if !cfg.DNS.NamecheapEnableApply {
		t.Errorf("flat NamecheapEnableApply must be true")
	}
}

// TestNamecheapEnvOverridesNested checks that the documented nested
// env keys (ORVIX_DNS_NAMECHEAP_* → "DNS_NAMECHEAP_*") override
// config values. These are the canonical documented env names.
func TestNamecheapEnvOverridesNested(t *testing.T) {
	v := viper.New()
	v.Set("DNS_NAMECHEAP_API_USER", "nested-user")
	v.Set("DNS_NAMECHEAP_API_KEY", "nested-key")
	v.Set("DNS_NAMECHEAP_USERNAME", "nested-username")
	v.Set("DNS_NAMECHEAP_CLIENT_IP", "5.6.7.8")
	v.Set("DNS_NAMECHEAP_SANDBOX", "true")
	v.Set("DNS_NAMECHEAP_ENABLE_APPLY", "true")

	cfg := Defaults()
	applyEnvOverrides(v, cfg)

	if cfg.DNS.NamecheapAPIUser != "nested-user" {
		t.Errorf("nested NamecheapAPIUser: got %q want %q", cfg.DNS.NamecheapAPIUser, "nested-user")
	}
	if cfg.DNS.NamecheapAPIKey != "nested-key" {
		t.Errorf("nested NamecheapAPIKey: got %q want %q", cfg.DNS.NamecheapAPIKey, "nested-key")
	}
	if cfg.DNS.NamecheapUsername != "nested-username" {
		t.Errorf("nested NamecheapUsername: got %q want %q", cfg.DNS.NamecheapUsername, "nested-username")
	}
	if cfg.DNS.NamecheapClientIP != "5.6.7.8" {
		t.Errorf("nested NamecheapClientIP: got %q want %q", cfg.DNS.NamecheapClientIP, "5.6.7.8")
	}
	if !cfg.DNS.NamecheapSandbox {
		t.Errorf("nested NamecheapSandbox must be true")
	}
	if !cfg.DNS.NamecheapEnableApply {
		t.Errorf("nested NamecheapEnableApply must be true")
	}
}

// TestNamecheapEnvOverridesNestedPriority checks that the nested
// form takes priority over the flat alias when both are set.
func TestNamecheapEnvOverridesNestedPriority(t *testing.T) {
	v := viper.New()
	// Both forms set — the nested form must win.
	v.Set("DNS_NAMECHEAP_API_USER", "nested-wins")
	v.Set("NAMECHEAP_API_USER", "flat-loses")

	cfg := Defaults()
	applyEnvOverrides(v, cfg)

	if cfg.DNS.NamecheapAPIUser != "nested-wins" {
		t.Errorf("nested form must take priority; got %q want %q", cfg.DNS.NamecheapAPIUser, "nested-wins")
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

// ── SUBMISSION-3D: env overrides for SMTP submission + TLS binding ──

// TestCoreMailSubmissionEnvOverrideEnable verifies the installer
// can flip submission on via env without rewriting orvix.yaml.
func TestCoreMailSubmissionEnvOverrideEnable(t *testing.T) {
	v := viper.New()
	v.Set("COREMAIL_SUBMISSION_ENABLED", "true")
	v.Set("COREMAIL_TLS_CERT_FILE", "/etc/orvix/tls/smtp/fullchain.pem")
	v.Set("COREMAIL_TLS_KEY_FILE", "/etc/orvix/tls/smtp/privkey.pem")

	cfg := Defaults()
	applyEnvOverrides(v, cfg)

	if !cfg.CoreMail.SubmissionEnabled {
		t.Error("COREMAIL_SUBMISSION_ENABLED=true must flip CoreMail.SubmissionEnabled")
	}
	if cfg.CoreMail.TLSCertFile != "/etc/orvix/tls/smtp/fullchain.pem" {
		t.Errorf("COREMAIL_TLS_CERT_FILE did not override, got %q", cfg.CoreMail.TLSCertFile)
	}
	if cfg.CoreMail.TLSKeyFile != "/etc/orvix/tls/smtp/privkey.pem" {
		t.Errorf("COREMAIL_TLS_KEY_FILE did not override, got %q", cfg.CoreMail.TLSKeyFile)
	}
}

// TestCoreMailSubmissionEnvOverrideDisable verifies a malformed
// "false" env keeps submission off even if YAML flipped it on
// (the override is the canonical truth from the installer).
func TestCoreMailSubmissionEnvOverrideDisable(t *testing.T) {
	v := viper.New()
	v.Set("COREMAIL_SUBMISSION_ENABLED", "false")

	cfg := Defaults()
	// Simulate a misconfigured YAML that flipped the flag on.
	cfg.CoreMail.SubmissionEnabled = true
	applyEnvOverrides(v, cfg)

	if cfg.CoreMail.SubmissionEnabled {
		t.Error("COREMAIL_SUBMISSION_ENABLED=false must force submission off")
	}
}

// TestCoreMailSubmissionEnvOverrideDefaults verifies missing env
// vars do NOT clobber the safe defaults.
func TestCoreMailSubmissionEnvOverrideDefaults(t *testing.T) {
	v := viper.New()

	cfg := Defaults()
	if cfg.CoreMail.SubmissionEnabled {
		t.Fatal("default CoreMail.SubmissionEnabled must be false")
	}
	if cfg.CoreMail.TLSCertFile != "" {
		t.Fatal("default CoreMail.TLSCertFile must be empty")
	}
	if cfg.CoreMail.TLSKeyFile != "" {
		t.Fatal("default CoreMail.TLSKeyFile must be empty")
	}

	applyEnvOverrides(v, cfg)

	if cfg.CoreMail.SubmissionEnabled {
		t.Error("missing COREMAIL_SUBMISSION_ENABLED must not flip submission on")
	}
	if cfg.CoreMail.TLSCertFile != "" {
		t.Error("missing COREMAIL_TLS_CERT_FILE must not set a cert path")
	}
	if cfg.CoreMail.TLSKeyFile != "" {
		t.Error("missing COREMAIL_TLS_KEY_FILE must not set a key path")
	}
}

// TestCoreMailSMTPSEnvOverrideDefaults verifies SMTPS still defaults
// off even when the env override function runs with no env set.
func TestCoreMailSMTPSEnvOverrideDefaults(t *testing.T) {
	v := viper.New()
	cfg := Defaults()
	applyEnvOverrides(v, cfg)
	if cfg.CoreMail.SMTPsEnabled {
		t.Error("SMTPS must remain disabled by default; env override must not enable it")
	}
}
