package billing

import (
	"database/sql"
	"errors"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

var (
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrPlanNotFound         = errors.New("plan not found")
	ErrInvalidTransition    = errors.New("invalid subscription state transition")
	ErrTenantAlreadyHasSub  = errors.New("tenant already has an active subscription")
)

type Service struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewService(db *sql.DB) *Service {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Service{db: db, dialect: dialect}
}

func (s *Service) withTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) GetPlan(id PlanID) (*Plan, error) {
	row := s.db.QueryRow(`SELECT id, name, description, price_monthly, price_yearly,
		max_domains, max_mailboxes, storage_mb, send_limit_day, features, created_at, updated_at
		FROM plans WHERE id = `+s.dialect.Placeholder(1), id)
	var p Plan
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.PriceMonthly, &p.PriceYearly,
		&p.MaxDomains, &p.MaxMailboxes, &p.StorageMB, &p.SendLimitDay, &p.Features, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPlanNotFound
	}
	return &p, err
}

func (s *Service) GetPlanTx(tx *sql.Tx, id PlanID) (*Plan, error) {
	row := tx.QueryRow(`SELECT id, name, description, price_monthly, price_yearly,
		max_domains, max_mailboxes, storage_mb, send_limit_day, features, created_at, updated_at
		FROM plans WHERE id = `+s.dialect.Placeholder(1), id)
	var p Plan
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.PriceMonthly, &p.PriceYearly,
		&p.MaxDomains, &p.MaxMailboxes, &p.StorageMB, &p.SendLimitDay, &p.Features, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPlanNotFound
	}
	return &p, err
}

