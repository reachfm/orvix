package customerdomain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/coremail"
)

// Service provides customer-facing domain administration operations.
// All domain access is scoped to the tenant provided at construction time.
type Service struct {
	db        *sql.DB
	domains   *coremail.DomainSQLRepo
	inspector *DNSInspector
	verifRepo *VerificationRepo
	cooldown  time.Duration

	// verifyMu guards verifyLocks. verifyLocks holds a per-domain lock
	// that serializes the cooldown-check → inspect → save sequence in
	// VerifyDomain so concurrent requests for the same domain cannot
	// both pass the cooldown gate and persist duplicate snapshots.
	// This makes verification correct within a single process (the
	// single-node SQLite deployment). A multi-instance deployment
	// additionally needs DB-level enforcement — see the review notes.
	verifyMu    sync.Mutex
	verifyLocks map[uint]*sync.Mutex
}

// NewService creates a domain administration service.
func NewService(db *sql.DB, domainRepo *coremail.DomainSQLRepo, inspector *DNSInspector, verifRepo *VerificationRepo) *Service {
	return &Service{
		db:          db,
		domains:     domainRepo,
		inspector:   inspector,
		verifRepo:   verifRepo,
		cooldown:    5 * time.Minute,
		verifyLocks: make(map[uint]*sync.Mutex),
	}
}

// domainVerifyLock returns the per-domain lock that serializes
// verification for a single domain.
func (s *Service) domainVerifyLock(domainID uint) *sync.Mutex {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	if s.verifyLocks == nil {
		s.verifyLocks = make(map[uint]*sync.Mutex)
	}
	lk := s.verifyLocks[domainID]
	if lk == nil {
		lk = &sync.Mutex{}
		s.verifyLocks[domainID] = lk
	}
	return lk
}

// ListDomains returns paginated domain overviews for a tenant.
func (s *Service) ListDomains(ctx context.Context, tenantID uint, req DomainListRequest) (*DomainListResponse, error) {
	if req.Limit < 1 || req.Limit > 100 {
		req.Limit = 20
	}
	if req.Offset < 0 {
		req.Offset = 0
	}

	tid := tenantID
	filter := coremail.DomainFilter{
		TenantID:   &tid,
		Pagination: coremail.Pagination{Offset: req.Offset, Limit: req.Limit},
	}
	if req.Search != "" {
		filter.Search = req.Search
	}
	if req.Status != "" {
		s := coremail.DomainStatus(req.Status)
		filter.Status = &s
	}

	domains, total, err := s.domains.List(ctx, filter, nil)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}

	overviews := make([]DomainOverview, 0, len(domains))
	for _, d := range domains {
		ov := DomainOverview{
			ID:           d.ID,
			Name:         d.Name,
			Status:       string(d.Status),
			MailboxCount: d.MailboxCount,
			CreatedAt:    d.CreatedAt,
			UpdatedAt:    d.UpdatedAt,
		}
		snap, _ := s.verifRepo.GetLatest(ctx, d.ID)
		if snap != nil {
			ov.HealthScore = snap.Score
			ov.DNSHealth = snap.Status
			cts := snap.CheckedAt.Format(time.RFC3339)
			ov.LastChecked = &cts
		} else {
			ov.DNSHealth = "unchecked"
		}
		overviews = append(overviews, ov)
	}

	return &DomainListResponse{
		Domains: overviews,
		Total:   total,
		Offset:  req.Offset,
		Limit:   req.Limit,
	}, nil
}

// GetDomain returns detailed domain information for a tenant-scoped domain.
func (s *Service) GetDomain(ctx context.Context, tenantID uint, domainID uint) (*DomainDetail, error) {
	d, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	if d == nil || d.TenantID != tenantID {
		return nil, ErrDomainNotFound
	}

	detail := &DomainDetail{
		ID:            d.ID,
		Name:          d.Name,
		Status:        string(d.Status),
		Plan:          d.Plan,
		Description:   d.Description,
		MaxMailboxes:  d.MaxMailboxes,
		MaxAliases:    d.MaxAliases,
		MaxQuotaMB:    d.MaxQuotaMB,
		MailboxCount:  d.MailboxCount,
		DKIMEnabled:   d.DKIMEnabled,
		DKIMSelector:  d.DKIMSelector,
		DMARCEnabled:  d.DMARCEnabled,
		MTASTSEnabled: d.MTASTSEnabled,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}

	expectedMX := "mail." + d.Name
	snap, _ := s.verifRepo.GetLatest(ctx, d.ID)
	if snap != nil {
		detail.HealthScore = snap.Score
		detail.DNSHealth = snap.Status
		if snap.Evidence != "" {
			var dnsResult DNSResult
			if err := json.Unmarshal([]byte(snap.Evidence), &dnsResult); err == nil {
				detail.LatestDNSResult = &dnsResult
			}
		}
	} else {
		result := s.inspector.Inspect(ctx, d.Name, expectedMX, d.DKIMSelector, "")
		detail.LatestDNSResult = result
		hr := HealthScore(result)
		detail.HealthScore = hr.Score
		detail.DNSHealth = overallStatus(result)
	}

	return detail, nil
}

