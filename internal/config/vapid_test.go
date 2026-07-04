package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestLoadReadsVAPIDPrivateKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "vapid_private.key")
	const privateKey = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	if err := os.WriteFile(keyPath, []byte("  "+privateKey+"\n"), 0o640); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	cfgPath := filepath.Join(dir, "orvix.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
coremail:
  vapid_public_key: "public-key"
  vapid_private_key_file: "`+filepath.ToSlash(keyPath)+`"
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
		t.Fatalf("load config: %v", err)
	}
	if cfg.CoreMail.VAPIDPrivateKey != privateKey {
		t.Fatalf("private key not loaded from file: got %q", cfg.CoreMail.VAPIDPrivateKey)
	}
}

func TestLoadFailsWhenVAPIDPrivateKeyFileMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing-vapid.key")
	cfgPath := filepath.Join(dir, "orvix.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
coremail:
  vapid_public_key: "public-key"
  vapid_private_key_file: "`+filepath.ToSlash(missing)+`"
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

	_, err = Load(zap.NewNop())
	if err == nil {
		t.Fatal("expected missing VAPID private key file to fail config load")
	}
	msg := err.Error()
	if !strings.Contains(msg, "coremail.vapid_private_key_file") {
		t.Fatalf("error must identify VAPID file field, got %q", msg)
	}
	if strings.Contains(msg, "public-key") {
		t.Fatalf("error must not leak key material, got %q", msg)
	}
}
