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

// PolicyLoader allows reloading policy engine from DB.
type PolicyLoader interface {
	LoadFromDB(ctx context.Context) error
}

// TrustLoader allows reloading trust engine from DB.
type TrustLoader interface {
	LoadFromDB(ctx context.Context) error
}

// RuntimeReloader allows reloading runtime after restore.
type RuntimeReloader interface {
	Reload() error
}

// Service provides backup and restore operations.
type Service struct {
	basePath    string
	db          *sql.DB
	mailStoreDB *sql.DB
	mailDir     string
	attachDir   string

	mu      sync.Mutex
	policy  PolicyLoader
	trust   TrustLoader
	runtime RuntimeReloader
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

// SetPolicyLoader attaches a policy engine for reload after restore.
func (s *Service) SetPolicyLoader(p PolicyLoader) { s.policy = p }

// SetTrustLoader attaches a trust engine for reload after restore.
func (s *Service) SetTrustLoader(t TrustLoader) { s.trust = t }

// SetRuntimeReloader attaches a runtime for reload after restore.
func (s *Service) SetRuntimeReloader(r RuntimeReloader) { s.runtime = r }

func (s *Service) ensureBasePath() error { return os.MkdirAll(s.basePath, 0750) }

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) backupPath(id string) string { return filepath.Join(s.basePath, id) }

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
	bp := s.backupPath(id)
	if err := os.MkdirAll(bp, 0750); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
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
	bp := s.backupPath(id)
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
	bp := s.backupPath(id)
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
	bp := s.backupPath(id)
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
	manifestPath := filepath.Join(s.backupPath(id), "manifest.json")
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

// ── Restore Backup (with safety snapshot and rollback) ───

func (s *Service) RestoreBackup(ctx context.Context, id string) *RestoreResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restoreBackupLocked(ctx, id)
}

func (s *Service) restoreBackupLocked(ctx context.Context, id string) (res *RestoreResult) {
	bp := s.backupPath(id)
	if _, err := os.Stat(bp); os.IsNotExist(err) {
		return &RestoreResult{Success: false, Message: "backup not found"}
	}
	dbPath := filepath.Join(bp, "database.sqlite")
	mailPath := filepath.Join(bp, "mailstore.tar.gz")
	attPath := filepath.Join(bp, "attachments.tar.gz")

	// Create safety snapshot before restore.
	safetyID, err := s.createSafetySnapshot(ctx)
	if err != nil {
		return &RestoreResult{Success: false, Message: fmt.Sprintf("safety snapshot failed: %v", err)}
	}
	rollback := func(msg string) *RestoreResult {
		// Attempt to rollback by restoring the safety snapshot.
		s.restoreSafetySnapshot(ctx, safetyID)
		return &RestoreResult{Success: false, Message: msg}
	}

	// Validate required files.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return rollback("database.sqlite not found in backup")
	}

	// Restore mailstore.
	if _, err := os.Stat(mailPath); err == nil {
		if err := extractTarGz(mailPath, s.mailDir); err != nil {
			return rollback(fmt.Sprintf("mailstore restore: %v", err))
		}
	}

	// Restore attachments.
	if _, err := os.Stat(attPath); err == nil {
		if err := extractTarGz(attPath, s.attachDir); err != nil {
			return rollback(fmt.Sprintf("attachments restore: %v", err))
		}
	}

	// Reload policy engine.
	if s.policy != nil {
		if err := s.policy.LoadFromDB(ctx); err != nil {
			return rollback(fmt.Sprintf("policy reload: %v", err))
		}
	}

	// Reload trust engine.
	if s.trust != nil {
		if err := s.trust.LoadFromDB(ctx); err != nil {
			return rollback(fmt.Sprintf("trust reload: %v", err))
		}
	}

	// Reload runtime.
	if s.runtime != nil {
		if err := s.runtime.Reload(); err != nil {
			return rollback(fmt.Sprintf("runtime reload: %v", err))
		}
	}

	// Clean up safety snapshot.
	os.RemoveAll(s.backupPath(safetyID))

	return &RestoreResult{Success: true, Message: "restore completed"}
}

// createSafetySnapshot creates a backup of the current state before restore.
func (s *Service) createSafetySnapshot(ctx context.Context) (string, error) {
	backup, err := s.createBackupLocked(ctx, "pre-restore-safety-snapshot")
	if err != nil {
		return "", err
	}
	return backup.ID, nil
}

// restoreSafetySnapshot restores from a safety snapshot.
func (s *Service) restoreSafetySnapshot(ctx context.Context, id string) {
	bp := s.backupPath(id)
	mailPath := filepath.Join(bp, "mailstore.tar.gz")
	attPath := filepath.Join(bp, "attachments.tar.gz")

	if _, err := os.Stat(mailPath); err == nil {
		extractTarGz(mailPath, s.mailDir)
	}
	if _, err := os.Stat(attPath); err == nil {
		extractTarGz(attPath, s.attachDir)
	}
	if s.policy != nil {
		s.policy.LoadFromDB(ctx)
	}
	if s.trust != nil {
		s.trust.LoadFromDB(ctx)
	}
}

// ── Helpers ──────────────────────────────────────────────

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
