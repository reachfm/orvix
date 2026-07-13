package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/backup"
	"github.com/orvix/orvix/internal/backup/targets"
	"github.com/orvix/orvix/internal/updater"
	"go.uber.org/zap"
)

const defaultBackupDir = "/var/backups/orvix/"

type backupAPIEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256,omitempty"`
	CreatedAt   string `json:"created_at"`
	CompletedAt string `json:"completed_at,omitempty"`
}

func (h *Handler) backupDir() string {
	if h.cfg != nil && h.cfg.Backup.Dir != "" {
		return h.cfg.Backup.Dir
	}
	return defaultBackupDir
}

func (h *Handler) backupService() (*backup.Service, error) {
	sqlDB, err := h.db.DB()
	if err != nil {
		return nil, err
	}
	if err := ensureBackupRegistry(sqlDB); err != nil {
		return nil, err
	}

	mailDir := ""
	attachDir := ""
	if h.cfg != nil {
		mailDir = h.cfg.CoreMail.MailStorePath
		if mailDir == "" && h.cfg.CoreMail.DataPath != "" {
			mailDir = filepath.Join(h.cfg.CoreMail.DataPath, "mailstore")
		}
		if h.cfg.CoreMail.DataPath != "" {
			attachDir = filepath.Join(h.cfg.CoreMail.DataPath, "attachments")
		}
	}
	svc := backup.NewService(h.backupDir(), sqlDB, sqlDB, mailDir, attachDir)
	svc.SetConfigPath("/etc/orvix/orvix.yaml")
	if h.cfg != nil {
		svc.SetDatabasePath(h.cfg.Database.SQLitePath)
		maintenanceFile := strings.TrimSpace(os.Getenv("ORVIX_RESTORE_MAINTENANCE_FILE"))
		if maintenanceFile == "" {
			maintenanceFile = filepath.Join(h.cfg.CoreMail.DataPath, "restore-maintenance.enabled")
			if h.cfg.CoreMail.DataPath == "" {
				maintenanceFile = "/var/lib/orvix/restore-maintenance.enabled"
			}
		}
		svc.SetRestoreMaintenanceChecker(func(context.Context) error {
			st, err := os.Stat(maintenanceFile)
			if err != nil {
				return fmt.Errorf("create %s to enable offline restore maintenance: %w", maintenanceFile, err)
			}
			if st.IsDir() {
				return fmt.Errorf("restore maintenance marker is a directory: %s", maintenanceFile)
			}
			return nil
		})
		// Only meaningful when the configured dialect is
		// PostgreSQL (see Service.snapshotDB); harmless to set
		// unconditionally otherwise, since it is never read.
		svc.SetPostgresDSN(h.cfg.Database.DSN)
		if h.cfg.Backup.EncryptionEnabled {
			if err := svc.SetEncryptionConfig(backup.BackupEncryptionConfig{
				Enabled: true,
				KeyFile: h.cfg.Backup.EncryptionKeyFile,
			}); err != nil {
				return nil, fmt.Errorf("backup encryption unavailable: %w", err)
			}
		} else if h.cfg.Database.IsProduction() {
			return nil, fmt.Errorf("backup encryption must be enabled in production")
		}
	}
	bi := updater.ReadBuildInfo()
	svc.SetBuildInfo(bi.Version, bi.SHA)

	// Wire the post-create hook so enabled backup
	// targets receive the finished archive. The hook
	// is a closure that holds the DB handle so an
	// admin writing new targets does not need a
	// router restart to see them picked up.
	mgr := targets.NewManager(h.cfg, sqlDB, h.logger, h.observability)
	svc.SetPostCreateHook(func(backupID, archivePath string) {
		// Use a fresh context per upload so the request
		// that created the backup can return without
		// waiting for slow remote targets.
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		mgr.Run(bgCtx, archivePath, backupID)
	})

	// Add key file paths from config to be included in backups.
	if h.cfg != nil {
		if h.cfg.Auth.JWTKeyPath != "" {
			svc.AddKeyPath(h.cfg.Auth.JWTKeyPath)
		}
		if h.cfg.CoreMail.VAPIDPrivateKeyFile != "" {
			svc.AddKeyPath(h.cfg.CoreMail.VAPIDPrivateKeyFile)
		}
	}
	return svc, nil
}

