package models

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

func TestMigrateAllRawCreatesDKIMTable(t *testing.T) {
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

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='coremail_dkim_config'").Scan(&count); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 1 {
		t.Fatalf("coremail_dkim_config table not created by MigrateAllRaw")
	}
}

func TestMigrateAllRawUpgradesMissingDKIMTable(t *testing.T) {
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

	// Create all coremail tables except coremail_dkim_config,
	// simulating the pre-2F VPS state.
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS coremail_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '', target TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '', timestamp DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'active',
			plan TEXT NOT NULL DEFAULT 'smb', max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_aliases INTEGER NOT NULL DEFAULT 0, max_quota_mb INTEGER NOT NULL DEFAULT 0,
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT, domain_id INTEGER NOT NULL,
			local_part TEXT NOT NULL, email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active',
			quota_mb INTEGER NOT NULL DEFAULT 0, is_admin INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT, status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
		)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	// Confirm dkim table does not exist before migration.
	var before int
	sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='coremail_dkim_config'").Scan(&before)
	if before != 0 {
		t.Fatalf("precondition failed: coremail_dkim_config should not exist before migration")
	}

	// Run the canonical migration.
	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Assert table now exists.
	var after int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='coremail_dkim_config'").Scan(&after); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if after != 1 {
		t.Fatalf("coremail_dkim_config should exist after MigrateAllRaw")
	}
}

func TestMigrateAllRawDKIMIdempotent(t *testing.T) {
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

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Second run must be safe.
	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("second migrate must not error: %v", err)
	}
	var count int
	sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='coremail_dkim_config'").Scan(&count)
	if count != 1 {
		t.Fatalf("coremail_dkim_config table missing after second migrate")
	}
}

func TestMigrateAllRawDKIMSurvivesRerun(t *testing.T) {
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

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Insert a DKIM row.
	_, err = sqlDB.Exec(`INSERT INTO coremail_dkim_config (domain, selector, private_key_pem, enabled, created_at, updated_at)
		VALUES (?, ?, ?, 1, datetime('now'), datetime('now'))`,
		"example.com", "orvix", "pem-data")
	if err != nil {
		t.Fatalf("insert dkim config: %v", err)
	}
	// Second migrate must not delete the row.
	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var selector string
	if err := sqlDB.QueryRow("SELECT selector FROM coremail_dkim_config WHERE domain = ?", "example.com").Scan(&selector); err != nil {
		t.Fatalf("dkim config row lost after second migrate: %v", err)
	}
	if selector != "orvix" {
		t.Fatalf("dkim selector corrupted after second migrate: got %q want orvix", selector)
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

// TestCriticalIndexesExist verifies that high-growth and security-critical
// indexes are created by MigrateAllRaw. This guards against accidental index
// removal and proves the schema is ready for enterprise query patterns.
func TestCriticalIndexesExist(t *testing.T) {
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

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	criticalIndexes := []struct {
		table  string
		index  string
		reason string
	}{
		// Sessions — soft-delete cleanup.
		{"sessions", "idx_sessions_deleted_at", "soft-delete cleanup"},
		// Users — soft-delete cleanup.
		{"users", "idx_users_deleted_at", "soft-delete cleanup"},
		// Mailboxes — filtered lists per tenant/domain.
		{"mailboxes", "idx_mailboxes_tenant_id", "mailbox list per tenant"},
		{"mailboxes", "idx_mailboxes_domain_id", "mailbox list per domain"},
		{"mailboxes", "idx_mailboxes_deleted_at", "soft-delete cleanup"},
		// Domains — filtered lists per tenant.
		{"domains", "idx_domains_tenant_id", "domain list per tenant"},
		{"domains", "idx_domains_deleted_at", "soft-delete cleanup"},
		// Tenants — reseller query and soft-delete.
		{"tenants", "idx_tenants_reseller_id", "tenant list per reseller"},
		{"tenants", "idx_tenants_deleted_at", "soft-delete cleanup"},
		// API keys — user lookup and soft-delete.
		{"api_keys", "idx_api_keys_user_id", "API key list per user"},
		{"api_keys", "idx_api_keys_deleted_at", "soft-delete cleanup"},
		// Security events — login protection queries.
		{"security_events", "idx_security_events_email", "rate-limit lookup by email"},
		{"security_events", "idx_security_events_event_type", "filter by event type"},
		{"security_events", "idx_security_events_created", "time-range queries"},
		// Audit trail — actor and time-range queries.
		{"coremail_audit", "idx_coremail_audit_timestamp", "audit time-range queries"},
		{"coremail_audit", "idx_coremail_audit_actor", "audit lookup by actor"},
	}

	for _, tc := range criticalIndexes {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			var count int
			err := sqlDB.QueryRow(
				"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?",
				tc.index,
			).Scan(&count)
			if err != nil {
				t.Fatalf("query sqlite_master for index %s: %v", tc.index, err)
			}
			if count == 0 {
				t.Errorf("CRITICAL INDEX MISSING: %s on %s (%s). "+
					"This index is required for enterprise query performance.",
					tc.index, tc.table, tc.reason)
			}
		})
	}
}

