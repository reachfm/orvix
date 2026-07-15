package abuse

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const testSchemaPrefix = "orvix_abuse_test_"

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	if pghost := os.Getenv("PGHOST"); pghost != "" {
		return newPostgresTestDB(t)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ddl := abuseDDL(false)
	for i, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("abuse DDL stmt %d: %v", i, err)
		}
	}
	return db
}

func newPostgresTestDB(t *testing.T) *sql.DB {
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

	ddl := abuseDDL(true)
	for i, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("abuse DDL stmt %d: %v", i, err)
		}
	}
	return db
}

func abuseDDL(pg bool) []string {
	autoInc := "INTEGER PRIMARY KEY AUTOINCREMENT"
	ts := "DATETIME"
	if pg {
		autoInc = "BIGSERIAL PRIMARY KEY"
		ts = "TIMESTAMP"
	}
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS abuse_send_counts (
			day_key TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			emails_sent INTEGER NOT NULL DEFAULT 0,
			created_at %s DEFAULT CURRENT_TIMESTAMP
		)`, ts),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS abuse_bounce_counts (
			day_key TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			bounce_count INTEGER NOT NULL DEFAULT 0,
			created_at %s DEFAULT CURRENT_TIMESTAMP
		)`, ts),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS abuse_signals (
			id %s,
			tenant_id INTEGER NOT NULL,
			mailbox_id INTEGER,
			signal_type TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT 'info',
			description TEXT DEFAULT '',
			metadata TEXT DEFAULT '',
			detected_at %s NOT NULL,
			acknowledged_at %s,
			resolved_at %s,
			resolved_by INTEGER,
			created_at %s DEFAULT CURRENT_TIMESTAMP
		)`, autoInc, ts, ts, ts, ts),
		`CREATE TABLE IF NOT EXISTS subscriptions (
			tenant_id INTEGER PRIMARY KEY,
			send_limit_day INTEGER NOT NULL DEFAULT 500,
			status TEXT NOT NULL DEFAULT 'active'
		)`,
	}
}

func isSQLiteDriver(db *sql.DB) bool {
	return strings.HasPrefix(fmt.Sprintf("%T", db.Driver()), "*sqlite")
}
