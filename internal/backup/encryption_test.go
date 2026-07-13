package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	password := "test-password-123"
	salt := make([]byte, 32)
	rand.Read(salt)
	BackupEncryptionKey = DeriveBackupKey(password, salt)

	plaintext := []byte("This is a test backup archive content that needs to be encrypted securely.")

	encrypted, err := EncryptBackup(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if bytes.Contains(encrypted, plaintext) {
		t.Fatal("encrypted data should not contain plaintext verbatim")
	}

	decrypted, err := DecryptBackup(encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("round-trip mismatch")
	}
}

func TestWrongKeyProducesError(t *testing.T) {
	password1 := "correct-password"
	password2 := "wrong-password"
	salt := make([]byte, 32)
	rand.Read(salt)

	BackupEncryptionKey = DeriveBackupKey(password1, salt)
	plaintext := []byte("sensitive backup data")
	encrypted, err := EncryptBackup(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	BackupEncryptionKey = DeriveBackupKey(password2, salt)
	_, err = DecryptBackup(encrypted)
	if err == nil {
		t.Fatal("expected decryption error with wrong password")
	}
}

func TestWrongKeyProducesErrorDifferentSalt(t *testing.T) {
	password := "same-password"
	salt1 := make([]byte, 32)
	salt2 := make([]byte, 32)
	rand.Read(salt1)
	rand.Read(salt2)

	BackupEncryptionKey = DeriveBackupKey(password, salt1)
	plaintext := []byte("sensitive backup data")
	encrypted, err := EncryptBackup(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	BackupEncryptionKey = DeriveBackupKey(password, salt2)
	_, err = DecryptBackup(encrypted)
	if err == nil {
		t.Fatal("expected decryption error with different salt")
	}
}

func TestEmptyPasswordHandling(t *testing.T) {
	salt := make([]byte, 32)
	rand.Read(salt)
	BackupEncryptionKey = DeriveBackupKey("", salt)

	plaintext := []byte("test data for empty password")
	encrypted, err := EncryptBackup(plaintext)
	if err != nil {
		t.Fatalf("encrypt with empty password: %v", err)
	}

	decrypted, err := DecryptBackup(encrypted)
	if err != nil {
		t.Fatalf("decrypt with empty password: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("empty password round-trip mismatch")
	}
}

func TestEmptyPasswordProducesDifferentKey(t *testing.T) {
	salt := make([]byte, 32)
	rand.Read(salt)

	key1 := DeriveBackupKey("", salt)
	key2 := DeriveBackupKey("actual-password", salt)

	if bytes.Equal(key1, key2) {
		t.Fatal("empty password and non-empty password should produce different keys")
	}
}

func TestChecksumValidation(t *testing.T) {
	password := "checksum-test-password"
	salt := make([]byte, 32)
	rand.Read(salt)
	BackupEncryptionKey = DeriveBackupKey(password, salt)

	data := []byte("data for checksum validation")
	encrypted, err := EncryptBackup(data)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	checksum := ComputeBackupChecksum(encrypted)
	if checksum == "" {
		t.Fatal("checksum should not be empty")
	}

	expected := sha256.Sum256(encrypted)
	expectedHex := hex.EncodeToString(expected[:])
	if checksum != expectedHex {
		t.Fatalf("checksum mismatch: got %s, expected %s", checksum, expectedHex)
	}
}

func TestChecksumDetectsTampering(t *testing.T) {
	password := "tamper-test"
	salt := make([]byte, 32)
	rand.Read(salt)
	BackupEncryptionKey = DeriveBackupKey(password, salt)

	data := []byte("original backup data")
	encrypted, err := EncryptBackup(data)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	originalChecksum := ComputeBackupChecksum(encrypted)

	if len(encrypted) > 0 {
		encrypted[len(encrypted)/2] ^= 0xFF
	}

	tamperedChecksum := ComputeBackupChecksum(encrypted)
	if originalChecksum == tamperedChecksum {
		t.Fatal("tampered data should produce different checksum")
	}
}

func TestEncryptWithoutKey(t *testing.T) {
	BackupEncryptionKey = nil
	_, err := EncryptBackup([]byte("test"))
	if err == nil {
		t.Fatal("expected error when encryption key is not set")
	}
}

func TestDecryptWithoutKey(t *testing.T) {
	BackupEncryptionKey = nil
	_, err := DecryptBackup([]byte("test"))
	if err == nil {
		t.Fatal("expected error when decryption key is not set")
	}
}

func TestDecryptCorruptedData(t *testing.T) {
	password := "corrupt-test"
	salt := make([]byte, 32)
	rand.Read(salt)
	BackupEncryptionKey = DeriveBackupKey(password, salt)

	data := []byte("data that will be encrypted")
	encrypted, err := EncryptBackup(data)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if len(encrypted) > 12 {
		encrypted[12] ^= 0x01
	}

	_, err = DecryptBackup(encrypted)
	if err == nil {
		t.Fatal("expected error when decrypting corrupted data")
	}
}

func TestDecryptTooShort(t *testing.T) {
	password := "short-test"
	salt := make([]byte, 32)
	rand.Read(salt)
	BackupEncryptionKey = DeriveBackupKey(password, salt)

	_, err := DecryptBackup([]byte("short"))
	if err == nil {
		t.Fatal("expected error for ciphertext too short")
	}
}

func TestLargeDataRoundTrip(t *testing.T) {
	password := "large-test"
	salt := make([]byte, 32)
	rand.Read(salt)
	BackupEncryptionKey = DeriveBackupKey(password, salt)

	largeData := make([]byte, 1024*1024)
	rand.Read(largeData)

	encrypted, err := EncryptBackup(largeData)
	if err != nil {
		t.Fatalf("encrypt large: %v", err)
	}

	decrypted, err := DecryptBackup(encrypted)
	if err != nil {
		t.Fatalf("decrypt large: %v", err)
	}

	if !bytes.Equal(decrypted, largeData) {
		t.Fatal("large data round-trip mismatch")
	}
}

func TestDeriveBackupKeyDeterministic(t *testing.T) {
	password := "deterministic-test"
	salt := []byte("fixed-salt-for-testing-32bytes!!")

	key1 := DeriveBackupKey(password, salt)
	key2 := DeriveBackupKey(password, salt)

	if !bytes.Equal(key1, key2) {
		t.Fatal("key derivation should be deterministic for same inputs")
	}

	if len(key1) != 32 {
		t.Fatalf("derived key should be 32 bytes, got %d", len(key1))
	}
}

func TestDeriveBackupKeyDifferentPasswords(t *testing.T) {
	salt := []byte("fixed-salt-for-testing-32bytes!!")

	key1 := DeriveBackupKey("password-alpha", salt)
	key2 := DeriveBackupKey("password-beta", salt)

	if bytes.Equal(key1, key2) {
		t.Fatal("different passwords should produce different keys")
	}
}

func TestDeriveBackupKeyDifferentSalts(t *testing.T) {
	password := "same-password"
	salt1 := []byte("salt-number-one-for-testing!!")
	salt2 := []byte("salt-number-two-for-testing!!")

	key1 := DeriveBackupKey(password, salt1)
	key2 := DeriveBackupKey(password, salt2)

	if bytes.Equal(key1, key2) {
		t.Fatal("different salts should produce different keys")
	}
}

func TestSetEncryptionKey(t *testing.T) {
	password := "orvix-test-password-2024"
	salt := make([]byte, 32)
	rand.Read(salt)
	BackupEncryptionKey = DeriveBackupKey(password, salt)

	plaintext := []byte("test data encrypted after SetEncryptionKey")
	encrypted, err := EncryptBackup(plaintext)
	if err != nil {
		t.Fatalf("encrypt after set encryption key: %v", err)
	}

	decrypted, err := DecryptBackup(encrypted)
	if err != nil {
		t.Fatalf("decrypt after set encryption key: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("set encryption key round-trip mismatch")
	}
}

func TestCreateBackupWithEncryption(t *testing.T) {
	s := testService(t)
	ctx := context.Background()

	if err := s.SetEncryptionKey("encrypted-backup-test-password"); err != nil {
		t.Fatalf("set encryption key: %v", err)
	}

	b, err := s.CreateBackup(ctx, "encrypted-test-backup")
	if err != nil {
		t.Fatalf("create encrypted backup: %v", err)
	}
	if b.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", b.Status)
	}

	bp := s.backupPath(b.ID)
	manifestPath := filepath.Join(bp, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if !manifest.Encrypted {
		t.Fatal("expected manifest.Encrypted to be true")
	}
	if manifest.Checksum == "" {
		t.Fatal("expected manifest.Checksum to be set")
	}

	archivePath := filepath.Join(bp, b.ID+".tar.gz")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Fatalf("archive not found: %s", archivePath)
	}

	archiveData, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	decrypted, err := DecryptBackup(archiveData)
	if err != nil {
		t.Fatalf("decrypt archive: %v", err)
	}

	gr, err := gzip.NewReader(bytes.NewReader(decrypted))
	if err != nil {
		t.Fatalf("decompressing decrypted archive: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	foundManifest := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if hdr.Name == "manifest.json" {
			foundManifest = true
			break
		}
	}
	if !foundManifest {
		t.Fatal("decrypted archive should contain manifest.json")
	}
}

func TestCreateBackupWithoutEncryption(t *testing.T) {
	s := testService(t)
	ctx := context.Background()

	b, err := s.CreateBackup(ctx, "unencrypted-test-backup")
	if err != nil {
		t.Fatalf("create unencrypted backup: %v", err)
	}
	if b.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", b.Status)
	}

	bp := s.backupPath(b.ID)
	manifestPath := filepath.Join(bp, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if manifest.Encrypted {
		t.Fatal("expected manifest.Encrypted to be false when no encryption key is set")
	}
}
