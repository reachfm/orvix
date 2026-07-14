package billing

import (
	"context"
	"database/sql"
	"time"
)

type Scheduler struct {
	db  *sql.DB
	svc *Service
}

func NewScheduler(db *sql.DB, svc *Service) *Scheduler {
	return &Scheduler{db: db, svc: svc}
}

func (s *Scheduler) ProcessTrialExpiry(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id FROM subscriptions
		WHERE status = ? AND trial_ends_at IS NOT NULL AND trial_ends_at <= ?
		LIMIT 100`, SubTrialing, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var tenantID uint
		if err := rows.Scan(&tenantID); err != nil {
			return count, err
		}
		if err := s.svc.TransitionState(tenantID, SubPastDue); err != nil {
			continue
		}
		count++
	}
	return count, rows.Err()
}

func (s *Scheduler) ProcessGracePeriodExpiry(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id FROM subscriptions
		WHERE status = ? AND grace_period_ends_at IS NOT NULL AND grace_period_ends_at <= ?
		LIMIT 100`, SubGracePeriod, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var tenantID uint
		if err := rows.Scan(&tenantID); err != nil {
			return count, err
		}
		if err := s.svc.TransitionState(tenantID, SubSuspended); err != nil {
			continue
		}
		count++
	}
	return count, rows.Err()
}

func (s *Scheduler) ProcessPastDueEscalation(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id FROM subscriptions
		WHERE status = ? AND grace_period_ends_at IS NOT NULL AND grace_period_ends_at <= ?
		LIMIT 100`, SubPastDue, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var tenantID uint
		if err := rows.Scan(&tenantID); err != nil {
			return count, err
		}
		if err := s.svc.TransitionState(tenantID, SubGracePeriod); err != nil {
			continue
		}
		count++
	}
	return count, rows.Err()
}

func (s *Scheduler) ProcessExpiredSubscriptions(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id FROM subscriptions
		WHERE status = ? AND cancelled_at IS NOT NULL AND cancelled_at <= datetime('now', '-30 days')
		LIMIT 100`, SubCancelled, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var tenantID uint
		if err := rows.Scan(&tenantID); err != nil {
			return count, err
		}
		if err := s.svc.TransitionState(tenantID, SubExpired); err != nil {
			continue
		}
		count++
	}
	return count, rows.Err()
}

func (s *Scheduler) RunAll(ctx context.Context) (int, error) {
	var total int
	n, err := s.ProcessTrialExpiry(ctx)
	if err != nil {
		return total, err
	}
	total += n
	n, err = s.ProcessPastDueEscalation(ctx)
	if err != nil {
		return total, err
	}
	total += n
	n, err = s.ProcessGracePeriodExpiry(ctx)
	if err != nil {
		return total, err
	}
	total += n
	n, err = s.ProcessExpiredSubscriptions(ctx)
	if err != nil {
		return total, err
	}
	total += n
	return total, nil
}
