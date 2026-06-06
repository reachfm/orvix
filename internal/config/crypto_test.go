package config

import (
	"os"
	"strings"
	"sync"
	"testing"
)

func TestEncryptDecryptString(t *testing.T) {
	key := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	os.Setenv(encryptionKeyEnv, key)
	defer os.Unsetenv(encryptionKeyEnv)
	keyOnce = sync.Once{}
	encryptionKey = nil
	keyErr = nil

	plaintext := "sensitive-data-123!@#"
	encrypted, err := EncryptString(plaintext)
	if err != nil {
		t.Fatalf("EncryptString failed: %v", err)
	}

	if encrypted == plaintext {
		t.Fatal("encrypted string should differ from plaintext")
	}

	if !strings.Contains(encrypted, ":") {
		t.Fatal("encrypted string should contain ':' separator")
	}

	decrypted, err := DecryptString(encrypted)
	if err != nil {
		t.Fatalf("DecryptString failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("decrypted text doesn't match: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecryptBytes(t *testing.T) {
	key := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	os.Setenv(encryptionKeyEnv, key)
	defer os.Unsetenv(encryptionKeyEnv)
	keyOnce = sync.Once{}
	encryptionKey = nil
	keyErr = nil

	plaintext := []byte("hello-world-binary-data")
	encrypted, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Fatalf("decrypted doesn't match: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDifferentNonce(t *testing.T) {
	key := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	os.Setenv(encryptionKeyEnv, key)
	defer os.Unsetenv(encryptionKeyEnv)
	keyOnce = sync.Once{}
	encryptionKey = nil
	keyErr = nil

	e1, _ := EncryptString("same-data")
	e2, _ := EncryptString("same-data")

	if e1 == e2 {
		t.Fatal("two encryptions of same data should produce different ciphertexts")
	}
}

func TestDecryptInvalidFormat(t *testing.T) {
	key := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	os.Setenv(encryptionKeyEnv, key)
	defer os.Unsetenv(encryptionKeyEnv)
	keyOnce = sync.Once{}
	encryptionKey = nil
	keyErr = nil

	_, err := DecryptString("invalid-format-no-colon")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	os.Setenv(encryptionKeyEnv, key)
	defer os.Unsetenv(encryptionKeyEnv)
	keyOnce = sync.Once{}
	encryptionKey = nil
	keyErr = nil

	encrypted, _ := EncryptString("secret")

	os.Unsetenv(encryptionKeyEnv)
	keyOnce = sync.Once{}
	encryptionKey = nil
	keyErr = nil

	wrongKey := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	os.Setenv(encryptionKeyEnv, wrongKey)

	_, err := DecryptString(encrypted)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestGetWatermark(t *testing.T) {
	wm := GetWatermark()
	if wm.Product != "Orvix Email Server Platform" {
		t.Fatalf("expected product 'Orvix Email Server Platform', got %q", wm.Product)
	}
	if wm.Version == "" {
		t.Fatal("expected non-empty version")
	}
	if !strings.Contains(wm.Copyright, "Orvix") {
		t.Fatal("copyright should contain Orvix")
	}
}

func TestCanaryToken(t *testing.T) {
	token := CanaryToken()
	if !strings.HasPrefix(token, "ORVIX-") {
		t.Fatal("canary token should start with ORVIX-")
	}
}

func TestSplitEncrypted(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"abc123:def456", []string{"abc123", "def456"}},
		{"a:b:c", []string{"a", "b:c"}},
		{"", nil},
		{"no-separator", nil},
	}

	for _, tt := range tests {
		result := splitEncrypted(tt.input)
		if tt.expected == nil {
			if result != nil {
				t.Errorf("splitEncrypted(%q) = %v, want nil", tt.input, result)
			}
			continue
		}
		if result == nil {
			t.Errorf("splitEncrypted(%q) = nil, want %v", tt.input, tt.expected)
			continue
		}
		if result[0] != tt.expected[0] || result[1] != tt.expected[1] {
			t.Errorf("splitEncrypted(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}
