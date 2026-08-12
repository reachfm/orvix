package importer

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type testCleanuper interface {
	Helper()
	Cleanup(func())
	TempDir() string
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

// newImporterTestDB opens a real SQLite database by default, or a real
// PostgreSQL database (in its own schema) when PGHOST is set — mirroring the
// billing/freshinstall helpers. The dialect-agnostic importer tests therefore
// exercise the SAME code paths against both backends.
func newImporterTestDB(t testCleanuper) *sql.DB {
	t.Helper()
	if pghost := os.Getenv("PGHOST"); pghost != "" {
		return newImporterPostgresTestDB(t)
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "importer.db")+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func newImporterPostgresTestDB(t testCleanuper) *sql.DB {
	t.Helper()
	host := os.Getenv("PGHOST")
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("PGUSER")
	password := os.Getenv("PGPASSWORD")
	dbname := os.Getenv("PGDATABASE")
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, dbname)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	schema := fmt.Sprintf("orvix_importer_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
	})
	return db
}
