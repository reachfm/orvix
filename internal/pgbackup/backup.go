package pgbackup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type BackupManifest struct {
	BackupID      string `json:"backup_id"`
	SchemaVersion string `json:"schema_version"`
	AppVersion    string `json:"app_version"`
	PGVersion     string `json:"pg_version"`
	DumpVersion   string `json:"dump_version"`
	DumpFormat    string `json:"dump_format"`
	Encrypted     bool   `json:"encrypted"`
	Checksum      string `json:"checksum"`
	FileSize      int64  `json:"file_size"`
	CreatedAt     string `json:"created_at"`
}

type BackupConfig struct {
	Host         string
	Port         string
	Database     string
	User         string
	Password     string
	OutputDir    string
	AppVersion   string
	SchemaVersion string
	EncryptionKey string
	Timeout      time.Duration
}

func DefaultConfig() BackupConfig {
	return BackupConfig{
		Host:     "127.0.0.1",
		Port:     "5432",
		Timeout:  5 * time.Minute,
		OutputDir: "/var/backups/orvix/pg",
	}
}

func generateBackupID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("pg_%s_%s", time.Now().UTC().Format("20060102_150405"), hex.EncodeToString(b[:8]))
}

func findPgDump() (string, error) {
	candidates := []string{"pg_dump", "/usr/lib/postgresql/16/bin/pg_dump", "/usr/bin/pg_dump"}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("pg_dump not found on PATH")
}

func findPgRestore() (string, error) {
	candidates := []string{"pg_restore", "/usr/lib/postgresql/16/bin/pg_restore", "/usr/bin/pg_restore"}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("pg_restore not found on PATH")
}

func CreateBackup(cfg BackupConfig) (*BackupManifest, error) {
	pgDump, err := findPgDump()
	if err != nil {
		return nil, err
	}
	backupID := generateBackupID()
	outputFile := filepath.Join(cfg.OutputDir, backupID+".pgdump")
	tmpFile := outputFile + ".tmp"

	if err := os.MkdirAll(cfg.OutputDir, 0700); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	args := []string{
		"--host", cfg.Host,
		"--port", cfg.Port,
		"--username", cfg.User,
		"--dbname", cfg.Database,
		"--format=custom",
		"--verbose",
		"--file", tmpFile,
	}

	cmd := exec.Command(pgDump, args...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+cfg.Password)
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(tmpFile)
		return nil, fmt.Errorf("pg_dump failed: %w\nOutput: %s", err, string(output))
	}

	if err := os.Rename(tmpFile, outputFile); err != nil {
		return nil, fmt.Errorf("rename backup file: %w", err)
	}

	fi, err := os.Stat(outputFile)
	if err != nil {
		return nil, err
	}
	if fi.Size() == 0 {
		os.Remove(outputFile)
		return nil, fmt.Errorf("backup file is empty")
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(data)
	checksum := hex.EncodeToString(h[:])

	pgVersion := "unknown"
	verCmd := exec.Command(pgDump, "--version")
	if verOut, err := verCmd.Output(); err == nil {
		pgVersion = strings.TrimSpace(string(verOut))
	}

	manifest := &BackupManifest{
		BackupID:      backupID,
		SchemaVersion: cfg.SchemaVersion,
		AppVersion:    cfg.AppVersion,
		PGVersion:     pgVersion,
		DumpFormat:    "custom",
		Encrypted:     cfg.EncryptionKey != "",
		Checksum:      checksum,
		FileSize:      fi.Size(),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	if cfg.EncryptionKey != "" && len(cfg.EncryptionKey) >= 32 {
		encryptedFile := outputFile + ".enc"
		encKey := sha256.Sum256([]byte(cfg.EncryptionKey))
		encData, err := encryptAES(data, encKey[:])
		if err != nil {
			os.Remove(outputFile)
			return nil, fmt.Errorf("encrypt backup: %w", err)
		}
		if err := os.WriteFile(encryptedFile, encData, 0600); err != nil {
			return nil, err
		}
		os.Remove(outputFile)
		manifest.Encrypted = true
	}

	manifestPath := outputFile + ".manifest.json"
	mData, _ := json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(manifestPath, mData, 0644)

	return manifest, nil
}

func RestoreBackup(cfg BackupConfig, backupPath string) error {
	pgRestore, err := findPgRestore()
	if err != nil {
		return err
	}

	if cfg.Password != "" {
		os.Setenv("PGPASSWORD", cfg.Password)
		defer os.Unsetenv("PGPASSWORD")
	}

	restoreFile := backupPath
	if strings.HasSuffix(backupPath, ".enc") {
		rawPath := strings.TrimSuffix(backupPath, ".enc")
		data, err := os.ReadFile(backupPath)
		if err != nil {
			return err
		}
		encKey := sha256.Sum256([]byte(cfg.EncryptionKey))
		decData, err := decryptAES(data, encKey[:])
		if err != nil {
			return fmt.Errorf("decrypt backup: %w", err)
		}
		if err := os.WriteFile(rawPath, decData, 0600); err != nil {
			return err
		}
		defer os.Remove(rawPath)
		restoreFile = rawPath
	}

	manifestPath := backupPath + ".manifest.json"
	if !strings.HasSuffix(backupPath, ".manifest.json") {
		manifestPath = strings.TrimSuffix(backupPath, ".enc") + ".manifest.json"
	}
	if _, err := os.Stat(manifestPath); err == nil {
		mData, _ := os.ReadFile(manifestPath)
		var manifest BackupManifest
		if err := json.Unmarshal(mData, &manifest); err == nil {
			if manifest.Checksum != "" {
				data, _ := os.ReadFile(restoreFile)
				h := sha256.Sum256(data)
				if hex.EncodeToString(h[:]) != manifest.Checksum {
					return fmt.Errorf("checksum mismatch: backup may be corrupted")
				}
			}
			if manifest.AppVersion != cfg.AppVersion {
				return fmt.Errorf("version mismatch: backup app=%s, current app=%s", manifest.AppVersion, cfg.AppVersion)
			}
		}
	}

	args := []string{
		"--host", cfg.Host,
		"--port", cfg.Port,
		"--username", cfg.User,
		"--dbname", cfg.Database,
		"--format=custom",
		"--clean",
		"--if-exists",
		restoreFile,
	}

	cmd := exec.Command(pgRestore, args...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+cfg.Password)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_restore failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}

func encryptAES(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, data, nil), nil
}

func decryptAES(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
