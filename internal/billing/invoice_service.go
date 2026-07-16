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

	err := s.db.QueryRowContext(ctx,
		`INSERT INTO invoices (created_at, updated_at, tenant_id, subscription_id, provider,
		provider_invoice_id, invoice_number, currency, subtotal, tax, total,
		amount_paid, amount_due, status, period_start, period_end, issued_at,
		due_at, paid_at, hosted_invoice_url, pdf_url,
		provider_event_created_at, provider_event_id, provider_updated_at)
		VALUES (`+d.Placeholders(24)+`)
		ON CONFLICT (provider, provider_invoice_id) DO UPDATE SET
		updated_at = `+d.Placeholder(25)+`,
		status = CASE
			WHEN invoices.status = 'paid' AND `+d.Placeholder(26)+` NOT IN ('void', 'uncollectible') THEN 'paid'
			ELSE `+d.Placeholder(27)+`
		END,
		total = CASE
			WHEN `+d.Placeholder(28)+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+d.Placeholder(29)+` < invoices.provider_event_created_at THEN invoices.total
			ELSE `+d.Placeholder(30)+`
		END,
		amount_paid = CASE
			WHEN `+d.Placeholder(31)+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+d.Placeholder(32)+` < invoices.provider_event_created_at THEN invoices.amount_paid
			ELSE `+d.Placeholder(33)+`
		END,
		amount_due = CASE
			WHEN `+d.Placeholder(34)+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+d.Placeholder(35)+` < invoices.provider_event_created_at THEN invoices.amount_due
			ELSE `+d.Placeholder(36)+`
		END,
		subtotal = CASE
			WHEN `+d.Placeholder(37)+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+d.Placeholder(38)+` < invoices.provider_event_created_at THEN invoices.subtotal
			ELSE `+d.Placeholder(39)+`
		END,
		tax = CASE
			WHEN `+d.Placeholder(40)+` IS NOT NULL AND invoices.provider_event_created_at IS NOT NULL AND `+d.Placeholder(41)+` < invoices.provider_event_created_at THEN invoices.tax
			ELSE `+d.Placeholder(42)+`
		END,
		currency = `+d.Placeholder(43)+`,
		invoice_number = `+d.Placeholder(44)+`,
		subscription_id = `+d.Placeholder(45)+`,
		period_start = `+d.Placeholder(46)+`,
		period_end = `+d.Placeholder(47)+`,
		issued_at = `+d.Placeholder(48)+`,
		due_at = `+d.Placeholder(49)+`,
		paid_at = `+d.Placeholder(50)+`,
		hosted_invoice_url = `+d.Placeholder(51)+`,
		pdf_url = `+d.Placeholder(52)+`,
		provider_event_created_at = CASE
			WHEN `+d.Placeholder(53)+` IS NULL OR invoices.provider_event_created_at IS NULL THEN `+d.Placeholder(54)+`
			WHEN `+d.Placeholder(55)+` > invoices.provider_event_created_at THEN `+d.Placeholder(56)+`
			ELSE invoices.provider_event_created_at
		END,
		provider_event_id = CASE
			WHEN `+d.Placeholder(57)+` IS NULL THEN `+d.Placeholder(58)+`
			WHEN invoices.provider_event_created_at IS NULL THEN `+d.Placeholder(59)+`
			WHEN `+d.Placeholder(60)+` > invoices.provider_event_created_at THEN `+d.Placeholder(61)+`
			ELSE invoices.provider_event_id
		END,
		provider_updated_at = `+d.Placeholder(62)+`
		RETURNING id`,
		// INSERT values (24)
		now, now, inv.TenantID, inv.SubscriptionID, prov,
		inv.ProviderInvoiceID, inv.InvoiceNumber, inv.Currency, inv.Subtotal, inv.Tax, inv.Total,
		inv.AmountPaid, inv.AmountDue, inv.Status, inv.PeriodStart, inv.PeriodEnd, inv.IssuedAt,
		inv.DueAt, inv.PaidAt, inv.HostedInvoiceURL, inv.PDFURL,
		eventCreatedAt, eventID, now,
		// UPDATE values starting at 25
		now,                    // 25: updated_at
		inv.Status, inv.Status, // 26-27: status CASE
		eventCreatedAt, eventCreatedAt, inv.Total, // 28-30: total CASE
		eventCreatedAt, eventCreatedAt, inv.AmountPaid, // 31-33: amount_paid CASE
		eventCreatedAt, eventCreatedAt, inv.AmountDue, // 34-36: amount_due CASE
		eventCreatedAt, eventCreatedAt, inv.Subtotal, // 37-39: subtotal CASE
		eventCreatedAt, eventCreatedAt, inv.Tax, // 40-42: tax CASE
		inv.Currency,                   // 43: currency
		inv.InvoiceNumber,              // 44: invoice_number
		inv.SubscriptionID,             // 45: subscription_id
		inv.PeriodStart,                // 46: period_start
		inv.PeriodEnd,                  // 47: period_end
		inv.IssuedAt,                   // 48: issued_at
		inv.DueAt,                      // 49: due_at
		inv.PaidAt,                     // 50: paid_at
		inv.HostedInvoiceURL,           // 51: hosted_invoice_url
		inv.PDFURL,                     // 52: pdf_url
		eventCreatedAt, eventCreatedAt, // 53-54: provider_event_created_at CASE (first)
		eventCreatedAt, eventCreatedAt, // 55-56: provider_event_created_at CASE (condition+value)
		eventID, eventID, // 57-58: provider_event_id CASE first
		eventID,                        // 59: provider_event_id CASE second
		eventCreatedAt, eventCreatedAt, // 60-61: provider_event_id CASE condition+value
		now, // 62: provider_updated_at
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
