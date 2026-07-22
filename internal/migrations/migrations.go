package migrations

import (
	"fmt"
	"time"

	"github.com/orvixemail/orvix/internal/models"
	"gorm.io/gorm"
)

type Migration struct {
	Name string
	Run  func(db *gorm.DB) error
}

func Run(db *gorm.DB, driver string) error {
	if err := db.AutoMigrate(&MigrationRecord{}); err != nil {
		return fmt.Errorf("failed to create migrations tracking table: %w", err)
	}

	migrations := getMigrations()

	for _, m := range migrations {
		var count int64
		db.Model(&MigrationRecord{}).Where("name = ?", m.Name).Count(&count)
		if count > 0 {
			continue
		}

		if err := m.Run(db); err != nil {
			return fmt.Errorf("migration %s failed: %w", m.Name, err)
		}

		record := MigrationRecord{
			Name:      m.Name,
			Batch:     1,
			AppliedAt: Now(),
		}
		if err := db.Create(&record).Error; err != nil {
			return fmt.Errorf("failed to record migration %s: %w", m.Name, err)
		}

		fmt.Printf("Migration applied: %s\n", m.Name)
	}

	if err := models.AutoMigrate(db); err != nil {
		return fmt.Errorf("auto-migrate failed: %w", err)
	}

	return nil
}

func getMigrations() []Migration {
	return []Migration{
		{Name: "00001_create_initial_schema", Run: runInitialSchema},
	}
}

func runInitialSchema(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(&models.License{}); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.Tenant{}); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.Domain{}); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.User{}); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.UserSettings{}); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.FeatureFlag{}); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.AuditLog{}); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.APIKey{}); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.Session{}); err != nil {
			return err
		}
		return nil
	})
}

type MigrationRecord struct {
	ID        uint   `gorm:"primaryKey"`
	Name      string `gorm:"uniqueIndex;size:255;not null"`
	Batch     int    `gorm:"not null"`
	AppliedAt int64  `gorm:"not null"`
}

func (MigrationRecord) TableName() string {
	return "_migrations"
}

var Now = func() int64 {
	return time.Now().Unix()
}
