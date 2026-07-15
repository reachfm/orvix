package abuse

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

var ErrSignalNotFound = errors.New("abuse signal not found or belongs to a different tenant")

type SignalService struct {
	db      *sql.DB
	dialect *dbdialect.Info
	rlSvc   *RateLimitService
}

func NewSignalService(db *sql.DB, rlSvc *RateLimitService) *SignalService {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &SignalService{db: db, dialect: dialect, rlSvc: rlSvc}
}

func (s *SignalService) RecordSignal(ctx context.Context, signal *AbuseSignal) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO abuse_signals (tenant_id, mailbox_id, signal_type, severity, description, metadata, detected_at, created_at)
		VALUES (`+s.dialect.Placeholder(1)+`, `+s.dialect.Placeholder(2)+`, `+s.dialect.Placeholder(3)+`, `+s.dialect.Placeholder(4)+`, `+s.dialect.Placeholder(5)+`, `+s.dialect.Placeholder(6)+`, `+s.dialect.Placeholder(7)+`, `+s.dialect.Placeholder(8)+`)`,
		signal.TenantID, signal.MailboxID, signal.SignalType, signal.Severity,
		signal.Description, signal.Metadata, signal.DetectedAt, now)
	return err
}

func (s *SignalService) AcknowledgeSignal(ctx context.Context, tenantID, signalID uint, operatorID uint) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		"UPDATE abuse_signals SET acknowledged_at = "+s.dialect.Placeholder(1)+", acknowledged_by = "+s.dialect.Placeholder(2)+" WHERE id = "+s.dialect.Placeholder(3)+" AND tenant_id = "+s.dialect.Placeholder(4),
		now, operatorID, signalID, tenantID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrSignalNotFound
	}
	return nil
}

func (s *SignalService) ResolveSignal(ctx context.Context, tenantID, signalID, operatorID uint) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		"UPDATE abuse_signals SET resolved_at = "+s.dialect.Placeholder(1)+", resolved_by = "+s.dialect.Placeholder(2)+" WHERE id = "+s.dialect.Placeholder(3)+" AND tenant_id = "+s.dialect.Placeholder(4),
		now, operatorID, signalID, tenantID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrSignalNotFound
	}
	return nil
}

func (s *SignalService) ListActiveSignals(ctx context.Context, tenantID uint) ([]AbuseSignal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tenant_id, mailbox_id, signal_type, severity, description, metadata, detected_at,
		acknowledged_at, acknowledged_by, resolved_at, resolved_by, created_at
		FROM abuse_signals WHERE tenant_id = `+s.dialect.Placeholder(1)+` AND resolved_at IS NULL
		ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, detected_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var signals []AbuseSignal
	for rows.Next() {
		var sig AbuseSignal
		err := rows.Scan(&sig.ID, &sig.TenantID, &sig.MailboxID, &sig.SignalType, &sig.Severity,
			&sig.Description, &sig.Metadata, &sig.DetectedAt, &sig.AcknowledgedAt, &sig.AcknowledgedBy, &sig.ResolvedAt, &sig.ResolvedBy, &sig.CreatedAt)
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
