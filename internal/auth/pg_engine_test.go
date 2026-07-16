package auth

// PostgreSQL-backed runs of the rotation and session-revocation suites. Gated on
// PGHOST + ORVIX_RUN_POSTGRES_DML_TEST so local SQLite runs skip them, while the
// PostgreSQL Runtime DML workflow (which starts a real postgres service and sets
// those vars) exercises the exact same behavior on the production engine.

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func newPostgresTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	host := os.Getenv("PGHOST")
	if host == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres engine: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	base := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, os.Getenv("PGUSER"), os.Getenv("PGPASSWORD"), os.Getenv("PGDATABASE"))

	// Isolate each test in its own schema so parallel/repeat runs never collide.
	schema := fmt.Sprintf("auth_test_%d", time.Now().UnixNano())
	raw, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if _, err := raw.Exec("CREATE SCHEMA IF NOT EXISTS " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = raw.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		_ = raw.Close()
	})

	logger := testLogger(t)
	cfg := config.Defaults()
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = base + " search_path=" + schema
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt_key.pem")

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("postgres database: %v", err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	if err := models.MigrateAllPostgres(db); err != nil {
		t.Fatalf("postgres migrate: %v", err)
	}

	a, err := NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	return a
}

func TestRotateByID_PostgreSQL(t *testing.T) {
	a := newPostgresTestAuth(t)
	rotationSuite(t, NewAPIKeyManager(a.db, a.logger))
}

func TestSessionRevocation_PostgreSQL(t *testing.T) {
	a := newPostgresTestAuth(t)
	sessionRevocationSuite(t, a)
}
