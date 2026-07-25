package billing

import (
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

func newCreditLedgerTestDB(t *testing.T) *CreditLedgerService {
	t.Helper()
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
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})

	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	svc, err := NewCreditLedgerService(sqlDB)
	if err != nil {
		t.Fatalf("NewCreditLedgerService: %v", err)
	}
	return svc
}

func TestCreditLedger_AddCreditEntry(t *testing.T) {
	svc := newCreditLedgerTestDB(t)
	entry, err := svc.AddEntry(1, "credit", 10000, "Initial credit", "signup", 0, 1, false)
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if entry.BalanceAfter != 10000 {
		t.Fatalf("expected balance_after 10000, got %d", entry.BalanceAfter)
	}
	if entry.EntryType != "credit" {
		t.Fatalf("expected entry_type credit, got %s", entry.EntryType)
	}
}

func TestCreditLedger_BalanceAfterMultipleCredits(t *testing.T) {
	svc := newCreditLedgerTestDB(t)

	svc.AddEntry(1, "credit", 5000, "Credit 1", "", 0, 1, false)
	svc.AddEntry(1, "credit", 3000, "Credit 2", "", 0, 1, false)

	bal, err := svc.GetBalance(1)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if bal != 8000 {
		t.Fatalf("expected balance 8000, got %d", bal)
	}
}

func TestCreditLedger_DebitWithEnforceBalance(t *testing.T) {
	svc := newCreditLedgerTestDB(t)

	svc.AddEntry(1, "credit", 10000, "Initial", "", 0, 1, false)
	svc.AddEntry(1, "debit", -3000, "Spend", "", 0, 1, true)

	bal, _ := svc.GetBalance(1)
	if bal != 7000 {
		t.Fatalf("expected balance 7000 after debit, got %d", bal)
	}
}

func TestCreditLedger_InsufficientCreditRejected(t *testing.T) {
	svc := newCreditLedgerTestDB(t)

	svc.AddEntry(1, "credit", 1000, "Initial", "", 0, 1, false)
	_, err := svc.AddEntry(1, "debit", -5000, "Overspend", "", 0, 1, true)
	if err != ErrInsufficientCredit {
		t.Fatalf("expected ErrInsufficientCredit, got %v", err)
	}

	// Balance unchanged.
	bal, _ := svc.GetBalance(1)
	if bal != 1000 {
		t.Fatalf("expected balance 1000 after rejected debit, got %d", bal)
	}
}

func TestCreditLedger_NoEnforceBalanceAllowsOverdraft(t *testing.T) {
	svc := newCreditLedgerTestDB(t)

	entry, err := svc.AddEntry(1, "debit", -5000, "Overdraft", "", 0, 1, false)
	if err != nil {
		t.Fatalf("AddEntry (no enforce): %v", err)
	}
	if entry.BalanceAfter != -5000 {
		t.Fatalf("expected balance_after -5000 (overdraft), got %d", entry.BalanceAfter)
	}
}

func TestCreditLedger_InvalidEntryTypeRejected(t *testing.T) {
	svc := newCreditLedgerTestDB(t)
	_, err := svc.AddEntry(1, "invalid_type", 100, "Bad", "", 0, 1, false)
	if err == nil {
		t.Fatal("expected error for invalid entry type, got nil")
	}
}

func TestCreditLedger_ListEntriesPaginated(t *testing.T) {
	svc := newCreditLedgerTestDB(t)
	for i := 0; i < 5; i++ {
		svc.AddEntry(1, "credit", 1000, "test", "", 0, 1, false)
	}

	entries, err := svc.ListEntries(1, 3, 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	entries2, err := svc.ListEntries(1, 3, 3)
	if err != nil {
		t.Fatalf("ListEntries offset: %v", err)
	}
	if len(entries2) != 2 {
		t.Fatalf("expected 2 entries on page 2, got %d", len(entries2))
	}
}

func TestCreditLedger_TenantIsolation(t *testing.T) {
	svc := newCreditLedgerTestDB(t)

	svc.AddEntry(1, "credit", 10000, "Tenant A", "", 0, 1, false)
	svc.AddEntry(2, "credit", 5000, "Tenant B", "", 0, 1, false)

	balA, _ := svc.GetBalance(1)
	balB, _ := svc.GetBalance(2)

	if balA != 10000 {
		t.Fatalf("tenant A expected 10000, got %d", balA)
	}
	if balB != 5000 {
		t.Fatalf("tenant B expected 5000, got %d", balB)
	}
}

func TestCreditLedger_ReferenceInfoStored(t *testing.T) {
	svc := newCreditLedgerTestDB(t)
	svc.AddEntry(1, "credit", 5000, "Invoice payment", "invoice", 42, 1, false)

	entries, _ := svc.ListEntries(1, 10, 0)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ReferenceType != "invoice" {
		t.Fatalf("expected reference_type invoice, got %s", entries[0].ReferenceType)
	}
	if entries[0].ReferenceID != 42 {
		t.Fatalf("expected reference_id 42, got %d", entries[0].ReferenceID)
	}
}

func TestCreditLedger_ZeroBalanceOnEmpty(t *testing.T) {
	svc := newCreditLedgerTestDB(t)
	bal, err := svc.GetBalance(999)
	if err != nil {
		t.Fatalf("GetBalance on empty: %v", err)
	}
	if bal != 0 {
		t.Fatalf("expected balance 0 for empty ledger, got %d", bal)
	}
}

func TestCreditLedger_ConcurrentCredits(t *testing.T) {
	svc := newCreditLedgerTestDB(t)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			svc.AddEntry(1, "credit", 1000, "concurrent", "", 0, 1, false)
			done <- true
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	bal, _ := svc.GetBalance(1)
	if bal != 10000 {
		t.Fatalf("expected balance 10000 after 10 concurrent credits, got %d", bal)
	}
}

func TestCreditLedger_BalanceAtPointInTime(t *testing.T) {
	svc := newCreditLedgerTestDB(t)

	svc.AddEntry(1, "credit", 1000, "C1", "", 0, 1, false)
	svc.AddEntry(1, "credit", 2000, "C2", "", 0, 1, false)
	svc.AddEntry(1, "debit", -500, "D1", "", 0, 1, false)

	entries, _ := svc.ListEntries(1, 100, 0)
	if len(entries) < 3 {
		t.Fatalf("expected at least 3 entries, got %d", len(entries))
	}

	lastEntry := entries[len(entries)-1]
	if lastEntry.BalanceAfter != 1000 {
		t.Fatalf("expected balance_after for first entry to be 1000, got %d", lastEntry.BalanceAfter)
	}
}
