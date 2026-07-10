package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

func TestMigrateDryRunSQLite(t *testing.T) {
	// Create a small SQLite source with a tenant row.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = srcPath + "?_loc=auto&_busy_timeout=5000"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate source: %v", err)
	}
	sqlDB, _ := db.DB()
	if _, err := sqlDB.Exec("INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active) VALUES (datetime('now'), datetime('now'), 'Test', 'test', 'test.example.com', 'smb', 1)"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	sqlDB.Close()

	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", "",
		"--dry-run", "true",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2 for empty DSN, got %d", code)
	}
}

func TestMigrateEmptyDSNRejected(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--dry-run", "true",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing DSN, got %d", code)
	}
}

func TestMigrateDryRunListsRows(t *testing.T) {
	if os.Getenv("ORVIX_RUN_POSTGRES_MIGRATE_TEST") != "1" {
		t.Skip("set ORVIX_RUN_POSTGRES_MIGRATE_TEST=1 to run postgres migration tests")
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = srcPath + "?_loc=auto&_busy_timeout=5000"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate source: %v", err)
	}
	sqlDB, _ := db.DB()
	if _, err := sqlDB.Exec("INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active) VALUES (datetime('now'), datetime('now'), 'Test', 'test', 'test.example.com', 'smb', 1)"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	sqlDB.Close()

	schema := fmt.Sprintf("orvix_migrate_test_%d", time.Now().UnixNano())
	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", dsn,
		"--target-schema", schema,
		"--dry-run", "true",
	})
	if code != 0 {
		t.Fatalf("expected dry-run exit 0, got %d", code)
	}

	// Cleanup schema.
	cleanupCfg := config.Defaults()
	cleanupCfg.Database.Driver = "postgres"
	cleanupCfg.Database.DSN = dsn
	gormDB, err := config.NewDatabase(&cleanupCfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("connect for cleanup: %v", err)
	}
	cleanupDB, _ := gormDB.DB()
	cleanupDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
	cleanupDB.Close()
}
