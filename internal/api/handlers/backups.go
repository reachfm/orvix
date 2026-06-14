package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/backup"
	"go.uber.org/zap"
)

const defaultBackupDir = "/var/lib/orvix/backups"

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
	info, err := os.Stat(archivePath)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "archive unavailable"})
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "archive unavailable"})
	}
	filename := "orvix-backup-" + id + ".tar.gz"
	c.Set("Content-Type", "application/gzip")
	c.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	h.writeAuditLog(c, "backup.download", fmt.Sprintf("backup_id:%s", id))
	return c.Send(data)
}

// DeleteBackup deletes a backup through the hardened backup service.
func (h *Handler) DeleteBackup(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
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
