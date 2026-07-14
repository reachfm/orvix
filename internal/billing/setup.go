package billing

import "database/sql"

func CreateTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS plans (
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
		);
		CREATE TABLE IF NOT EXISTS subscriptions (
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
		);
		CREATE TABLE IF NOT EXISTS usage_records (
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
		);
	`)
	return err
}

func Initialize(db *sql.DB) (*Service, *UsageService, *QuotaService, error) {
	if err := CreateTables(db); err != nil {
		return nil, nil, nil, err
	}
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		return nil, nil, nil, err
	}
	usageSvc := NewUsageService(db)
	quotaSvc := NewQuotaService(db, svc)
	return svc, usageSvc, quotaSvc, nil
}
