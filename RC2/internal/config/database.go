package config

import (
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	// Use modernc.org/sqlite for pure Go SQLite support (no CGO required)
	// This driver is compatible with database/sql and works without CGO
	_ "modernc.org/sqlite"
)

// NewDatabase creates a GORM database connection from config.
func NewDatabase(cfg *DatabaseConfig, logger *zap.Logger) (*gorm.DB, error) {
	var sqlDB *sql.DB
	var err error
	var driverName string

	switch cfg.Driver {
	case "postgres":
		sqlDB, err = sql.Open("pgx", cfg.DSN)
		driverName = "postgres"
		if err != nil {
			return nil, fmt.Errorf("failed to open postgres connection: %w", err)
		}

	case "sqlite":
		// Use modernc.org/sqlite driver (pure Go, no CGO required)
		// The driver name is "sqlite" for modernc.org/sqlite
		path := cfg.SQLitePath
		if path == "" {
			path = "/var/lib/orvix/orvix.db"
		}
		// modernc.org/sqlite uses the format: file:<path>?_loc=auto&_busy_timeout=5000
		dsn := fmt.Sprintf("file:%s?_loc=auto&_busy_timeout=5000&_txlock=immediate", path)
		sqlDB, err = sql.Open("sqlite", dsn)
		driverName = "sqlite"
		if err != nil {
			return nil, fmt.Errorf("failed to open sqlite connection: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported database driver: %s (supported: postgres, sqlite)", cfg.Driver)
	}

	// Test the connection
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure connection pool
	sqlDB.SetMaxOpenConns(cfg.MaxOpen)
	sqlDB.SetMaxIdleConns(cfg.MaxIdle)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.MaxLifetime) * time.Second)

	// Create GORM DB from sql.DB
	gormLevel := gormlogger.Warn
	if cfg.Driver == "sqlite" {
		gormLevel = gormlogger.Error // Reduce noise for SQLite
	}

	db, err := gorm.Open(dialectorFromSQLDB(sqlDB, cfg.Driver), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormLevel),
	})
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to create gorm db: %w", err)
	}

	logger.Info("database connection established",
		zap.String("driver", driverName),
		zap.String("dsn", maskDSN(cfg.DSN)),
		zap.Int("max_open", cfg.MaxOpen),
		zap.Int("max_idle", cfg.MaxIdle),
	)

	return db, nil
}

// dialectorFromSQLDB creates a GORM dialector from an existing sql.DB
type sqlDBDialector struct {
	db    *sql.DB
	driver string
}

func (d *sqlDBDialector) Name() string {
	return d.driver
}

func (d *sqlDBDialector) Initialize(db *gorm.DB) error {
	// For postgres, use the pgx dialector
	if d.driver == "postgres" {
		// Use standard postgres dialector
		return nil
	}
	return nil
}

func (d *sqlDBDialector) Migrator(db *gorm.DB) gorm.Migrator {
	if d.driver == "postgres" {
		return postgres.Migrator(db)
	}
	return postgres.Migrator(db) // Fallback
}

func (d *sqlDBDialector) DataTypeOf(field *gorm.FieldInfo) string {
	if d.driver == "postgres" {
		return postgres.DataTypeOf(field)
	}
	return "TEXT"
}

func (d *sqlDBDialector) DefaultValueOf(field *gorm.FieldInfo) clause.Expression {
	return clause.Expr{SQL: "DEFAULT VALUES"}
}

func (d *sqlDBDialector) BindVarTo(writer clause.Writer, stmt *gorm.Statement, v interface{}) {}

func (d *sqlDBDialector) QuoteTo(writer clause.Writer, str string) {
	if d.driver == "postgres" {
		postgres.QuoteTo(writer, str)
		return
	}
	fmt.Fprintf(writer, "\"%s\"", str)
}

func (d *sqlDBDialector) Explain(sql string, vars ...interface{}) string {
	if d.driver == "postgres" {
		return postgres.Explain(sql, vars...)
	}
	return fmt.Sprintf("SQL: %s, Vars: %v", sql, vars)
}

func dialectorFromSQLDB(sqlDB *sql.DB, driver string) gorm.Dialector {
	return &sqlDBDialector{db: sqlDB, driver: driver}
}

func maskDSN(dsn string) string {
	if len(dsn) > 20 {
		return dsn[:20] + "..."
	}
	return "***"
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Driver     string `mapstructure:"driver"`
	Host       string `mapstructure:"host"`
	Port       int    `mapstructure:"port"`
	User       string `mapstructure:"user"`
	Password   string `mapstructure:"password"`
	DBName     string `mapstructure:"dbname"`
	SSLMode    string `mapstructure:"sslmode"`
	DSN        string `mapstructure:"dsn"`
	SQLitePath string `mapstructure:"sqlite_path"`
	MaxOpen    int    `mapstructure:"max_open"`
	MaxIdle    int    `mapstructure:"max_idle"`
	MaxLifetime int   `mapstructure:"max_lifetime"`
}