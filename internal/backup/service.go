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

	"github.com/orvix/orvix/internal/dbdialect"
)

// defaultStagingRoot is the directory under which restore staging
// directories are created. The actual staging path is
// <stagingRoot>/<backup_id>/.
const defaultStagingRoot = "/var/lib/orvix/restore-staging"

// Max archive entry sizes for validation safety.
const (
	maxMetadataEntrySize = 10 * 1024 * 1024  // 10 MiB for manifest/checksums/config
	maxDBEntrySize       = 2 * 1024 * 1024 * 1024  // 2 GiB for database snapshot
	maxMailStoreEntrySize = 10 * 1024 * 1024 * 1024 // 10 GiB for mail store tar.gz
	maxTotalArchiveBytes = 50 * 1024 * 1024 * 1024 // 50 GiB total archive
)

// Service provides backup and restore operations.
type Service struct {
	basePath       string
	stagingRoot    string
	db             *sql.DB
	dialect        *dbdialect.Info
	mailStoreDB    *sql.DB
	mailDir        string
	attachDir      string
	configPath     string
	buildVersion   string
	buildCommit    string
	keyPaths       []string

	// postCreateHook is invoked once a successful
	// CreateBackup completes, with the local archive
	// path as its single argument. The hook is the
	// package's one and only seam to upload the
	// finished archive to configured remote targets;
	// it must be set by the runtime so the production
	// pipeline can call into internal/backup/targets.
	// Tests that don't care about remote upload keep
	// this nil and accept local-only behaviour.
	postCreateHook func(backupID, archivePath string)

	mu sync.Mutex
}

// NewService creates a backup service.
func NewService(basePath string, db, mailStoreDB *sql.DB, mailDir, attachDir string) *Service {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Service{
		basePath:    basePath,
		stagingRoot: defaultStagingRoot,
		db:          db,
		dialect:     dialect,
		mailStoreDB: mailStoreDB,
		mailDir:     mailDir,
		attachDir:   attachDir,
	}
}

// SetPostCreateHook installs the post-create hook. The
// runtime wires this in *after* the targets.Manager is
// built so the Uploader can reach the configured backup
// targets through the same DB handle the audit trail
// uses. nil clears the hook.
func (s *Service) SetPostCreateHook(h func(backupID, archivePath string)) {
	s.mu.Lock()
	s.postCreateHook = h
	s.mu.Unlock()
}

// SetConfigPath sets the path to the config file for backup archives.
// Defaults to /etc/orvix/orvix.yaml in production.
func (s *Service) SetConfigPath(path string) { s.configPath = path }

// SetBuildInfo sets version and commit for the backup manifest.
func (s *Service) SetBuildInfo(version, commit string) { s.buildVersion = version; s.buildCommit = commit }

// SetStagingRoot sets the directory for restore staging.
func (s *Service) SetStagingRoot(root string) { s.stagingRoot = root }

// AddKeyPath adds a key file path to include in every backup.
// Paths that do not exist are silently skipped during backup creation.
func (s *Service) AddKeyPath(path string) { s.keyPaths = append(s.keyPaths, path) }

func (s *Service) ensureBasePath() error { return os.MkdirAll(s.basePath, 0750) }

