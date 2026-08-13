package auth

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Portability proof for the two tables this remediation introduces that are
// created at runtime (not by MigrateAllRaw): csrf_records (H-1) and
// mfa_challenges (H-6). Both must work identically on SQLite and PostgreSQL.
//
// The SQLite side is covered implicitly by every other test in this package;
// this file pins the PostgreSQL side. It is gated on PGHOST +
// ORVIX_RUN_POSTGRES_DML_TEST=1, matching the existing PostgreSQL suites, so a
// developer running plain `go test ./...` on SQLite skips it.

func openPortabilityPG(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("PGHOST")
	if host == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres portability: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, os.Getenv("PGUSER"), os.Getenv("PGPASSWORD"), os.Getenv("PGDATABASE"))
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	schema := fmt.Sprintf("portability_%d", time.Now().UnixNano())
	if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		_ = db.Close()
	})
	return db
}

// TestMFAChallengeStore_PostgreSQLPortability exercises the full challenge
// lifecycle against real PostgreSQL: schema creation is idempotent, the
// guarded attempt UPDATE bounds attempts, and consumption is single-use.
func TestMFAChallengeStore_PostgreSQLPortability(t *testing.T) {
	db := openPortabilityPG(t)
	ctx := context.Background()

	s := NewMFAChallengeStore(db)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	// Idempotent.
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema twice: %v", err)
	}

	jti, err := NewChallengeID()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Issue(ctx, jti, 4242, 5*time.Minute); err != nil {
		t.Fatalf("issue: %v", err)
	}

	for i := 0; i < MFAMaxChallengeAttempts; i++ {
		if err := s.Begin(ctx, jti, 4242); err != nil {
			t.Fatalf("attempt %d should be allowed on postgres: %v", i+1, err)
		}
	}
	if err := s.Begin(ctx, jti, 4242); err == nil {
		t.Fatal("postgres must enforce the attempt cap")
	}

	jti2, _ := NewChallengeID()
	if err := s.Issue(ctx, jti2, 4243, 5*time.Minute); err != nil {
		t.Fatalf("issue second: %v", err)
	}
	if err := s.Consume(ctx, jti2, 4243); err != nil {
		t.Fatalf("consume on postgres: %v", err)
	}
	if err := s.Consume(ctx, jti2, 4243); err == nil {
		t.Fatal("postgres must refuse a replayed consume")
	}
}

// TestCSRFStore_PostgreSQLPortability proves the CSRF token store's DDL and
// queries work on PostgreSQL. H-1 moved this store off GORM (whose callbacks
// are never registered on the custom SQLite dialector) onto database/sql +
// dbdialect, so both engines must be exercised.
func TestCSRFStore_PostgreSQLPortability(t *testing.T) {
	db := openPortabilityPG(t)
	ctx := context.Background()

	cm := &CSRFManager{sqlDB: db, dialect: pgDialectForTest(db)}
	if err := cm.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if err := cm.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema twice: %v", err)
	}

	// Insert a token row the same way GenerateToken does, then prove lookup
	// and invalidation behave.
	now := time.Now().UTC()
	d := cm.dialectOrDefault()
	hash := fmt.Sprintf("pg-portability-%d", now.UnixNano())
	if _, err := db.ExecContext(ctx,
		`INSERT INTO csrf_records (token_hash, user_id, expires_at, created_at) VALUES (`+d.Placeholders(4)+`)`,
		hash, 99, now.Add(time.Hour), now); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	owner, err := cm.lookupToken(ctx, hash)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if owner != 99 {
		t.Fatalf("expected owner 99, got %d", owner)
	}
	if err := cm.InvalidateUserTokens(99); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if _, err := cm.lookupToken(ctx, hash); err == nil {
		t.Fatal("an invalidated token must no longer resolve")
	}
}

// pgDialectForTest resolves the dialect helpers for a PostgreSQL handle.
func pgDialectForTest(db *sql.DB) *dbdialect.Info {
	if d, err := dbdialect.Detect(db); err == nil {
		return d
	}
	return dbdialect.FromDriver("postgres")
}
