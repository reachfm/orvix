package models

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// MigrateAllPostgres creates core production metadata tables using
// PostgreSQL-native DDL.  It is the PostgreSQL counterpart to
// MigrateAllRaw (which is SQLite-only).
//
// This function does NOT use PRAGMA, sqlite_master, AUTOINCREMENT,
// datetime('now'), or any SQLite-ism.
//
// Scope: 12 core tables sufficient for schema-compatibility smoke.
// The remaining 33 tables (backup, monitoring, TLS, AV, trust,
// updater, and detailed CoreMail operational tables) are deferred
// to DB-4.
//
// Tables created:
//
//	licenses, feature_flags, tenants, users, domains, mailboxes,
//	api_keys, sessions, coremail_audit, security_events,
//	mfa_recovery_codes, coremail_mailboxes
//
// This function is opt-in.  It is NOT called from cmd/orvix/main.go.
// SQLite deployments are unaffected.
func MigrateAllPostgres(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("postgres migrate: get sql.DB: %w", err)
	}

	tables := postgresTables()
	for _, ddl := range tables {
		if _, err := sqlDB.Exec(ddl); err != nil {
			return fmt.Errorf("postgres migrate: create table: %w\nDDL: %s", err, ddl)
		}
	}

	indexes := postgresIndexes()
	for _, ddl := range indexes {
		if _, err := sqlDB.Exec(ddl); err != nil {
			return fmt.Errorf("postgres migrate: create index: %w\nDDL: %s", err, ddl)
		}
	}

	// Verify critical tables exist (PostgreSQL-native, no sqlite_master).
	critical := []string{
		"licenses", "feature_flags", "sessions", "coremail_audit",
		"users", "domains", "mailboxes", "api_keys",
	}
	for _, table := range critical {
		var count int
		if err := sqlDB.QueryRow(
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1`, table,
		).Scan(&count); err != nil {
			return fmt.Errorf("postgres migrate: verify table %s: %w", table, err)
		}
		if count == 0 {
			return fmt.Errorf("postgres migrate: critical table %s was not created", table)
		}
	}

	return nil
}

// postgresTables returns PostgreSQL-native CREATE TABLE statements.
// Key differences from the SQLite DDL:
//
//   - BIGSERIAL PRIMARY KEY instead of INTEGER PRIMARY KEY AUTOINCREMENT
//   - TIMESTAMP instead of DATETIME
//   - BOOLEAN instead of INTEGER (for flag columns)
//   - DOUBLE PRECISION instead of REAL
//   - NOW() instead of datetime('now')
//   - No PRAGMA or sqlite_master references
func postgresTables() []string {
	return []string{
		// --- Admin / platform core ---

		`CREATE TABLE IF NOT EXISTS licenses (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			key_hash TEXT NOT NULL UNIQUE,
			issued_at TIMESTAMP,
			expires_at TIMESTAMP,
			active BOOLEAN NOT NULL DEFAULT true
		)`,

		`CREATE TABLE IF NOT EXISTS feature_flags (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			name TEXT NOT NULL UNIQUE,
			enabled BOOLEAN NOT NULL DEFAULT false
		)`,

		`CREATE TABLE IF NOT EXISTS tenants (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			name TEXT NOT NULL,
			slug TEXT NOT NULL,
			domain TEXT NOT NULL,
			plan TEXT DEFAULT 'smb',
			max_domains INTEGER DEFAULT 10,
			max_mailboxes INTEGER DEFAULT 500,
			logo_url TEXT,
			primary_color TEXT DEFAULT '#4F7CFF',
			active BOOLEAN DEFAULT true,
			reseller_id INTEGER,
			UNIQUE(slug, deleted_at),
			UNIQUE(domain, deleted_at)
		)`,

		`CREATE TABLE IF NOT EXISTS users (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			tenant_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin',
			full_name TEXT NOT NULL DEFAULT '',
			active BOOLEAN NOT NULL DEFAULT true,
			email_verified BOOLEAN NOT NULL DEFAULT false,
			last_login TIMESTAMP,
			UNIQUE(email, deleted_at)
		)`,

		`CREATE TABLE IF NOT EXISTS domains (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			tenant_id INTEGER NOT NULL,
			domain TEXT NOT NULL,
			plan TEXT DEFAULT 'smb',
			max_mailboxes INTEGER DEFAULT 50,
			max_aliases INTEGER DEFAULT 20,
			max_quota_mb INTEGER DEFAULT 1024,
			is_verified BOOLEAN NOT NULL DEFAULT false,
			is_primary BOOLEAN NOT NULL DEFAULT false,
			dns_record_json TEXT NOT NULL DEFAULT '{}',
			UNIQUE(domain, deleted_at)
		)`,

		`CREATE TABLE IF NOT EXISTS mailboxes (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			tenant_id INTEGER NOT NULL,
			domain_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			quota_mb INTEGER DEFAULT 0,
			is_alias BOOLEAN NOT NULL DEFAULT false,
			is_catchall BOOLEAN NOT NULL DEFAULT false,
			is_active BOOLEAN NOT NULL DEFAULT true,
			UNIQUE(email, deleted_at)
		)`,

		`CREATE TABLE IF NOT EXISTS api_keys (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			key_hash TEXT NOT NULL UNIQUE,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			active BOOLEAN NOT NULL DEFAULT true,
			expires_at TIMESTAMP,
			last_used_at TIMESTAMP
		)`,

		// --- Sessions / security / audit ---

		`CREATE TABLE IF NOT EXISTS sessions (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			user_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT,
			expires_at TIMESTAMP NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_audit (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			actor TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT,
			target_type TEXT NOT NULL DEFAULT '',
			target_id INTEGER NOT NULL DEFAULT 0,
			timestamp TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS security_events (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			ip TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			count INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			user_id INTEGER NOT NULL,
			code_hash TEXT NOT NULL DEFAULT '',
			used_at TIMESTAMP
		)`,

		// --- CoreMail operational ---

		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			domain_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			password_hash TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			quota_mb INTEGER NOT NULL DEFAULT 0,
			msg_count INTEGER NOT NULL DEFAULT 0,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
			mfa_enabled BOOLEAN NOT NULL DEFAULT false,
			mfa_secret TEXT NOT NULL DEFAULT '',
			app_passwords TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			is_admin BOOLEAN NOT NULL DEFAULT false,
			is_forwarder BOOLEAN NOT NULL DEFAULT false,
			forward_to TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			recv_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			last_login TIMESTAMP,
			last_ip TEXT NOT NULL DEFAULT '',
			deleted_at TIMESTAMP,
			class_id INTEGER NOT NULL DEFAULT 0,
			allow_smtp BOOLEAN NOT NULL DEFAULT true,
			allow_imap BOOLEAN NOT NULL DEFAULT true,
			allow_pop3 BOOLEAN NOT NULL DEFAULT true,
			allow_jmap BOOLEAN NOT NULL DEFAULT true,
			allow_webmail BOOLEAN NOT NULL DEFAULT true
		)`,
	}
}

// postgresIndexes returns PostgreSQL-native index DDL.
func postgresIndexes() []string {
	idx := func(name, table, columns string) string {
		return fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", name, table, columns)
	}

	return []string{
		// Soft-delete cleanup indexes
		idx("idx_licenses_deleted_at", "licenses", "deleted_at"),
		idx("idx_feature_flags_deleted_at", "feature_flags", "deleted_at"),
		idx("idx_tenants_deleted_at", "tenants", "deleted_at"),
		idx("idx_users_deleted_at", "users", "deleted_at"),
		idx("idx_domains_deleted_at", "domains", "deleted_at"),
		idx("idx_mailboxes_deleted_at", "mailboxes", "deleted_at"),
		idx("idx_api_keys_deleted_at", "api_keys", "deleted_at"),
		idx("idx_sessions_deleted_at", "sessions", "deleted_at"),

		// Foreign-key / lookup indexes
		idx("idx_tenants_reseller_id", "tenants", "reseller_id"),
		idx("idx_users_email", "users", "email"),
		idx("idx_domains_tenant_id", "domains", "tenant_id"),
		idx("idx_mailboxes_tenant_id", "mailboxes", "tenant_id"),
		idx("idx_mailboxes_domain_id", "mailboxes", "domain_id"),
		idx("idx_api_keys_user_id", "api_keys", "user_id"),
		idx("idx_sessions_user_id", "sessions", "user_id"),
		idx("idx_sessions_token_hash", "sessions", "token_hash"),

		// Audit indexes
		idx("idx_coremail_audit_timestamp", "coremail_audit", "timestamp"),
		idx("idx_coremail_audit_actor", "coremail_audit", "actor, timestamp"),

		// Security events
		idx("idx_security_events_email", "security_events", "email"),
		idx("idx_security_events_event_type", "security_events", "event_type"),
		idx("idx_security_events_created", "security_events", "created_at"),

		// CoreMail mailboxes
		idx("idx_pg_coremail_mailboxes_domain_id", "coremail_mailboxes", "domain_id"),
		idx("idx_pg_coremail_mailboxes_email", "coremail_mailboxes", "email"),
	}
}

// PostgresSchemaCompatible checks whether a PostgreSQL schema
// contains the expected core tables.  Returns nil when all critical
// tables exist, or an error listing missing tables.
func PostgresSchemaCompatible(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}

	critical := []string{
		"licenses", "feature_flags", "tenants", "users", "domains",
		"mailboxes", "api_keys", "sessions", "coremail_audit",
		"security_events", "mfa_recovery_codes", "coremail_mailboxes",
	}

	var missing []string
	for _, table := range critical {
		var count int
		if err := sqlDB.QueryRow(
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1`, table,
		).Scan(&count); err != nil {
			return fmt.Errorf("check table %s: %w", table, err)
		}
		if count == 0 {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing tables: %s", strings.Join(missing, ", "))
	}
	return nil
}
