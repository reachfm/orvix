package billing

import (
	"database/sql"
	"time"
)

type UsageService struct {
	db *sql.DB
}

func NewUsageService(db *sql.DB) *UsageService {
	return &UsageService{db: db}
}

func (s *UsageService) GetCurrentUsage(tenantID uint) (*UsageRecord, error) {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	row := s.db.QueryRow(`SELECT id, tenant_id, period_start, period_end, mailboxes_used, domains_used,
		storage_used_mb, emails_sent, emails_received, api_calls, created_at
		FROM usage_records WHERE tenant_id = ? AND period_start = ?`, tenantID, periodStart)
	var rec UsageRecord
	err := row.Scan(&rec.ID, &rec.TenantID, &rec.PeriodStart, &rec.PeriodEnd,
		&rec.MailboxesUsed, &rec.DomainsUsed, &rec.StorageUsedMB,
		&rec.EmailsSent, &rec.EmailsReceived, &rec.APICalls, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return &UsageRecord{
			TenantID:    tenantID,
			PeriodStart: periodStart,
			PeriodEnd:   periodStart.AddDate(0, 1, 0),
		}, nil
	}
	return &rec, err
}

func (s *UsageService) IncrementEmailsSent(tenantID uint, count int64) error {
	return s.increment(tenantID, "emails_sent", count)
}

func (s *UsageService) IncrementEmailsReceived(tenantID uint, count int64) error {
	return s.increment(tenantID, "emails_received", count)
}

func (s *UsageService) IncrementAPICalls(tenantID uint, count int64) error {
	return s.increment(tenantID, "api_calls", count)
}

func (s *UsageService) SetMailboxCount(tenantID uint, count int) error {
	return s.setField(tenantID, "mailboxes_used", int64(count))
}

func (s *UsageService) SetDomainCount(tenantID uint, count int) error {
	return s.setField(tenantID, "domains_used", int64(count))
}

func (s *UsageService) increment(tenantID uint, field string, count int64) error {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	query := `INSERT INTO usage_records (tenant_id, period_start, period_end, ` + field + `, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(tenant_id, period_start) DO UPDATE SET ` + field + ` = ` + field + ` + ?`
	_, err := s.db.Exec(query, tenantID, periodStart, periodStart.AddDate(0, 1, 0), count, count)
	return err
}

func (s *UsageService) setField(tenantID uint, field string, value int64) error {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	query := `INSERT INTO usage_records (tenant_id, period_start, period_end, ` + field + `, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(tenant_id, period_start) DO UPDATE SET ` + field + ` = ?`
	_, err := s.db.Exec(query, tenantID, periodStart, periodStart.AddDate(0, 1, 0), value, value)
	return err
}
