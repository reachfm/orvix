package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func randomBackupKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func writeBackupKeyFile(t *testing.T, dir string, key []byte) string {
	t.Helper()
	path := filepath.Join(dir, "backup.key")
	encoded := base64.RawURLEncoding.EncodeToString(key) + "\n"
	if err := os.WriteFile(path, []byte(encoded), 0o640); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func TestBackupEncryptionFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "backup.tar.gz")
	encrypted := plain + ".enc"
	restored := filepath.Join(dir, "restored.tar.gz")
	want := make([]byte, backupChunkSize+731)
	if _, err := rand.Read(want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, want, 0o600); err != nil {
		t.Fatal(err)
	}
	key := randomBackupKey(t)
	if err := EncryptBackupFile(key, plain, encrypted); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := DecryptBackupFile(key, encrypted, restored); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("decrypted backup differs from source")
	}
}

func TestBackupEncryptionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	encrypted := filepath.Join(dir, "encrypted")
	if err := os.WriteFile(plain, []byte("confidential backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := randomBackupKey(t)
	if err := EncryptBackupFile(key, plain, encrypted); err != nil {
		t.Fatal(err)
	}

	t.Run("wrong key", func(t *testing.T) {
		if err := DecryptBackupFile(randomBackupKey(t), encrypted, filepath.Join(dir, "wrong")); err == nil {
			t.Fatal("expected wrong key to fail")
		}
	})
	t.Run("tampered", func(t *testing.T) {
		data, err := os.ReadFile(encrypted)
		if err != nil {
			t.Fatal(err)
		}
		data[len(data)/2] ^= 0xff
		path := filepath.Join(dir, "tampered")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := DecryptBackupFile(key, path, filepath.Join(dir, "tampered.out")); err == nil {
			t.Fatal("expected tampered envelope to fail")
		}
	})
	t.Run("truncated", func(t *testing.T) {
		data, err := os.ReadFile(encrypted)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "truncated")
		if err := os.WriteFile(path, data[:len(data)-1], 0o600); err != nil {
			t.Fatal(err)
		}
		if err := DecryptBackupFile(key, path, filepath.Join(dir, "truncated.out")); err == nil {
			t.Fatal("expected truncated envelope to fail")
		}
	})
}

func TestLoadBackupEncryptionKey(t *testing.T) {
	dir := t.TempDir()
	key := randomBackupKey(t)
	path := writeBackupKeyFile(t, dir, key)
	got, err := LoadBackupEncryptionKey(path)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatal("loaded key differs")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o660); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBackupEncryptionKey(path); err == nil {
			t.Fatal("group-writable key must be rejected")
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBackupEncryptionKey(path); err == nil {
			t.Fatal("world-readable key must be rejected")
		}
	}
}

func TestEncryptedBackupSurvivesServiceRestartAndRestores(t *testing.T) {
	s := testService(t)
	key := randomBackupKey(t)
	keyFile := writeBackupKeyFile(t, t.TempDir(), key)
	if err := s.SetEncryptionConfig(BackupEncryptionConfig{Enabled: true, KeyFile: keyFile}); err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBackup(context.Background(), "encrypted-restart")
	if err != nil {
		t.Fatalf("create encrypted backup: %v", err)
	}
	bp := s.backupPath(b.ID)
	archive := filepath.Join(bp, "backup-archive.tar.gz.enc")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("encrypted archive missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bp, "backup-archive.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("plaintext archive remains after encryption")
	}
	data, err := os.ReadFile(filepath.Join(bp, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.Encrypted || manifest.Checksum == "" {
		t.Fatal("encrypted manifest metadata missing")
	}

	// Reconfigure from the persisted key file to model a process restart.
	s.encryptionKey = nil
	s.encryptedBackups = nil
	if err := s.SetEncryptionConfig(BackupEncryptionConfig{Enabled: true, KeyFile: keyFile}); err != nil {
		t.Fatal(err)
	}
	result, err := s.RestoreBackup(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("activate encrypted restore: %v", err)
	}
	if result.Status != RestoreStatusActivated {
		t.Fatalf("restore status = %s", result.Status)
	}
	if _, err := os.Stat(filepath.Join(s.mailDir, "test.eml")); err != nil {
		t.Fatalf("restored mail missing: %v", err)
	}
}

func TestCreateBackupEncryptionFailureRemovesPlaintextSecrets(t *testing.T) {
	s := testService(t)
	key := randomBackupKey(t)
	keyFile := writeBackupKeyFile(t, t.TempDir(), key)
	if err := s.SetEncryptionConfig(BackupEncryptionConfig{Enabled: true, KeyFile: keyFile}); err != nil {
		t.Fatal(err)
	}
	s.AddKeyPath(keyFile)
	s.encryptFile = func([]byte, string, string) error { return errors.New("injected encryption failure") }

	if _, err := s.CreateBackup(context.Background(), "encryption-failure"); err == nil {
		t.Fatal("expected encryption failure")
	}
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("backup directories = %d, want 1 failed backup", len(entries))
	}
	failedPath := filepath.Join(s.basePath, entries[0].Name())
	for _, forbidden := range []string{
		"backup-archive.tar.gz",
		"backup-archive.tar.gz.sha256",
		filepath.Base(keyFile),
	} {
		if _, err := os.Stat(filepath.Join(failedPath, forbidden)); !os.IsNotExist(err) {
			t.Fatalf("plaintext sensitive artifact remains after encryption failure: %s", forbidden)
		}
	}
	for _, retained := range []string{"database.sqlite", "mailstore.tar.gz", "attachments.tar.gz"} {
		if _, err := os.Stat(filepath.Join(failedPath, retained)); err != nil {
			t.Fatalf("non-secret recovery snapshot %s was not retained: %v", retained, err)
		}
	}
}

func TestCreateBackupFailsWhenCompletedRegistryWriteFails(t *testing.T) {
	s := testService(t)
	if _, err := s.db.Exec(`DROP TABLE backup_registry`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBackup(context.Background(), "registry-failure"); err == nil {
		t.Fatal("backup must not report success when its completed state cannot be persisted")
	}
}