func (s *Service) ListPlans() ([]Plan, error) {
	rows, err := s.db.Query(`SELECT id, name, description, price_monthly, price_yearly,
		max_domains, max_mailboxes, storage_mb, send_limit_day, features, created_at, updated_at
		FROM plans ORDER BY price_monthly ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plans []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.PriceMonthly, &p.PriceYearly,
			&p.MaxDomains, &p.MaxMailboxes, &p.StorageMB, &p.SendLimitDay, &p.Features, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

func (s *Service) SeedDefaultPlans() error {
	defaults := []Plan{
		{ID: PlanFree, Name: "Free", Description: "For individuals", PriceMonthly: 0, PriceYearly: 0, MaxDomains: 1, MaxMailboxes: 5, StorageMB: 1024, SendLimitDay: 500, Features: `["custom_domain","dkim"]`},
		{ID: PlanStarter, Name: "Starter", Description: "For small teams", PriceMonthly: 999, PriceYearly: 9990, MaxDomains: 3, MaxMailboxes: 25, StorageMB: 10240, SendLimitDay: 2000, Features: `["custom_domain","dkim","mta_sts","api","team"]`},
		{ID: PlanBusiness, Name: "Business", Description: "For growing companies", PriceMonthly: 2999, PriceYearly: 29990, MaxDomains: 10, MaxMailboxes: 100, StorageMB: 102400, SendLimitDay: 10000, Features: `["custom_domain","dkim","mta_sts","api","team","groups","catch_all","mail_forwarding","backup","audit_log","mfa"]`},
		{ID: PlanEnterprise, Name: "Enterprise", Description: "For large organizations", PriceMonthly: 9999, PriceYearly: 99990, MaxDomains: 100, MaxMailboxes: 1000, StorageMB: 1048576, SendLimitDay: 100000, Features: `["custom_domain","dkim","mta_sts","api","team","groups","catch_all","mail_forwarding","backup","audit_log","sso","mfa","sla","priority_support"]`},
	}
	return s.withTx(func(tx *sql.Tx) error {
		for _, p := range defaults {
			var existing string
			err := tx.QueryRow("SELECT id FROM plans WHERE id = "+s.dialect.Placeholder(1), p.ID).Scan(&existing)
			if errors.Is(err, sql.ErrNoRows) {
				now := time.Now().UTC()
				_, err = tx.Exec(`INSERT INTO plans (id, name, description, price_monthly, price_yearly,
					max_domains, max_mailboxes, storage_mb, send_limit_day, features, created_at, updated_at)
					VALUES (`+s.dialect.Placeholders(12)+`)`,
					p.ID, p.Name, p.Description, p.PriceMonthly, p.PriceYearly,
					p.MaxDomains, p.MaxMailboxes, p.StorageMB, p.SendLimitDay, p.Features, now, now)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Service) GetSubscription(tenantID uint) (*Subscription, error) {
	row := s.db.QueryRow(`SELECT id, tenant_id, plan_id, status, billing_interval, trial_ends_at,
		current_period_start, current_period_end, cancelled_at, past_due_since, grace_period_ends_at,
		suspended_at, storage_mb, send_limit_day, provider, provider_sub_id, created_at, updated_at
		FROM subscriptions WHERE tenant_id = `+s.dialect.Placeholder(1), tenantID)
	var sub Subscription
	err := row.Scan(&sub.ID, &sub.TenantID, &sub.PlanID, &sub.Status, &sub.BillingInterval,
		&sub.TrialEndsAt, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CancelledAt,
		&sub.PastDueSince, &sub.GracePeriodEndsAt, &sub.SuspendedAt, &sub.StorageMB,
		&sub.SendLimitDay, &sub.Provider, &sub.ProviderSubID, &sub.CreatedAt, &sub.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSubscriptionNotFound
	}
	return &sub, err
}

func (s *Service) GetSubscriptionByProviderID(providerSubID string) (*Subscription, error) {
	row := s.db.QueryRow(`SELECT id, tenant_id, plan_id, status, billing_interval, trial_ends_at,
		current_period_start, current_period_end, cancelled_at, past_due_since, grace_period_ends_at,
		suspended_at, storage_mb, send_limit_day, provider, provider_sub_id, created_at, updated_at
		FROM subscriptions WHERE provider_sub_id = `+s.dialect.Placeholder(1), providerSubID)
	var sub Subscription
	err := row.Scan(&sub.ID, &sub.TenantID, &sub.PlanID, &sub.Status, &sub.BillingInterval,
		&sub.TrialEndsAt, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CancelledAt,
		&sub.PastDueSince, &sub.GracePeriodEndsAt, &sub.SuspendedAt, &sub.StorageMB,
		&sub.SendLimitDay, &sub.Provider, &sub.ProviderSubID, &sub.CreatedAt, &sub.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSubscriptionNotFound
	}
	return &sub, err
}

func (s *Service) CreateSubscription(tenantID uint, planID PlanID, interval BillingInterval, trialDays int) (*Subscription, error) {
	plan, err := s.GetPlan(planID)
	if err != nil {
		return nil, err
	}
	var sub *Subscription
	err = s.withTx(func(tx *sql.Tx) error {
		var innerErr error
		sub, innerErr = s.createSubscriptionTx(tx, plan, tenantID, planID, interval, trialDays)
		return innerErr
	})
	return sub, err
}

func (s *Service) CreateSubscriptionTx(tx *sql.Tx, dial *dbdialect.Info, tenantID uint, planID PlanID, interval BillingInterval, trialDays int) (*Subscription, error) {
	plan, err := s.GetPlanTx(tx, planID)
	if err != nil {
		return nil, err
	}
	return s.createSubscriptionWithDialect(tx, dial, plan, tenantID, interval, trialDays)
}

func (s *Service) createSubscriptionTx(tx *sql.Tx, plan *Plan, tenantID uint, planID PlanID, interval BillingInterval, trialDays int) (*Subscription, error) {
	return s.createSubscriptionWithDialect(tx, s.dialect, plan, tenantID, interval, trialDays)
}

func (s *Service) createSubscriptionWithDialect(tx *sql.Tx, dial *dbdialect.Info, plan *Plan, tenantID uint, interval BillingInterval, trialDays int) (*Subscription, error) {
	now := time.Now().UTC()
	periodEnd := now.AddDate(0, 1, 0)
	var trialEnd *time.Time
	status := SubActive
	if trialDays > 0 {
		t := now.AddDate(0, 0, trialDays)
		trialEnd = &t
		status = SubTrialing
		periodEnd = t
	}
	var existingID uint
	existingErr := tx.QueryRow("SELECT id FROM subscriptions WHERE tenant_id = "+dial.Placeholder(1), tenantID).Scan(&existingID)
	if existingErr == nil {
		var existingStatus SubscriptionStatus
		if err := tx.QueryRow("SELECT status FROM subscriptions WHERE id = "+dial.Placeholder(1), existingID).Scan(&existingStatus); err != nil {
			return nil, err
		}
		if existingStatus != SubCancelled && existingStatus != SubExpired {
			return nil, ErrTenantAlreadyHasSub
		}
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return nil, existingErr
	}

	var id uint
	var insertErr error
	if dial.IsPostgres() {
		insertErr = tx.QueryRow(`INSERT INTO subscriptions (tenant_id, plan_id, status, billing_interval, trial_ends_at,
			current_period_start, current_period_end, storage_mb, send_limit_day, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
			tenantID, plan.ID, status, interval, trialEnd, now, periodEnd, plan.StorageMB, plan.SendLimitDay, now, now).Scan(&id)
	} else {
		res, err2 := tx.Exec(`INSERT INTO subscriptions (tenant_id, plan_id, status, billing_interval, trial_ends_at,
			current_period_start, current_period_end, storage_mb, send_limit_day, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tenantID, plan.ID, status, interval, trialEnd, now, periodEnd, plan.StorageMB, plan.SendLimitDay, now, now)
		if err2 != nil {
			insertErr = err2
		} else {
			lastID, _ := res.LastInsertId()
			id = uint(lastID)
		}
	}
	if insertErr != nil {
		return nil, insertErr
	}
	return &Subscription{
		ID: id, TenantID: tenantID, PlanID: plan.ID, Status: status, BillingInterval: interval,
		TrialEndsAt: trialEnd, CurrentPeriodStart: now, CurrentPeriodEnd: periodEnd,
		StorageMB: plan.StorageMB, SendLimitDay: plan.SendLimitDay, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Service) ChangePlan(tenantID uint, newPlanID PlanID) (*Subscription, error) {
	plan, err := s.GetPlan(newPlanID)
	if err != nil {
		return nil, err
	}
	err = s.withTx(func(tx *sql.Tx) error {
		now := time.Now().UTC()
		_, err := tx.Exec("UPDATE subscriptions SET plan_id = "+s.dialect.Placeholder(1)+", storage_mb = "+s.dialect.Placeholder(2)+", send_limit_day = "+s.dialect.Placeholder(3)+", updated_at = "+s.dialect.Placeholder(4)+" WHERE tenant_id = "+s.dialect.Placeholder(5),
			newPlanID, plan.StorageMB, plan.SendLimitDay, now, tenantID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetSubscription(tenantID)
}

func (s *Service) TransitionState(tenantID uint, newStatus SubscriptionStatus) error {
	return s.withTx(func(tx *sql.Tx) error {
		var current struct {
			status            SubscriptionStatus
			pastDueSince      *time.Time
			gracePeriodEndsAt *time.Time
		}
		err := tx.QueryRow("SELECT status, past_due_since, grace_period_ends_at FROM subscriptions WHERE tenant_id = "+s.dialect.Placeholder(1), tenantID).
			Scan(&current.status, &current.pastDueSince, &current.gracePeriodEndsAt)
		if err != nil {
			return ErrSubscriptionNotFound
		}
		if !validTransition(current.status, newStatus) {
			return ErrInvalidTransition
		}
		now := time.Now().UTC()
		pastDueSince := current.pastDueSince
		graceEndsAt := current.gracePeriodEndsAt
		var suspendedAt, cancelledAt *time.Time
		switch newStatus {
		case SubPastDue:
			pastDueSince = &now
			g := now.AddDate(0, 0, 7)
			graceEndsAt = &g
		case SubGracePeriod:
			g := now.AddDate(0, 0, 7)
			graceEndsAt = &g
		case SubSuspended:
			suspendedAt = &now
		case SubCancelled:
			cancelledAt = &now
		}
		_, err = tx.Exec(`UPDATE subscriptions SET status = `+s.dialect.Placeholder(1)+`, past_due_since = `+s.dialect.Placeholder(2)+`, grace_period_ends_at = `+s.dialect.Placeholder(3)+`,
			suspended_at = `+s.dialect.Placeholder(4)+`, cancelled_at = `+s.dialect.Placeholder(5)+`, updated_at = `+s.dialect.Placeholder(6)+` WHERE tenant_id = `+s.dialect.Placeholder(7),
			newStatus, pastDueSince, graceEndsAt, suspendedAt, cancelledAt, now, tenantID)
		return err
	})
}

func validTransition(from, to SubscriptionStatus) bool {
	transitions := map[SubscriptionStatus][]SubscriptionStatus{
		SubTrialing:    {SubActive, SubPastDue, SubCancelled, SubExpired},
		SubActive:      {SubPastDue, SubCancelled, SubExpired},
		SubPastDue:     {SubActive, SubGracePeriod, SubSuspended, SubCancelled},
		SubGracePeriod: {SubActive, SubSuspended, SubCancelled, SubExpired},
		SubSuspended:   {SubActive, SubCancelled, SubExpired},
		SubCancelled:   {SubExpired},
		SubExpired:     {},
	}
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}
