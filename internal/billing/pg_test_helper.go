package billing

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const testSchemaPrefix = "orvix_test_"

type testCleanuper interface {
	Helper()
	Cleanup(func())
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

func newTestDB(t testCleanuper) *sql.DB {
	t.Helper()
	if pghost := os.Getenv("PGHOST"); pghost != "" {
		return newPostgresTestDB(t)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// modernc.org/sqlite's :memory: DSN gives each new pooled connection
	// its own separate, empty in-memory database unless shared-cache mode
	// is used — with MaxOpenConns left unbounded, a test that runs enough
	// concurrent goroutines forces the pool to open more than one
	// connection, and writers on a later connection silently never see
	// rows a schema-setup/earlier writer inserted on the first one. This
	// surfaced as a real test failure (TestUsageCounter_
	// ConcurrentReservesNeverExceedLimit — 20 concurrent goroutines got
	// far fewer successes than the real constraint allowed, because most
	// of them landed on a connection that had never seen EnsureCounter's
	// insert). SQLite only supports one writer at a time regardless, so
	// forcing a single connection matches both the Postgres branch below
	// and the database's real concurrency model — it does not change
	// what any test actually verifies, it makes the harness correctly
	// exercise the database-level atomicity/locking the code under test
	// depends on.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newPostgresTestDB(t testCleanuper) *sql.DB {
	t.Helper()
	host := os.Getenv("PGHOST")
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("PGUSER")
	password := os.Getenv("PGPASSWORD")
	dbname := os.Getenv("PGDATABASE")

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	schemaName := fmt.Sprintf("%s%d", testSchemaPrefix, time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("SET search_path TO %s", schemaName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
	})

	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	return db
}