// GetDNS returns structured DNS inspection results for a domain.
func (s *Service) GetDNS(ctx context.Context, tenantID uint, domainID uint) (*DNSResult, error) {
	d, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	if d == nil || d.TenantID != tenantID {
		return nil, ErrDomainNotFound
	}

	expectedMX := "mail." + d.Name
	return s.inspector.Inspect(ctx, d.Name, expectedMX, d.DKIMSelector, ""), nil
}

// VerifyDomain runs a fresh DNS verification and persists the result.
func (s *Service) VerifyDomain(ctx context.Context, tenantID uint, domainID uint) error {
	d, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return fmt.Errorf("get domain: %w", err)
	}
	if d == nil || d.TenantID != tenantID {
		return ErrDomainNotFound
	}

	// Serialize the cooldown-check → inspect → save sequence per domain
	// so two concurrent verify requests for the same domain cannot both
	// observe "no recent verification", run the inspection, and persist
	// duplicate snapshots (bypassing the cooldown).
	lk := s.domainVerifyLock(domainID)
	lk.Lock()
	defer lk.Unlock()

	recent, err := s.verifRepo.ExistsRecent(ctx, domainID, s.cooldown)
	if err != nil {
		return fmt.Errorf("check cooldown: %w", err)
	}
	if recent {
		return ErrVerificationCooldown
	}

	expectedMX := "mail." + d.Name
	result := s.inspector.Inspect(ctx, d.Name, expectedMX, d.DKIMSelector, "")
	hr := HealthScore(result)

	evidence, _ := json.Marshal(result)
	snap := &VerificationSnapshot{
		DomainID: domainID,
		Score:    hr.Score,
		Status:   overallStatus(result),
		MXStatus: statusField(result, func(r *DNSResult) string {
			if r.MX != nil {
				return r.MX.Status
			}
			return ""
		}),
		SPFStatus: statusField(result, func(r *DNSResult) string {
			if r.SPF != nil {
				return r.SPF.Status
			}
			return ""
		}),
		DKIMStatus: statusField(result, func(r *DNSResult) string {
			if r.DKIM != nil {
				return r.DKIM.Status
			}
			return ""
		}),
		DMARCStatus: statusField(result, func(r *DNSResult) string {
			if r.DMARC != nil {
				return r.DMARC.Status
			}
			return ""
		}),
		Evidence: string(evidence),
	}
	return s.verifRepo.Save(ctx, snap)
}

// GetLatestSnapshot returns the most recent persisted verification for a domain.
func (s *Service) GetLatestSnapshot(ctx context.Context, tenantID uint, domainID uint) (*VerificationSnapshot, error) {
	d, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	if d == nil || d.TenantID != tenantID {
		return nil, ErrDomainNotFound
	}
	return s.verifRepo.GetLatest(ctx, domainID)
}

// ── Helpers ──────────────────────────────────────────────────

func overallStatus(r *DNSResult) string {
	if r == nil {
		return "unchecked"
	}
	statuses := []string{}
	if r.MX != nil {
		statuses = append(statuses, r.MX.Status)
	}
	if r.SPF != nil {
		statuses = append(statuses, r.SPF.Status)
	}
	if r.DKIM != nil {
		statuses = append(statuses, r.DKIM.Status)
	}
	if r.DMARC != nil {
		statuses = append(statuses, r.DMARC.Status)
	}
	for _, s := range statuses {
		if s == "fail" || s == "unknown" {
			return s
		}
	}
	for _, s := range statuses {
		if s == "warning" {
			return "warning"
		}
	}
	if len(statuses) > 0 {
		return "pass"
	}
	return "unchecked"
}

func statusField(r *DNSResult, fn func(*DNSResult) string) string {
	if r == nil {
		return ""
	}
	return fn(r)
}

var (
	ErrDomainNotFound       = fmt.Errorf("domain not found")
	ErrVerificationCooldown = fmt.Errorf("verification cooldown active, try again later")
	ErrInvalidDomainID      = fmt.Errorf("invalid domain id")
)
