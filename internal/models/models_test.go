package models

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
)

func TestMigrateAllRawUpgradesOldCoremailMailboxesSchema(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()

	createOldCoremailMailboxesSchema(t, sqlDB)
	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, column := range []string{
		"tenant_id",
		"name",
		"auth_scheme",
		"mfa_enabled",
		"mfa_secret",
		"app_passwords",
		"status",
		"quota_mb",
		"msg_count",
		"is_forwarder",
		"forward_to",
		"labels",
		"send_limit_per_hour",
		"recv_limit_per_hour",
		"last_login",
		"last_ip",
		"deleted_at",
	} {
		if !testSQLiteColumnExists(t, sqlDB, "coremail_mailboxes", column) {
			t.Fatalf("expected migrated coremail_mailboxes.%s column", column)
		}
	}

	now := time.Now().UTC()
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		1, 1, "admin", "admin@example.com", "$argon2id$v=19$m=1024,t=1,p=1$salt$hash", now, now,
	)
	if err != nil {
		t.Fatalf("bootstrap-compatible mailbox insert failed after migration: %v", err)
	}
}

func createOldCoremailMailboxesSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE coremail_mailboxes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain_id INTEGER NOT NULL,
		local_part TEXT NOT NULL,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		quota INTEGER NOT NULL DEFAULT 0,
		used_bytes INTEGER NOT NULL DEFAULT 0,
		active INTEGER NOT NULL DEFAULT 1,
		is_admin INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create old coremail_mailboxes schema: %v", err)
	}
}

func testSQLiteColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan pragma row: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pragma rows: %v", err)
	}
	return false
}
