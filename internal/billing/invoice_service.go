package billing

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type InvoiceService struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewInvoiceService(db *sql.DB) *InvoiceService {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &InvoiceService{db: db, dialect: dialect}
}

type InvoiceRecord struct {
	ID                uint       `json:"id"`
	TenantID          uint       `json:"tenant_id"`
	SubscriptionID    *uint      `json:"subscription_id,omitempty"`
	Provider          string     `json:"provider"`
	ProviderInvoiceID string     `json:"provider_invoice_id"`
	InvoiceNumber     string     `json:"invoice_number"`
	Currency          string     `json:"currency"`
	Subtotal          int64      `json:"subtotal"`
	Tax               int64      `json:"tax"`
	Total             int64      `json:"total"`
	AmountPaid        int64      `json:"amount_paid"`
	AmountDue         int64      `json:"amount_due"`
	Status            string     `json:"status"`
	PeriodStart       *time.Time `json:"period_start,omitempty"`
	PeriodEnd         *time.Time `json:"period_end,omitempty"`
	IssuedAt          *time.Time `json:"issued_at,omitempty"`
	DueAt             *time.Time `json:"due_at,omitempty"`
	PaidAt            *time.Time `json:"paid_at,omitempty"`
	HostedInvoiceURL  string     `json:"hosted_invoice_url,omitempty"`
	PDFURL            string     `json:"pdf_url,omitempty"`
	ProviderEventCreatedAt *time.Time `json:"provider_event_created_at,omitempty"`
	ProviderEventID        string     `json:"provider_event_id,omitempty"`
	ProviderUpdatedAt      *time.Time `json:"provider_updated_at,omitempty"`
}

type InvoiceFilter struct {
	TenantID uint
	Status   string
	Before   *time.Time
	After    *time.Time
	Limit    int
	Offset   int
}

