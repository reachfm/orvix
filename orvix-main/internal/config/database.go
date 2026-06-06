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
		// RC2 FIX: Use modernc.org/sqlite via gorm.io/driver/sqlite
		// The replace directive swaps mattn/go-sqlite3 with modernc.org/sqlite
		// Both register as "sqlite" driver, so no code changes needed
		dsn := cfg.DSN
		if dsn == "" {
			dsn = "/var/lib/orvix/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
		}
		dial = sqlite.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	gormLevel := gormlogger.Warn
	if cfg.Driver == "sqlite" {
		// Reduce log noise for SQLite
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

	// Configure connection pool based on driver
	if cfg.Driver == "sqlite" {
		// SQLite doesn't support concurrent writes well
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		sqlDB.SetMaxOpenConns(cfg.MaxOpen)
		sqlDB.SetMaxIdleConns(cfg.MaxIdle)
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.MaxLifetime) * time.Second)
	}

	logger.Info("database connection established",
		zap.String("driver", cfg.Driver),
		zap.Int("max_open", cfg.MaxOpen),
		zap.Int("max_idle", cfg.MaxIdle),
	)

	return db, nil
}