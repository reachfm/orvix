package queue

// Tables returns all DDL statements for the queue engine.
func Tables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
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
			next_attempt_at DATETIME,
			last_attempt_at DATETIME,
			last_error TEXT NOT NULL DEFAULT '',
			delivery_mode TEXT NOT NULL DEFAULT 'remote_smtp',
			remote_host TEXT NOT NULL DEFAULT '',
			remote_ip TEXT NOT NULL DEFAULT '',
			tls_used INTEGER NOT NULL DEFAULT 0,
			-- Remote SMTP diagnostics. Populated by
			-- the delivery worker when the entry
			-- transitions to deferred / bounced /
			-- dead_letter. The admin queue UI
			-- shows these verbatim — they are the
			-- "why" of the row's terminal state.
			last_status_code INTEGER NOT NULL DEFAULT 0,
			last_enhanced_code TEXT NOT NULL DEFAULT '',
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_expires_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME,
			dead_letter_at DATETIME,
			deleted_at DATETIME
		)`,
	}
}

// Indexes returns all index DDL for the queue engine.
func Indexes() []string {
	return []string{
		// Primary lookup for leasing: pending + deferred jobs, ordered by priority and next attempt.
		`CREATE INDEX IF NOT EXISTS idx_queue_lease
			ON coremail_queue(status, next_attempt_at, priority)`,
		// Lookup by tenant (multi-tenant isolation).
		`CREATE INDEX IF NOT EXISTS idx_queue_tenant
			ON coremail_queue(tenant_id, status, created_at)`,
		// Lookup by domain.
		`CREATE INDEX IF NOT EXISTS idx_queue_domain
			ON coremail_queue(domain_id, status, created_at)`,
		// Lookup by recipient domain (anti-spam, rate limiting).
		`CREATE INDEX IF NOT EXISTS idx_queue_recipient_domain
			ON coremail_queue(recipient_domain, status, created_at)`,
		// Lookup by message.
		`CREATE INDEX IF NOT EXISTS idx_queue_message
			ON coremail_queue(message_id)`,
		// Expired lease recovery.
		`CREATE INDEX IF NOT EXISTS idx_queue_expired_leases
			ON coremail_queue(status, lease_expires_at) WHERE status = 'leased'`,
		// Cleanup old completed/delivered entries.
		`CREATE INDEX IF NOT EXISTS idx_queue_completed
			ON coremail_queue(status, completed_at)`,
		// Dead letter operations.
		`CREATE INDEX IF NOT EXISTS idx_queue_dead_letter
			ON coremail_queue(status, dead_letter_at) WHERE status = 'dead_letter'`,
		// Pagination by tenant+status.
		`CREATE INDEX IF NOT EXISTS idx_queue_list
			ON coremail_queue(tenant_id, status, id)`,
	}
}
