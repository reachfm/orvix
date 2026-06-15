package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Service provides backup and restore operations.
type Service struct {
	basePath    string
	db          *sql.DB
	mailStoreDB *sql.DB
	mailDir     string
	attachDir   string
	configPath  string

	mu sync.Mutex
}

// NewService creates a backup service.
func NewService(basePath string, db, mailStoreDB *sql.DB, mailDir, attachDir string) *Service {
	return &Service{
		basePath:    basePath,
		db:          db,
		mailStoreDB: mailStoreDB,
		mailDir:     mailDir,
		attachDir:   attachDir,
	}
}

// SetConfigPath sets the path to the config file for backup archives.
// Defaults to /etc/orvix/orvix.yaml in production.
func (s *Service) SetConfigPath(path string) { s.configPath = path }

func (s *Service) ensureBasePath() error { return os.MkdirAll(s.basePath, 0750) }

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) backupPath(id string) string { return filepath.Join(s.basePath, id) }

// safeBackupPath validates a backup ID and returns a contained path.
// Rejects empty IDs, path traversal (..), separators (/ \), and null bytes.
// Uses EvalSymlinks to prevent symlink escape:
//   - If the candidate path exists, its real path is resolved and checked.
//   - If the candidate path does not exist, an Abs+prefix check is used.
//
// Returns the real (symlink-resolved) path inside basePath.
func (s *Service) safeBackupPath(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("backup ID is empty")
	}
	if strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.ContainsRune(id, 0) {
		return "", fmt.Errorf("backup ID contains forbidden characters")
	}

	absBase, err := filepath.Abs(s.basePath)
	if err != nil {
		return "", fmt.Errorf("resolve base path: %w", err)
	}

	realBase := absBase
	if fi, err := os.Stat(absBase); err == nil && fi.IsDir() {
		realBase, err = filepath.EvalSymlinks(absBase)
		if err != nil {
			return "", fmt.Errorf("resolve base symlinks: %w", err)
		}
	}

	candidate := filepath.Join(realBase, id)

	// Use Lstat to detect symlinks (including dangling symlinks).
	if _, err := os.Lstat(candidate); err == nil {
		realCandidate, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve candidate symlinks: %w", err)
		}
		if realCandidate != realBase && !strings.HasPrefix(realCandidate, realBase+string(os.PathSeparator)) {
			return "", fmt.Errorf("backup ID escapes base path via symlink")
		}
		return realCandidate, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("lstat candidate: %w", err)
	}

	// Candidate does not exist — use Abs containment check.
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve candidate path: %w", err)
	}
	if absCandidate != realBase && !strings.HasPrefix(absCandidate, realBase+string(os.PathSeparator)) {
		return "", fmt.Errorf("backup ID escapes base path")
	}
	return absCandidate, nil
}

// safeCreateBackupDir creates a backup directory after resolving base symlinks.
// Ensures the created path is within the resolved base.
func (s *Service) safeCreateBackupDir(id string) (string, error) {
	realBase, err := filepath.EvalSymlinks(s.basePath)
	if err != nil {
		return "", fmt.Errorf("resolve base path: %w", err)
	}
	candidate := filepath.Join(realBase, id)
	if err := os.MkdirAll(candidate, 0750); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		os.RemoveAll(candidate)
		return "", fmt.Errorf("resolve candidate: %w", err)
	}
	if absCandidate != realBase && !strings.HasPrefix(absCandidate, realBase+string(os.PathSeparator)) {
		os.RemoveAll(candidate)
		return "", fmt.Errorf("backup directory escapes base path")
	}
	return absCandidate, nil
}

// CreateBackup creates a full backup with mutex protection.
func (s *Service) CreateBackup(ctx context.Context, name string) (*Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createBackupLocked(ctx, name)
}

