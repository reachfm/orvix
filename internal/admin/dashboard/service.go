package dashboard

import (
	"context"
	"database/sql"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type DashboardService struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewDashboardService(db *sql.DB) *DashboardService {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &DashboardService{db: db, dialect: d}
}

func (s *DashboardService) CustomerDashboard(ctx context.Context, tenantID uint) (*CustomerDashboard, error) {
	d := &CustomerDashboard{}

	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_domains WHERE tenant_id="+s.dialect.Placeholder(1)+" AND deleted_at IS NULL", tenantID).Scan(&d.TotalDomains)

	s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM coremail_domains WHERE tenant_id=`+s.dialect.Placeholder(1)+` AND status='active' AND deleted_at IS NULL`, tenantID).Scan(&d.HealthyDomains)

	s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM coremail_domains WHERE tenant_id=`+s.dialect.Placeholder(1)+` AND status NOT IN ('active','deleted') AND deleted_at IS NULL`, tenantID).Scan(&d.DomainsNeedingAttention)

	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+s.dialect.Placeholder(1)+" AND deleted_at IS NULL", tenantID).Scan(&d.TotalMailboxes)

	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+s.dialect.Placeholder(1)+" AND status='active' AND deleted_at IS NULL", tenantID).Scan(&d.ActiveMailboxes)

	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+s.dialect.Placeholder(1)+" AND status='suspended' AND deleted_at IS NULL", tenantID).Scan(&d.SuspendedMailboxes)

	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+s.dialect.Placeholder(1)+" AND status='disabled' AND deleted_at IS NULL", tenantID).Scan(&d.DisabledMailboxes)

	var quotaUsed sql.NullInt64
	s.db.QueryRowContext(ctx,
		"SELECT SUM(used_bytes) FROM coremail_mailboxes WHERE tenant_id="+s.dialect.Placeholder(1)+" AND deleted_at IS NULL", tenantID).Scan(&quotaUsed)
	d.QuotaUsedBytes = quotaUsed.Int64

	s.loadRecentActions(ctx, tenantID, d)
	return d, nil
}

func (s *DashboardService) PlatformDashboard(ctx context.Context) (*PlatformDashboard, error) {
	d := &PlatformDashboard{}

	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL").Scan(&d.TotalOrganizations)
	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tenants WHERE active=1 AND deleted_at IS NULL").Scan(&d.ActiveOrganizations)
	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_domains WHERE deleted_at IS NULL").Scan(&d.TotalDomains)
	s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL").Scan(&d.TotalMailboxes)

	var quotaUsed sql.NullInt64
	s.db.QueryRowContext(ctx,
		"SELECT SUM(used_bytes) FROM coremail_mailboxes WHERE deleted_at IS NULL").Scan(&quotaUsed)
	d.QuotaUsedBytes = quotaUsed.Int64

	s.loadPlatformAudit(ctx, d)
	return d, nil
}

func (s *DashboardService) loadRecentActions(ctx context.Context, tenantID uint, d *CustomerDashboard) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT action, target, timestamp FROM orvix_audit WHERE tenant_id="+s.dialect.Placeholder(1)+" ORDER BY id DESC LIMIT 10", tenantID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a RecentAction
		var ts time.Time
		if err := rows.Scan(&a.Action, &a.Target, &ts); err != nil {
			continue
		}
		a.Timestamp = ts.Format(time.RFC3339)
		d.RecentActions = append(d.RecentActions, a)
	}
}

func (s *DashboardService) loadPlatformAudit(ctx context.Context, d *PlatformDashboard) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT action, target, timestamp FROM orvix_audit ORDER BY id DESC LIMIT 10")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a RecentAction
		var ts time.Time
		if err := rows.Scan(&a.Action, &a.Target, &ts); err != nil {
			continue
		}
		a.Timestamp = ts.Format(time.RFC3339)
		d.RecentAuditEntries = append(d.RecentAuditEntries, a)
	}
}
