package updater

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ChangelogManager manages module changelogs.
type ChangelogManager struct {
	db *gorm.DB
}

// ChangelogEntry represents a single changelog entry.
type ChangelogEntry struct {
	ID         uint      `gorm:"primaryKey"`
	ModuleID   string    `gorm:"index;not null"`
	Version    string    `gorm:"not null"`
	Changes    string    `gorm:"type:text;not null"`
	ReleasedAt time.Time `gorm:"not null"`
}

// NewChangelogManager creates a new changelog manager.
func NewChangelogManager(db *gorm.DB) *ChangelogManager {
	return &ChangelogManager{db: db}
}

// GetChangelog returns the changelog for a specific module.
func (cm *ChangelogManager) GetChangelog(moduleID string) ([]ChangelogEntry, error) {
	var entries []ChangelogEntry
	if err := cm.db.Where("module_id = ?", moduleID).Order("released_at desc").Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("failed to get changelog: %w", err)
	}
	return entries, nil
}

// AddEntry adds a new changelog entry.
func (cm *ChangelogManager) AddEntry(moduleID, version, changes string) error {
	entry := ChangelogEntry{
		ModuleID:   moduleID,
		Version:    version,
		Changes:    changes,
		ReleasedAt: time.Now(),
	}
	return cm.db.Create(&entry).Error
}
