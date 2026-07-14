package pgbackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type DRManifest struct {
	PostgreSQLBackupID string `json:"postgresql_backup_id"`
	CoreMailBackupID   string `json:"coremail_backup_id"`
	AppVersion         string `json:"app_version"`
	PGSchemaVersion    string `json:"pg_schema_version"`
	CMSchemaVersion    string `json:"cm_schema_version"`
	Timestamp          string `json:"timestamp"`
	Consistent         bool   `json:"consistent"`
	RecoveryOrder      string `json:"recovery_order"`
}

func TestDRManifest(t *testing.T) {
	manifest := DRManifest{
		PostgreSQLBackupID: "pg_20260714_120000_abcdef123456",
		CoreMailBackupID:   "cm_20260714_120000_abcdef123456",
		AppVersion:         "1.0.3-rc5",
		PGSchemaVersion:    "1.0",
		CMSchemaVersion:    "1.0",
		Timestamp:          time.Now().UTC().Format(time.RFC3339),
		Consistent:         true,
		RecoveryOrder:      "postgresql_first_then_coremail",
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var restored DRManifest
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.PostgreSQLBackupID != manifest.PostgreSQLBackupID {
		t.Fatal("backup ID mismatch")
	}
	if restored.RecoveryOrder != "postgresql_first_then_coremail" {
		t.Fatal("recovery order must be enforced")
	}
	if !restored.Consistent {
		t.Fatal("backups should be marked consistent")
	}
}

func TestDRCoordinatedBackup(t *testing.T) {
	// Simulate coordinated backup creation
	pgBackupID := generateBackupID()
	cmBackupID := "cm_" + strings.TrimPrefix(pgBackupID, "pg_")

	manifest := DRManifest{
		PostgreSQLBackupID: pgBackupID,
		CoreMailBackupID:   cmBackupID,
		AppVersion:         "1.0.3-rc5",
		PGSchemaVersion:    "1.0",
		CMSchemaVersion:    "1.0",
		Timestamp:          time.Now().UTC().Format(time.RFC3339),
		Consistent:         true,
		RecoveryOrder:      "postgresql_first_then_coremail",
	}

	dir := t.TempDir()
	mPath := filepath.Join(dir, "dr_manifest.json")
	mData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(mPath, mData, 0644); err != nil {
		t.Fatal(err)
	}
	readback, err := os.ReadFile(mPath)
	if err != nil {
		t.Fatal(err)
	}
	var restored DRManifest
	json.Unmarshal(readback, &restored)

	if restored.Consistent == false {
		t.Fatal("coordinated backups must be consistent")
	}
	if restored.RecoveryOrder != "postgresql_first_then_coremail" {
		t.Fatal("must enforce recovery order: PostgreSQL then CoreMail")
	}
	if restored.AppVersion != "1.0.3-rc5" {
		t.Fatal("app version must match")
	}
}

func TestBackupRetention(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.OutputDir = dir

	// Simulate creating a backup
	backupPath := filepath.Join(dir, "test_backup.pgdump")
	if err := os.WriteFile(backupPath, []byte("simulated backup content"), 0600); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(backupPath)
	h := sha256.Sum256(data)
	manifest := BackupManifest{
		BackupID:   "test_retention",
		AppVersion: "1.0.3-rc5",
		Checksum:   hex.EncodeToString(h[:]),
		FileSize:   int64(len(data)),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	mPath := backupPath + ".manifest.json"
	mData, _ := json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(mPath, mData, 0644)

	// Verify manifest
	var readManifest BackupManifest
	mData, _ = os.ReadFile(mPath)
	json.Unmarshal(mData, &readManifest)
	if readManifest.BackupID != "test_retention" {
		t.Fatal("manifest readback failed")
	}

	// Verify checksum
	data2, _ := os.ReadFile(backupPath)
	h2 := sha256.Sum256(data2)
	if hex.EncodeToString(h2[:]) != readManifest.Checksum {
		t.Fatal("checksum mismatch")
	}

	// Verify app version match
	if readManifest.AppVersion != "1.0.3-rc5" {
		t.Fatal("app version mismatch")
	}
}