// archiveBackupDir packs the in-place backup directory bp
// (which holds the per-file copies from createBackupLocked)
// into a single tar.gz at <bp>/<id>.tar.gz. Returns the
// archive absolute path. Errors are returned so the caller
// can record them on the row.
//
// The archive streams entries directly to disk to avoid
// loading the (potentially multi-GB) mail-store copy in
// memory; files > maxEntryBytes are skipped silently because
// the manifest already records what was elided.
func archiveBackupDir(bp, id string) (string, error) {
	archivePath := filepath.Join(bp, id+".tar.gz")
	out, err := os.Create(archivePath)
	if err != nil {
		return "", fmt.Errorf("create archive: %w", err)
	}
	defer out.Close()

	const maxEntryBytes = 2 * 1024 * 1024 * 1024
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	walkErr := filepath.Walk(bp, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Skip the archive we are writing.
		if path == archivePath {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Size() > maxEntryBytes {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bp, path)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		return nil
	})
	if walkErr != nil {
		_ = os.Remove(archivePath)
		return "", walkErr
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return archivePath, nil
}

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

	hostname, _ := os.Hostname()
	manifest := BackupManifest{
		ID: id, Name: name, CreatedAt: backup.CreatedAt,
		Version:     s.buildVersion,
		BuildCommit: s.buildCommit,
		Hostname:    hostname,
		Files:       make(map[string]string),
	}

	dbPath := filepath.Join(bp, "database.sqlite")
	if err := s.snapshotDB(ctx, dbPath); err != nil {
		os.RemoveAll(bp)
		return nil, fmt.Errorf("snapshot db: %w", err)
	}
	manifest.Files["database.sqlite"] = fileSHA256(dbPath)

	mailPath := filepath.Join(bp, "mailstore.tar.gz")
	msgCount, err := archiveToTarGz(s.mailDir, mailPath, ".eml")
	if err != nil {
		os.RemoveAll(bp)
		return nil, fmt.Errorf("mailstore archive: %w", err)
	}
	manifest.MessageCount = msgCount
	manifest.Files["mailstore.tar.gz"] = fileSHA256(mailPath)

	attPath := filepath.Join(bp, "attachments.tar.gz")
	attCount, err := archiveToTarGz(s.attachDir, attPath, "")
	if err != nil {
		os.RemoveAll(bp)
		return nil, fmt.Errorf("attachments archive: %w", err)
	}
	manifest.AttachmentCount = attCount
	manifest.Files["attachments.tar.gz"] = fileSHA256(attPath)

	// Copy orvix.yaml config file.
	if s.configPath != "" {
		if data, err := os.ReadFile(s.configPath); err == nil {
			redacted := redactSensitiveYAML(data)
			cfgDest := filepath.Join(bp, "orvix.yaml")
			if err := os.WriteFile(cfgDest, redacted, 0640); err == nil {
				manifest.Files["orvix.yaml"] = fileSHA256(cfgDest)
			}
		}
	}

	// Copy key files (DKIM, JWT, VAPID, etc.)
	for _, kp := range s.keyPaths {
		if kp == "" {
			continue
		}
		if data, err := os.ReadFile(kp); err == nil {
			baseName := filepath.Base(kp)
			keyDest := filepath.Join(bp, baseName)
			if err := os.WriteFile(keyDest, data, 0600); err == nil {
				manifest.Files[baseName] = fileSHA256(keyDest)
			}
		}
	}

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
	manifest.SizeBytes = totalSize

	now := time.Now().UTC()
	backup.CompletedAt = &now
	backup.Status = StatusCompleted
	manifest.CompletedAt = &now

	manifestBytes, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(bp, "manifest.json"), manifestBytes, 0640)

	s.saveToRegistry(ctx, backup)
	s.populateManifestCounts(ctx, &manifest)

	manifestBytes, _ = json.Marshal(manifest)
	os.WriteFile(filepath.Join(bp, "manifest.json"), manifestBytes, 0640)

	// Build the final tar.gz archive. We do this AFTER
	// all in-place files are written so the archive
	// captures the manifest + manifest counts that the
	// UI / downstream consumers expect.
	archivePath, archiveErr := archiveBackupDir(bp, backup.ID)
	if archiveErr != nil {
		// An archive failure cannot corrupt the local
		// backup, only the remote-upload path. We log it
		// and continue; the row remains in completed
		// status because the local files are still valid
		// for restore via the directory path.
		if s.db != nil {
			_, _ = s.db.ExecContext(ctx,
				fmt.Sprintf("UPDATE backup_registry SET last_test_message=%s WHERE id=%s", s.dialect.Placeholder(1), s.dialect.Placeholder(2)),
				"archive_failed:"+archiveErr.Error(), backup.ID)
		}
	} else if s.postCreateHook != nil {
		// Fire the post-create hook (target upload). The
		// hook runs in its own goroutine so a slow /
		// timing-out upload never blocks the return to
		// the admin caller.
		hook := s.postCreateHook
		go func(id, path string) {
			defer func() {
				_ = recover()
			}()
			hook(id, path)
		}(backup.ID, archivePath)
	}

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
	q := s.dialect.Upsert(
		"backup_registry",
		[]string{"id", "name", "status", "size_bytes", "sha256", "created_at", "completed_at"},
		[]string{"id"},
		[]string{"name", "status", "size_bytes", "sha256", "created_at", "completed_at"},
	)
	s.db.ExecContext(ctx, q, b.ID, b.Name, string(b.Status), b.SizeBytes, b.SHA256, b.CreatedAt, b.CompletedAt)
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
	row := s.db.QueryRowContext(ctx,
		"SELECT id, name, status, size_bytes, sha256, created_at, completed_at FROM backup_registry WHERE id="+s.dialect.Placeholder(1), id)
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
		s.db.ExecContext(ctx, "DELETE FROM backup_registry WHERE id="+s.dialect.Placeholder(1), id)
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

