package billing

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

var (
	ErrInsufficientCredit = errors.New("insufficient credit balance")
	ErrInvalidEntryType   = errors.New("invalid ledger entry type")
)

// LedgerEntry represents an immutable row in the credit_ledger table.
type LedgerEntry struct {
	ID            int64     `json:"id"`
	TenantID      uint      `json:"tenant_id"`
	EntryType     string    `json:"entry_type"`
	Amount        int64     `json:"amount"`
	BalanceAfter  int64     `json:"balance_after"`
	Description   string    `json:"description"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   int64     `json:"reference_id"`
	CreatedBy     uint      `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

// CreditLedgerService provides an append-only ledger for prepaid credit
// tracking. Every entry is immutable and balance is always computed as
// SUM(amount) over a tenant's entries.
type CreditLedgerService struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewCreditLedgerService(db *sql.DB) (*CreditLedgerService, error) {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &CreditLedgerService{db: db, dialect: dialect}, nil
}

// AddEntry appends an immutable ledger entry and returns the new balance.
// For debit entries, it atomically checks that balance >= amount (if
// enforceBalance is true).
func (s *CreditLedgerService) AddEntry(tenantID uint, entryType string, amount int64, description, referenceType string, referenceID int64, createdBy uint, enforceBalance bool) (*LedgerEntry, error) {
	validTypes := map[string]bool{"credit": true, "debit": true, "adjustment": true, "refund": true, "expiration": true}
	if !validTypes[entryType] {
		return nil, ErrInvalidEntryType
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Get current balance (locked via SELECT ... FOR UPDATE on PG,
	// or via transaction serialization on SQLite).
	p := s.dialect.Placeholder
	query := "SELECT COALESCE(SUM(amount), 0) FROM credit_ledger WHERE tenant_id = " + p(1)
	if s.dialect.IsPostgres() {
		query += " FOR UPDATE"
	}
	var balance int64
	err = tx.QueryRow(query, tenantID).Scan(&balance)
	if err != nil {
		return nil, fmt.Errorf("get balance: %w", err)
	}

	newBalance := balance + amount
	if entryType == "debit" && enforceBalance && newBalance < 0 {
		return nil, ErrInsufficientCredit
	}

	now := time.Now()
	if _, err := tx.Exec(
		"INSERT INTO credit_ledger (tenant_id, entry_type, amount, balance_after, description, reference_type, reference_id, created_by, created_at) VALUES ("+
			p(1)+","+p(2)+","+p(3)+","+p(4)+","+p(5)+","+p(6)+","+p(7)+","+p(8)+","+p(9)+")",
		tenantID, entryType, amount, newBalance, description, referenceType, referenceID, createdBy, now,
	); err != nil {
		return nil, fmt.Errorf("insert ledger entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &LedgerEntry{
		TenantID:      tenantID,
		EntryType:     entryType,
		Amount:        amount,
		BalanceAfter:  newBalance,
		Description:   description,
		ReferenceType: referenceType,
		ReferenceID:   referenceID,
		CreatedBy:     createdBy,
		CreatedAt:     now,
	}, nil
}

// GetBalance returns the current credit balance for a tenant.
func (s *CreditLedgerService) GetBalance(tenantID uint) (int64, error) {
	var balance int64
	err := s.db.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM credit_ledger WHERE tenant_id = ?", tenantID).Scan(&balance)
	return balance, err
}

// GetBalanceAt returns the balance at a specific ledger entry index
// (i.e. SUM up to and including entry with the given id).
func (s *CreditLedgerService) GetBalanceAt(tenantID uint, upToEntryID int64) (int64, error) {
	var balance int64
	err := s.db.QueryRow(
		"SELECT balance_after FROM credit_ledger WHERE tenant_id = ? AND id = ?",
		tenantID, upToEntryID,
	).Scan(&balance)
	return balance, err
}

// ListEntries returns paginated ledger entries for a tenant.
func (s *CreditLedgerService) ListEntries(tenantID uint, limit, offset int) ([]LedgerEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(
		"SELECT id, tenant_id, entry_type, amount, balance_after, description, reference_type, reference_id, created_by, created_at FROM credit_ledger WHERE tenant_id = ? ORDER BY id DESC LIMIT ? OFFSET ?",
		tenantID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.EntryType, &e.Amount, &e.BalanceAfter, &e.Description, &e.ReferenceType, &e.ReferenceID, &e.CreatedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
