package trust

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	_ "modernc.org/sqlite"
)

func testRepoSQLite(t *testing.T) (*sql.DB, *Repository) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "trust.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db, NewRepository(db)
}

func testRepoPostgres(t *testing.T) (*sql.DB, *Repository) {
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
	cfg := config.Defaults()
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = dsn
	cfg.Database.MaxOpen = 5
	cfg.Database.MaxIdle = 2
	gormDB, err := config.NewDatabase(&cfg.Database, nil)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	db, err := gormDB.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := fmt.Sprintf("orvix_pg_dml_test_%d", time.Now().UnixNano())
	if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + schema); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	if _, err := db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("SET search_path TO public")
		db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
	})

	for _, stmt := range Tables() {
		// Convert SQLite DDL to PostgreSQL for the isolated test schema.
		pgStmt := sqliteToPostgresDDL(stmt)
		if _, err := db.Exec(pgStmt); err != nil {
			t.Fatalf("create table in postgres: %v\nDDL: %s", err, pgStmt)
		}
	}
	return db, NewRepository(db)
}

func sqliteToPostgresDDL(stmt string) string {
	// Minimal transformations for trust schema only.
	stmt = strings.ReplaceAll(stmt, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
	stmt = strings.ReplaceAll(stmt, "DATETIME", "TIMESTAMP")
	stmt = strings.ReplaceAll(stmt, "TEXT PRIMARY KEY", "TEXT PRIMARY KEY")
	return stmt
}

func TestRepositoryDialectDetection(t *testing.T) {
	db, repo := testRepoSQLite(t)
	_ = db
	if repo.dialect.Dialect != dbdialect.SQLite {
		t.Fatalf("expected sqlite dialect, got %v", repo.dialect.Dialect)
	}
}

func TestSaveAndLoadLockoutSQLite(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepoSQLite(t)

	expires := time.Now().UTC().Add(time.Hour)
	if err := repo.SaveLockout(ctx, "alice@example.com", expires); err != nil {
		t.Fatalf("save lockout: %v", err)
	}

	loaded, err := repo.LoadLockouts(ctx)
	if err != nil {
		t.Fatalf("load lockouts: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 lockout, got %d", len(loaded))
	}
	if got, ok := loaded["alice@example.com"]; !ok || got.Round(time.Second) != expires.Round(time.Second) {
		t.Fatalf("lockout mismatch: got %v want %v", got, expires)
	}

	n, err := repo.DeleteExpiredLockouts(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 expired, got %d", n)
	}
}

func TestSaveAndLoadTrustScoreSQLite(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepoSQLite(t)

	if err := repo.SaveTrustScore(ctx, "user", "bob@example.com", "known good", TrustHigh); err != nil {
		t.Fatalf("save trust score: %v", err)
	}

	scores, err := repo.LoadTrustScores(ctx)
	if err != nil {
		t.Fatalf("load trust scores: %v", err)
	}
	if got := scores.Users["bob@example.com"]; got == nil || got.Score != TrustHigh {
		t.Fatalf("trust score mismatch: %#v", got)
	}

	// Upsert should update the score.
	if err := repo.SaveTrustScore(ctx, "user", "bob@example.com", "still good", TrustMedium); err != nil {
		t.Fatalf("upsert trust score: %v", err)
	}
	scores, err = repo.LoadTrustScores(ctx)
	if err != nil {
		t.Fatalf("load after upsert: %v", err)
	}
	if got := scores.Users["bob@example.com"]; got == nil || got.Score != TrustMedium || got.Reason != "still good" {
		t.Fatalf("upsert mismatch: %#v", got)
	}
}

func TestSaveAndLoadLockoutPostgres(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepoPostgres(t)

	expires := time.Now().UTC().Add(time.Hour)
	if err := repo.SaveLockout(ctx, "alice@example.com", expires); err != nil {
		t.Fatalf("save lockout on postgres: %v", err)
	}

	loaded, err := repo.LoadLockouts(ctx)
	if err != nil {
		t.Fatalf("load lockouts on postgres: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 lockout on postgres, got %d", len(loaded))
	}
	if got, ok := loaded["alice@example.com"]; !ok || got.Round(time.Second) != expires.Round(time.Second) {
		t.Fatalf("lockout mismatch on postgres: got %v want %v", got, expires)
	}
}

func TestSaveAndLoadTrustScorePostgres(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepoPostgres(t)

	if err := repo.SaveTrustScore(ctx, "user", "bob@example.com", "known good", TrustHigh); err != nil {
		t.Fatalf("save trust score on postgres: %v", err)
	}

	scores, err := repo.LoadTrustScores(ctx)
	if err != nil {
		t.Fatalf("load trust scores on postgres: %v", err)
	}
	if got := scores.Users["bob@example.com"]; got == nil || got.Score != TrustHigh {
		t.Fatalf("trust score mismatch on postgres: %#v", got)
	}

	if err := repo.SaveTrustScore(ctx, "user", "bob@example.com", "still good", TrustMedium); err != nil {
		t.Fatalf("upsert trust score on postgres: %v", err)
	}
	scores, err = repo.LoadTrustScores(ctx)
	if err != nil {
		t.Fatalf("load after upsert on postgres: %v", err)
	}
	if got := scores.Users["bob@example.com"]; got == nil || got.Score != TrustMedium || got.Reason != "still good" {
		t.Fatalf("upsert mismatch on postgres: %#v", got)
	}
}

func TestDeleteExpiredLockouts(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepoSQLite(t)

	if err := repo.SaveLockout(ctx, "expired@example.com", time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatalf("save expired lockout: %v", err)
	}
	if err := repo.SaveLockout(ctx, "active@example.com", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("save active lockout: %v", err)
	}

	n, err := repo.DeleteExpiredLockouts(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 expired deleted, got %d", n)
	}

	loaded, err := repo.LoadLockouts(ctx)
	if err != nil {
		t.Fatalf("load lockouts: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 remaining lockout, got %d", len(loaded))
	}
}