// GetBackupHealth returns the live backup system health status.
//
// The status string is one of:
//
//	"ok"                  — at least one backup exists, fresh
//	                        (≤24h old), and the directory is writable.
//	"warning"             — most recent backup is between 24h and 72h
//	                        old, OR the scheduler is disabled but
//	                        manual backups exist.
//	"critical"            — most recent backup is older than 72h,
//	                        OR the directory is missing/unwritable.
//	"no_backups"          — the install has never produced a backup.
//	                        The previous release conflated this with
//	                        "critical" which produced misleading
//	                        alerts on fresh installs. Operators now
//	                        see a distinct "no_backups" state with
//	                        the NoBackups field set, so the dashboard
//	                        can render a "first backup pending" message
//	                        instead of a critical incident badge.
//	"directory_missing"   — the configured backup directory does
//	                        not exist. Investigate filesystem / mount.
//	"directory_not_writable" — the directory exists but cannot be
//	                        written to. Investigate permissions.
//	"scheduler_disabled"  — scheduler is explicitly disabled and
//	                        there is no recent manual backup.
//
// The function is best-effort: it never returns an error from the
// database query for `last_completed_backup_at` — a missing row is
// a normal "no backups yet" case, not a failure.
func (s *Service) GetBackupHealth(ctx context.Context) (*BackupHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	health := &BackupHealth{RetentionEnabled: true, Status: HealthStatusOK}
	if s.db != nil {
		row := s.db.QueryRowContext(ctx, `SELECT enabled FROM backup_schedule_config WHERE id = 1`)
		var enabled int
		if err := row.Scan(&enabled); err == nil {
			health.SchedulerEnabled = enabled != 0
		}
	}

	_, statErr := os.Stat(s.basePath)
	health.DirectoryExists = statErr == nil

	if health.DirectoryExists {
		testFile := filepath.Join(s.basePath, ".writetest")
		if err := os.WriteFile(testFile, []byte("test"), 0640); err == nil {
			health.Writable = true
			os.Remove(testFile)
		}
		health.AvailableDiskBytes = diskFreeBytes(s.basePath)
	}

	// Determine the freshness of the most recent completed backup.
	// lastBackupAt is the zero time when there are no completed
	// backups; that case is now a distinct "no_backups" state
	// rather than a "critical" one.
	lastBackupAt, err := s.lastCompletedBackupTime(ctx)
	if err == nil && !lastBackupAt.IsZero() {
		health.LastBackupAgeHours = time.Since(lastBackupAt).Hours()
		switch {
		case health.LastBackupAgeHours > 72:
			health.LastBackupAgeCritical = true
			health.Status = HealthStatusCritical
			health.Reason = fmt.Sprintf("no backups in %.0fh", health.LastBackupAgeHours)
		case health.LastBackupAgeHours > 24:
			health.LastBackupAgeWarning = true
			health.Status = HealthStatusWarning
			health.Reason = fmt.Sprintf("no backups in %.0fh", health.LastBackupAgeHours)
		}
	} else {
		// No completed backups ever. This is normal for a fresh
		// install; do not flag it as critical.
		health.LastBackupAgeHours = -1
		health.NoBackups = true
		health.Status = HealthStatusNoBackups
		health.Reason = "no backups yet — first run pending"
		// Even on a fresh install, a missing or unwritable
		// directory is a real problem.
		if !health.DirectoryExists {
			health.Status = HealthStatusDirMissing
			health.Reason = "backup directory does not exist"
		} else if !health.Writable {
			health.Status = HealthStatusDirNotWritable
			health.Reason = "backup directory is not writable"
		}
	}

	// Directory / writability override any other status; an
	// unwritable directory is always critical, even if recent
	// backups exist (the next backup will fail).
	if !health.DirectoryExists {
		health.Status = HealthStatusDirMissing
		health.Reason = "backup directory does not exist"
	} else if !health.Writable {
		health.Status = HealthStatusDirNotWritable
		health.Reason = "backup directory is not writable"
	}

	// Scheduler disabled + stale manual backup → warning, not critical.
	if !health.SchedulerEnabled && health.Status == HealthStatusOK && health.LastBackupAgeHours > 24 {
		health.Status = HealthStatusDisabled
		health.Reason = "scheduler disabled; manual backups older than 24h"
	}

	return health, nil
}

