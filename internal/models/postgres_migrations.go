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
// Tables created (59):
//
//	licenses, feature_flags, module_versions, tenants, users, domains,
//	mailboxes, api_keys, sessions, coremail_audit, security_events,
//	mfa_recovery_codes, firewall_rules, firewall_logs, guardian_logs,
//	heal_histories, update_histories, coremail_mailboxes,
//	coremail_folders, coremail_messages, coremail_attachments,
//	coremail_queue, coremail_delivery_attempts, resellers,
//	l_dap_configs, s_s_o_configs, alert_configs, provisioned_domains,
//	coremail_domains, coremail_aliases, coremail_account_classes,
//	coremail_admin_groups, coremail_admin_group_members,
//	coremail_domain_groups, coremail_domain_group_members,
//	coremail_mailing_lists, coremail_mailing_list_members,
//	coremail_public_folders, coremail_public_folder_members,
//	coremail_acl_rules, coremail_log_rules, coremail_quarantine_index,
//	coremail_dkim_config, coremail_acceptance_rules,
//	coremail_incoming_msg_rules, coremail_migration_sources,
//	coremail_migration_source_secrets, coremail_backup_targets,
//	coremail_backup_target_secrets, coremail_uploaded_certificates,
//	coremail_lockouts, coremail_trust_scores, tls_certificates,
//	monitoring_alerts, monitoring_alert_deliveries, backup_registry,
//	backup_schedule_config, upgrade_history, coremail_versions
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

	additions := postgresColumnAdditions()
	for _, ddl := range additions {
		if _, err := sqlDB.Exec(ddl); err != nil {
			// Column may already exist; log but don't fail.
			_ = err
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
			tier TEXT NOT NULL DEFAULT 'smb',
			issued_at TIMESTAMP,
			expires_at TIMESTAMP,
			max_domains INTEGER NOT NULL DEFAULT 10,
			max_mailboxes INTEGER NOT NULL DEFAULT 500,
			hardware_id TEXT NOT NULL DEFAULT '',
			metadata TEXT,
			active BOOLEAN NOT NULL DEFAULT true
		)`,

		`CREATE TABLE IF NOT EXISTS feature_flags (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			name TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT false,
			tier_required TEXT NOT NULL DEFAULT '',
			module_version TEXT NOT NULL DEFAULT '1.0.0',
			description TEXT
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
			last_login TIMESTAMP,
			mfa_enabled BOOLEAN NOT NULL DEFAULT false,
			mfa_secret TEXT NOT NULL DEFAULT '',
			pending_mfa_secret TEXT NOT NULL DEFAULT '',
			pending_mfa_secret_raw TEXT NOT NULL DEFAULT '',
			mfa_secret_raw TEXT NOT NULL DEFAULT '',
			mfa_label TEXT NOT NULL DEFAULT ''
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
			status TEXT NOT NULL DEFAULT 'pending',
			dkim_selector TEXT NOT NULL DEFAULT ''
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
			display_name TEXT NOT NULL DEFAULT '',
			send_limit INTEGER NOT NULL DEFAULT 0
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
			local_part TEXT NOT NULL DEFAULT '',
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

		// --- Expanded tables (DB-4 readiness) ---

		// Resellers
		`CREATE TABLE IF NOT EXISTS resellers (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			name TEXT NOT NULL,
			email TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			max_tenants INTEGER DEFAULT 50,
			max_domains INTEGER DEFAULT 500,
			max_mailboxes INTEGER DEFAULT 10000,
			commission DOUBLE PRECISION DEFAULT 0.0,
			active BOOLEAN DEFAULT true
		)`,

		// LDAP configs
		`CREATE TABLE IF NOT EXISTS l_dap_configs (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			tenant_id INTEGER NOT NULL,
			host TEXT NOT NULL,
			port INTEGER DEFAULT 389,
			base_dn TEXT NOT NULL,
			bind_dn TEXT NOT NULL,
			bind_password TEXT NOT NULL,
			user_filter TEXT DEFAULT '(objectClass=person)',
			sync_enabled BOOLEAN DEFAULT false,
			last_sync TIMESTAMP
		)`,

		// SSO configs
		`CREATE TABLE IF NOT EXISTS s_s_o_configs (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			tenant_id INTEGER NOT NULL,
			provider TEXT NOT NULL,
			client_id TEXT NOT NULL,
			client_secret TEXT NOT NULL,
			issuer_url TEXT,
			enabled BOOLEAN DEFAULT false
		)`,

		// Alert configs
		`CREATE TABLE IF NOT EXISTS alert_configs (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			tenant_id INTEGER NOT NULL,
			smtp_enabled BOOLEAN DEFAULT false,
			smtp_server TEXT,
			smtp_port INTEGER DEFAULT 587,
			smtp_username TEXT,
			smtp_password TEXT,
			smtp_from TEXT,
			webhook_enabled BOOLEAN DEFAULT false,
			webhook_url TEXT,
			alert_on_failed_login BOOLEAN DEFAULT true,
			alert_on_suspicious_key BOOLEAN DEFAULT true
		)`,

		// Provisioned domains
		`CREATE TABLE IF NOT EXISTS provisioned_domains (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			domain TEXT NOT NULL,
			plan TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			provisioned_by INTEGER NOT NULL,
			metadata TEXT
		)`,

		// CoreMail domains
		`CREATE TABLE IF NOT EXISTS coremail_domains (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			reseller_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			plan TEXT NOT NULL DEFAULT 'smb',
			description TEXT NOT NULL DEFAULT '',
			max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_aliases INTEGER NOT NULL DEFAULT 0,
			max_quota_mb INTEGER NOT NULL DEFAULT 0,
			dkim_enabled BOOLEAN NOT NULL DEFAULT false,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled BOOLEAN NOT NULL DEFAULT false,
			mtasts_enabled BOOLEAN NOT NULL DEFAULT false,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail aliases
		`CREATE TABLE IF NOT EXISTS coremail_aliases (
			id BIGSERIAL PRIMARY KEY,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			from_addr TEXT NOT NULL,
			to_addr TEXT NOT NULL,
			active BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail account classes
		`CREATE TABLE IF NOT EXISTS coremail_account_classes (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			default_quota_mb INTEGER NOT NULL DEFAULT 1024,
			max_quota_mb INTEGER NOT NULL DEFAULT 5120,
			max_send_per_hour INTEGER NOT NULL DEFAULT 500,
			max_recv_per_hour INTEGER NOT NULL DEFAULT 5000,
			allow_external_forwarding BOOLEAN NOT NULL DEFAULT true,
			allow_imap BOOLEAN NOT NULL DEFAULT true,
			allow_pop3 BOOLEAN NOT NULL DEFAULT true,
			allow_jmap BOOLEAN NOT NULL DEFAULT true,
			allow_webmail BOOLEAN NOT NULL DEFAULT true,
			is_admin_class BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail admin groups
		`CREATE TABLE IF NOT EXISTS coremail_admin_groups (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			grants TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail admin group members
		`CREATE TABLE IF NOT EXISTS coremail_admin_group_members (
			id BIGSERIAL PRIMARY KEY,
			group_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		// CoreMail ACL rules
		`CREATE TABLE IF NOT EXISTS coremail_acl_rules (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			scope TEXT NOT NULL DEFAULT 'global',
			scope_target TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'allow',
			protocol TEXT NOT NULL DEFAULT 'all',
			source TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			note TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail quarantine index
		`CREATE TABLE IF NOT EXISTS coremail_quarantine_index (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			message_id TEXT NOT NULL,
			recipient TEXT NOT NULL DEFAULT '',
			sender TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT 'low',
			status TEXT NOT NULL DEFAULT 'held',
			resolved_at TIMESTAMP,
			resolved_by TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		// CoreMail DKIM config
		`CREATE TABLE IF NOT EXISTS coremail_dkim_config (
			id BIGSERIAL PRIMARY KEY,
			domain TEXT NOT NULL,
			selector TEXT NOT NULL DEFAULT 'default',
			private_key_pem TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		// CoreMail acceptance rules
		`CREATE TABLE IF NOT EXISTS coremail_acceptance_rules (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			enabled BOOLEAN NOT NULL DEFAULT true,
			scope TEXT NOT NULL DEFAULT 'global',
			scope_target TEXT NOT NULL DEFAULT '',
			sender_pattern TEXT NOT NULL DEFAULT '',
			recipient_pattern TEXT NOT NULL DEFAULT '',
			source_ip_cidr TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'accept',
			redirect_to TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail incoming message rules
		`CREATE TABLE IF NOT EXISTS coremail_incoming_msg_rules (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			enabled BOOLEAN NOT NULL DEFAULT true,
			field TEXT NOT NULL DEFAULT 'subject',
			operator TEXT NOT NULL DEFAULT 'contains',
			value TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'move',
			action_target TEXT NOT NULL DEFAULT '',
			apply_to TEXT NOT NULL DEFAULT 'all',
			stop_processing BOOLEAN NOT NULL DEFAULT false,
			note TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail migration sources
		`CREATE TABLE IF NOT EXISTS coremail_migration_sources (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'imap',
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 993,
			username TEXT NOT NULL DEFAULT '',
			use_tls BOOLEAN NOT NULL DEFAULT true,
			allow_insecure BOOLEAN NOT NULL DEFAULT false,
			default_base_folder TEXT NOT NULL DEFAULT 'INBOX',
			verify_hostname TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			has_secret BOOLEAN NOT NULL DEFAULT false,
			last_test_status TEXT NOT NULL DEFAULT '',
			last_test_at TIMESTAMP,
			last_test_message TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail migration source secrets (PK = FK identity, no auto-gen)
		`CREATE TABLE IF NOT EXISTS coremail_migration_source_secrets (
			source_id BIGINT PRIMARY KEY,
			password_enc TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		// CoreMail backup targets
		`CREATE TABLE IF NOT EXISTS coremail_backup_targets (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'ftp',
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 21,
			username TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '/',
			enabled BOOLEAN NOT NULL DEFAULT false,
			verify_hostname BOOLEAN NOT NULL DEFAULT true,
			has_secret BOOLEAN NOT NULL DEFAULT false,
			last_test_status TEXT NOT NULL DEFAULT '',
			last_test_at TIMESTAMP,
			last_test_message TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// CoreMail backup target secrets (PK = FK identity, no auto-gen)
		`CREATE TABLE IF NOT EXISTS coremail_backup_target_secrets (
			target_id BIGINT PRIMARY KEY,
			password_enc TEXT NOT NULL DEFAULT '',
			private_key_path TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		// CoreMail uploaded certificates
		`CREATE TABLE IF NOT EXISTS coremail_uploaded_certificates (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			cert_path TEXT NOT NULL DEFAULT '',
			key_path TEXT NOT NULL DEFAULT '',
			common_name TEXT NOT NULL DEFAULT '',
			sans TEXT NOT NULL DEFAULT '',
			issuer TEXT NOT NULL DEFAULT '',
			serial_number TEXT NOT NULL DEFAULT '',
			not_before TIMESTAMP,
			not_after TIMESTAMP,
			fingerprint_sha256 TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'unknown',
			created_by INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// --- Models from MigrateAll (module/lifecycle/trust/security) ---

		`CREATE TABLE IF NOT EXISTS module_versions (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			module_id TEXT NOT NULL,
			version TEXT NOT NULL,
			installed_at TIMESTAMP NOT NULL DEFAULT NOW(),
			checksum TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			changelog TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS firewall_rules (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			name TEXT NOT NULL,
			condition TEXT NOT NULL,
			action TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			enabled BOOLEAN NOT NULL DEFAULT true
		)`,

		`CREATE TABLE IF NOT EXISTS firewall_logs (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			ip TEXT NOT NULL,
			domain TEXT NOT NULL,
			sender TEXT,
			recipient TEXT,
			action TEXT NOT NULL,
			reason TEXT,
			threat_score DOUBLE PRECISION NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS guardian_logs (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			message_id TEXT NOT NULL,
			threat_score DOUBLE PRECISION NOT NULL,
			verdict TEXT NOT NULL,
			confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
			reasons TEXT,
			action TEXT NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS heal_histories (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			check_name TEXT NOT NULL,
			severity TEXT NOT NULL,
			issue TEXT NOT NULL,
			fix_applied TEXT,
			success BOOLEAN NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS update_histories (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP,
			module_id TEXT NOT NULL,
			from_version TEXT NOT NULL,
			to_version TEXT NOT NULL,
			status TEXT NOT NULL,
			backup_path TEXT
		)`,

		// --- Admin enterprise v2 group/list/folder/rule tables ---

		`CREATE TABLE IF NOT EXISTS coremail_domain_groups (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			color TEXT NOT NULL DEFAULT '#1f6feb',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_domain_group_members (
			id BIGSERIAL PRIMARY KEY,
			group_id INTEGER NOT NULL,
			domain_id INTEGER NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_mailing_lists (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL,
			address TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			moderation_required BOOLEAN NOT NULL DEFAULT false,
			archive_enabled BOOLEAN NOT NULL DEFAULT false,
			subscription_policy TEXT NOT NULL DEFAULT 'closed',
			max_members INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_mailing_list_members (
			id BIGSERIAL PRIMARY KEY,
			list_id INTEGER NOT NULL,
			address TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'subscriber',
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_public_folders (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			owner_mailbox_id INTEGER NOT NULL,
			folder_path TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			read_only BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_public_folder_members (
			id BIGSERIAL PRIMARY KEY,
			folder_id INTEGER NOT NULL,
			mailbox_id INTEGER NOT NULL,
			permission TEXT NOT NULL DEFAULT 'readonly',
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_log_rules (
			id BIGSERIAL PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'journald',
			severity TEXT NOT NULL DEFAULT 'info',
			match_pattern TEXT NOT NULL DEFAULT '',
			destination TEXT NOT NULL DEFAULT '',
			enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMP
		)`,

		// --- Package-specific schemas (trust, tlsmgmt, monitoring, backup, lifecycle) ---

		`CREATE TABLE IF NOT EXISTS coremail_lockouts (
			key TEXT PRIMARY KEY,
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_trust_scores (
			id BIGSERIAL PRIMARY KEY,
			scope TEXT NOT NULL,
			target TEXT NOT NULL,
			score INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS tls_certificates (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			cert_path TEXT NOT NULL DEFAULT '',
			key_path TEXT NOT NULL DEFAULT '',
			common_name TEXT NOT NULL DEFAULT '',
			sans TEXT NOT NULL DEFAULT '',
			issuer TEXT NOT NULL DEFAULT '',
			serial_number TEXT NOT NULL DEFAULT '',
			not_before TIMESTAMP,
			not_after TIMESTAMP,
			fingerprint_sha256 TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'unknown',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS monitoring_alerts (
			id BIGSERIAL PRIMARY KEY,
			category TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			active BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			resolved_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS monitoring_alert_deliveries (
			id BIGSERIAL PRIMARY KEY,
			alert_title TEXT NOT NULL DEFAULT '',
			alert_severity TEXT NOT NULL DEFAULT '',
			alert_category TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS backup_registry (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			completed_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS backup_schedule_config (
			id INTEGER PRIMARY KEY DEFAULT 1,
			enabled BOOLEAN NOT NULL DEFAULT false,
			frequency TEXT NOT NULL DEFAULT 'manual',
			retention_count INTEGER NOT NULL DEFAULT 10,
			last_run_at TIMESTAMP,
			next_run_at TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS upgrade_history (
			id BIGSERIAL PRIMARY KEY,
			from_version TEXT NOT NULL DEFAULT '',
			to_version TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			started_at TIMESTAMP NOT NULL DEFAULT NOW(),
			completed_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS coremail_versions (
			id BIGSERIAL PRIMARY KEY,
			version TEXT NOT NULL DEFAULT '',
			installed_at TIMESTAMP NOT NULL DEFAULT NOW(),
			installed_by TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT ''
		)`,
	}
}

// postgresColumnAdditions returns ALTER TABLE statements for columns
// added after the initial CREATE TABLE. Uses IF NOT EXISTS (PG 9.6+)
// so re-runs are safe.
func postgresColumnAdditions() []string {
	return []string{
		`ALTER TABLE licenses ADD COLUMN IF NOT EXISTS tier TEXT NOT NULL DEFAULT 'smb'`,
		`ALTER TABLE licenses ADD COLUMN IF NOT EXISTS max_domains INTEGER NOT NULL DEFAULT 10`,
		`ALTER TABLE licenses ADD COLUMN IF NOT EXISTS max_mailboxes INTEGER NOT NULL DEFAULT 500`,
		`ALTER TABLE licenses ADD COLUMN IF NOT EXISTS hardware_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE licenses ADD COLUMN IF NOT EXISTS metadata TEXT`,

		`ALTER TABLE feature_flags ADD COLUMN IF NOT EXISTS tier_required TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE feature_flags ADD COLUMN IF NOT EXISTS module_version TEXT NOT NULL DEFAULT '1.0.0'`,
		`ALTER TABLE feature_flags ADD COLUMN IF NOT EXISTS description TEXT`,

		`ALTER TABLE domains ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending'`,
		`ALTER TABLE domains ADD COLUMN IF NOT EXISTS dkim_selector TEXT NOT NULL DEFAULT ''`,

		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS send_limit INTEGER NOT NULL DEFAULT 0`,
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

		// --- Expanded table indexes ---

		// Resellers
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_resellers_email ON resellers (email)`,
		idx("idx_resellers_deleted_at", "resellers", "deleted_at"),

		// LDAP/SSO
		idx("idx_l_dap_configs_tenant_id", "l_dap_configs", "tenant_id"),
		idx("idx_l_dap_configs_deleted_at", "l_dap_configs", "deleted_at"),
		idx("idx_s_s_o_configs_tenant_id", "s_s_o_configs", "tenant_id"),
		idx("idx_s_s_o_configs_deleted_at", "s_s_o_configs", "deleted_at"),

		// Alert configs
		idx("idx_alert_configs_tenant_id", "alert_configs", "tenant_id"),
		idx("idx_alert_configs_deleted_at", "alert_configs", "deleted_at"),

		// Provisioned domains
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_provisioned_domains_domain ON provisioned_domains (domain)`,
		idx("idx_provisioned_domains_deleted_at", "provisioned_domains", "deleted_at"),

		// CoreMail domains
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_coremail_domains_name ON coremail_domains (name)`,
		idx("idx_coremail_domains_tenant_id", "coremail_domains", "tenant_id"),
		idx("idx_coremail_domains_deleted_at", "coremail_domains", "deleted_at"),

		// CoreMail aliases
		idx("idx_coremail_aliases_domain_id", "coremail_aliases", "domain_id"),
		idx("idx_coremail_aliases_from", "coremail_aliases", "from_addr"),

		// CoreMail account classes
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_account_classes_tenant_name ON coremail_account_classes (tenant_id, name)`,
		idx("idx_account_classes_deleted_at", "coremail_account_classes", "deleted_at"),

		// Admin groups
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_admin_groups_tenant_name ON coremail_admin_groups (tenant_id, name)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_admin_group_members_group_user ON coremail_admin_group_members (group_id, user_id)`,
		idx("idx_admin_groups_tenant_id", "coremail_admin_groups", "tenant_id"),

		// ACL rules
		idx("idx_acl_rules_tenant_id", "coremail_acl_rules", "tenant_id"),

		// Quarantine
		idx("idx_quarantine_tenant_id", "coremail_quarantine_index", "tenant_id"),
		idx("idx_quarantine_status", "coremail_quarantine_index", "status"),

		// DKIM
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_dkim_config_domain ON coremail_dkim_config (domain)`,

		// Acceptance rules
		idx("idx_acceptance_rules_tenant_id", "coremail_acceptance_rules", "tenant_id"),
		idx("idx_acceptance_rules_priority", "coremail_acceptance_rules", "priority"),

		// Incoming message rules
		idx("idx_incoming_msg_rules_tenant_id", "coremail_incoming_msg_rules", "tenant_id"),
		idx("idx_incoming_msg_rules_priority", "coremail_incoming_msg_rules", "priority"),

		// Migration sources
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_migration_sources_tenant_name ON coremail_migration_sources (tenant_id, name)`,
		idx("idx_migration_sources_tenant_id", "coremail_migration_sources", "tenant_id"),

		// Backup targets
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_backup_targets_tenant_name ON coremail_backup_targets (tenant_id, name)`,
		idx("idx_backup_targets_tenant_id", "coremail_backup_targets", "tenant_id"),

		// Uploaded certificates
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_uploaded_certs_tenant_name ON coremail_uploaded_certificates (tenant_id, name)`,
		idx("idx_uploaded_certs_tenant_id", "coremail_uploaded_certificates", "tenant_id"),

		// --- Models from MigrateAll ---

		// Module versions
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_module_versions_module_id ON module_versions (module_id)`,
		idx("idx_module_versions_deleted_at", "module_versions", "deleted_at"),

		// Firewall
		idx("idx_firewall_rules_deleted_at", "firewall_rules", "deleted_at"),
		idx("idx_firewall_rules_priority", "firewall_rules", "priority"),
		idx("idx_firewall_logs_deleted_at", "firewall_logs", "deleted_at"),
		idx("idx_firewall_logs_ip", "firewall_logs", "ip"),
		idx("idx_firewall_logs_domain", "firewall_logs", "domain"),

		// Guardian / heal / update
		idx("idx_guardian_logs_deleted_at", "guardian_logs", "deleted_at"),
		idx("idx_guardian_logs_message_id", "guardian_logs", "message_id"),
		idx("idx_heal_histories_deleted_at", "heal_histories", "deleted_at"),
		idx("idx_heal_histories_check_name", "heal_histories", "check_name"),
		idx("idx_update_histories_deleted_at", "update_histories", "deleted_at"),
		idx("idx_update_histories_module_id", "update_histories", "module_id"),

		// --- Admin enterprise v2 group/list/folder/rule tables ---

		// Domain groups
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_domain_groups_tenant_name ON coremail_domain_groups (tenant_id, name)`,
		idx("idx_domain_groups_tenant_id", "coremail_domain_groups", "tenant_id"),
		idx("idx_domain_groups_deleted_at", "coremail_domain_groups", "deleted_at"),
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_domain_group_members_group_domain ON coremail_domain_group_members (group_id, domain_id)`,
		idx("idx_domain_group_members_group_id", "coremail_domain_group_members", "group_id"),

		// Mailing lists
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_mailing_lists_domain_address ON coremail_mailing_lists (domain_id, address)`,
		idx("idx_mailing_lists_domain_id", "coremail_mailing_lists", "domain_id"),
		idx("idx_mailing_lists_deleted_at", "coremail_mailing_lists", "deleted_at"),
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_mailing_list_members_list_address ON coremail_mailing_list_members (list_id, address)`,
		idx("idx_mailing_list_members_list_id", "coremail_mailing_list_members", "list_id"),

		// Public folders
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_public_folders_owner_path ON coremail_public_folders (owner_mailbox_id, folder_path)`,
		idx("idx_public_folders_owner_id", "coremail_public_folders", "owner_mailbox_id"),
		idx("idx_public_folders_deleted_at", "coremail_public_folders", "deleted_at"),
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_public_folder_members_folder_mailbox ON coremail_public_folder_members (folder_id, mailbox_id)`,
		idx("idx_public_folder_members_folder_id", "coremail_public_folder_members", "folder_id"),

		// Log rules
		idx("idx_log_rules_tenant_id", "coremail_log_rules", "tenant_id"),
		idx("idx_log_rules_deleted_at", "coremail_log_rules", "deleted_at"),

		// --- Package-specific schemas ---

		// Trust
		idx("idx_coremail_lockouts_expires_at", "coremail_lockouts", "expires_at"),
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_trust_scores_scope_target ON coremail_trust_scores (scope, target)`,
		idx("idx_trust_scores_scope", "coremail_trust_scores", "scope"),

		// TLS management
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_tls_certificates_fingerprint ON tls_certificates (fingerprint_sha256)`,
		idx("idx_tls_certificates_status", "tls_certificates", "status"),

		// Monitoring
		idx("idx_monitoring_alerts_category", "monitoring_alerts", "category"),
		idx("idx_monitoring_alerts_severity", "monitoring_alerts", "severity"),
		idx("idx_monitoring_alerts_active", "monitoring_alerts", "active"),
		idx("idx_monitoring_alerts_created_at", "monitoring_alerts", "created_at"),
		idx("idx_monitoring_alert_deliveries_created_at", "monitoring_alert_deliveries", "created_at"),
		idx("idx_monitoring_alert_deliveries_provider", "monitoring_alert_deliveries", "provider"),

		// Backup
		idx("idx_backup_registry_status", "backup_registry", "status"),
		idx("idx_backup_registry_created_at", "backup_registry", "created_at"),
		idx("idx_backup_schedule_enabled", "backup_schedule_config", "enabled"),

		// Lifecycle
		idx("idx_upgrade_history_status", "upgrade_history", "status"),
		idx("idx_upgrade_history_started_at", "upgrade_history", "started_at"),
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_coremail_versions_version ON coremail_versions (version)`,
		idx("idx_coremail_versions_installed_at", "coremail_versions", "installed_at"),
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
		"licenses", "feature_flags", "module_versions", "tenants", "users", "domains",
		"mailboxes", "api_keys", "sessions", "coremail_audit",
		"security_events", "mfa_recovery_codes", "firewall_rules",
		"firewall_logs", "guardian_logs", "heal_histories", "update_histories",
		"coremail_mailboxes", "coremail_folders", "coremail_messages",
		"coremail_attachments", "coremail_queue", "coremail_delivery_attempts",
		"resellers", "l_dap_configs", "s_s_o_configs", "alert_configs",
		"provisioned_domains", "coremail_domains", "coremail_aliases",
		"coremail_account_classes", "coremail_admin_groups",
		"coremail_admin_group_members", "coremail_domain_groups",
		"coremail_domain_group_members", "coremail_mailing_lists",
		"coremail_mailing_list_members", "coremail_public_folders",
		"coremail_public_folder_members", "coremail_acl_rules",
		"coremail_log_rules", "coremail_quarantine_index",
		"coremail_dkim_config", "coremail_acceptance_rules",
		"coremail_incoming_msg_rules", "coremail_migration_sources",
		"coremail_migration_source_secrets", "coremail_backup_targets",
		"coremail_backup_target_secrets", "coremail_uploaded_certificates",
		"coremail_lockouts", "coremail_trust_scores", "tls_certificates",
		"monitoring_alerts", "monitoring_alert_deliveries", "backup_registry",
		"backup_schedule_config", "upgrade_history", "coremail_versions",
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
