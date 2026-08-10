package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type QuotaService struct {
	db        *sql.DB
	svc       *Service
	overrides *OverrideStore // nil is valid: quota checks then fall back to plan limits only
}

func NewQuotaService(db *sql.DB, svc *Service) *QuotaService {
	return &QuotaService{db: db, svc: svc}
}

// WithOverrides attaches an OverrideStore so quota checks consult active
// operator overrides before falling back to the plan's static limit.
// Returns the same *QuotaService for convenient chaining at construction.
func (s *QuotaService) WithOverrides(o *OverrideStore) *QuotaService {
	s.overrides = o
	return s
}

// effectiveLimit returns the active override's limit for (tenantID, dim)
// if one exists and hasn't expired, else planLimit unchanged.
func (s *QuotaService) effectiveLimit(tenantID uint, dim OverrideDimension, planLimit int) int {
	if s.overrides == nil {
		return planLimit
	}
	o, err := s.overrides.ActiveOverride(context.Background(), tenantID, dim, time.Now().UTC())
	if err != nil || o == nil {
		return planLimit
	}
	return o.Limit
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
	limit := s.effectiveLimit(tenantID, OverrideMaxDomains, plan.MaxDomains)
	remaining := limit - currentDomains
	return &QuotaCheckResult{
		Allowed:   remaining > 0,
		Limit:     limit,
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
	limit := s.effectiveLimit(tenantID, OverrideMaxMailboxes, plan.MaxMailboxes)
	remaining := limit - currentMailboxes
	return &QuotaCheckResult{
		Allowed:   remaining > 0,
		Limit:     limit,
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
	limit := s.effectiveLimit(tenantID, OverrideSendLimitDay, sub.SendLimitDay)
	remaining := int64(limit) - sentToday
	return &QuotaCheckResult{
		Allowed:   remaining > 0,
		Limit:     limit,
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