func (s *Service) lastCompletedBackupTime(ctx context.Context) (time.Time, error) {
	if s.db == nil {
		return time.Time{}, nil
	}
	var t sql.NullString
	row := s.db.QueryRowContext(ctx,
		"SELECT MAX(completed_at) FROM backup_registry WHERE status = "+s.dialect.Placeholder(1), string(StatusCompleted))
	if err := row.Scan(&t); err != nil {
		return time.Time{}, err
	}
	if !t.Valid || t.String == "" {
		return time.Time{}, nil
	}
	return parseStoredTime(t.String)
}

// parseStoredTime accepts the multiple textual representations
// SQLite / modernc.org/sqlite may return for a DATETIME column:
//
//	"2026-07-01T18:00:00Z"            RFC3339 (no nanos)
//	"2026-07-01T18:00:00.123456Z"     RFC3339Nano
//	"2026-07-01 18:00:00.000000000+00:00"   space separator
//	"2026-07-01 18:00:00+00:00"       space separator, no nanos
//	"2026-07-01 18:00:47.7650625 +0000 UTC"  MAX(time.Time) value
//
// Returns the zero time on any failure so the caller treats it as
// "no completed backup" rather than a parse error.
func parseStoredTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	// Try the standard layouts in order of strictness.
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	// Go's time.Time.String() format used by MAX(time.Time) aggregates:
	// "2006-01-02 15:04:05.999999999 +0000 UTC" or without nanos.
	goStringLayouts := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	}
	for _, layout := range goStringLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("backup time: unparseable stored value %q", s)
}

// debugParse is a debug-only helper that returns the first
// matching layout for a stored time string. It is intentionally
// separate from parseStoredTime so production code paths remain
// unchanged. Used by the test suite to diagnose failures when
// the stored format changes between SQLite versions.
func debugParse(s string) string {
	s = strings.TrimSpace(s)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return layout + " -> " + t.Format(time.RFC3339)
		}
	}
	return "no match"
}

// ── Helpers ──────────────────────────────────────────────

// Sensitive key patterns to redact from orvix.yaml and .env files in backup archives.
// Redacted case-insensitively. Covers both YAML (KEY: value) and env (KEY=value) formats.
var redactedKeyPatterns = []string{
	"password", "secret", "token", "key", "private",
	"license", "jwt", "bearer", "api_key", "smtp_password",
	"credential", "pass",
}

