package config

import (
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

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
}
