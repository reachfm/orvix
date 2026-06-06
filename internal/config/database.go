package config

import (
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// NewDatabase creates a GORM database connection from config.
func NewDatabase(cfg *DatabaseConfig, logger *zap.Logger) (*gorm.DB, error) {
	var dial gorm.Dialector

	switch cfg.Driver {
	case "postgres":
		dial = postgres.Open(cfg.DSN)
	case "sqlite":
		dial = sqlite.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	gormLevel := gormlogger.Warn
	if cfg.Driver == "sqlite" {
		gormLevel = gormlogger.Error
	}

	db, err := gorm.Open(dial, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpen)
	sqlDB.SetMaxIdleConns(cfg.MaxIdle)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.MaxLifetime) * time.Second)

	logger.Info("database connection established",
		zap.String("driver", cfg.Driver),
		zap.Int("max_open", cfg.MaxOpen),
		zap.Int("max_idle", cfg.MaxIdle),
	)

	return db, nil
}