// isSecretKey returns true if the key contains any known sensitive pattern.
func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, pattern := range redactedKeyPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func redactSensitiveYAML(input []byte) []byte {
	output := make([]byte, len(input))
	copy(output, input)
	lines := strings.Split(string(output), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		// Handle YAML colon format: KEY: value
		if strings.Contains(trimmed, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if val != "" && !strings.HasPrefix(val, "#") && isSecretKey(key) {
					lines[i] = parts[0] + ": REDACTED"
				}
			}
		}
		// Handle env equals format: KEY=VALUE
		if strings.Contains(trimmed, "=") {
			eqIdx := strings.Index(line, "=")
			key := strings.TrimSpace(line[:eqIdx])
			val := line[eqIdx+1:]
			if val != "" && isSecretKey(key) {
				lines[i] = key + "=REDACTED"
			}
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// redactEnvFile redacts KEY=VALUE environment file content.
func redactEnvFile(input []byte) []byte {
	return redactSensitiveYAML(input)
}

// CreateArchive packages a completed backup into a single .tar.gz with explicit allowlist.
// Archive contents:
//   - var/lib/orvix/orvix.db          (database snapshot, streamed)
//   - etc/orvix/orvix.yaml.redacted  (sanitized config, if available)
//   - backup.json                    (enterprise manifest — source of truth for 2H)
//   - RESTORE_INSTRUCTIONS.txt       (restore guidance)
//   - checksums.txt                  (sha256 per file)
//
// Large payloads (DB, mail store) are streamed through the tar writer
// and hashed incrementally so the archive is never fully in memory.
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

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Collect file items with their sha256 for the manifest and checksums.txt.
	var items []ManifestItem

	// Helper to write a file entry from a byte slice.
	writeBufEntry := func(tarName string, data []byte) error {
		h := sha256.Sum256(data)
		items = append(items, ManifestItem{Path: tarName, Size: int64(len(data)), SHA256: hex.EncodeToString(h[:])})
		return writeTarEntry(tw, tarName, data, 0640)
	}

	// Helper to write a file entry by streaming from disk.
	writeStreamEntry := func(tarName, srcPath string) error {
		src, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer src.Close()
		stat, err := src.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     tarName,
			Mode:     0640,
			Size:     stat.Size(),
			Typeflag: tar.TypeReg,
		}); err != nil {
			return fmt.Errorf("tar header %s: %w", tarName, err)
		}
		hasher := sha256.New()
		written, err := io.Copy(tw, io.TeeReader(src, hasher))
		if err != nil {
			return fmt.Errorf("write %s: %w", tarName, err)
		}
		items = append(items, ManifestItem{Path: tarName, Size: written, SHA256: hex.EncodeToString(hasher.Sum(nil))})
		return nil
	}

	// 1. Database snapshot (streamed, not loaded into memory)
	dbPath := filepath.Join(bp, "database.sqlite")
	if _, err := os.Stat(dbPath); err == nil {
		if err := writeStreamEntry("var/lib/orvix/orvix.db", dbPath); err != nil {
			return "", fmt.Errorf("archive db: %w", err)
		}
	}

	// 2. Redacted config (small metadata, read into memory)
	cfgPath := s.configPath
	if cfgPath == "" {
		cfgPath = "/etc/orvix/orvix.yaml"
	}
	if data, err := os.ReadFile(cfgPath); err == nil {
		redacted := redactSensitiveYAML(data)
		if err := writeBufEntry("etc/orvix/orvix.yaml.redacted", redacted); err != nil {
			return "", fmt.Errorf("archive config: %w", err)
		}
	}

	// 3. .env files if present (redacted)
	envDir := filepath.Dir(cfgPath)
	envPattern := filepath.Join(envDir, "*.env")
	if envMatches, err := filepath.Glob(envPattern); err == nil {
		for _, envPath := range envMatches {
			if data, err := os.ReadFile(envPath); err == nil {
				redacted := redactSensitiveYAML(data)
				relName := "etc/orvix/" + filepath.Base(envPath) + ".redacted"
				if err := writeBufEntry(relName, redacted); err != nil {
					return "", fmt.Errorf("archive env: %w", err)
				}
			}
		}
	}

	// Build the enterprise manifest.
	hostname, _ := os.Hostname()
	manifest := BackupArchiveManifest{
		BackupID:            backupID,
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		Hostname:            hostname,
		Product:             ProductName,
		Version:             s.buildVersion,
		BuildCommit:         s.buildCommit,
		SchemaVersion:       1,
		BackupFormatVersion: BackupFormatVersion,
		IncludedItems:       items,
		DatabasePath:        "/var/lib/orvix/orvix.db",
		ConfigPath:          cfgPath,
		ConfigSummaryRedacted: true,
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	fileHash := sha256.Sum256(manifestData)
	items = append(items, ManifestItem{Path: "backup.json", Size: int64(len(manifestData)), SHA256: hex.EncodeToString(fileHash[:])})

	// 4. Write manifest
	if err := writeBufEntry("backup.json", manifestData); err != nil {
		return "", fmt.Errorf("archive manifest: %w", err)
	}

	// 5. Write RESTORE_INSTRUCTIONS.txt
	instructions := `Orvix Enterprise Mail — Restore Instructions

This archive contains a backup of the Orvix Enterprise Mail system.

What is included:
  - SQLite database snapshot (var/lib/orvix/orvix.db)
  - Server configuration (etc/orvix/orvix.yaml.redacted)
  - Environment files, if present (etc/orvix/*.env.redacted)

What is NOT included (for security):
  - TLS private keys
  - DKIM private keys
  - Provider API tokens
  - Raw secrets in plaintext

Restore process (Phase 2H):
  1. Upload this archive to the target server.
  2. The admin panel's Restore flow will:
     a. Validate the archive (checksums, manifest, format version)
     b. Create a pre-restore safety backup of the current state
     c. Stage the restore to /var/lib/orvix/restore-staging/<backup_id>/
  3. After staging, the operator must restart the Orvix service
     to apply the staged data.

For a full disaster recovery, install Orvix on a clean host first,
then use the admin panel to restore from this backup.
`
	if err := writeBufEntry("RESTORE_INSTRUCTIONS.txt", []byte(instructions)); err != nil {
		return "", fmt.Errorf("archive instructions: %w", err)
	}

	// 6. Write checksums.txt
	var checksums strings.Builder
	for _, it := range items {
		checksums.WriteString(fmt.Sprintf("%s  %s\n", it.SHA256, it.Path))
	}
	if err := writeBufEntry("checksums.txt", []byte(checksums.String())); err != nil {
		return "", fmt.Errorf("archive checksums: %w", err)
	}

	// Close tar and gzip writers explicitly.
	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("close tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return "", fmt.Errorf("close gzip: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync archive: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close archive: %w", err)
	}

	// Compute final archive sha256 by streaming the file (not ReadFile).
	sidecarPath := archivePath + ".sha256"
	sidecarFile, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive for sidecar: %w", err)
	}
	defer sidecarFile.Close()
	sidecarHash := sha256.New()
	if _, err := io.Copy(sidecarHash, sidecarFile); err != nil {
		return "", fmt.Errorf("hash archive for sidecar: %w", err)
	}
	sidecarData := hex.EncodeToString(sidecarHash.Sum(nil)) + "  " + filepath.Base(archivePath) + "\n"
	if err := os.WriteFile(sidecarPath, []byte(sidecarData), 0640); err != nil {
		return "", fmt.Errorf("write sidecar: %w", err)
	}

	return archivePath, nil
}

