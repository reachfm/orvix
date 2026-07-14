package abuse

import (
	"context"
	"database/sql"
	"time"
)

type SignalService struct {
	db *sql.DB
}

func NewSignalService(db *sql.DB) *SignalService {
	return &SignalService{db: db}
}

func (s *SignalService) RecordSignal(ctx context.Context, signal *AbuseSignal) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO abuse_signals (tenant_id, mailbox_id, signal_type, severity, description, metadata, detected_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		signal.TenantID, signal.MailboxID, signal.SignalType, signal.Severity,
		signal.Description, signal.Metadata, signal.DetectedAt)
	return err
}

func (s *SignalService) AcknowledgeSignal(ctx context.Context, signalID, operatorID uint) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"UPDATE abuse_signals SET acknowledged_at = ?, resolved_by = ? WHERE id = ?",
		now, operatorID, signalID)
	return err
}

func (s *SignalService) ResolveSignal(ctx context.Context, signalID, operatorID uint) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"UPDATE abuse_signals SET resolved_at = ?, resolved_by = ? WHERE id = ?",
		now, operatorID, signalID)
	return err
}

func (s *SignalService) ListActiveSignals(ctx context.Context, tenantID uint) ([]AbuseSignal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tenant_id, mailbox_id, signal_type, severity, description, metadata, detected_at,
		acknowledged_at, resolved_at, resolved_by, created_at
		FROM abuse_signals WHERE tenant_id = ? AND resolved_at IS NULL
		ORDER BY severity DESC, detected_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var signals []AbuseSignal
	for rows.Next() {
		var s AbuseSignal
		err := rows.Scan(&s.ID, &s.TenantID, &s.MailboxID, &s.SignalType, &s.Severity,
			&s.Description, &s.Metadata, &s.DetectedAt, &s.AcknowledgedAt, &s.ResolvedAt, &s.ResolvedBy, &s.CreatedAt)
		if err != nil {
			return nil, err
		}
		signals = append(signals, s)
	}
	return signals, rows.Err()
}

func (s *SignalService) CheckAndAlert(ctx context.Context, tenantID uint) error {
	bounceRate, err := (&RateLimitService{db: s.db}).CheckBounceRate(ctx, tenantID)
	if err != nil {
		return err
	}
	if bounceRate > 0.05 {
		sig := &AbuseSignal{
			TenantID:    tenantID,
			SignalType:  SignalHighBounceRate,
			Severity:    SeverityWarning,
			Description: "High bounce rate detected",
			DetectedAt:  time.Now().UTC(),
		}
		s.RecordSignal(ctx, sig)
	}
	if bounceRate > 0.15 {
		sig := &AbuseSignal{
			TenantID:    tenantID,
			SignalType:  SignalHighBounceRate,
			Severity:    SeverityCritical,
			Description: "Critical bounce rate - possible abuse",
			DetectedAt:  time.Now().UTC(),
		}
		s.RecordSignal(ctx, sig)
	}
	return nil
}
