package billing

import (
	"context"
	"database/sql"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Scheduler struct {
	db      *sql.DB
	dialect *dbdialect.Info
	svc     *Service
}

func NewScheduler(db *sql.DB, svc *Service) *Scheduler {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Scheduler{db: db, dialect: dialect, svc: svc}
}

func (s *Scheduler) ProcessTrialExpiry(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id FROM subscriptions
		WHERE status = `+s.dialect.Placeholder(1)+` AND trial_ends_at IS NOT NULL AND trial_ends_at <= `+s.dialect.Placeholder(2)+`
		ORDER BY trial_ends_at ASC LIMIT 100`, SubTrialing, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return s.transitionRows(ctx, rows, SubPastDue)
}

func (s *Scheduler) ProcessGracePeriodExpiry(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id FROM subscriptions
		WHERE status = `+s.dialect.Placeholder(1)+` AND grace_period_ends_at IS NOT NULL AND grace_period_ends_at <= `+s.dialect.Placeholder(2)+`
		ORDER BY grace_period_ends_at ASC LIMIT 100`, SubGracePeriod, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return s.transitionRows(ctx, rows, SubSuspended)
}

func (s *Scheduler) ProcessPastDueEscalation(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id FROM subscriptions
		WHERE status = `+s.dialect.Placeholder(1)+` AND grace_period_ends_at IS NOT NULL AND grace_period_ends_at <= `+s.dialect.Placeholder(2)+`
		ORDER BY grace_period_ends_at ASC LIMIT 100`, SubPastDue, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return s.transitionRows(ctx, rows, SubGracePeriod)
}

func (s *Scheduler) ProcessExpiredSubscriptions(ctx context.Context) (int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id FROM subscriptions
		WHERE status = `+s.dialect.Placeholder(1)+` AND cancelled_at IS NOT NULL AND cancelled_at <= `+s.dialect.Placeholder(2)+`
		ORDER BY cancelled_at ASC LIMIT 100`, SubCancelled, cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return s.transitionRows(ctx, rows, SubExpired)
}

func (s *Scheduler) transitionRows(ctx context.Context, rows *sql.Rows, target SubscriptionStatus) (int, error) {
	var count int
	for rows.Next() {
		var tenantID uint
		if err := rows.Scan(&tenantID); err != nil {
			return count, err
		}
		if err := s.svc.TransitionState(tenantID, target); err != nil {
			continue
		}
		count++
	}
	return count, rows.Err()
}

func (s *Scheduler) RunAll(ctx context.Context) (int, error) {
	var total int
	n, _ := s.ProcessTrialExpiry(ctx)
	total += n
	n, _ = s.ProcessPastDueEscalation(ctx)
	total += n
	n, _ = s.ProcessGracePeriodExpiry(ctx)
	total += n
	n, _ = s.ProcessExpiredSubscriptions(ctx)
	total += n
	return total, nil
}
