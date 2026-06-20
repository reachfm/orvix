package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/backup"
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
	bi := updater.ReadBuildInfo()
	svc.SetBuildInfo(bi.Version, bi.SHA)
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
	c.Set("Content-Type", "application/gzip")
	c.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	h.writeAuditLog(c, "backup.download", fmt.Sprintf("backup_id:%s", id))
	file, err := os.Open(archivePath)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "archive unavailable"})
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "archive unavailable"})
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "archive unavailable"})
	}
	c.Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	return c.Send(data)
}

// DeleteBackup deletes a backup through the hardened backup service.
// Requires typed confirmation "delete-orvix-backup".
func (h *Handler) DeleteBackup(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "delete requires JSON body with confirm field"})
	}
	if req.Confirm != "delete-orvix-backup" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "delete requires typed confirmation: delete-orvix-backup"})
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

// PostRestoreBackup stages a backup for restore (Phase 2H: staged only, not live).
// Requires typed confirmation "restore-orvix-backup".
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
		h.logger.Error("backup restore failed", zap.String("backup_id", id), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "backup.restore", fmt.Sprintf("backup_id:%s|status:%s", id, result.Status))
	return c.JSON(result)
}
