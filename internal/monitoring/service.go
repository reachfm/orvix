package monitoring

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DataSources provides access to all subsystems for monitoring.
type DataSources struct {
	DB                *sql.DB
	QueuePending      func() (int64, error)
	TLSCerts          func() (expiring30, expiring7 int, err error)
	LatestBackup      func() (time.Time, error)
	DomainCount       func() (int, error)
	MailboxCount      func() (int64, error)
	MessageCount      func() (int64, error)
	AttachmentCount   func() (int64, error)
	StorageBytes      func() (int64, error)
	SMTPHealthy       func() bool
	IMAPHealthy       func() bool
	POP3Healthy       func() bool
	JMAPHealthy       func() bool
	DatabaseHealthy   func() bool
	MailStoreHealthy  func() bool
	BackupCount       func() (int, error)
}

// Service provides monitoring and alerting.
type Service struct {
	db  *sql.DB
	src *DataSources
}

// NewService creates a monitoring service.
func NewService(db *sql.DB, src *DataSources) *Service {
	return &Service{db: db, src: src}
}

// EnsureSchema creates required tables.
func (s *Service) EnsureSchema(ctx context.Context) error {
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// ── Alert Generation ────────────────────────────────────

func (s *Service) EvaluateAlerts(ctx context.Context) ([]Alert, error) {
	var alerts []Alert

	// Resolve previous alerts before re-evaluating.
	s.resolveAll(ctx)

	// Queue checks.
	if s.src.QueuePending != nil {
		if count, err := s.src.QueuePending(); err == nil {
			if count > 1000 {
				alerts = append(alerts, s.newAlert(CatQueue, SeverityCritical, "Queue growth critical", fmt.Sprintf("%d pending messages", count)))
			} else if count > 100 {
				alerts = append(alerts, s.newAlert(CatQueue, SeverityWarning, "Queue growth warning", fmt.Sprintf("%d pending messages", count)))
			}
		}
	}

	// TLS expiry.
	if s.src.TLSCerts != nil {
		if exp30, exp7, err := s.src.TLSCerts(); err == nil {
			if exp7 > 0 {
				alerts = append(alerts, s.newAlert(CatTLS, SeverityCritical, "TLS certificates expiring soon", fmt.Sprintf("%d certificates expire within 7 days", exp7)))
			}
			if exp30 > 0 {
				alerts = append(alerts, s.newAlert(CatTLS, SeverityWarning, "TLS certificates expiring", fmt.Sprintf("%d certificates expire within 30 days", exp30)))
			}
		}
	}

	// Backup freshness.
	if s.src.LatestBackup != nil {
		if latest, err := s.src.LatestBackup(); err == nil {
			days := int(time.Since(latest).Hours() / 24)
			if days > 30 {
				alerts = append(alerts, s.newAlert(CatBackup, SeverityCritical, "No recent backup", fmt.Sprintf("Last backup was %d days ago", days)))
			} else if days > 7 {
				alerts = append(alerts, s.newAlert(CatBackup, SeverityWarning, "Backup is aging", fmt.Sprintf("Last backup was %d days ago", days)))
			}
		}
	}

	// Runtime health.
	if s.src.SMTPHealthy != nil && !s.src.SMTPHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "SMTP unhealthy", "SMTP service is not healthy"))
	}
	if s.src.IMAPHealthy != nil && !s.src.IMAPHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "IMAP unhealthy", "IMAP service is not healthy"))
	}
	if s.src.POP3Healthy != nil && !s.src.POP3Healthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "POP3 unhealthy", "POP3 service is not healthy"))
	}
	if s.src.JMAPHealthy != nil && !s.src.JMAPHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "JMAP unhealthy", "JMAP service is not healthy"))
	}
	if s.src.DatabaseHealthy != nil && !s.src.DatabaseHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "Database unhealthy", "Database health check failed"))
	}
	if s.src.MailStoreHealthy != nil && !s.src.MailStoreHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "MailStore unhealthy", "MailStore health check failed"))
	}

	// Persist alerts.
	for i := range alerts {
		s.saveAlert(ctx, &alerts[i])
	}

	return s.ListActiveAlerts(ctx)
}

func (s *Service) newAlert(cat Category, sev Severity, title, msg string) Alert {
	return Alert{
		Category: cat, Severity: sev, Title: title, Message: msg,
		Source: string(cat), Active: true, CreatedAt: time.Now().UTC(),
	}
}

func (s *Service) saveAlert(ctx context.Context, a *Alert) {
	if s.db == nil { return }
	s.db.ExecContext(ctx,
		`INSERT INTO monitoring_alerts (category, severity, title, message, source, active, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(a.Category), string(a.Severity), a.Title, a.Message, a.Source, a.Active, a.CreatedAt)
}

func (s *Service) resolveAll(ctx context.Context) {
	if s.db == nil { return }
	s.db.ExecContext(ctx, "UPDATE monitoring_alerts SET active=0, resolved_at=datetime('now') WHERE active=1")
}

// ── Alert CRUD ──────────────────────────────────────────

func (s *Service) ListActiveAlerts(ctx context.Context) ([]Alert, error) {
	return s.listAlerts(ctx, true)
}

func (s *Service) ListAllAlerts(ctx context.Context) ([]Alert, error) {
	return s.listAlerts(ctx, false)
}

func (s *Service) listAlerts(ctx context.Context, activeOnly bool) ([]Alert, error) {
	if s.db == nil { return nil, nil }
	query := "SELECT id, category, severity, title, message, source, active, created_at, resolved_at FROM monitoring_alerts"
	if activeOnly { query += " WHERE active=1" }
	query += " ORDER BY created_at DESC LIMIT 200"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil { return nil, err }
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		var resolvedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.Category, &a.Severity, &a.Title, &a.Message, &a.Source, &a.Active, &a.CreatedAt, &resolvedAt); err != nil {
			return nil, err
		}
		if resolvedAt.Valid { a.ResolvedAt = &resolvedAt.Time }
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

func (s *Service) ResolveAlert(ctx context.Context, id uint) error {
	_, err := s.db.ExecContext(ctx, "UPDATE monitoring_alerts SET active=0, resolved_at=datetime('now') WHERE id=?", id)
	return err
}

// ── Capacity ────────────────────────────────────────────

func (s *Service) GetCapacity(ctx context.Context) *Capacity {
	c := &Capacity{}
	if s.src.DomainCount != nil { c.DomainCount, _ = s.src.DomainCount() }
	if s.src.MailboxCount != nil { c.MailboxCount, _ = s.src.MailboxCount() }
	if s.src.MessageCount != nil { c.MessageCount, _ = s.src.MessageCount() }
	if s.src.AttachmentCount != nil { c.AttachmentCount, _ = s.src.AttachmentCount() }
	if s.src.StorageBytes != nil { c.StorageBytes, _ = s.src.StorageBytes() }
	if s.src.BackupCount != nil { c.BackupCount, _ = s.src.BackupCount() }

	if s.db != nil {
		var size int64
		s.db.QueryRowContext(ctx, "SELECT IFNULL(SUM(pgsize), 0) FROM (SELECT page_count * page_size as pgsize FROM pragma_page_count(), pragma_page_size())").Scan(&size)
		c.DatabaseSize = size
	}

	if s.src.QueuePending != nil {
		count, _ := s.src.QueuePending()
		c.QueueCount = count
	}

	return c
}
