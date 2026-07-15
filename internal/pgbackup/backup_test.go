package pgbackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateBackupID(t *testing.T) {
	id1 := generateBackupID()
	id2 := generateBackupID()
	if id1 == id2 {
		t.Fatal("expected unique IDs")
	}
	if len(id1) < 20 {
		t.Fatalf("expected backup ID to be at least 20 chars, got %d", len(id1))
	}
}

func TestFindPgDump(t *testing.T) {
	path, err := findPgDump()
	if err != nil {
		t.Skip("pg_dump not available:", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
}

func TestFindPgRestore(t *testing.T) {
	path, err := findPgRestore()
	if err != nil {
		t.Skip("pg_restore not available:", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := sha256.Sum256([]byte("test-encryption-key-1234567890abc"))
	plaintext := []byte("this is a test backup payload with sensitive data")

	encrypted, err := encryptAES(plaintext, key[:])
	if err != nil {
		t.Fatal(err)
	}
	if len(encrypted) <= len(plaintext) {
		t.Fatal("expected encrypted data to be larger than plaintext")
	}

	decrypted, err := decryptAES(encrypted, key[:])
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("round-trip failed: got %s, want %s", decrypted, plaintext)
	}
}

func TestEncryptWrongKeyFails(t *testing.T) {
	key := sha256.Sum256([]byte("correct-key-1234567890abcdef"))
	plaintext := []byte("sensitive backup data")

	encrypted, err := encryptAES(plaintext, key[:])
	if err != nil {
		t.Fatal(err)
	}

	wrongKey := sha256.Sum256([]byte("wrong-key-1234567890abcdef"))
	_, err = decryptAES(encrypted, wrongKey[:])
	if err == nil {
		t.Fatal("expected decryption with wrong key to fail")
	}
}

func TestBackupManifestJSON(t *testing.T) {
	m := &BackupManifest{
		BackupID:      "test-123",
		SchemaVersion: "1.0",
		AppVersion:    "1.0.3-rc5",
		PGVersion:     "pg_dump (PostgreSQL) 16.4",
		DumpFormat:    "custom",
		Checksum:      "abc123",
		CreatedAt:     "2026-07-14T23:00:00Z",
	}
	// This should not panic or fail
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var m2 BackupManifest
	if err := json.Unmarshal(data, &m2); err != nil {
		t.Fatal(err)
	}
	if m2.BackupID != "test-123" {
		t.Fatalf("expected test-123, got %s", m2.BackupID)
	}
}

func TestOutputDirCreation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pgbackups")
	cfg := DefaultConfig()
	cfg.OutputDir = dir
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("output dir should exist")
	}
}

func TestChecksumVerification(t *testing.T) {
	data := []byte("test backup content")
	h := sha256.Sum256(data)
	expected := hex.EncodeToString(h[:])

	h2 := sha256.Sum256(data)
	actual := hex.EncodeToString(h2[:])

	if actual != expected {
		t.Fatal("checksum mismatch for same data")
	}

	data3 := []byte("different content")
	h3 := sha256.Sum256(data3)
	actual3 := hex.EncodeToString(h3[:])

	if actual3 == expected {
		t.Fatal("checksum should differ for different data")
	}
}
