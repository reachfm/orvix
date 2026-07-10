package models

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// postgresDMLTestDSN returns the DSN from the environment gate or skips the test.
func postgresDMLTestDSN(t *testing.T) string {
	t.Helper()
	if strings.TrimSpace(os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST")) != "1" {
		t.Skip("set ORVIX_RUN_POSTGRES_DML_TEST=1 to run postgres DML tests")
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}
	return dsn
}

// openPostgresTestDB connects to PostgreSQL and creates an isolated schema.
func openPostgresTestDB(t *testing.T) (*testPGEnv, error) {
	t.Helper()
	dsn := postgresDMLTestDSN(t)
	cfg := config.Defaults()
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = dsn
	cfg.Database.MaxOpen = 5
	cfg.Database.MaxIdle = 2
	cfg.Database.MaxLifetime = 300

	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}

	schema := fmt.Sprintf("orvix_pg_dml_test_%d", time.Now().UnixNano())
	if _, err := sqlDB.Exec("CREATE SCHEMA IF NOT EXISTS " + schema); err != nil {
		return nil, fmt.Errorf("create schema %s: %w", schema, err)
	}
	if _, err := sqlDB.Exec("SET search_path TO " + schema); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}
	t.Cleanup(func() {
		sqlDB.Exec("SET search_path TO public")
		sqlDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		sqlDB.Close()
	})

	return &testPGEnv{db: db, sqlDB: sqlDB, schema: schema}, nil
}

type testPGEnv struct {
	db     *gorm.DB
	sqlDB  *sql.DB
	schema string
}

// TestPlaceholderHelperProducesDollarNOnPostgres verifies that the dialect
// helper returns $N placeholders on a real PostgreSQL connection.
func TestPlaceholderHelperProducesDollarNOnPostgres(t *testing.T) {
	env, err := openPostgresTestDB(t)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}

	dialect, err := dbdialect.Detect(env.sqlDB)
	if err != nil {
		t.Fatalf("detect dialect: %v", err)
	}
	if dialect.Dialect != dbdialect.Postgres {
		t.Fatalf("expected postgres dialect, got %v", dialect.Dialect)
	}
	if got := dialect.Placeholder(1); got != "$1" {
		t.Fatalf("expected $1, got %s", got)
	}
	if got := dialect.Placeholder(3); got != "$3" {
		t.Fatalf("expected $3, got %s", got)
	}
}

// TestNowExpressionWorksOnPostgres proves that NOW() evaluates on PostgreSQL
// and returns a recent timestamp.
func TestNowExpressionWorksOnPostgres(t *testing.T) {
	env, err := openPostgresTestDB(t)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}

	dialect, _ := dbdialect.Detect(env.sqlDB)
	var ts time.Time
	if err := env.sqlDB.QueryRow("SELECT " + dialect.NowExpr()).Scan(&ts); err != nil {
		t.Fatalf("evaluate NOW(): %v", err)
	}
	if ts.IsZero() || time.Since(ts) > time.Minute {
		t.Fatalf("NOW() returned unexpected time: %v", ts)
	}
}

// TestPostgresDMLOverSchemaCompat verifies that MigrateAllPostgres plus a
// representative insert work inside an isolated schema used by the DML tests.
func TestPostgresDMLOverSchemaCompat(t *testing.T) {
	env, err := openPostgresTestDB(t)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}

	if err := MigrateAllPostgres(env.db); err != nil {
		t.Fatalf("MigrateAllPostgres: %v", err)
	}
	if err := PostgresSchemaCompatible(env.db, env.schema); err != nil {
		t.Fatalf("PostgresSchemaCompatible: %v", err)
	}

	ctx := context.Background()
	_, err = env.sqlDB.ExecContext(ctx,
		"INSERT INTO tenants (name, slug, domain) VALUES ($1, $2, $3)",
		"DML Tenant", "dml-tenant", "dml.example.com",
	)
	if err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	var slug string
	if err := env.sqlDB.QueryRowContext(ctx,
		"SELECT slug FROM tenants WHERE name = $1", "DML Tenant").Scan(&slug); err != nil {
		t.Fatalf("select tenant: %v", err)
	}
	if slug != "dml-tenant" {
		t.Fatalf("unexpected slug: %s", slug)
	}
}

// TestSQLiteEquivalentStillPasses ensures the same conceptual operations work
// on SQLite so we have not regressed the default path.
func TestSQLiteEquivalentStillPasses(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = "file:" + t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	dialect := dbdialect.FromDriver("sqlite")
	if dialect.Placeholder(1) != "?" {
		t.Fatalf("expected ? for sqlite, got %s", dialect.Placeholder(1))
	}

	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := sqlDB.ExecContext(ctx,
		"INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active) VALUES (?, ?, ?, ?, ?, ?, ?)",
		now, now, "SQLite Tenant", "sqlite-tenant", "sqlite.example.com", "smb", 1); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	var slug string
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT slug FROM tenants WHERE name = ?", "SQLite Tenant").Scan(&slug); err != nil {
		t.Fatalf("select tenant: %v", err)
	}
	if slug != "sqlite-tenant" {
		t.Fatalf("unexpected slug: %s", slug)
	}
}
