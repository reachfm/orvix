package backup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// BackupHistory stores metadata about completed backups.
type BackupHistory struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Path      string    `gorm:"not null" json:"path"`
	SizeBytes int64     `gorm:"not null" json:"size_bytes"`
	Type      string    `gorm:"not null" json:"type"`
	Status    string    `gorm:"not null;default:'completed'" json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Manager handles database and config backups.
type Manager struct {
	db        *gorm.DB
	logger    *zap.Logger
	backupDir string
}

// NewManager creates a new backup manager.
func NewManager(db *gorm.DB, logger *zap.Logger, backupDir string) *Manager {
	os.MkdirAll(backupDir, 0700)
	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL
	return &Manager{
		db:        db,
		logger:    logger,
		backupDir: backupDir,
	}
}

// BackupDatabase creates a database backup.
func (m *Manager) BackupDatabase() (*BackupHistory, error) {
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("orvix_db_%s.sql", timestamp)
	path := filepath.Join(m.backupDir, filename)

	sqlDB, err := m.db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get db connection: %w", err)
	}

	if err := sqlDB.Close(); err != nil {
		return nil, fmt.Errorf("failed to close db: %w", err)
	}

	cmd := exec.Command("sqlite3", m.db.Dialector.Name(), ".backup", path)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	info, _ := os.Stat(path)
	record := &BackupHistory{
		Path:      path,
		SizeBytes: info.Size(),
		Type:      "database",
		Status:    "completed",
	}
	m.db.Create(record)

	m.logger.Info("database backup created", zap.String("path", path), zap.Int64("size", info.Size()))
	return record, nil
}

// BackupConfig creates a backup of configuration files.
func (m *Manager) BackupConfig(configPaths []string) (*BackupHistory, error) {
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("orvix_config_%s.tar.gz", timestamp)
	path := filepath.Join(m.backupDir, filename)

	args := append([]string{"-czf", path}, configPaths...)
	cmd := exec.Command("tar", args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("config backup failed: %w", err)
	}

	info, _ := os.Stat(path)
	record := &BackupHistory{
		Path:      path,
		SizeBytes: info.Size(),
		Type:      "config",
		Status:    "completed",
	}
	m.db.Create(record)

	m.logger.Info("config backup created", zap.String("path", path))
	return record, nil
}

// ListBackups returns backup history.
func (m *Manager) ListBackups(limit int) ([]BackupHistory, error) {
	var backups []BackupHistory
	m.db.Order("created_at desc").Limit(limit).Find(&backups)
	return backups, nil
}