func (s *InvoiceService) UpsertFromProviderEvent(ctx context.Context, inv *InvoiceRecord, eventCreatedAt *time.Time, eventID string) (*InvoiceRecord, error) {
	prov := NormalizeProvider(inv.Provider)
	if prov == "" {
		return nil, fmt.Errorf("invoice provider is required")
	}
	if inv.ProviderInvoiceID == "" {
		return nil, fmt.Errorf("provider invoice id is required")
	}
	if inv.TenantID == 0 {
		return nil, fmt.Errorf("tenant id is required")
	}

	d := s.dialect
	now := time.Now().UTC()

	// Try to get existing invoice to check ordering.
	existing, err := s.getExistingByProvider(ctx, prov, inv.ProviderInvoiceID)
	if err == nil && existing != nil {
		// Out-of-order protection: if the incoming event is older than what we already
		// have, skip the update but report success (idempotent).
		if eventCreatedAt != nil && existing.ProviderEventCreatedAt != nil {
			if eventCreatedAt.Before(*existing.ProviderEventCreatedAt) {
				// Older event — preserve existing state, especially if already paid.
				return existing, nil
			}
		}
		// Paid state protection: never regress from paid unless explicit void/uncollectible.
		if existing.Status == "paid" && inv.Status != "void" && inv.Status != "uncollectible" {
			// Keep paid status, just update amounts/payment info.
			inv.Status = "paid"
		}
		// Update the existing record.
		_, err := s.db.ExecContext(ctx,
			`UPDATE invoices SET updated_at = `+d.Placeholder(1)+`,
			status = `+d.Placeholder(2)+`, total = `+d.Placeholder(3)+`,
			amount_paid = `+d.Placeholder(4)+`, amount_due = `+d.Placeholder(5)+`,
			subtotal = `+d.Placeholder(6)+`, tax = `+d.Placeholder(7)+`,
			currency = `+d.Placeholder(8)+`, invoice_number = `+d.Placeholder(9)+`,
			subscription_id = `+d.Placeholder(10)+`,
			period_start = `+d.Placeholder(11)+`, period_end = `+d.Placeholder(12)+`,
			issued_at = `+d.Placeholder(13)+`, due_at = `+d.Placeholder(14)+`,
			paid_at = `+d.Placeholder(15)+`,
			hosted_invoice_url = `+d.Placeholder(16)+`, pdf_url = `+d.Placeholder(17)+`,
			provider_event_created_at = `+d.Placeholder(18)+`,
			provider_event_id = `+d.Placeholder(19)+`,
			provider_updated_at = `+d.Placeholder(20)+`
			WHERE provider = `+d.Placeholder(21)+` AND provider_invoice_id = `+d.Placeholder(22)+` AND tenant_id = `+d.Placeholder(23),
			now, inv.Status, inv.Total, inv.AmountPaid, inv.AmountDue,
			inv.Subtotal, inv.Tax, inv.Currency, inv.InvoiceNumber,
			inv.SubscriptionID, inv.PeriodStart, inv.PeriodEnd, inv.IssuedAt, inv.DueAt,
			inv.PaidAt, inv.HostedInvoiceURL, inv.PDFURL,
			eventCreatedAt, eventID, now,
			prov, inv.ProviderInvoiceID, inv.TenantID)
		if err != nil {
			return nil, fmt.Errorf("update invoice: %w", err)
		}
		inv.ID = existing.ID
		return inv, nil
	}

	// Insert new.
	var id uint
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO invoices (created_at, updated_at, tenant_id, subscription_id, provider,
		provider_invoice_id, invoice_number, currency, subtotal, tax, total,
		amount_paid, amount_due, status, period_start, period_end, issued_at,
		due_at, paid_at, hosted_invoice_url, pdf_url,
		provider_event_created_at, provider_event_id, provider_updated_at)
		VALUES (`+d.Placeholders(24)+`)
		RETURNING id`,
		now, now, inv.TenantID, inv.SubscriptionID, prov,
		inv.ProviderInvoiceID, inv.InvoiceNumber, inv.Currency, inv.Subtotal, inv.Tax, inv.Total,
		inv.AmountPaid, inv.AmountDue, inv.Status, inv.PeriodStart, inv.PeriodEnd, inv.IssuedAt,
		inv.DueAt, inv.PaidAt, inv.HostedInvoiceURL, inv.PDFURL,
		eventCreatedAt, eventID, now,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert invoice: %w", err)
	}
	inv.ID = id
	return inv, nil
}

func (s *InvoiceService) getExistingByProvider(ctx context.Context, provider, providerID string) (*InvoiceRecord, error) {
	var inv InvoiceRecord
	var eventCreated sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, status, provider_event_created_at
		FROM invoices WHERE provider = `+s.dialect.Placeholder(1)+` AND provider_invoice_id = `+s.dialect.Placeholder(2),
		provider, providerID,
	).Scan(&inv.ID, &inv.TenantID, &inv.Status, &eventCreated)
	if err != nil {
		return nil, err
	}
	if eventCreated.Valid {
		inv.ProviderEventCreatedAt = &eventCreated.Time
	}
	return &inv, nil
}

