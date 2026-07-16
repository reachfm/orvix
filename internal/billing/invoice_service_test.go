package billing

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testInvoiceStore(t *testing.T) *InvoiceService {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// Create invoices table.
	db.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS invoices (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		tenant_id INTEGER NOT NULL,
		subscription_id INTEGER,
		provider TEXT NOT NULL DEFAULT '',
		provider_invoice_id TEXT,
		invoice_number TEXT,
		currency TEXT NOT NULL DEFAULT 'usd',
		subtotal INTEGER NOT NULL DEFAULT 0,
		tax INTEGER NOT NULL DEFAULT 0,
		total INTEGER NOT NULL DEFAULT 0,
		amount_paid INTEGER NOT NULL DEFAULT 0,
		amount_due INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'draft',
		period_start DATETIME,
		period_end DATETIME,
		issued_at DATETIME,
		due_at DATETIME,
		paid_at DATETIME,
		hosted_invoice_url TEXT,
		pdf_url TEXT,
		provider_event_created_at DATETIME,
		provider_event_id TEXT NOT NULL DEFAULT '',
		provider_updated_at DATETIME
	)`)
	db.ExecContext(context.Background(), `CREATE UNIQUE INDEX IF NOT EXISTS uq_invoices_provider ON invoices(provider, provider_invoice_id)`)

	return NewInvoiceService(db)
}

func TestInvoiceCreateFromProviderEvent(t *testing.T) {
	svc := testInvoiceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	inv := &InvoiceRecord{
		TenantID:          1,
		Provider:          "stripe",
		ProviderInvoiceID: "in_123",
		InvoiceNumber:     "INV-001",
		Currency:          "usd",
		Subtotal:          10000,
		Tax:               1000,
		Total:             11000,
		AmountPaid:        0,
		AmountDue:         11000,
		Status:            "open",
	}

	result, err := svc.UpsertFromProviderEvent(ctx, inv, &now, "evt_001")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if result.ID == 0 {
		t.Fatal("expected invoice ID")
	}

	// Verify it exists.
	got, err := svc.GetTenantInvoice(ctx, result.ID, 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Total != 11000 {
		t.Errorf("total: want 11000, got %d", got.Total)
	}
}

func TestInvoiceUpdateFromNonOlderEventIsIgnored(t *testing.T) {
	svc := testInvoiceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)

	// Create with recent timestamp.
	inv := &InvoiceRecord{TenantID: 1, Provider: "stripe", ProviderInvoiceID: "in_456", Total: 5000, Status: "paid"}
	_, err := svc.UpsertFromProviderEvent(ctx, inv, &now, "evt_paid")
	if err != nil {
		t.Fatal(err)
	}

	// Try to update with OLDER timestamp — should be ignored.
	oldInv := &InvoiceRecord{TenantID: 1, Provider: "stripe", ProviderInvoiceID: "in_456", Total: 9999, Status: "open"}
	_, err = svc.UpsertFromProviderEvent(ctx, oldInv, &past, "evt_old")
	if err != nil {
		t.Fatal(err)
	}

	// Verify still paid with original total.
	got, _ := svc.GetTenantInvoice(ctx, 1, 1)
	if got.Status != "paid" {
		t.Fatalf("expected paid after older event, got %s", got.Status)
	}
	if got.Total != 5000 {
		t.Fatalf("expected total 5000, got %d", got.Total)
	}
}

func TestInvoicePaidNeverRegresses(t *testing.T) {
	svc := testInvoiceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	later := now.Add(time.Hour)

	// Create paid invoice.
	inv := &InvoiceRecord{TenantID: 2, Provider: "stripe", ProviderInvoiceID: "in_789", Total: 5000, Status: "paid"}
	svc.UpsertFromProviderEvent(ctx, inv, &now, "evt_paid")

	// Try open with NEWER timestamp but invoice is already paid.
	openInv := &InvoiceRecord{TenantID: 2, Provider: "stripe", ProviderInvoiceID: "in_789", Total: 5000, Status: "open"}
	svc.UpsertFromProviderEvent(ctx, openInv, &later, "evt_later")

	got, _ := svc.GetTenantInvoice(ctx, 1, 2)
	if got.Status != "paid" {
		t.Fatalf("expected paid to remain paid, got %s", got.Status)
	}
}

func TestInvoiceTenantIsolation(t *testing.T) {
	svc := testInvoiceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	inv := &InvoiceRecord{TenantID: 1, Provider: "stripe", ProviderInvoiceID: "in_iso", Total: 100, Status: "open"}
	result, _ := svc.UpsertFromProviderEvent(ctx, inv, &now, "evt_iso")

	// Tenant 2 cannot read Tenant 1 invoice.
	_, err := svc.GetTenantInvoice(ctx, result.ID, 2)
	if err == nil {
		t.Fatal("tenant 2 should not see tenant 1 invoice")
	}
}

func TestInvoiceListPagination(t *testing.T) {
	svc := testInvoiceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		inv := &InvoiceRecord{
			TenantID:          1,
			Provider:          "stripe",
			ProviderInvoiceID: fmt.Sprintf("in_page_%d", i),
			Total:             1000 + int64(i*100),
			Status:            "open",
		}
		svc.UpsertFromProviderEvent(ctx, inv, &now, fmt.Sprintf("evt_%d", i))
	}

	list, total, err := svc.ListTenantInvoices(ctx, &InvoiceFilter{TenantID: 1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("total: want 5, got %d", total)
	}
	if len(list) != 2 {
		t.Fatalf("page: want 2, got %d", len(list))
	}
}

func TestInvoiceStatusFilter(t *testing.T) {
	svc := testInvoiceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	svc.UpsertFromProviderEvent(ctx, &InvoiceRecord{TenantID: 1, Provider: "stripe", ProviderInvoiceID: "in_p", Total: 100, Status: "paid"}, &now, "evt_p")
	svc.UpsertFromProviderEvent(ctx, &InvoiceRecord{TenantID: 1, Provider: "stripe", ProviderInvoiceID: "in_o", Total: 200, Status: "open"}, &now, "evt_o")

	list, total, _ := svc.ListTenantInvoices(ctx, &InvoiceFilter{TenantID: 1, Status: "paid"})
	if total != 1 {
		t.Fatalf("paid filter: want 1, got %d", total)
	}
	_ = list
}

func TestInvoiceEmptyTenantReturnsEmpty(t *testing.T) {
	svc := testInvoiceStore(t)
	ctx := context.Background()

	list, total, _ := svc.ListTenantInvoices(ctx, &InvoiceFilter{TenantID: 999})
	if total != 0 {
		t.Fatalf("empty tenant: want 0, got %d", total)
	}
	if list == nil {
		t.Fatal("list should be non-nil empty slice")
	}
}
