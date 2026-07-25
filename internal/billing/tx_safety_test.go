package billing

import (
	"testing"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

func TestTxSafety_CreditLedgerRollsBackOnFailure(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_tx.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}
	sqlDB, _ := db.DB()

	// Start a transaction, add a ledger entry, then force a rollback.
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	// Insert a ledger entry manually in the transaction.
	_, err = tx.Exec(
		"INSERT INTO credit_ledger (tenant_id, entry_type, amount, balance_after, description, created_at) VALUES (?, ?, ?, ?, ?, datetime('now'))",
		1, "credit", 5000, 5000, "should not persist",
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Force rollback.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Verify no entry was persisted.
	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM credit_ledger WHERE tenant_id = 1").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatal("credit ledger entry persisted after rollback — expected 0")
	}
}

func TestTxSafety_CreditLedgerCommitPersists(t *testing.T) {
	// Positive control: verify commit works.
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_tx2.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}
	sqlDB, _ := db.DB()

	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, err = tx.Exec(
		"INSERT INTO credit_ledger (tenant_id, entry_type, amount, balance_after, description, created_at) VALUES (?, ?, ?, ?, ?, datetime('now'))",
		1, "credit", 5000, 5000, "should persist",
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM credit_ledger WHERE tenant_id = 1").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 persisted entry, got %d", count)
	}
}

func TestTxSafety_SubscriptionAndLedgerRollback(t *testing.T) {
	// Simulate a subscription creation + ledger credit that must be atomic.
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_tx3.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}
	sqlDB, _ := db.DB()

	// Create the subscriptions table (created by billing.CreateTables,
	// not by models.MigrateAllRaw).
	_, err = sqlDB.Exec(`CREATE TABLE IF NOT EXISTS subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL UNIQUE,
		plan_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'trialing',
		billing_interval TEXT NOT NULL DEFAULT 'monthly',
		trial_ends_at DATETIME,
		current_period_start DATETIME NOT NULL,
		current_period_end DATETIME NOT NULL,
		cancelled_at DATETIME,
		past_due_since DATETIME,
		grace_period_ends_at DATETIME,
		suspended_at DATETIME,
		storage_mb INTEGER NOT NULL DEFAULT 1024,
		send_limit_day INTEGER NOT NULL DEFAULT 500,
		provider TEXT NOT NULL DEFAULT '',
		provider_sub_id TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create subscriptions table: %v", err)
	}

	// Create tenant first.
	_, err = sqlDB.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))",
		"Test Tenant", "test-tenant", "test.example.com", 1)
	if err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	var tenantID int64
	sqlDB.QueryRow("SELECT id FROM tenants WHERE slug = 'test-tenant'").Scan(&tenantID)

	// Begin transaction: create subscription + add ledger credit.
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, err = tx.Exec(
		"INSERT INTO subscriptions (tenant_id, plan_id, status, billing_interval, current_period_start, current_period_end, storage_mb, send_limit_day, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now', '+30 days'), 1024, 500, datetime('now'), datetime('now'))",
		tenantID, "starter", "trialing", "monthly",
	)
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	_, err = tx.Exec(
		"INSERT INTO credit_ledger (tenant_id, entry_type, amount, balance_after, description, created_at) VALUES (?, ?, ?, ?, ?, datetime('now'))",
		tenantID, "credit", 10000, 10000, "signup bonus",
	)
	if err != nil {
		t.Fatalf("insert ledger: %v", err)
	}

	// Force rollback of both.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Verify neither persisted.
	var subCount int
	sqlDB.QueryRow("SELECT COUNT(*) FROM subscriptions WHERE tenant_id = ?", tenantID).Scan(&subCount)
	if subCount != 0 {
		t.Fatal("subscription persisted after rollback")
	}

	var ledgerCount int
	sqlDB.QueryRow("SELECT COUNT(*) FROM credit_ledger WHERE tenant_id = ?", tenantID).Scan(&ledgerCount)
	if ledgerCount != 0 {
		t.Fatal("ledger entry persisted after rollback")
	}
}

func TestTxSafety_ConcurrentDebitAtomicity(t *testing.T) {
	// Prove that concurrent debits do not create inconsistent balances.
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_tx4.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}
	sqlDB, _ := db.DB()
	svc, _ := NewCreditLedgerService(sqlDB)

	// Add initial balance.
	svc.AddEntry(1, "credit", 10000, "initial", "", 0, 1, false)

	// Perform 10 concurrent debits.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			svc.AddEntry(1, "debit", -500, "concurrent", "", 0, 1, true)
			done <- true
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	// Balance should be 10000 - 10*500 = 5000.
	var balance int64
	if err := sqlDB.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM credit_ledger WHERE tenant_id = 1").Scan(&balance); err != nil {
		t.Fatalf("balance: %v", err)
	}
	if balance != 5000 {
		t.Fatalf("expected balance 5000 after 10 concurrent debits, got %d", balance)
	}
}

func TestTxSafety_ReauthGrantDeleteWithinTx(t *testing.T) {
	// Prove that deleting reauth grants within a transaction is atomic.
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_tx5.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}
	sqlDB, _ := db.DB()

	// Insert a grant.
	_, err = sqlDB.Exec(
		"INSERT INTO recent_auth_grants (user_id, session_id, tenant_id, scope, expires_at, created_at) VALUES (?, ?, ?, ?, datetime('now', '+1 hour'), datetime('now'))",
		1, "sess", 1, "test_scope",
	)
	if err != nil {
		t.Fatalf("insert grant: %v", err)
	}

	// Begin tx, delete grant, rollback.
	tx, _ := sqlDB.Begin()
	tx.Exec("DELETE FROM recent_auth_grants WHERE user_id = 1")
	tx.Rollback()

	var count int
	sqlDB.QueryRow("SELECT COUNT(*) FROM recent_auth_grants WHERE user_id = 1").Scan(&count)
	if count != 1 {
		t.Fatal("grant was deleted after rollback — expected 1")
	}
}

func TestTxSafety_ReauthFailureDeleteWithinTx(t *testing.T) {
	// Prove that clearing reauth failures is atomic.
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_tx6.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}
	sqlDB, _ := db.DB()

	// Insert failure records.
	for i := 0; i < 3; i++ {
		_, err := sqlDB.Exec("INSERT INTO reauth_failures (user_id, created_at) VALUES (?, datetime('now'))", 1)
		if err != nil {
			t.Fatalf("insert failure: %v", err)
		}
	}

	tx, _ := sqlDB.Begin()
	tx.Exec("DELETE FROM reauth_failures WHERE user_id = 1")
	tx.Rollback()

	var count int
	sqlDB.QueryRow("SELECT COUNT(*) FROM reauth_failures WHERE user_id = 1").Scan(&count)
	if count != 3 {
		t.Fatalf("expected 3 failures after rollback, got %d", count)
	}
}