// TestPostgresProductionSchemaCompat creates the PostgreSQL-native
// production schema via MigrateAllPostgres and verifies all 12 core
// tables and their indexes exist.  Runs only when:
//
//	ORVIX_RUN_POSTGRES_SCHEMA_TEST=1
//	ORVIX_DB_DRIVER=postgres
//	ORVIX_DB_DSN=<valid postgres DSN>
//
// The DSN is never printed.  Skipped silently when env vars are
// not set — normal CI is unaffected.
func TestPostgresProductionSchemaCompat(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ORVIX_RUN_POSTGRES_SCHEMA_TEST")) != "1" {
		t.Skip("set ORVIX_RUN_POSTGRES_SCHEMA_TEST=1 to run postgres schema smoke")
	}
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER")))
	if driver != "postgres" {
		t.Skipf("ORVIX_DB_DRIVER=%q (want postgres)", driver)
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
	cfg.Database.MaxLifetime = 300

	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	defer sqlDB.Close()

	// Drop any leftover tables from a previous run (safe: these are
	// the same tables MigrateAllPostgres creates).
	dropOrder := []string{
		"security_events", "mfa_recovery_codes", "coremail_mailboxes",
		"mailboxes", "domains", "users", "coremail_audit",
		"sessions", "api_keys", "tenants", "feature_flags", "licenses",
	}
	for _, tbl := range dropOrder {
		sqlDB.Exec(`DROP TABLE IF EXISTS ` + tbl + ` CASCADE`)
	}

	if err := MigrateAllPostgres(db); err != nil {
		t.Fatalf("MigrateAllPostgres: %v", err)
	}

	// Verify all 12 core tables exist.
	if err := PostgresSchemaCompatible(db); err != nil {
		t.Fatalf("PostgresSchemaCompatible: %v", err)
	}
	t.Log("all 12 core postgres tables created and verified")

	// Insert a representative row to prove DML works.
	_, err = sqlDB.Exec(
		`INSERT INTO tenants (name, slug, domain) VALUES ($1, $2, $3)`,
		"Smoke Tenant", "smoke-tenant", "smoke.example.com",
	)
	if err != nil {
		t.Fatalf("insert into tenants: %v", err)
	}

	var slug string
	if err := sqlDB.QueryRow(`SELECT slug FROM tenants WHERE name = $1`, "Smoke Tenant").Scan(&slug); err != nil {
		t.Fatalf("query tenants: %v", err)
	}
	if slug != "smoke-tenant" {
		t.Errorf("expected slug=smoke-tenant, got %s", slug)
	}

	// Verify key indexes exist.
	pgIndexes := []string{
		"idx_tenants_deleted_at",
		"idx_users_email",
		"idx_users_deleted_at",
		"idx_domains_tenant_id",
		"idx_mailboxes_tenant_id",
		"idx_sessions_token_hash",
		"idx_sessions_user_id",
		"idx_coremail_audit_timestamp",
		"idx_coremail_audit_actor",
		"idx_security_events_email",
		"idx_security_events_event_type",
	}
	for _, idxName := range pgIndexes {
		var count int
		if err := sqlDB.QueryRow(
			`SELECT COUNT(*) FROM pg_indexes WHERE indexname = $1`, idxName,
		).Scan(&count); err != nil {
			t.Errorf("check index %s: %v", idxName, err)
		} else if count == 0 {
			t.Errorf("index %s not found", idxName)
		} else {
			t.Logf("index %s verified", idxName)
		}
	}

	// Clean up.
	for _, tbl := range dropOrder {
		sqlDB.Exec(`DROP TABLE IF EXISTS ` + tbl + ` CASCADE`)
	}

	t.Log("postgres production schema smoke: PASS")
}