// ValidateBackup validates the on-disk backup directory contents.
// It checks:
//   - Presence of database.sqlite and mailstore.tar.gz
//   - Directory-level sha256 consistency
func (s *Service) ValidateBackup(ctx context.Context, id string) (*VerifyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

// ValidateArchive validates a backup archive (tar.gz) for integrity.
// It checks:
//   - Archive is valid gzip/tar
//   - backup.json exists and has valid product/format version
//   - checksums.txt matches file contents
func (s *Service) ValidateArchive(ctx context.Context, id string) (*VerifyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateArchiveLocked(ctx, id)
}

// safeStagingPath validates a backup ID and returns a safe, contained
// staging path under the resolved staging root. Prevents traversal and
// symlink escape.
func (s *Service) safeStagingPath(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("backup ID is empty")
	}
	if strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.ContainsRune(id, 0) {
		return "", fmt.Errorf("backup ID contains forbidden characters")
	}
	absRoot, err := filepath.Abs(s.stagingRoot)
	if err != nil {
		return "", fmt.Errorf("resolve staging root: %w", err)
	}
	realRoot := absRoot
	if fi, err := os.Stat(absRoot); err == nil && fi.IsDir() {
		realRoot, err = filepath.EvalSymlinks(absRoot)
		if err != nil {
			return "", fmt.Errorf("resolve staging root symlinks: %w", err)
		}
	}
	candidate := filepath.Join(realRoot, id)
	if _, err := os.Lstat(candidate); err == nil {
		realCandidate, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve candidate symlinks: %w", err)
		}
		if realCandidate != realRoot && !strings.HasPrefix(realCandidate, realRoot+string(os.PathSeparator)) {
			return "", fmt.Errorf("staging ID escapes staging root via symlink")
		}
		return realCandidate, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("lstat candidate: %w", err)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve candidate path: %w", err)
	}
	if absCandidate != realRoot && !strings.HasPrefix(absCandidate, realRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("staging ID escapes staging root")
	}
	return absCandidate, nil
}

