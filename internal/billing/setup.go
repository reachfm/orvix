package billing

import "database/sql"

func CreateTables(db *sql.DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS plans (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			description TEXT DEFAULT '',
			price_monthly INTEGER NOT NULL DEFAULT 0,
			price_yearly INTEGER NOT NULL DEFAULT 0,
			max_domains INTEGER NOT NULL DEFAULT 1,
			max_mailboxes INTEGER NOT NULL DEFAULT 5,
			storage_mb INTEGER NOT NULL DEFAULT 1024,
			send_limit_day INTEGER NOT NULL DEFAULT 500,
			features TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL UNIQUE,
			plan_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'trialing',
			billing_interval TEXT NOT NULL DEFAULT 'monthly',
			trial_ends_at DATETIME,
			current_period_start DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			current_period_end DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			cancelled_at DATETIME,
			past_due_since DATETIME,
			grace_period_ends_at DATETIME,
			suspended_at DATETIME,
			storage_mb INTEGER NOT NULL DEFAULT 1024,
			send_limit_day INTEGER NOT NULL DEFAULT 500,
			provider TEXT DEFAULT '',
			provider_sub_id TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS usage_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			period_start DATETIME NOT NULL,
			period_end DATETIME NOT NULL,
			mailboxes_used INTEGER NOT NULL DEFAULT 0,
			domains_used INTEGER NOT NULL DEFAULT 0,
			storage_used_mb INTEGER NOT NULL DEFAULT 0,
			emails_sent INTEGER NOT NULL DEFAULT 0,
			emails_received INTEGER NOT NULL DEFAULT 0,
			api_calls INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tenant_id, period_start)
		)`,
		`CREATE TABLE IF NOT EXISTS webhook_events (
			id TEXT PRIMARY KEY,
			provider TEXT DEFAULT '',
			event_type TEXT DEFAULT '',
			provider_sub_id TEXT DEFAULT '',
			raw_payload BLOB,
			signature TEXT DEFAULT '',
			received_at DATETIME NOT NULL,
			processed_at DATETIME,
			processing_error TEXT DEFAULT '',
			idempotency_key TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS org_invitations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			organization_id INTEGER NOT NULL,
			inviter_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			status TEXT NOT NULL DEFAULT 'pending',
			expires_at DATETIME NOT NULL,
			accepted_at DATETIME,
			revoked_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS org_suspensions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			organization_id INTEGER NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			suspended_by INTEGER NOT NULL,
			note TEXT DEFAULT '',
			suspended_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			reactivated_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS org_deletions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			organization_id INTEGER NOT NULL,
			requested_by INTEGER NOT NULL,
			state TEXT NOT NULL DEFAULT 'deletion_requested',
			retention_expires_at DATETIME,
			requested_at DATETIME NOT NULL,
			confirmed_at DATETIME,
			cancelled_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS domain_ownership (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL UNIQUE,
			token_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			token_generated_at DATETIME NOT NULL,
			token_rotated_at DATETIME,
			verified_at DATETIME,
			last_check_at DATETIME,
			last_error TEXT DEFAULT '',
			check_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS abuse_send_counts (
			day_key TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			emails_sent INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS abuse_bounce_counts (
			day_key TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			bounce_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS abuse_signals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			mailbox_id INTEGER,
			signal_type TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT 'info',
			description TEXT DEFAULT '',
			metadata TEXT DEFAULT '',
			detected_at DATETIME NOT NULL,
			acknowledged_at DATETIME,
			resolved_at DATETIME,
			resolved_by INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
	}
	return nil
}

func Initialize(db *sql.DB) (*Service, *UsageService, *QuotaService, *WebhookService, *Scheduler, error) {
	if err := CreateTables(db); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	usageSvc := NewUsageService(db)
	quotaSvc := NewQuotaService(db, svc)
	webhookSvc := NewWebhookService(db)
	scheduler := NewScheduler(db, svc)
	return svc, usageSvc, quotaSvc, webhookSvc, scheduler, nil
}