func (s *InvoiceService) GetByProviderInvoice(ctx context.Context, provider, providerID string) (*InvoiceRecord, error) {
	prov := NormalizeProvider(provider)
	var inv InvoiceRecord
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, subscription_id, provider, provider_invoice_id,
		invoice_number, currency, subtotal, tax, total, amount_paid, amount_due,
		status, period_start, period_end, issued_at, due_at, paid_at,
		hosted_invoice_url, pdf_url
		FROM invoices WHERE provider = `+s.dialect.Placeholder(1)+` AND provider_invoice_id = `+s.dialect.Placeholder(2),
		prov, providerID,
	).Scan(&inv.ID, &inv.TenantID, &inv.SubscriptionID, &inv.Provider, &inv.ProviderInvoiceID,
		&inv.InvoiceNumber, &inv.Currency, &inv.Subtotal, &inv.Tax, &inv.Total,
		&inv.AmountPaid, &inv.AmountDue, &inv.Status, &inv.PeriodStart, &inv.PeriodEnd,
		&inv.IssuedAt, &inv.DueAt, &inv.PaidAt, &inv.HostedInvoiceURL, &inv.PDFURL)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *InvoiceService) GetTenantInvoice(ctx context.Context, id, tenantID uint) (*InvoiceRecord, error) {
	var inv InvoiceRecord
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, subscription_id, provider, provider_invoice_id,
		invoice_number, currency, subtotal, tax, total, amount_paid, amount_due,
		status, period_start, period_end, issued_at, due_at, paid_at,
		hosted_invoice_url, pdf_url
		FROM invoices WHERE id = `+s.dialect.Placeholder(1)+` AND tenant_id = `+s.dialect.Placeholder(2),
		id, tenantID,
	).Scan(&inv.ID, &inv.TenantID, &inv.SubscriptionID, &inv.Provider, &inv.ProviderInvoiceID,
		&inv.InvoiceNumber, &inv.Currency, &inv.Subtotal, &inv.Tax, &inv.Total,
		&inv.AmountPaid, &inv.AmountDue, &inv.Status, &inv.PeriodStart, &inv.PeriodEnd,
		&inv.IssuedAt, &inv.DueAt, &inv.PaidAt, &inv.HostedInvoiceURL, &inv.PDFURL)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *InvoiceService) ListTenantInvoices(ctx context.Context, filter *InvoiceFilter) ([]InvoiceRecord, int64, error) {
	var where []string
	var args []interface{}
	argNum := 0
	d := s.dialect

	where = append(where, "tenant_id = "+d.Placeholder(argNum+1))
	args = append(args, filter.TenantID)
	argNum++

	if filter.Status != "" {
		where = append(where, "status = "+d.Placeholder(argNum+1))
		args = append(args, filter.Status)
		argNum++
	}
	if filter.After != nil {
		where = append(where, "issued_at >= "+d.Placeholder(argNum+1))
		args = append(args, *filter.After)
		argNum++
	}
	if filter.Before != nil {
		where = append(where, "issued_at <= "+d.Placeholder(argNum+1))
		args = append(args, *filter.Before)
		argNum++
	}

	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	whereClause := " WHERE " + buildWhereClause(where)

	var total int64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM invoices"+whereClause, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `SELECT id, tenant_id, subscription_id, provider, provider_invoice_id,
		invoice_number, currency, subtotal, tax, total, amount_paid, amount_due,
		status, period_start, period_end, issued_at, due_at, paid_at,
		hosted_invoice_url, pdf_url
		FROM invoices` + whereClause + ` ORDER BY issued_at DESC LIMIT ` + d.Placeholder(argNum+1) + ` OFFSET ` + d.Placeholder(argNum+2)
	dataArgs := append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	invoices := make([]InvoiceRecord, 0)
	for rows.Next() {
		var inv InvoiceRecord
		if err := rows.Scan(&inv.ID, &inv.TenantID, &inv.SubscriptionID, &inv.Provider, &inv.ProviderInvoiceID,
			&inv.InvoiceNumber, &inv.Currency, &inv.Subtotal, &inv.Tax, &inv.Total,
			&inv.AmountPaid, &inv.AmountDue, &inv.Status, &inv.PeriodStart, &inv.PeriodEnd,
			&inv.IssuedAt, &inv.DueAt, &inv.PaidAt, &inv.HostedInvoiceURL, &inv.PDFURL); err != nil {
			return nil, 0, err
		}
		invoices = append(invoices, inv)
	}
	return invoices, total, rows.Err()
}

func buildWhereClause(conditions []string) string {
	result := conditions[0]
	for _, c := range conditions[1:] {
		result += " AND " + c
	}
	return result
}
