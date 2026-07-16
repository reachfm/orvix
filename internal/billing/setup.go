package billing

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/dbdialect"
)

func CreateTables(db *sql.DB) error {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	autoInc := dialect.AutoIncrement()
	ts := dialect.TimestampType()

	// Replace SQLite-specific keywords in DDL templates.
	ddl := func(sql string) string {
		sql = strings.ReplaceAll(sql, "__AUTOINC__", autoInc)
		sql = strings.ReplaceAll(sql, "__TS__", ts)
		sql = strings.ReplaceAll(sql, "__BLOB__", dialect.BlobType())
		return sql
	}

	templates := []string{
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
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP,
			updated_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id __AUTOINC__,
			tenant_id INTEGER NOT NULL UNIQUE,
			plan_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'trialing',
			billing_interval TEXT NOT NULL DEFAULT 'monthly',
			trial_ends_at __TS__,
			current_period_start __TS__ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			current_period_end __TS__ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			cancelled_at __TS__,
			past_due_since __TS__,
			grace_period_ends_at __TS__,
			suspended_at __TS__,
			storage_mb INTEGER NOT NULL DEFAULT 1024,
			send_limit_day INTEGER NOT NULL DEFAULT 500,
			provider TEXT DEFAULT '',
			provider_sub_id TEXT DEFAULT '',
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP,
			updated_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS usage_records (
			id __AUTOINC__,
			tenant_id INTEGER NOT NULL,
			period_start __TS__ NOT NULL,
			period_end __TS__ NOT NULL,
			mailboxes_used INTEGER NOT NULL DEFAULT 0,
			domains_used INTEGER NOT NULL DEFAULT 0,
			storage_used_mb INTEGER NOT NULL DEFAULT 0,
			emails_sent INTEGER NOT NULL DEFAULT 0,
			emails_received INTEGER NOT NULL DEFAULT 0,
			api_calls INTEGER NOT NULL DEFAULT 0,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tenant_id, period_start)
		)`,
		`CREATE TABLE IF NOT EXISTS webhook_events (
			row_id __AUTOINC__,
			id TEXT NOT NULL,
			provider TEXT NOT NULL,
			event_type TEXT DEFAULT '',
			provider_sub_id TEXT DEFAULT '',
			raw_payload __BLOB__,
			signature TEXT DEFAULT '',
			received_at __TS__ NOT NULL,
			processed_at __TS__,
			processing_error TEXT DEFAULT '',
			idempotency_key TEXT NOT NULL,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(provider, id),
			UNIQUE(provider, idempotency_key)
		)`,
		`CREATE TABLE IF NOT EXISTS org_invitations (
			id __AUTOINC__,
			organization_id INTEGER NOT NULL,
			inviter_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			status TEXT NOT NULL DEFAULT 'pending',
			expires_at __TS__ NOT NULL,
			accepted_at __TS__,
			revoked_at __TS__,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP,
			updated_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS org_ownership_transfers (
			id __AUTOINC__,
			organization_id INTEGER NOT NULL,
			from_user_id INTEGER NOT NULL,
			to_user_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			expires_at __TS__ NOT NULL,
			accepted_at __TS__,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS org_suspensions (
			id __AUTOINC__,
			organization_id INTEGER NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			suspended_by INTEGER NOT NULL,
			note TEXT DEFAULT '',
			suspended_at __TS__ DEFAULT CURRENT_TIMESTAMP,
			reactivated_at __TS__,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS org_deletions (
			id __AUTOINC__,
			organization_id INTEGER NOT NULL,
			requested_by INTEGER NOT NULL,
			state TEXT NOT NULL DEFAULT 'deletion_requested',
			retention_expires_at __TS__,
			requested_at __TS__ NOT NULL,
			confirmed_at __TS__,
			cancelled_at __TS__,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS domain_ownership (
			id __AUTOINC__,
			domain_id INTEGER NOT NULL UNIQUE,
			token_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			token_generated_at __TS__ NOT NULL,
			token_rotated_at __TS__,
			verified_at __TS__,
			last_check_at __TS__,
			last_error TEXT DEFAULT '',
			check_count INTEGER NOT NULL DEFAULT 0,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP,
			updated_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS abuse_send_counts (
			day_key TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			emails_sent INTEGER NOT NULL DEFAULT 0,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS abuse_bounce_counts (
			day_key TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			bounce_count INTEGER NOT NULL DEFAULT 0,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS abuse_signals (
			id __AUTOINC__,
			tenant_id INTEGER NOT NULL,
			mailbox_id INTEGER,
			signal_type TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT 'info',
			description TEXT DEFAULT '',
			metadata TEXT DEFAULT '',
			detected_at __TS__ NOT NULL,
			acknowledged_at __TS__,
			acknowledged_by INTEGER,
			resolved_at __TS__,
			resolved_by INTEGER,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS send_events (
			event_id TEXT NOT NULL,
			tenant_id INTEGER NOT NULL,
			mailbox_id INTEGER,
			event_type TEXT NOT NULL DEFAULT 'send',
			recipient_count INTEGER NOT NULL DEFAULT 1,
			created_at __TS__ DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (event_id, tenant_id)
		)`,
	}
	for _, t := range templates {
		if _, err := db.Exec(ddl(t)); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}
	return nil
}

// MigrateWebhookEvents migrates old webhook_events schema to the current
// one (surrogate row_id, provider-scoped unique constraints). It is
// idempotent: if the table already has the expected shape (row_id
// column exists), the function returns immediately without data loss.
func MigrateWebhookEvents(db *sql.DB) error {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}

	var migrationNeeded bool
	if dialect.IsPostgres() {
		var colExists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name='webhook_events' AND column_name='row_id')").Scan(&colExists); err != nil {
			return fmt.Errorf("inspect postgres webhook_events schema: %w", err)
		}
		migrationNeeded = !colExists
	} else {
		var tableDDL string
		if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='webhook_events'").Scan(&tableDDL); err != nil {
			return fmt.Errorf("inspect sqlite webhook_events schema: %w", err)
		}
		migrationNeeded = !strings.Contains(tableDDL, "row_id")
	}
	if !migrationNeeded {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if dialect.IsPostgres() {
		if migrationNeeded {
			if _, err := tx.Exec(`
				ALTER TABLE webhook_events DROP CONSTRAINT IF EXISTS webhook_events_pkey CASCADE;
				ALTER TABLE webhook_events ADD COLUMN IF NOT EXISTS row_id BIGSERIAL;
				UPDATE webhook_events SET row_id = DEFAULT WHERE row_id IS NULL;
				ALTER TABLE webhook_events ADD PRIMARY KEY (row_id);
			`); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`
			UPDATE webhook_events
			SET provider = COALESCE(NULLIF(LOWER(TRIM(provider)), ''), 'legacy');
			ALTER TABLE webhook_events ALTER COLUMN provider SET NOT NULL;
			ALTER TABLE webhook_events ALTER COLUMN provider SET DEFAULT 'legacy';
			ALTER TABLE webhook_events DROP CONSTRAINT IF EXISTS webhook_events_id_key;
			ALTER TABLE webhook_events DROP CONSTRAINT IF EXISTS webhook_events_idempotency_key_key;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_events_provider_id
				ON webhook_events (provider, id);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_events_provider_idempotency
				ON webhook_events (provider, idempotency_key);
		`); err != nil {
			return err
		}
	} else if migrationNeeded {
		if _, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS webhook_events_new (
				row_id INTEGER PRIMARY KEY AUTOINCREMENT,
				id TEXT NOT NULL,
				provider TEXT NOT NULL,
				event_type TEXT DEFAULT '',
				provider_sub_id TEXT DEFAULT '',
				raw_payload BLOB,
				signature TEXT DEFAULT '',
				received_at DATETIME NOT NULL,
				processed_at DATETIME,
				processing_error TEXT DEFAULT '',
				idempotency_key TEXT NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(provider, id),
				UNIQUE(provider, idempotency_key)
			);
			INSERT INTO webhook_events_new
				(id, provider, event_type, provider_sub_id, raw_payload, signature,
				received_at, processed_at, processing_error, idempotency_key, created_at)
			SELECT id, COALESCE(NULLIF(LOWER(TRIM(provider)), ''), 'legacy'), event_type, provider_sub_id, raw_payload, signature,
				received_at, processed_at, processing_error, idempotency_key, created_at
			FROM webhook_events
			WHERE true
			ON CONFLICT (provider, id) DO NOTHING;
			DROP TABLE webhook_events;
			ALTER TABLE webhook_events_new RENAME TO webhook_events;
		`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func Initialize(db *sql.DB) (*Service, *UsageService, *QuotaService, *WebhookService, *Scheduler, *SendEnforcer, error) {
	if err := CreateTables(db); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if err := MigrateWebhookEvents(db); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("migrate webhook events: %w", err)
	}
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	usageSvc := NewUsageService(db)
	quotaSvc := NewQuotaService(db, svc)
	webhookSvc := NewWebhookService(db)
	scheduler := NewScheduler(db, svc)
	enforcer := NewSendEnforcer(db, svc, quotaSvc)
	return svc, usageSvc, quotaSvc, webhookSvc, scheduler, enforcer, nil
}
