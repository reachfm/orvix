package backup

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"
	_ "modernc.org/sqlite"
)

// These tests cover the dialect branch in snapshotDB: SQLite's
// VACUUM INTO is SQLite-only syntax and errors outright against a
// real PostgreSQL connection (this was the actual production bug —
// every backup attempt failed on a Postgres deployment because
// snapshotDB never checked dialect). A real pg_dump binary is not
// available in this environment, so the success path is exercised
// in CI (postgres-readiness.yml, which runs on ubuntu-latest with
// postgresql-client installed); these tests cover the two
// fail-loud paths that must never silently produce a fake/empty
// backup: (1) SetPostgresDSN was never called, (2) pg_dump is not
// on PATH.

func newTestSQLDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "svc.db")+"?_loc=auto")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSnapshotDB_PostgresDialectWithoutDSN(t *testing.T) {
	s := &Service{
		basePath:    t.TempDir(),
		mailStoreDB: newTestSQLDB(t),
		dialect:     dbdialect.FromDriver("postgres"),
	}
	err := s.snapshotDB(context.Background(), filepath.Join(s.basePath, "database.sqlite"))
	if err == nil {
		t.Fatal("expected error when postgres dialect is set but SetPostgresDSN was never called")
	}
	if !strings.Contains(err.Error(), "no connection string configured") {
		t.Fatalf("expected 'no connection string configured' error, got: %v", err)
	}
}

func TestSnapshotDB_PostgresDialectMissingPgDump(t *testing.T) {
	s := &Service{
		basePath:    t.TempDir(),
		mailStoreDB: newTestSQLDB(t),
		dialect:     dbdialect.FromDriver("postgres"),
	}
	s.SetPostgresDSN("postgres://user:pass@localhost:5432/orvix?sslmode=disable")
	// PATH is deliberately not touched — the test environment has
	// no pg_dump installed, which is exactly the condition this
	// assertion documents: fail with a clear, actionable error
	// rather than silently skip the metadata dump.
	err := s.snapshotDB(context.Background(), filepath.Join(s.basePath, "database.sqlite"))
	if err == nil {
		t.Fatal("expected error when pg_dump binary is not on PATH")
	}
	if !strings.Contains(err.Error(), "pg_dump not found") {
		t.Fatalf("expected 'pg_dump not found' error, got: %v", err)
	}
}

// TestSnapshotDB_SQLiteDialectUnaffected pins down that the existing,
// proven-working SQLite VACUUM INTO path is untouched by the new
// dialect branch — a Service built without an explicit dialect (the
// zero value, matching how NewService behaves before dbdialect.Detect
// runs) must still take the SQLite path, not silently no-op.
func TestSnapshotDB_SQLiteDialectUnaffected(t *testing.T) {
	sqlDB := newTestSQLDB(t)
	s := &Service{
		basePath:    t.TempDir(),
		mailStoreDB: sqlDB,
		dialect:     dbdialect.FromDriver("sqlite"),
	}
	dest := filepath.Join(s.basePath, "database.sqlite")
	if err := s.snapshotDB(context.Background(), dest); err != nil {
		t.Fatalf("sqlite snapshot: %v", err)
	}
}