func ensureBackupRegistry(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS backup_registry (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		size_bytes INTEGER NOT NULL DEFAULT 0,
		sha256 TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		completed_at DATETIME
	)`)
	return err
}

func backupToAPIEntry(b backup.Backup) backupAPIEntry {
	entry := backupAPIEntry{
		ID:        b.ID,
		Name:      b.Name,
		Status:    string(b.Status),
		SizeBytes: b.SizeBytes,
		SHA256:    b.SHA256,
		CreatedAt: b.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if b.CompletedAt != nil {
		entry.CompletedAt = b.CompletedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return entry
}

func invalidBackupID(id string) bool {
	return id == "" ||
		strings.Contains(id, "..") ||
		strings.ContainsAny(id, `/\`) ||
		strings.ContainsRune(id, 0)
}

// ListBackups returns safe backup metadata only.
func (h *Handler) ListBackups(c fiber.Ctx) error {
	svc, err := h.backupService()
	if err != nil {
		h.logger.Error("backup service unavailable", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	backups, err := svc.ListBackups(c.Context())
	if err != nil {
		h.logger.Error("backup list failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup list failed"})
	}
	out := make([]backupAPIEntry, 0, len(backups))
	for _, b := range backups {
		out = append(out, backupToAPIEntry(b))
	}
	return c.JSON(out)
}

// CreateBackup creates a backup through the hardened backup service.
func (h *Handler) CreateBackup(c fiber.Ctx) error {
	svc, err := h.backupService()
	if err != nil {
		h.logger.Error("backup service unavailable", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	b, err := svc.CreateBackup(c.Context(), "")
	if err != nil {
		h.logger.Error("backup create failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup create failed"})
	}
	h.writeAuditLog(c, "backup.create", fmt.Sprintf("backup_id:%s", b.ID))
	return c.Status(fiber.StatusCreated).JSON(backupToAPIEntry(*b))
}

// DownloadBackup generates the safe allowlisted archive and sends it to the browser.
func (h *Handler) DownloadBackup(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
	}
	svc, err := h.backupService()
	if err != nil {
		h.logger.Error("backup service unavailable", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	archivePath, err := svc.CreateArchive(c.Context(), id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") || errors.Is(err, os.ErrNotExist) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "backup not found"})
		}
		h.logger.Error("backup archive failed", zap.String("backup_id", id), zap.Error(err))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "backup archive failed"})
	}
	filename := "orvix-backup-" + id + ".tar.gz"
	contentType := "application/gzip"
	if strings.HasSuffix(archivePath, ".enc") {
		filename += ".enc"
		contentType = "application/octet-stream"
	}
	c.Set("Content-Type", contentType)
	c.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	h.writeAuditLog(c, "backup.download", fmt.Sprintf("backup_id:%s", id))
	file, err := os.Open(archivePath)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "archive unavailable"})
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "archive unavailable"})
	}
	c.Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	return c.SendStream(file, int(stat.Size()))
}

// backupDeleteConfirm is the typed-confirmation string required to
// authorize a destructive backup delete. It is intentionally non-trivial
// and must match exactly. Used by both the JSON body and the
// X-Orvix-Confirm header fallback.
const backupDeleteConfirm = "delete-orvix-backup"

// DeleteBackup deletes a backup through the hardened backup service.
// Requires typed confirmation "delete-orvix-backup". The confirmation may
// be supplied either via JSON body field {"confirm":"delete-orvix-backup"}
// or via the X-Orvix-Confirm request header. Both paths require an
// authenticated admin session and a valid CSRF token, which are enforced
// by the router middleware (admin group + CSRF). Deleting without typed
// confirmation is rejected.
func (h *Handler) DeleteBackup(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
	}
	// Typed confirmation: accept JSON body OR X-Orvix-Confirm header.
	// Header fallback exists because some HTTP intermediaries strip bodies
	// from DELETE requests; CSRF + admin role still gate this path.
	confirm := strings.TrimSpace(c.Get("X-Orvix-Confirm"))
	bodyParsed := false
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := c.Bind().JSON(&req); err == nil {
		bodyParsed = true
		if v := strings.TrimSpace(req.Confirm); v != "" {
			confirm = v
		}
	}
	if confirm != backupDeleteConfirm {
		// Differentiate error messages to aid debugging without weakening
		// the typed-confirmation requirement.
		if bodyParsed {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "delete requires typed confirmation: " + backupDeleteConfirm})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "delete requires JSON body with confirm field or X-Orvix-Confirm header"})
	}
	svc, err := h.backupService()
	if err != nil {
		h.logger.Error("backup service unavailable", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	if _, err := svc.GetBackup(c.Context(), id); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "backup not found"})
	}
	if err := svc.DeleteBackup(c.Context(), id); err != nil {
		h.logger.Error("backup delete failed", zap.String("backup_id", id), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup delete failed"})
	}
	h.writeAuditLog(c, "backup.delete", fmt.Sprintf("backup_id:%s", id))
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) ensureBackupScheduleTable(sqlDB *sql.DB) error {
	_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS backup_schedule_config (
		id INTEGER PRIMARY KEY DEFAULT 1,
		enabled INTEGER NOT NULL DEFAULT 0,
		frequency TEXT NOT NULL DEFAULT 'manual',
		retention_count INTEGER NOT NULL DEFAULT 10,
		last_run_at DATETIME,
		next_run_at DATETIME,
		updated_at DATETIME NOT NULL
	)`)
	return err
}

