package models

import (
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
)

func TestEnterpriseTablesMigration_SQLite(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_ent_test.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	// Verify new tables exist.
	tables := []string{"credit_ledger", "webhook_subscriptions", "data_retention_policies"}
	for _, table := range tables {
		var count int
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count).Error; err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("table %s was not created by MigrateAllRaw", table)
		}
	}
}

func TestCreditLedger_InsertAndBalance(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_ledger_test.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	now := time.Now()
	entries := []struct {
		entryType    string
		amount       int
		balanceAfter int
	}{
		{"credit", 10000, 10000},
		{"credit", 5000, 15000},
		{"debit", 3000, 12000},
		{"adjustment", -2000, 10000},
		{"refund", 1500, 11500},
	}

	for _, e := range entries {
		if err := db.Exec(
			`INSERT INTO credit_ledger (tenant_id, entry_type, amount, balance_after, description, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			1, e.entryType, e.amount, e.balanceAfter, "test", now,
		).Error; err != nil {
			t.Fatalf("insert credit_ledger: %v", err)
		}
	}

	var balance int
	if err := db.Raw("SELECT balance_after FROM credit_ledger WHERE tenant_id = ? ORDER BY id DESC LIMIT 1", 1).Scan(&balance).Error; err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if balance != 11500 {
		t.Fatalf("expected balance 11500, got %d", balance)
	}
}

func TestCreditLedger_EntryTypeValidation(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_ledger_val.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	if err := db.Exec(
		`INSERT INTO credit_ledger (tenant_id, entry_type, amount, balance_after, description, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		1, "invalid_type", 100, 100, "should fail", time.Now(),
	).Error; err == nil {
		t.Fatal("expected CHECK constraint failure for invalid entry_type, got nil")
	}
}

func TestWebhookSubscriptions_CRUD(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_webhook_test.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	now := time.Now()
	if err := db.Exec(
		`INSERT INTO webhook_subscriptions (tenant_id, name, url, events, active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		1, "billing-webhook", "https://example.com/billing-hook",
		"invoice.paid,subscription.updated", 1, now, now,
	).Error; err != nil {
		t.Fatalf("insert webhook_subscriptions: %v", err)
	}

	var count int
	if err := db.Raw("SELECT COUNT(*) FROM webhook_subscriptions WHERE tenant_id = ? AND active = 1", 1).Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active webhook, got %d", count)
	}
}

func TestWebhookSubscriptions_UniqueNamePerTenant(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_webhook_uniq.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	now := time.Now()
	insert := func() error {
		return db.Exec(
			`INSERT INTO webhook_subscriptions (tenant_id, name, url, events, active, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			1, "dup-name", "https://example.com/hook", "test", 1, now, now,
		).Error
	}
	if err := insert(); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insert(); err == nil {
		t.Fatal("expected unique constraint error for duplicate name, got nil")
	}

	// Different tenant should be allowed.
	if err := db.Exec(
		`INSERT INTO webhook_subscriptions (tenant_id, name, url, events, active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		2, "dup-name", "https://example.com/hook2", "test", 1, now, now,
	).Error; err != nil {
		t.Fatalf("same name different tenant: %v", err)
	}
}

func TestDataRetentionPolicies_CRUD(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_retention_test.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	now := time.Now()
	if err := db.Exec(
		`INSERT INTO data_retention_policies (tenant_id, name, description, retention_days, action, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		1, "default-365", "Standard 1-year retention", 365, "delete", now, now,
	).Error; err != nil {
		t.Fatalf("insert data_retention_policies: %v", err)
	}

	var action string
	if err := db.Raw("SELECT action FROM data_retention_policies WHERE tenant_id = ? AND name = ?", 1, "default-365").Scan(&action).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if action != "delete" {
		t.Fatalf("expected action 'delete', got %q", action)
	}
}

func TestDataRetentionPolicies_ActionValidation(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_retention_val.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()

	if err := MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	if err := db.Exec(
		`INSERT INTO data_retention_policies (tenant_id, name, retention_days, action, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		1, "invalid-action", 30, "destroy", time.Now(), time.Now(),
	).Error; err == nil {
		t.Fatal("expected CHECK constraint failure for invalid action, got nil")
	}
}
