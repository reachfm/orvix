package models

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
)

// ── Mailbox access-mode migration tests (MAILBOX-ACCESS-MODE-PHASE1) ─

// oldMailboxSchema is the coremail_mailboxes shape from the release
// BEFORE per-mailbox mail_access_mode / version existed.
const oldMailboxSchema = `CREATE TABLE coremail_mailboxes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	domain_id INTEGER NOT NULL,
	tenant_id INTEGER NOT NULL DEFAULT 0,
	local_part TEXT NOT NULL,
	email TEXT UNIQUE NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
	mfa_enabled INTEGER NOT NULL DEFAULT 0,
	mfa_secret TEXT NOT NULL DEFAULT '',
	app_passwords TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	quota_mb INTEGER NOT NULL DEFAULT 0,
	used_bytes INTEGER NOT NULL DEFAULT 0,
	msg_count INTEGER NOT NULL DEFAULT 0,
	is_admin INTEGER NOT NULL DEFAULT 0,
	is_forwarder INTEGER NOT NULL DEFAULT 0,
	forward_to TEXT NOT NULL DEFAULT '',
	labels TEXT NOT NULL DEFAULT '',
	send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
	recv_limit_per_hour INTEGER NOT NULL DEFAULT 0,
	last_login DATETIME,
	last_ip TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	deleted_at DATETIME
)`

// TestMigrateAllRaw_MailboxAccessModeOnOldDatabase proves the additive
// migration: an OLD database (pre-mailbox-mode) keeps every existing
// mailbox row, gains mail_access_mode='inherit' and version=1, and the
// migration is safe to re-run.
func TestMigrateAllRaw_MailboxAccessModeOnOldDatabase(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "old.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()

	if _, err := sqlDB.Exec(oldMailboxSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	// Seed pre-existing mailbox data that must survive the migration.
	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, created_at, updated_at)
		 VALUES (1, 7, 'legacy', 'legacy@old.example', 'Legacy', 'hash', 'argon2id', 'active', 1024, ?, ?)`,
		now, now); err != nil {
		t.Fatalf("seed legacy mailbox: %v", err)
	}

	// Run the canonical migration (adds the new columns + everything else).
	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The legacy row survives with the inherit default.
	var (
		email   string
		mode    string
		version int
		quota   int64
		status  string
	)
	if err := sqlDB.QueryRow(
		`SELECT email, mail_access_mode, version, quota_mb, status FROM coremail_mailboxes WHERE email='legacy@old.example'`,
	).Scan(&email, &mode, &version, &quota, &status); err != nil {
		t.Fatalf("read legacy mailbox after migration: %v", err)
	}
	if email != "legacy@old.example" || status != "active" || quota != 1024 {
		t.Fatalf("legacy row data changed: %+v", email)
	}
	if mode != "inherit" {
		t.Fatalf("legacy mailbox must default to inherit, got %q", mode)
	}
	if version != 1 {
		t.Fatalf("legacy mailbox version must be 1, got %d", version)
	}

	// Re-running the migration is idempotent.
	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	var mode2 string
	if err := sqlDB.QueryRow(`SELECT mail_access_mode FROM coremail_mailboxes WHERE email='legacy@old.example'`).Scan(&mode2); err != nil {
		t.Fatal(err)
	}
	if mode2 != "inherit" {
		t.Fatalf("re-migration changed the mode: %q", mode2)
	}
}

// TestMigrateAllRaw_MailboxAccessModeAdditiveWhenColumnsExist proves
// the migration is a no-op on a database that already has the columns
// (fresh install re-boot).
func TestMigrateAllRaw_MailboxAccessModeAdditiveWhenColumnsExist(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "new.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Columns exist with the canonical defaults on a fresh table.
	cols, err := sqliteColumns(t.Context(), sqlDB, "coremail_mailboxes")
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	if !cols["mail_access_mode"] || !cols["version"] {
		t.Fatalf("fresh schema must include mail_access_mode/version: %v", cols)
	}
	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// TestMigrateAllPostgres_MailboxAccessMode is the PostgreSQL leg of the
// migration proof. Gated on PGHOST + ORVIX_RUN_POSTGRES_DML_TEST=1 like
// every other PostgreSQL suite; it runs in the disposable container
// verification step with a dedicated database.
func TestMigrateAllPostgres_MailboxAccessMode(t *testing.T) {
	host := os.Getenv("PGHOST")
	if host == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
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
	schema := fmt.Sprintf("prov_migration_%d", time.Now().UnixNano())
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

	// 1. Create a PRE-column coremail_mailboxes table (the old shape).
	if _, err := db.Exec(`CREATE TABLE coremail_mailboxes (
		id BIGSERIAL PRIMARY KEY,
		domain_id INTEGER NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		local_part TEXT NOT NULL,
		email TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL,
		auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
		status TEXT NOT NULL DEFAULT 'active',
		quota_mb BIGINT NOT NULL DEFAULT 0,
		created_at TIMESTAMP NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMP
	)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, status, quota_mb)
		 VALUES (1, 7, 'legacy', 'legacy@old.example', 'Legacy', 'hash', 'active', 1024)`); err != nil {
		t.Fatalf("seed legacy mailbox: %v", err)
	}

	// 2. Apply the canonical PostgreSQL migration twice (idempotent).
	for i := 0; i < 2; i++ {
		if err := MigrateAllPostgresRaw(db); err != nil {
			t.Fatalf("postgres migrate pass %d: %v", i+1, err)
		}
	}

	// 3. The legacy row survives with the inherit default.
	var mode string
	var version int
	if err := db.QueryRow(`SELECT mail_access_mode, version FROM coremail_mailboxes WHERE email='legacy@old.example'`).Scan(&mode, &version); err != nil {
		t.Fatalf("read legacy mailbox: %v", err)
	}
	if mode != "inherit" {
		t.Fatalf("legacy mailbox mode=%q want inherit", mode)
	}
	if version != 1 {
		t.Fatalf("legacy mailbox version=%d want 1", version)
	}
}
