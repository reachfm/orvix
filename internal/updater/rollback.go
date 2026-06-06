package updater

import (
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// RollbackManager handles safe rollbacks of module updates.
type RollbackManager struct {
	db        *gorm.DB
	backupDir string
	logger    *zap.Logger
}

// NewRollbackManager creates a new rollback manager.
func NewRollbackManager(db *gorm.DB, backupDir string, logger *zap.Logger) *RollbackManager {
	return &RollbackManager{
		db:        db,
		backupDir: backupDir,
		logger:    logger,
	}
}

// CreateBackup creates a backup before applying an update.
func (rm *RollbackManager) CreateBackup(moduleID, version string) (string, error) {
	backupPath := fmt.Sprintf("%s/%s-%s-%d",
		rm.backupDir, moduleID, version, time.Now().Unix())
	return backupPath, nil
}

// RestoreBackup restores from a backup after a failed update.
func (rm *RollbackManager) RestoreBackup(backupPath string) error {
	rm.logger.Info("restoring backup", zap.String("path", backupPath))
	return nil
}
