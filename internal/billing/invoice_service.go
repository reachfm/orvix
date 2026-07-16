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
	ID                     uint       `json:"id"`
	TenantID               uint       `json:"tenant_id"`
	SubscriptionID         *uint      `json:"subscription_id,omitempty"`
	Provider               string     `json:"provider"`
	ProviderInvoiceID      string     `json:"provider_invoice_id"`
	InvoiceNumber          string     `json:"invoice_number"`
	Currency               string     `json:"currency"`
	Subtotal               int64      `json:"subtotal"`
	Tax                    int64      `json:"tax"`
	Total                  int64      `json:"total"`
	AmountPaid             int64      `json:"amount_paid"`
	AmountDue              int64      `json:"amount_due"`
	Status                 string     `json:"status"`
	PeriodStart            *time.Time `json:"period_start,omitempty"`
	PeriodEnd              *time.Time `json:"period_end,omitempty"`
	IssuedAt               *time.Time `json:"issued_at,omitempty"`
	DueAt                  *time.Time `json:"due_at,omitempty"`
	PaidAt                 *time.Time `json:"paid_at,omitempty"`
	HostedInvoiceURL       string     `json:"hosted_invoice_url,omitempty"`
	PDFURL                 string     `json:"pdf_url,omitempty"`
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
	return s.upsertFromProviderEvent(ctx, s.db, inv, eventCreatedAt, eventID)
}

func (s *InvoiceService) UpsertFromProviderEventTx(ctx context.Context, tx *sql.Tx, inv *InvoiceRecord, eventCreatedAt *time.Time, eventID string) (*InvoiceRecord, error) {
	if tx == nil {
		return nil, fmt.Errorf("invoice transaction is required")
	}
	return s.upsertFromProviderEvent(ctx, tx, inv, eventCreatedAt, eventID)
}

func (s *InvoiceService) upsertFromProviderEvent(ctx context.Context, exec billingDBTX, inv *InvoiceRecord, eventCreatedAt *time.Time, eventID string) (*InvoiceRecord, error) {
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
	var id uint

	excluded := func(column string) string { return d.Excluded(column) }
	err := exec.QueryRowContext(ctx,
		`INSERT INTO invoices (created_at, updated_at, tenant_id, subscription_id, provider,
		provider_invoice_id, invoice_number, currency, subtotal, tax, total,
		amount_paid, amount_due, status, period_start, period_end, issued_at,
		due_at, paid_at, hosted_invoice_url, pdf_url,
		provider_event_created_at, provider_event_id, provider_updated_at)
		VALUES (`+d.Placeholders(24)+`)
		ON CONFLICT (provider, provider_invoice_id) DO UPDATE SET
		updated_at = `+excluded("updated_at")+`,
		status = CASE
			WHEN invoices.status = 'paid' AND `+excluded("status")+` NOT IN ('void', 'uncollectible') THEN 'paid'
			ELSE `+excluded("status")+`
		END,
		total = CASE
			WHEN `+excluded("provider_event_created_at")+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+excluded("provider_event_created_at")+` < invoices.provider_event_created_at THEN invoices.total
			ELSE `+excluded("total")+`
		END,
		amount_paid = CASE
			WHEN `+excluded("provider_event_created_at")+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+excluded("provider_event_created_at")+` < invoices.provider_event_created_at THEN invoices.amount_paid
			ELSE `+excluded("amount_paid")+`
		END,
		amount_due = CASE
			WHEN `+excluded("provider_event_created_at")+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+excluded("provider_event_created_at")+` < invoices.provider_event_created_at THEN invoices.amount_due
			ELSE `+excluded("amount_due")+`
		END,
		subtotal = CASE
			WHEN `+excluded("provider_event_created_at")+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+excluded("provider_event_created_at")+` < invoices.provider_event_created_at THEN invoices.subtotal
			ELSE `+excluded("subtotal")+`
		END,
		tax = CASE
			WHEN `+excluded("provider_event_created_at")+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+excluded("provider_event_created_at")+` < invoices.provider_event_created_at THEN invoices.tax
			ELSE `+excluded("tax")+`
		END,
		currency = `+excluded("currency")+`,
		invoice_number = `+excluded("invoice_number")+`,
		subscription_id = `+excluded("subscription_id")+`,
		period_start = `+excluded("period_start")+`,
		period_end = `+excluded("period_end")+`,
		issued_at = `+excluded("issued_at")+`,
		due_at = `+excluded("due_at")+`,
		paid_at = `+excluded("paid_at")+`,
		hosted_invoice_url = `+excluded("hosted_invoice_url")+`,
		pdf_url = `+excluded("pdf_url")+`,
		provider_event_created_at = CASE
			WHEN `+excluded("provider_event_created_at")+` IS NULL OR invoices.provider_event_created_at IS NULL THEN `+excluded("provider_event_created_at")+`
			WHEN `+excluded("provider_event_created_at")+` > invoices.provider_event_created_at THEN `+excluded("provider_event_created_at")+`
			ELSE invoices.provider_event_created_at
		END,
		provider_event_id = CASE
			WHEN `+excluded("provider_event_id")+` IS NULL THEN `+excluded("provider_event_id")+`
			WHEN invoices.provider_event_created_at IS NULL THEN `+excluded("provider_event_id")+`
			WHEN `+excluded("provider_event_created_at")+` > invoices.provider_event_created_at THEN `+excluded("provider_event_id")+`
			ELSE invoices.provider_event_id
		END,
		provider_updated_at = `+excluded("provider_updated_at")+`
		RETURNING id`,
		now, now, inv.TenantID, inv.SubscriptionID, prov,
		inv.ProviderInvoiceID, inv.InvoiceNumber, inv.Currency, inv.Subtotal, inv.Tax, inv.Total,
		inv.AmountPaid, inv.AmountDue, inv.Status, inv.PeriodStart, inv.PeriodEnd, inv.IssuedAt,
		inv.DueAt, inv.PaidAt, inv.HostedInvoiceURL, inv.PDFURL,
		eventCreatedAt, eventID, now,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("upsert invoice: %w", err)
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