func (h *Handler) backupScheduleService() (*backup.Service, error) {
	return h.backupService()
}

// GetBackupSchedule returns the current backup schedule configuration.
func (h *Handler) GetBackupSchedule(c fiber.Ctx) error {
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	cfg, err := svc.GetScheduleConfig(c.Context())
	if err != nil {
		h.logger.Error("backup schedule get failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get backup schedule"})
	}
	return c.JSON(cfg)
}

// SetBackupSchedule updates the backup schedule configuration.
func (h *Handler) SetBackupSchedule(c fiber.Ctx) error {
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	var req backup.ScheduleConfig
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	cfg, err := svc.SetScheduleConfig(c.Context(), &req)
	if err != nil {
		h.logger.Error("backup schedule update failed", zap.Error(err))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "backup.schedule_updated", fmt.Sprintf("enabled:%v|frequency:%s|retention:%d", cfg.Enabled, cfg.Frequency, cfg.RetentionCount))
	return c.JSON(cfg)
}

// GetBackupMetrics returns aggregate backup metrics.
func (h *Handler) GetBackupMetrics(c fiber.Ctx) error {
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	metrics, err := svc.GetBackupMetrics(c.Context())
	if err != nil {
		h.logger.Error("backup metrics failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get backup metrics"})
	}
	return c.JSON(metrics)
}

// GetBackupHealth returns backup system health status.
func (h *Handler) GetBackupHealth(c fiber.Ctx) error {
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	health, err := svc.GetBackupHealth(c.Context())
	if err != nil {
		h.logger.Error("backup health failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get backup health"})
	}
	return c.JSON(health)
}

// RunBackupRetention triggers a retention cleanup.
func (h *Handler) RunBackupRetention(c fiber.Ctx) error {
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	deleted, err := svc.RunRetention(c.Context())
	if err != nil {
		h.logger.Error("backup retention failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "retention cleanup failed"})
	}
	h.writeAuditLog(c, "backup.retention_cleanup", fmt.Sprintf("deleted:%d", deleted))
	return c.JSON(fiber.Map{"deleted": deleted})
}

// LegacyGone returns 410 for moved backup routes.
func (h *Handler) LegacyGone(c fiber.Ctx) error {
	return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "this endpoint has moved; use /api/v1/admin/backups"})
}

// GetBackup returns details for a single backup (no secrets).
func (h *Handler) GetBackup(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
	}
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	b, err := svc.GetBackup(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "backup not found"})
	}
	return c.JSON(backupToAPIEntry(*b))
}

// PostValidateBackup validates a backup archive (checksums, manifest, format version).
func (h *Handler) PostValidateBackup(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
	}
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	result, err := svc.ValidateArchive(c.Context(), id)
	if err != nil {
		h.logger.Error("backup validation failed", zap.String("backup_id", id), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup validation failed"})
	}
	h.writeAuditLog(c, "backup.validate", fmt.Sprintf("backup_id:%s|valid:%v", id, result.Valid))
	return c.JSON(result)
}

// PostRestoreBackup validates, safety-snapshots, and activates a backup.
// It fails closed unless the operator has enabled restore maintenance mode.
func (h *Handler) PostRestoreBackup(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Confirm != "restore-orvix-backup" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "restore requires typed confirmation: restore-orvix-backup"})
	}
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	result, err := svc.RestoreBackup(c.Context(), id)
	if err != nil {
		h.logger.Error("backup restore activation failed", zap.String("backup_id", id), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "backup.restore", fmt.Sprintf("backup_id:%s|status:%s|rolled_back:%v", id, result.Status, result.RolledBack))
	return c.JSON(result)
}

// PostBackupNow creates an immediate backup and returns the backup ID.
// This is the explicit "backup now" endpoint for administrator-triggered backups.
func (h *Handler) PostBackupNow(c fiber.Ctx) error {
	svc, err := h.backupService()
	if err != nil {
		h.logger.Error("backup service unavailable", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = c.Bind().JSON(&req)

	b, err := svc.CreateBackup(c.Context(), req.Name)
	if err != nil {
		h.logger.Error("backup now failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup now failed"})
	}
	h.writeAuditLog(c, "backup.now", fmt.Sprintf("backup_id:%s|name:%s", b.ID, req.Name))
	return c.Status(fiber.StatusCreated).JSON(backupToAPIEntry(*b))
}
