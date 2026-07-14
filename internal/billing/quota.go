package billing

import "database/sql"

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
	remaining := int64(sub.SendLimitDay) - sentToday
	return &QuotaCheckResult{
		Allowed:   remaining > 0,
		Limit:     sub.SendLimitDay,
		Used:      int(sentToday),
		Remaining: int(remaining),
	}
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
	return planHasFeature(plan, feature)
}

func planHasFeature(plan *Plan, feature PlanFeature) bool {
	freeOnly := map[PlanFeature]bool{
		FeatureCustomDomain: true,
		FeatureDKIM:         true,
	}
	businessPlus := map[PlanFeature]bool{
		FeatureCustomDomain:    true,
		FeatureDKIM:            true,
		FeatureMTASTS:          true,
		FeatureAPI:             true,
		FeatureTeam:            true,
		FeatureGroups:          true,
		FeatureCatchAll:        true,
		FeatureMailForwarding:  true,
		FeatureBackup:          true,
		FeatureAuditLog:        true,
		FeatureMFA:             true,
	}
	switch plan.ID {
	case PlanFree:
		return freeOnly[feature]
	case PlanStarter:
		return feature == FeatureCustomDomain || feature == FeatureDKIM || feature == FeatureMTASTS || feature == FeatureAPI || feature == FeatureTeam
	case PlanBusiness:
		return businessPlus[feature]
	case PlanEnterprise:
		return true
	}
	return false
}
