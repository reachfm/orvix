package billing

import (
	"database/sql"
	"encoding/json"
	"strings"
)

type QuotaService struct {
	db  *sql.DB
	svc *Service
}

func NewQuotaService(db *sql.DB, svc *Service) *QuotaService {
	return &QuotaService{db: db, svc: svc}
}

func (s *QuotaService) CanCreateDomain(tenantID uint, currentDomains int) *QuotaCheckResult {
	sub, err := s.svc.GetSubscription(tenantID)
	if err != nil {
		return &QuotaCheckResult{Allowed: false, Reason: "no active subscription"}
	}
	if sub.Status == SubSuspended || sub.Status == SubExpired {
		return &QuotaCheckResult{Allowed: false, Reason: "subscription is " + string(sub.Status)}
	}
	plan, err := s.svc.GetPlan(sub.PlanID)
	if err != nil {
		return &QuotaCheckResult{Allowed: false, Reason: "plan not found"}
	}
	remaining := plan.MaxDomains - currentDomains
	return &QuotaCheckResult{
		Allowed:   remaining > 0,
		Limit:     plan.MaxDomains,
		Used:      currentDomains,
		Remaining: remaining,
	}
}

func (s *QuotaService) CanCreateMailbox(tenantID uint, currentMailboxes int) *QuotaCheckResult {
	sub, err := s.svc.GetSubscription(tenantID)
	if err != nil {
		return &QuotaCheckResult{Allowed: false, Reason: "no active subscription"}
	}
	if sub.Status == SubSuspended || sub.Status == SubExpired {
		return &QuotaCheckResult{Allowed: false, Reason: "subscription is " + string(sub.Status)}
	}
	plan, err := s.svc.GetPlan(sub.PlanID)
	if err != nil {
		return &QuotaCheckResult{Allowed: false, Reason: "plan not found"}
	}
	remaining := plan.MaxMailboxes - currentMailboxes
	return &QuotaCheckResult{
		Allowed:   remaining > 0,
		Limit:     plan.MaxMailboxes,
		Used:      currentMailboxes,
		Remaining: remaining,
	}
}

func (s *QuotaService) CanSendEmail(tenantID uint, sentToday int64) *QuotaCheckResult {
	sub, err := s.svc.GetSubscription(tenantID)
	if err != nil {
		return &QuotaCheckResult{Allowed: false, Reason: "no active subscription"}
	}
	if sub.Status == SubSuspended || sub.Status == SubCancelled || sub.Status == SubExpired {
		return &QuotaCheckResult{Allowed: false, Reason: "subscription is " + string(sub.Status)}
	}
	remaining := int64(sub.SendLimitDay) - sentToday
	return &QuotaCheckResult{
		Allowed:   remaining > 0,
		Limit:     sub.SendLimitDay,
		Used:      int(sentToday),
		Remaining: int(remaining),
	}
}

func PlanHasFeature(plan *Plan, feature PlanFeature) bool {
	var features []string
	if plan.Features != "" {
		json.Unmarshal([]byte(plan.Features), &features)
	}
	feat := string(feature)
	for _, f := range features {
		if strings.EqualFold(f, feat) {
			return true
		}
	}
	return false
}

func (s *QuotaService) HasFeature(tenantID uint, feature PlanFeature) bool {
	sub, err := s.svc.GetSubscription(tenantID)
	if err != nil {
		return false
	}
	plan, err := s.svc.GetPlan(sub.PlanID)
	if err != nil {
		return false
	}
	return PlanHasFeature(plan, feature)
}
