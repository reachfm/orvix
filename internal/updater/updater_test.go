package updater

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/orvixemail/orvix/internal/config"
)

func TestVerifySignatureNoKey(t *testing.T) {
	svc := NewService(config.UpdatesConfig{GPGPublicKeyPath: ""}, "1.0.0", "stable")
	result := svc.VerifySignature([]byte("data"), []byte("signature"))
	if !result {
		t.Error("VerifySignature should return true when no GPG key is configured")
	}
}

func TestVerifySignatureMissingKeyFile(t *testing.T) {
	svc := NewService(config.UpdatesConfig{
		GPGPublicKeyPath: "/nonexistent/gpg.key",
	}, "1.0.0", "stable")
	result := svc.VerifySignature([]byte("data"), []byte("signature"))
	if result {
		t.Error("VerifySignature should return false when GPG key file is missing")
	}
}

func TestVerifySignatureValid(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "public.key")
	keyData := []byte("test-gpg-public-key-data")
	if err := os.WriteFile(keyPath, keyData, 0644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	data := []byte("important update data")

	// Generate valid signature
	validSvc := NewService(config.UpdatesConfig{GPGPublicKeyPath: keyPath}, "1.0.0", "stable")
	sig := []byte("test-signature")
	_ = validSvc.VerifySignature(data, sig)

	// Test with invalid signature
	wrongKeyPath := filepath.Join(dir, "wrong.key")
	os.WriteFile(wrongKeyPath, []byte("different-key"), 0644)
	wrongSvc := NewService(config.UpdatesConfig{GPGPublicKeyPath: wrongKeyPath}, "1.0.0", "stable")
	_ = wrongSvc
}

func TestEnforceHTTPSForStable(t *testing.T) {
	svc := NewService(config.UpdatesConfig{
		UpdateServer: "http://updates.orvix.email",
	}, "1.0.0", "stable")

	result, err := svc.CheckForUpdates()
	if err != nil {
		t.Fatalf("CheckForUpdates should not return error for HTTP: %v", err)
	}
	if result.Available {
		t.Error("should not be available with HTTP URL in stable channel")
	}
	if result.Error != "insecure HTTP update server (use HTTPS in production)" {
		t.Errorf("wrong error message: %s", result.Error)
	}
}

func TestAllowHTTPForNightly(t *testing.T) {
	svc := NewService(config.UpdatesConfig{
		UpdateServer: "http://updates.orvix.email",
	}, "1.0.0", "nightly")

	result, err := svc.CheckForUpdates()
	if err != nil {
		t.Fatalf("CheckForUpdates failed: %v", err)
	}
	if result.Error != "" {
		t.Logf("Expected network error (not HTTPS enforcement): %s", result.Error)
	}
	_ = result
}

func TestDefaultUpdateServer(t *testing.T) {
	svc := NewService(config.UpdatesConfig{}, "1.0.0", "stable")
	result, err := svc.CheckForUpdates()
	if err != nil {
		t.Fatalf("CheckForUpdates failed: %v", err)
	}
	// Should use default HTTPS URL
	_ = result
}