func (s *Service) createBackupLocked(ctx context.Context, name string) (*Backup, error) {
	if err := s.ensureBasePath(); err != nil {
		return nil, fmt.Errorf("base path: %w", err)
	}
	id := generateID()
	if name == "" {
		name = fmt.Sprintf("backup-%s", time.Now().UTC().Format("20060102-150405"))
	}
	bp, err := s.safeCreateBackupDir(id)
	if err != nil {
		return nil, fmt.Errorf("safe create dir: %w", err)
	}
	backup := &Backup{ID: id, Name: name, Status: StatusInProgress, CreatedAt: time.Now().UTC()}
	manifest := BackupManifest{ID: id, Name: name, CreatedAt: backup.CreatedAt}
	manifestBytes, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(bp, "manifest.json"), manifestBytes, 0640)

	dbPath := filepath.Join(bp, "database.sqlite")
	if err := s.snapshotDB(ctx, dbPath); err != nil {
		os.RemoveAll(bp)
		return nil, fmt.Errorf("snapshot db: %w", err)
	}
	mailPath := filepath.Join(bp, "mailstore.tar.gz")
	msgCount, err := archiveToTarGz(s.mailDir, mailPath, ".eml")
	if err != nil {
		os.RemoveAll(bp)
		return nil, fmt.Errorf("mailstore archive: %w", err)
	}
	manifest.MessageCount = msgCount
	attPath := filepath.Join(bp, "attachments.tar.gz")
	attCount, err := archiveToTarGz(s.attachDir, attPath, "")
	if err != nil {
		os.RemoveAll(bp)
		return nil, fmt.Errorf("attachments archive: %w", err)
	}
	manifest.AttachmentCount = attCount

	var totalSize int64
	filepath.Walk(bp, func(path string, info fs.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	backup.SizeBytes = totalSize
	sha, _ := computeDirSHA256(bp)
	backup.SHA256 = sha
	manifest.SHA256 = sha

	now := time.Now().UTC()
	backup.CompletedAt = &now
	backup.Status = StatusCompleted
	manifest.CompletedAt = &now
	manifest.SizeBytes = totalSize
	manifestBytes, _ = json.Marshal(manifest)
	os.WriteFile(filepath.Join(bp, "manifest.json"), manifestBytes, 0640)
	s.saveToRegistry(ctx, backup)
	s.populateManifestCounts(ctx, &manifest)
	manifestBytes, _ = json.Marshal(manifest)
	os.WriteFile(filepath.Join(bp, "manifest.json"), manifestBytes, 0640)
	return backup, nil
}

func (s *Service) setFailed(ctx context.Context, b *Backup, reason string) {
	b.Status = StatusFailed
	s.saveToRegistry(ctx, b)
}

func (s *Service) saveToRegistry(ctx context.Context, b *Backup) {
	if s.db == nil {
		return
	}
	s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO backup_registry (id, name, status, size_bytes, sha256, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.Name, string(b.Status), b.SizeBytes, b.SHA256, b.CreatedAt, b.CompletedAt)
}

func (s *Service) populateManifestCounts(ctx context.Context, m *BackupManifest) {
	if s.mailStoreDB == nil {
		return
	}
	s.mailStoreDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_domains WHERE deleted_at IS NULL").Scan(&m.DomainCount)
	s.mailStoreDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL").Scan(&m.MailboxCount)
	m.PolicyCount = 1
	s.mailStoreDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_messages WHERE deleted_at IS NULL OR deleted_at = 0").Scan(&m.MessageCount)
	s.mailStoreDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_attachments").Scan(&m.AttachmentCount)
}

// ── List Backups ─────────────────────────────────────────

func (s *Service) ListBackups(ctx context.Context) ([]Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listBackupsLocked(ctx)
}

func (s *Service) listBackupsLocked(ctx context.Context) ([]Backup, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, status, size_bytes, sha256, created_at, completed_at FROM backup_registry ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Backup
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.Name, &b.Status, &b.SizeBytes, &b.SHA256, &b.CreatedAt, &b.CompletedAt); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

// ── Get Backup ───────────────────────────────────────────

func (s *Service) GetBackup(ctx context.Context, id string) (*Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getBackupLocked(ctx, id)
}

func (s *Service) getBackupLocked(ctx context.Context, id string) (*Backup, error) {
	if s.db == nil {
		return s.readFromDisk(id)
	}
	row := s.db.QueryRowContext(ctx, "SELECT id, name, status, size_bytes, sha256, created_at, completed_at FROM backup_registry WHERE id=?", id)
	var b Backup
	err := row.Scan(&b.ID, &b.Name, &b.Status, &b.SizeBytes, &b.SHA256, &b.CreatedAt, &b.CompletedAt)
	if err == sql.ErrNoRows {
		return s.readFromDisk(id)
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Service) readFromDisk(id string) (*Backup, error) {
	bp, err := s.safeBackupPath(id)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(bp, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("backup not found")
	}
	var manifest BackupManifest
	json.Unmarshal(data, &manifest)
	status := StatusCompleted
	var completedAt *time.Time
	if manifest.CompletedAt != nil {
		completedAt = manifest.CompletedAt
	}
	return &Backup{
		ID: manifest.ID, Name: manifest.Name, Status: status,
		SizeBytes: manifest.SizeBytes, SHA256: manifest.SHA256,
		CreatedAt: manifest.CreatedAt, CompletedAt: completedAt,
	}, nil
}

// ── Delete Backup ────────────────────────────────────────

func (s *Service) DeleteBackup(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bp, err := s.safeBackupPath(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(bp); err != nil {
		return err
	}
	if s.db != nil {
		s.db.ExecContext(ctx, "DELETE FROM backup_registry WHERE id=?", id)
	}
	return nil
}

// ── Verify Backup ────────────────────────────────────────

func (s *Service) VerifyBackup(ctx context.Context, id string) (*VerifyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyBackupLocked(ctx, id)
}

func (s *Service) verifyBackupLocked(ctx context.Context, id string) (*VerifyResult, error) {
	result := &VerifyResult{Valid: true}
	bp, err := s.safeBackupPath(id)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(bp, "manifest.json")); os.IsNotExist(err) {
		result.Valid = false
		result.Errors = append(result.Errors, "manifest.json not found")
	}
	if _, err := os.Stat(filepath.Join(bp, "database.sqlite")); os.IsNotExist(err) {
		result.Valid = false
		result.Errors = append(result.Errors, "database.sqlite not found")
	}
	if _, err := os.Stat(filepath.Join(bp, "mailstore.tar.gz")); os.IsNotExist(err) {
		result.Valid = false
		result.Errors = append(result.Errors, "mailstore.tar.gz not found")
	}
	if _, err := os.Stat(filepath.Join(bp, "attachments.tar.gz")); os.IsNotExist(err) {
		result.Errors = append(result.Errors, "attachments.tar.gz not found")
	}
	var totalSize int64
	filepath.Walk(bp, func(path string, info fs.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	result.SizeBytes = totalSize
	sha, _ := computeDirSHA256(bp)
	result.SHA256 = sha
	return result, nil
}

// ── Restore Preview ──────────────────────────────────────

func (s *Service) RestorePreview(ctx context.Context, id string) (*RestorePreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bp, err := s.safeBackupPath(id)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(bp, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("backup not found")
	}
	var manifest BackupManifest
	json.Unmarshal(data, &manifest)
	return &RestorePreview{
		DomainCount: manifest.DomainCount, MailboxCount: manifest.MailboxCount,
		PolicyCount: manifest.PolicyCount, MessageCount: manifest.MessageCount,
		AttachmentCount: manifest.AttachmentCount, SizeBytes: manifest.SizeBytes,
	}, nil
}

// ── Backup Metrics ───────────────────────────────────────

func (s *Service) GetBackupMetrics(ctx context.Context) (*BackupMetrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	metrics := &BackupMetrics{}
	if s.db == nil {
		return metrics, nil
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0),
		       MAX(created_at), MIN(created_at),
		       MAX(CASE WHEN status = 'completed' THEN completed_at END)
		FROM backup_registry
	`)
	var newest, oldest, lastSuccess sql.NullString
	if err := row.Scan(&metrics.TotalBackups, &metrics.TotalSizeBytes, &newest, &oldest, &lastSuccess); err != nil {
		return nil, err
	}
	if newest.Valid && newest.String != "" {
		if t, err := time.Parse(time.RFC3339, newest.String); err == nil {
			metrics.NewestBackupAt = t.Format(time.RFC3339)
		} else {
			metrics.NewestBackupAt = newest.String
		}
	}
	if oldest.Valid && oldest.String != "" {
		if t, err := time.Parse(time.RFC3339, oldest.String); err == nil {
			metrics.OldestBackupAt = t.Format(time.RFC3339)
		} else {
			metrics.OldestBackupAt = oldest.String
		}
	}
	if lastSuccess.Valid && lastSuccess.String != "" {
		if t, err := time.Parse(time.RFC3339, lastSuccess.String); err == nil {
			metrics.LastSuccessfulAt = t.Format(time.RFC3339)
		} else {
			metrics.LastSuccessfulAt = lastSuccess.String
		}
	}

	var nextRun sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT next_run_at FROM backup_schedule_config WHERE id = 1`).Scan(&nextRun)
	if err == nil && nextRun.Valid && nextRun.String != "" {
		if t, err := time.Parse(time.RFC3339, nextRun.String); err == nil {
			metrics.NextScheduledAt = t.Format(time.RFC3339)
		} else {
			metrics.NextScheduledAt = nextRun.String
		}
	}

	return metrics, nil
}

// ── Backup Health ────────────────────────────────────────

func (s *Service) GetBackupHealth(ctx context.Context) (*BackupHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	health := &BackupHealth{RetentionEnabled: true}
	if s.db != nil {
		row := s.db.QueryRowContext(ctx, `SELECT enabled FROM backup_schedule_config WHERE id = 1`)
		var enabled int
		if err := row.Scan(&enabled); err == nil {
			health.SchedulerEnabled = enabled != 0
		}
	}

	_, err := os.Stat(s.basePath)
	health.DirectoryExists = err == nil

	if health.DirectoryExists {
		testFile := filepath.Join(s.basePath, ".writetest")
		if err := os.WriteFile(testFile, []byte("test"), 0640); err == nil {
			health.Writable = true
			os.Remove(testFile)
		}
		health.AvailableDiskBytes = diskFreeBytes(s.basePath)
	}

	return health, nil
}

// ── Helpers ──────────────────────────────────────────────

// Sensitive key patterns to redact from orvix.yaml in backup archives.
var redactedKeys = []string{
	"password", "secret", "token", "key", "private",
	"license", "jwt", "bearer", "api_key", "smtp_password",
}

func redactSensitiveYAML(input []byte) []byte {
	output := make([]byte, len(input))
	copy(output, input)
	lines := strings.Split(string(output), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, k := range redactedKeys {
			if strings.Contains(strings.ToLower(trimmed), k) && strings.Contains(trimmed, ":") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					val := strings.TrimSpace(parts[1])
					if val != "" && !strings.HasPrefix(val, "#") {
						lines[i] = parts[0] + ": REDACTED"
					}
				}
				break
			}
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// CreateArchive packages a completed backup into a single .tar.gz with explicit allowlist.
// Archive contents:
//   - var/lib/orvix/orvix.db          (database snapshot)
//   - etc/orvix/orvix.yaml.redacted  (sanitized config, if available)
//   - BACKUP_INFO.txt                (metadata)
//
// Sensitive files (.env, .key, .pem, .crt, .p12, .pfx, license, token files)
// are NEVER included.
func (s *Service) CreateArchive(ctx context.Context, backupID string) (string, error) {
	bp, err := s.safeBackupPath(backupID)
	if err != nil {
		return "", err
	}
	archivePath := filepath.Join(bp, "backup-archive.tar.gz")

	f, err := os.Create(archivePath)
	if err != nil {
		return "", fmt.Errorf("create archive: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// 1. Add database snapshot: var/lib/orvix/orvix.db
	dbPath := filepath.Join(bp, "database.sqlite")
	if data, err := os.ReadFile(dbPath); err == nil {
		if err := writeTarEntry(tw, "var/lib/orvix/orvix.db", data, 0640); err != nil {
			return "", fmt.Errorf("archive db: %w", err)
		}
	}

	// 2. Add redacted orvix.yaml: etc/orvix/orvix.yaml.redacted
	configPath := s.configPath
	if configPath == "" {
		configPath = "/etc/orvix/orvix.yaml"
	}
	if data, err := os.ReadFile(configPath); err == nil {
		redacted := redactSensitiveYAML(data)
		if err := writeTarEntry(tw, "etc/orvix/orvix.yaml.redacted", redacted, 0640); err != nil {
			return "", fmt.Errorf("archive config: %w", err)
		}
	}

	// 3. Add BACKUP_INFO.txt
	info := fmt.Sprintf("Backup ID: %s\nCreated At: %s\n",
		backupID, time.Now().UTC().Format(time.RFC3339))
	// Try to read manifest for richer metadata.
	if manifestData, err := os.ReadFile(filepath.Join(bp, "manifest.json")); err == nil {
		var manifest BackupManifest
		if json.Unmarshal(manifestData, &manifest) == nil {
			info = fmt.Sprintf("Backup ID: %s\nName: %s\nCreated At: %s\nSize Bytes: %d\nSHA256: %s\nDomain Count: %d\nMailbox Count: %d\nMessage Count: %d\nAttachment Count: %d\n",
				manifest.ID, manifest.Name, manifest.CreatedAt.Format(time.RFC3339),
				manifest.SizeBytes, manifest.SHA256,
				manifest.DomainCount, manifest.MailboxCount,
				manifest.MessageCount, manifest.AttachmentCount)
		}
	}
	if err := writeTarEntry(tw, "BACKUP_INFO.txt", []byte(info), 0640); err != nil {
		return "", fmt.Errorf("archive info: %w", err)
	}

	return archivePath, nil
}

func writeTarEntry(tw *tar.Writer, name string, data []byte, mode int64) error {
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func (s *Service) snapshotDB(ctx context.Context, destPath string) error {
	if s.mailStoreDB == nil {
		return nil
	}
	if err := s.validateBackupOutputPath(destPath); err != nil {
		return err
	}
	_, err := s.mailStoreDB.ExecContext(ctx, "VACUUM INTO ?", destPath)
	if err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}

func (s *Service) validateBackupOutputPath(path string) error {
	root, err := filepath.Abs(s.basePath)
	if err != nil {
		return fmt.Errorf("backup root: %w", err)
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("backup output path: %w", err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("backup output relation: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("backup output path escapes backup root: %s", path)
	}
	return nil
}

func archiveToTarGz(srcDir, destPath, extFilter string) (int64, error) {
	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()
	var count int64
	filepath.Walk(srcDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if extFilter != "" && !strings.HasSuffix(path, extFilter) {
			return nil
		}
		relPath, _ := filepath.Rel(srcDir, path)
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil
		}
		header.Name = relPath
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fh.Close()
		io.Copy(tw, fh)
		count++
		return nil
	})
	return count, nil
}

func extractTarGz(srcPath, destDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := safeArchiveTarget(destDir, header)
		if err != nil {
			return err
		}
		if header.FileInfo().IsDir() {
			os.MkdirAll(target, 0750)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0750)
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		io.Copy(out, tr)
		out.Close()
	}
	return nil
}

func safeArchiveTarget(destDir string, header *tar.Header) (string, error) {
	if header == nil {
		return "", fmt.Errorf("nil archive header")
	}
	if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
		return "", fmt.Errorf("archive links are not allowed: %s", header.Name)
	}
	name := strings.ReplaceAll(header.Name, "\\", "/")
	clean := filepath.Clean(name)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("invalid archive entry: %s", header.Name)
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) || filepath.IsAbs(clean) || filepath.VolumeName(name) != "" || filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("absolute archive entry rejected: %s", header.Name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("archive traversal rejected: %s", header.Name)
		}
	}
	root, err := filepath.Abs(destDir)
	if err != nil {
		return "", fmt.Errorf("restore root: %w", err)
	}
	target, err := filepath.Abs(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("restore target: %w", err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("restore target relation: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive entry escapes restore root: %s", header.Name)
	}
	return target, nil
}

func computeDirSHA256(dir string) (string, error) {
	h := sha256.New()
	filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write(data)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), nil
}
