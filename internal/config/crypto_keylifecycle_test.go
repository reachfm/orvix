package config

// C-3 regression coverage: the AES-256 encryption key must have a stable,
// persistent lifecycle so that data encrypted at rest survives a process
// restart. Prior behaviour generated an ephemeral in-memory key on every start
// (never persisted), silently making previously encrypted data undecryptable
// after a reboot.
//
// These tests exercise resolveEncryptionKey directly (the pure resolver behind
// the sync.Once wrapper) so a "restart" can be simulated within one test binary.

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestEncryptionKey_PersistsAndSurvivesRestart is the core C-3 guarantee: a
// generated key is written to disk, and a subsequent "restart" (fresh
// resolveEncryptionKey call) loads the SAME key, so ciphertext produced before
// the restart still decrypts after it.
func TestEncryptionKey_PersistsAndSurvivesRestart(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "encryption_key")
	t.Setenv(encryptionKeyEnv, "") // force the file path, not the env key
	t.Setenv(encryptionKeyPathEnv, keyPath)

	// First "boot": no key file exists yet → generate + persist.
	key1, err := resolveEncryptionKey()
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if len(key1) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(key1))
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key was not persisted to disk: %v", err)
	}

	// Second "boot" (simulated restart): the key file now exists → must load
	// the identical key, not generate a new one.
	key2, err := resolveEncryptionKey()
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if hex.EncodeToString(key1) != hex.EncodeToString(key2) {
		t.Fatalf("key changed across restart: %x != %x", key1, key2)
	}
}

// TestEncryptionKey_FilePermissions asserts the persisted key file is 0600
// (owner read/write only) on POSIX, matching the JWT key's on-disk contract.
func TestEncryptionKey_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file-mode semantics not applicable on Windows")
	}
	keyPath := filepath.Join(t.TempDir(), "encryption_key")
	t.Setenv(encryptionKeyEnv, "")
	t.Setenv(encryptionKeyPathEnv, keyPath)

	if _, err := resolveEncryptionKey(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected key file mode 0600, got %o", perm)
	}
}

// TestEncryptionKey_EnvVarTakesPriority asserts the explicit
// ORVIX_ENCRYPTION_KEY env var still wins over any persisted file, preserving
// prior operator-provided-key behaviour.
func TestEncryptionKey_EnvVarTakesPriority(t *testing.T) {
	envKey := make([]byte, 32)
	for i := range envKey {
		envKey[i] = byte(i + 1)
	}
	keyPath := filepath.Join(t.TempDir(), "encryption_key")
	// Pre-populate a DIFFERENT key on disk to prove the env wins.
	other := make([]byte, 32) // all-zero, distinct from envKey
	if err := persistEncryptionKey(keyPath, other); err != nil {
		t.Fatalf("seed key file: %v", err)
	}

	t.Setenv(encryptionKeyEnv, hex.EncodeToString(envKey))
	t.Setenv(encryptionKeyPathEnv, keyPath)

	got, err := resolveEncryptionKey()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(envKey) {
		t.Fatalf("env key did not take priority: got %x", got)
	}
}

// TestEncryptionKey_InvalidEnvRejected asserts a malformed env key is a hard
// error (not silently ignored), unchanged from prior behaviour.
func TestEncryptionKey_InvalidEnvRejected(t *testing.T) {
	t.Setenv(encryptionKeyPathEnv, filepath.Join(t.TempDir(), "unused"))

	t.Setenv(encryptionKeyEnv, "not-hex")
	if _, err := resolveEncryptionKey(); err == nil {
		t.Fatal("expected error for non-hex env key")
	}

	t.Setenv(encryptionKeyEnv, hex.EncodeToString(make([]byte, 16))) // 16 bytes, too short
	if _, err := resolveEncryptionKey(); err == nil {
		t.Fatal("expected error for wrong-length env key")
	}
}

// TestEncryptionKey_EndToEndAcrossRestart ties it together at the Encrypt/
// Decrypt level: data encrypted under a persisted key decrypts after a
// simulated restart that reloads the key from disk. This is the concrete
// data-loss scenario C-3 fixes.
func TestEncryptionKey_EndToEndAcrossRestart(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "encryption_key")
	t.Setenv(encryptionKeyEnv, "")
	t.Setenv(encryptionKeyPathEnv, keyPath)

	// Encrypt under the freshly generated+persisted key.
	key1, err := resolveEncryptionKey()
	if err != nil {
		t.Fatalf("resolve1: %v", err)
	}
	plaintext := []byte("license-key-and-metadata-secret")
	ciphertext, err := encryptWithKey(key1, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Simulate restart: reload the key from disk and decrypt.
	key2, err := resolveEncryptionKey()
	if err != nil {
		t.Fatalf("resolve2: %v", err)
	}
	got, err := decryptWithKey(key2, ciphertext)
	if err != nil {
		t.Fatalf("decrypt after restart: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("round-trip mismatch: %q != %q", got, plaintext)
	}
}
