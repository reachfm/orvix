package config

import (
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	// RC2 FIX: Use modernc.org/sqlite for pure Go SQLite (no CGO required)
	// Import for side-effect: registers "sqlite" driver
	_ "modernc.org/sqlite"
)

// RC2 FIX: Direct SQLite connection using modernc.org/sqlite with custom dialector
// Avoids gorm.io/driver/sqlite which hardcodes "sqlite3" driver name

// NewDatabase creates a GORM database connection from config.
func NewDatabase(cfg *DatabaseConfig, logger *zap.Logger) (*gorm.DB, error) {
	switch cfg.Driver {
	case "postgres":
		return newPostgresDB(cfg, logger)
	case "sqlite":
		return newSQLiteDB(cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}
}

// newSQLiteDB creates a SQLite connection using modernc.org/sqlite with custom dialector
func newSQLiteDB(cfg *DatabaseConfig, logger *zap.Logger) (*gorm.DB, error) {
	dsn := cfg.DSN
	if dsn == "" {
		dsn = "/var/lib/orvix/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	}

	// Open database using modernc.org/sqlite (registers as "sqlite")
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	// Test the connection
	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("failed to ping sqlite: %w", err)
	}

	// Configure connection pool for SQLite
	sqldb.SetMaxOpenConns(1)
	sqldb.SetMaxIdleConns(1)

	// Create GORM DB using our custom SQLite dialector
	db, err := gorm.Open(&sqliteDialect{}, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Error),
	})
	if err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("failed to create gorm.DB: %w", err)
	}

	// Use our pre-configured connection pool
	db.ConnPool = sqldb

	if logger != nil {
		logger.Info("database connection established",
			zap.String("driver", "sqlite"),
			zap.Int("max_open", 1),
			zap.Int("max_idle", 1),
		)
	}

	return db, nil
}

// newPostgresDB creates a PostgreSQL connection using gorm.io/driver/postgres
func newPostgresDB(cfg *DatabaseConfig, logger *zap.Logger) (*gorm.DB, error) {
	dial := postgres.Open(cfg.DSN)

	db, err := gorm.Open(dial, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpen)
	sqlDB.SetMaxIdleConns(cfg.MaxIdle)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.MaxLifetime) * time.Second)

	if logger != nil {
		logger.Info("database connection established",
			zap.String("driver", "postgres"),
			zap.Int("max_open", cfg.MaxOpen),
			zap.Int("max_idle", cfg.MaxIdle),
			zap.Int("max_lifetime", cfg.MaxLifetime),
		)
	}
	// Audit log: record driver selection without leaking DSN or credentials.
	// The DSN is never logged at any level; structured fields (driver, pool)
	// are safe to surface in monitoring dashboards.

	return db, nil
}