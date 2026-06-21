package config

import (
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
	if cfg.CoreMail.RequireAuthForSubmission {
		t.Fatal("coremail.require_auth_for_submission must default false so port 25 accepts unauthenticated inbound mail before RCPT relay checks")
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
	if cfg.CoreMail.RequireAuthForSubmission {
		t.Fatal("deployment example must keep coremail.require_auth_for_submission false for inbound port 25")
	}
}
