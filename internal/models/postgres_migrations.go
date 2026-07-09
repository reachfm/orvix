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
// Soft-delete uniqueness is enforced via partial unique indexes
// (WHERE deleted_at IS NULL) which is the correct PostgreSQL pattern.
// The SQLite pattern UNIQUE(col, deleted_at) allows duplicate active
// rows in both databases because NULLs are distinct in UNIQUE
// constraints per the SQL standard.
//
// Tables created (17):
//
//	licenses, feature_flags, tenants, users, domains, mailboxes,
//	api_keys, sessions, coremail_audit, security_events,
//	mfa_recovery_codes, coremail_mailboxes,
//	coremail_folders, coremail_messages, coremail_attachments,
//	coremail_queue, coremail_delivery_attempts
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

	return nil
}

// postgresTables returns PostgreSQL-native CREATE TABLE statements.
// Key differences from the SQLite DDL:
//
//   - BIGSERIAL PRIMARY KEY instead of INTEGER PRIMARY KEY AUTOINCREMENT
//   - TIMESTAMP instead of DATETIME
//   - BOOLEAN instead of INTEGER (for flag columns)
//   - NOW() instead of datetime('now')
//   - No UNIQUE(col, deleted_at) — soft-delete uniqueness uses
//     partial unique indexes (WHERE deleted_at IS NULL)
//   - No PRAGMA or sqlite_master references
func postgresTables() []string {
	return []string{
		// --- Admin / platform core ---

		`CREATE TABLE IF NOT EXISTS licenses (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			key_hash TEXT NOT NULL,
			issued_at TIMESTAMP,
			expires_at TIMESTAMP,
			active BOOLEAN NOT NULL DEFAULT true
		)`,

		`CREATE TABLE IF NOT EXISTS feature_flags (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			name TEXT NOT NULL,
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
			reseller_id INTEGER
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
			last_login TIMESTAMP
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
			dns_record_json TEXT NOT NULL DEFAULT '{}'
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
			is_active BOOLEAN NOT NULL DEFAULT true
		)`,

		`CREATE TABLE IF NOT EXISTS api_keys (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			key_hash TEXT NOT NULL,
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
			token_hash TEXT NOT NULL,
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

		// --- Mail storage (messages, folders, attachments) ---

		`CREATE TABLE IF NOT EXISTS coremail_folders (
			id BIGSERIAL PRIMARY KEY,
			mailbox_id INTEGER NOT NULL,
			parent_id INTEGER REFERENCES coremail_folders(id),
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			folder_type TEXT NOT NULL DEFAULT 'custom',
			message_count INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0,
			total_size INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_messages (
			id BIGSERIAL PRIMARY KEY,
			message_id TEXT NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL DEFAULT 0,
			mailbox_id INTEGER NOT NULL,
			folder_id INTEGER NOT NULL,
			thread_id TEXT,
			internet_message_id TEXT,
			subject TEXT NOT NULL DEFAULT '',
			from_address TEXT NOT NULL DEFAULT '',
			to_addresses TEXT NOT NULL DEFAULT '',
			cc_addresses TEXT NOT NULL DEFAULT '',
			bcc_addresses TEXT NOT NULL DEFAULT '',
			reply_to TEXT NOT NULL DEFAULT '',
			message_date TIMESTAMP,
			received_date TIMESTAMP NOT NULL,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			rfc822_path TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			seen BOOLEAN NOT NULL DEFAULT false,
			answered BOOLEAN NOT NULL DEFAULT false,
			flagged BOOLEAN NOT NULL DEFAULT false,
			draft BOOLEAN NOT NULL DEFAULT false,
			deleted BOOLEAN NOT NULL DEFAULT false,
			junk BOOLEAN NOT NULL DEFAULT false,
			importance INTEGER NOT NULL DEFAULT 0,
			retention_policy_id INTEGER,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			purged_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_attachments (
			id BIGSERIAL PRIMARY KEY,
			message_id INTEGER NOT NULL,
			filename TEXT NOT NULL DEFAULT '',
			content_type TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT NOT NULL DEFAULT '',
			storage_path TEXT NOT NULL DEFAULT '',
			cid TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		// --- Queue ---

		`CREATE TABLE IF NOT EXISTS coremail_queue (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL DEFAULT 0,
			mailbox_id INTEGER,
			message_id TEXT NOT NULL,
			from_address TEXT NOT NULL DEFAULT '',
			to_address TEXT NOT NULL,
			recipient_domain TEXT NOT NULL DEFAULT '',
			direction TEXT NOT NULL DEFAULT 'outbound',
			status TEXT NOT NULL DEFAULT 'pending',
			priority INTEGER NOT NULL DEFAULT 0,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 16,
			next_attempt_at TIMESTAMP,
			last_attempt_at TIMESTAMP,
			last_error TEXT NOT NULL DEFAULT '',
			delivery_mode TEXT NOT NULL DEFAULT 'remote_smtp',
			remote_host TEXT NOT NULL DEFAULT '',
			remote_ip TEXT NOT NULL DEFAULT '',
			tls_used BOOLEAN NOT NULL DEFAULT false,
			last_status_code INTEGER NOT NULL DEFAULT 0,
			last_enhanced_code TEXT NOT NULL DEFAULT '',
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_expires_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			completed_at TIMESTAMP,
			dead_letter_at TIMESTAMP,
			deleted_at TIMESTAMP
		)`,

		// --- Delivery attempts ---

		`CREATE TABLE IF NOT EXISTS coremail_delivery_attempts (
			id BIGSERIAL PRIMARY KEY,
			queue_entry_id INTEGER NOT NULL,
			attempt_number INTEGER NOT NULL,
			status TEXT NOT NULL,
			remote_host TEXT NOT NULL DEFAULT '',
			remote_ip TEXT NOT NULL DEFAULT '',
			status_code INTEGER NOT NULL DEFAULT 0,
			status_msg TEXT NOT NULL DEFAULT '',
			enhanced_code TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			tls_used BOOLEAN NOT NULL DEFAULT false,
			worker_id TEXT NOT NULL DEFAULT '',
			attempted_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,
	}
}

// postgresIndexes returns PostgreSQL-native index DDL.
// Includes both regular indexes and partial unique indexes for
// soft-delete uniqueness enforcement.
func postgresIndexes() []string {
	idx := func(name, table, columns string) string {
		return fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", name, table, columns)
	}

	// partialUniqueIndex creates a partial unique index WHERE deleted_at IS NULL.
	partialUniqueIndex := func(name, table, columns string) string {
		return fmt.Sprintf(
			"CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s (%s) WHERE deleted_at IS NULL",
			name, table, columns,
		)
	}

	return []string{
		// --- Soft-delete uniqueness (partial unique indexes) ---
		// These replace the SQLite pattern UNIQUE(col, deleted_at) which
		// does NOT prevent duplicate active rows in either database
		// because NULLs are distinct in UNIQUE constraints.
		partialUniqueIndex("uq_tenants_slug_active", "tenants", "slug"),
		partialUniqueIndex("uq_tenants_domain_active", "tenants", "domain"),
		partialUniqueIndex("uq_users_email_active", "users", "email"),
		partialUniqueIndex("uq_domains_domain_active", "domains", "domain"),
		partialUniqueIndex("uq_mailboxes_email_active", "mailboxes", "email"),

		// Simple unique indexes (non-soft-delete)
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_licenses_key_hash ON licenses (key_hash)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_feature_flags_name ON feature_flags (name)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_api_keys_key_hash ON api_keys (key_hash)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_sessions_token_hash ON sessions (token_hash)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_coremail_messages_message_id ON coremail_messages (message_id)`,

		// --- Standard indexes ---

		// Soft-delete cleanup
		idx("idx_licenses_deleted_at", "licenses", "deleted_at"),
		idx("idx_feature_flags_deleted_at", "feature_flags", "deleted_at"),
		idx("idx_tenants_deleted_at", "tenants", "deleted_at"),
		idx("idx_users_deleted_at", "users", "deleted_at"),
		idx("idx_domains_deleted_at", "domains", "deleted_at"),
		idx("idx_mailboxes_deleted_at", "mailboxes", "deleted_at"),
		idx("idx_api_keys_deleted_at", "api_keys", "deleted_at"),
		idx("idx_sessions_deleted_at", "sessions", "deleted_at"),

		// FK / lookup
		idx("idx_tenants_reseller_id", "tenants", "reseller_id"),
		idx("idx_users_email", "users", "email"),
		idx("idx_users_tenant_id", "users", "tenant_id"),
		idx("idx_domains_tenant_id", "domains", "tenant_id"),
		idx("idx_mailboxes_tenant_id", "mailboxes", "tenant_id"),
		idx("idx_mailboxes_domain_id", "mailboxes", "domain_id"),
		idx("idx_api_keys_user_id", "api_keys", "user_id"),
		idx("idx_sessions_user_id", "sessions", "user_id"),

		// Audit
		idx("idx_coremail_audit_timestamp", "coremail_audit", "timestamp"),
		idx("idx_coremail_audit_actor", "coremail_audit", "actor, timestamp"),

		// Security events
		idx("idx_security_events_email", "security_events", "email"),
		idx("idx_security_events_event_type", "security_events", "event_type"),
		idx("idx_security_events_created", "security_events", "created_at"),

		// CoreMail mailboxes
		idx("idx_pg_cm_mailboxes_domain_id", "coremail_mailboxes", "domain_id"),
		idx("idx_pg_cm_mailboxes_email", "coremail_mailboxes", "email"),

		// CoreMail messages — high-growth query patterns
		idx("idx_cm_messages_mailbox_id", "coremail_messages", "mailbox_id"),
		idx("idx_cm_messages_mailbox_folder_id", "coremail_messages", "mailbox_id, folder_id, id"),
		idx("idx_cm_messages_mailbox_date", "coremail_messages", "mailbox_id, received_date DESC"),
		idx("idx_cm_messages_folder_id", "coremail_messages", "folder_id, id"),
		idx("idx_cm_messages_from_addr", "coremail_messages", "from_address"),
		idx("idx_cm_messages_flags", "coremail_messages", "mailbox_id, folder_id, seen, deleted, junk"),
		idx("idx_cm_messages_purged", "coremail_messages", "purged_at"),

		// CoreMail folders
		idx("idx_cm_folders_mailbox_path", "coremail_folders", "mailbox_id, path"),
		idx("idx_cm_folders_parent", "coremail_folders", "parent_id"),

		// CoreMail attachments
		idx("idx_cm_attachments_message", "coremail_attachments", "message_id"),
		idx("idx_cm_attachments_sha256", "coremail_attachments", "sha256"),

		// Queue
		idx("idx_pg_queue_lease", "coremail_queue", "status, next_attempt_at, priority"),
		idx("idx_pg_queue_tenant", "coremail_queue", "tenant_id, status, created_at"),
		idx("idx_pg_queue_message", "coremail_queue", "message_id"),
		idx("idx_pg_queue_completed", "coremail_queue", "status, completed_at"),

		// Delivery attempts
		idx("idx_pg_del_attempts_entry", "coremail_delivery_attempts", "queue_entry_id, attempt_number"),
		idx("idx_pg_del_attempts_status", "coremail_delivery_attempts", "status"),
		idx("idx_pg_del_attempts_time", "coremail_delivery_attempts", "attempted_at"),
	}
}

// PostgresSchemaCompatible checks whether a PostgreSQL schema
// contains the expected core tables.  Returns nil when all critical
// tables exist, or an error listing missing tables.
func PostgresSchemaCompatible(db *gorm.DB, tableSchema string) error {
	if tableSchema == "" {
		tableSchema = "public"
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}

	critical := []string{
		"licenses", "feature_flags", "tenants", "users", "domains",
		"mailboxes", "api_keys", "sessions", "coremail_audit",
		"security_events", "mfa_recovery_codes", "coremail_mailboxes",
		"coremail_folders", "coremail_messages", "coremail_attachments",
		"coremail_queue", "coremail_delivery_attempts",
	}

	var missing []string
	for _, table := range critical {
		var count int
		if err := sqlDB.QueryRow(
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2`,
			tableSchema, table,
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