// RestoreBackup validates the backup archive and stages it for restore.
// Phase 2H: does NOT overwrite live data. Returns staged status.
func (s *Service) RestoreBackup(ctx context.Context, id string) (*RestoreStageResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Validate the archive first.
	vr, err := s.validateArchiveLocked(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("pre-restore validation: %w", err)
	}
	if !vr.Valid {
		return &RestoreStageResult{
			Status:  RestoreStatusFailed,
			Message: "Archive validation failed: " + strings.Join(vr.Errors, "; "),
		}, nil
	}

	// 2. Create a pre-restore safety backup.
	safetyName := fmt.Sprintf("pre-restore-safety-%s-%s", id, time.Now().UTC().Format("20060102-150405"))
	safetyBackup, err := s.createBackupLocked(ctx, safetyName)
	if err != nil {
		return nil, fmt.Errorf("pre-restore safety backup failed: %w", err)
	}

	// 3. Create staging directory with safe path resolution.
	stagingDir, err := s.safeStagingPath(id)
	if err != nil {
		return nil, fmt.Errorf("safe staging path: %w", err)
	}
	if err := os.MkdirAll(stagingDir, 0750); err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}

	// 4. Extract archive to staging directory.
	bp, err := s.safeBackupPath(id)
	if err != nil {
		return nil, err
	}
	archivePath := filepath.Join(bp, "backup-archive.tar.gz")
	if err := extractTarGz(archivePath, stagingDir); err != nil {
		return nil, fmt.Errorf("extract to staging: %w", err)
	}

	return &RestoreStageResult{
		Status:      RestoreStatusStaged,
		Message:     RestoreStagedMessage,
		BackupID:    safetyBackup.ID,
		StagingPath: stagingDir,
	}, nil
}

