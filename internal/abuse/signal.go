package abuse

import (
	"context"
	"database/sql"
	"time"
)

type SignalService struct {
	db      *sql.DB
	rlSvc   *RateLimitService
}

func NewSignalService(db *sql.DB, rlSvc *RateLimitService) *SignalService {
	return &SignalService{db: db, rlSvc: rlSvc}
}

func (s *SignalService) RecordSignal(ctx context.Context, signal *AbuseSignal) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO abuse_signals (tenant_id, mailbox_id, signal_type, severity, description, metadata, detected_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		signal.TenantID, signal.MailboxID, signal.SignalType, signal.Severity,
		signal.Description, signal.Metadata, signal.DetectedAt)
	return err
}

func (s *SignalService) AcknowledgeSignal(ctx context.Context, signalID uint) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"UPDATE abuse_signals SET acknowledged_at = ? WHERE id = ?",
		now, signalID)
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
		ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, detected_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var signals []AbuseSignal
	for rows.Next() {
		var sig AbuseSignal
		err := rows.Scan(&sig.ID, &sig.TenantID, &sig.MailboxID, &sig.SignalType, &sig.Severity,
			&sig.Description, &sig.Metadata, &sig.DetectedAt, &sig.AcknowledgedAt, &sig.ResolvedAt, &sig.ResolvedBy, &sig.CreatedAt)
		if err != nil {
			return nil, err
		}
		signals = append(signals, sig)
	}
	return signals, rows.Err()
}

func (s *SignalService) CheckAndAlert(ctx context.Context, tenantID uint) error {
	bounceRate, err := s.rlSvc.CheckBounceRate(ctx, tenantID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if bounceRate > 0.05 {
		sev := SeverityWarning
		if bounceRate > 0.15 {
			sev = SeverityCritical
		}
		sig := &AbuseSignal{
			TenantID:    tenantID,
			SignalType:  SignalHighBounceRate,
			Severity:    sev,
			Description: "High bounce rate detected",
			DetectedAt:  now,
		}
		s.RecordSignal(ctx, sig)
	}
	return nil
}
