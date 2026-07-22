package encryption

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitAndIsEnabled(t *testing.T) {
	if IsEnabled() {
		t.Error("encryption should be disabled before Init")
	}

	if err := Init(""); err != nil {
		t.Fatalf("Init with empty key failed: %v", err)
	}
	if IsEnabled() {
		t.Error("encryption should be disabled with empty key")
	}

	if err := Init("test-key-32-bytes!"); err != nil {
		t.Fatalf("Init with key failed: %v", err)
	}
	if !IsEnabled() {
		t.Error("encryption should be enabled with valid key")
	}
}

func TestEncryptDecryptString(t *testing.T) {
	Init("test-encryption-key-for-testing")

	plaintext := "Hello, OrvixEM! This is sensitive data."
	ciphertext, err := EncryptString(plaintext)
	if err != nil {
		t.Fatalf("EncryptString failed: %v", err)
	}
	if ciphertext == plaintext {
		t.Error("ciphertext should differ from plaintext")
	}

	decrypted, err := DecryptString(ciphertext)
	if err != nil {
		t.Fatalf("DecryptString failed: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("decrypted text mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecryptFile(t *testing.T) {
	Init("test-key-for-file-encryption")

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := []byte("This is a test file for encryption.")

	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := EncryptFile(filePath); err != nil {
		t.Fatalf("EncryptFile failed: %v", err)
	}

	encrypted, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read encrypted file: %v", err)
	}
	if string(encrypted) == string(content) {
		t.Error("encrypted file should differ from original")
	}

	if err := DecryptFile(filePath); err != nil {
		t.Fatalf("DecryptFile failed: %v", err)
	}

	decrypted, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read decrypted file: %v", err)
	}
	if string(decrypted) != string(content) {
		t.Errorf("decrypted file mismatch: got %q, want %q", string(decrypted), string(content))
	}
}

func TestWrongKeyFails(t *testing.T) {
	Init("key-one-for-encryption")

	plaintext := "Sensitive backup data"
	ciphertext, err := EncryptString(plaintext)
	if err != nil {
		t.Fatalf("EncryptString failed: %v", err)
	}

	// Decrypt with different key
	Init("key-two-for-decryption")
	_, err = DecryptString(ciphertext)
	if err == nil {
		t.Error("DecryptString should fail with wrong key")
	}
}

func TestEmptyString(t *testing.T) {
	Init("test-key")

	result, err := EncryptString("")
	if err != nil {
		t.Fatalf("EncryptString empty failed: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}

	result, err = DecryptString("")
	if err != nil {
		t.Fatalf("DecryptString empty failed: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
}