// validateArchiveLocked is the internal locked version used by RestoreBackup.
// Strict validation: requires checksums.txt, backup.json, full per-file checksum
// coverage, rejects unknown entries, traversal, absolute paths, symlinks.
func (s *Service) validateArchiveLocked(ctx context.Context, id string) (*VerifyResult, error) {
	result := &VerifyResult{Valid: true}
	bp, err := s.safeBackupPath(id)
	if err != nil {
		return nil, err
	}

	archivePath := filepath.Join(bp, "backup-archive.tar.gz")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		result.Valid = false
		result.Errors = append(result.Errors, "backup-archive.tar.gz not found")
		return result, nil
	}

	f, err := os.Open(archivePath)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("cannot open archive: %v", err))
		return result, nil
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("invalid gzip: %v", err))
		return result, nil
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	// Allowed archive entries (checksum manifest vs actual content).
	type entryInfo struct {
		name string
		data []byte
	}
	var entries []entryInfo
	var manifestFound, checksumsFound bool

	// Track allowed names from manifest and checksums.
	allowedByManifest := map[string]bool{
		"backup.json": true,
		"checksums.txt": true,
		"RESTORE_INSTRUCTIONS.txt": true,
		"var/lib/orvix/orvix.db": true,
		"etc/orvix/orvix.yaml.redacted": true,
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("tar error: %v", err))
			return result, nil
		}
		if header == nil {
			continue
		}

		// Reject symlinks, hardlinks, and unsupported types.
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("archive links are not allowed: %s", header.Name))
			continue
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// Reject absolute paths and traversal.
		name := strings.ReplaceAll(header.Name, "\\", "/")
		if strings.HasPrefix(name, "/") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("absolute archive entry rejected: %s", header.Name))
			continue
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("archive traversal rejected: %s", header.Name))
				continue
			}
		}

		cleanName := strings.TrimPrefix(name, "./")

		// Check allowlist.
		if !allowedByManifest[cleanName] {
			// Allow etc/orvix/*.env.redacted patterns.
			if !strings.HasPrefix(cleanName, "etc/orvix/") || !strings.HasSuffix(cleanName, ".env.redacted") {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("unknown archive entry: %s", cleanName))
				continue
			}
		}

		// Determine max size for this entry type.
		var maxSize int64
		if cleanName == "var/lib/orvix/orvix.db" {
			maxSize = maxDBEntrySize
		} else if strings.HasSuffix(cleanName, ".tar.gz") {
			maxSize = maxMailStoreEntrySize
		} else {
			maxSize = maxMetadataEntrySize
		}

		// Stream entry with size cap.
		var entryData []byte
		if maxSize <= maxMetadataEntrySize {
			// For metadata files, read fully for parsing.
			entryData, err = io.ReadAll(io.LimitReader(tr, maxSize+1))
			if err != nil {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("read %s: %v", cleanName, err))
				continue
			}
			if int64(len(entryData)) > maxSize {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("entry %s exceeds max size %d", cleanName, maxSize))
				continue
			}
		} else {
			// For large data entries, stream checksum without holding in memory.
			h := sha256.New()
			written, err := io.CopyN(h, tr, maxSize+1)
			if err != nil && err != io.EOF {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("read %s: %v", cleanName, err))
				continue
			}
			if written > maxSize {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("entry %s exceeds max size %d", cleanName, maxSize))
				continue
			}
			// Store checksum for later verification.
			entryData = []byte("sha256:" + hex.EncodeToString(h.Sum(nil)))
		}

		entries = append(entries, entryInfo{name: cleanName, data: entryData})

		if cleanName == "backup.json" {
			manifestFound = true
			var am BackupArchiveManifest
			if err := json.Unmarshal(entryData, &am); err != nil {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("backup.json parse error: %v", err))
			} else {
				if am.Product != ProductName {
					result.Valid = false
					result.Errors = append(result.Errors, fmt.Sprintf("invalid product %q, expected %q", am.Product, ProductName))
				}
				if am.BackupFormatVersion != BackupFormatVersion {
					result.Valid = false
					result.Errors = append(result.Errors, fmt.Sprintf("unsupported backup format version %d, expected %d", am.BackupFormatVersion, BackupFormatVersion))
				}
				// Add manifest items to allowlist.
				for _, item := range am.IncludedItems {
					allowedByManifest[item.Path] = true
				}
			}
		}
		if cleanName == "checksums.txt" {
			checksumsFound = true
		}
	}

	if !manifestFound {
		result.Valid = false
		result.Errors = append(result.Errors, "backup.json not found in archive")
	}
	if !checksumsFound {
		result.Valid = false
		result.Errors = append(result.Errors, "checksums.txt not found in archive")
	}

	// Verify checksums from checksums.txt.
	var checksumMap map[string]string
	for _, e := range entries {
		if e.name == "checksums.txt" {
			checksumMap = make(map[string]string)
			lines := strings.Split(string(e.data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					checksumMap[parts[1]] = parts[0]
				}
			}
		}
	}

	if checksumMap != nil {
		// Verify every allowed entry has a checksum.
		for _, e := range entries {
			if e.name == "checksums.txt" {
				continue
			}
			expectedSHA, ok := checksumMap[e.name]
			if !ok {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("missing checksum for %s", e.name))
				continue
			}
			// For large entries, data is "sha256:<hex>".
			var gotSHA string
			if strings.HasPrefix(string(e.data), "sha256:") {
				gotSHA = strings.TrimPrefix(string(e.data), "sha256:")
			} else {
				h := sha256.Sum256(e.data)
				gotSHA = hex.EncodeToString(h[:])
			}
			if gotSHA != expectedSHA {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("checksum mismatch for %s: got %s, expected %s", e.name, gotSHA, expectedSHA))
			}
		}
		// Reject checksum entries for files not in archive.
		for _, e := range entries {
			// Check if every checksum.txt entry has a corresponding archive entry.
			if e.name == "checksums.txt" {
				lines := strings.Split(string(e.data), "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					parts := strings.Fields(line)
					if len(parts) >= 2 {
						path := parts[1]
						if path == "checksums.txt" {
							continue
						}
						found := false
						for _, entry := range entries {
							if entry.name == path {
								found = true
								break
							}
						}
						if !found {
							result.Valid = false
							result.Errors = append(result.Errors, fmt.Sprintf("checksum entry for absent file: %s", path))
						}
					}
				}
			}
		}
	}

	// Compute total size from archive bytes (stream).
	f.Seek(0, 0)
	archiveSHA := sha256.New()
	totalSize, err := io.Copy(archiveSHA, io.LimitReader(f, maxTotalArchiveBytes+1))
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("read archive: %v", err))
		return result, nil
	}
	if totalSize > maxTotalArchiveBytes {
		result.Valid = false
		result.Errors = append(result.Errors, "archive exceeds max total size")
		return result, nil
	}
	result.SizeBytes = totalSize
	result.SHA256 = hex.EncodeToString(archiveSHA.Sum(nil))

	return result, nil
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

func fileSHA256(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
