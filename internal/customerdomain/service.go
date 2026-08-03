package customerdomain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/dkim"
)

// Service provides customer-facing domain administration operations.
// All domain access is scoped to the tenant provided at construction time.
type Service struct {
	db        *sql.DB
	domains   *coremail.DomainSQLRepo
	inspector *DNSInspector
	verifRepo *VerificationRepo
	dkimRepo  *dkim.SQLRepo
	cooldown  time.Duration
}

// NewService creates a domain administration service.
func NewService(db *sql.DB, domainRepo *coremail.DomainSQLRepo, inspector *DNSInspector, verifRepo *VerificationRepo) *Service {
	return &Service{
		db:        db,
		domains:   domainRepo,
		inspector: inspector,
		verifRepo: verifRepo,
		cooldown:  5 * time.Minute,
	}
}

// SetDKIMRepo injects the DKIM configuration repository for real key matching.
// If never called, dkimRepo remains nil and DKIM matching falls back to the
// previous behaviour (no key matching, no panic).
func (s *Service) SetDKIMRepo(repo *dkim.SQLRepo) {
	s.dkimRepo = repo
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
// Concurrency: uses a DB-backed claim (not a Go mutex) so it is safe
// across multiple application instances. The claim is acquired in a
// short atomic INSERT, released before DNS work, and the snapshot is
// persisted atomically with the claim release in SaveAndRelease.
func (s *Service) VerifyDomain(ctx context.Context, tenantID uint, domainID uint) error {
	d, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return fmt.Errorf("get domain: %w", err)
	}
	if d == nil || d.TenantID != tenantID {
		return ErrDomainNotFound
	}

	// DB-backed atomic claim: also checks cooldown.
	// No transaction held after this call returns.
	claimed, err := s.verifRepo.TryClaim(ctx, domainID, s.cooldown)
	if err != nil {
		return fmt.Errorf("claim verification: %w", err)
	}
	if !claimed {
		return ErrVerificationCooldown
	}

	// DNS inspection runs OUTSIDE any database transaction.
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

	// Persist snapshot and release claim in a single transaction.
	return s.verifRepo.SaveAndRelease(ctx, snap, domainID)
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

// resolveExpectedMX returns the caller-configured expected MX hostnames
// (core-mail.expected_mx) unchanged when set, or the legacy single-host
// fallback "mail.<domainName>" when the config is empty — matching the
// hostname convention documented for deployments that predate the
// ExpectedMX config field. domainName must be the real domain (e.g.
// "example.com"), never a numeric id or path parameter: a bug fixed here
// previously built this fallback from the request's :id path param
// ("mail.42") in the handler, before the domain name was resolved.
func resolveExpectedMX(expectedMX []string, domainName string) []string {
	if len(expectedMX) > 0 {
		return expectedMX
	}
	return []string{"mail." + domainName}
}

// GetEnterpriseDNS returns cached or fresh DNS health data from the enterprise context.
func (s *Service) GetEnterpriseDNS(ctx context.Context, tenantID uint, domainID uint, expectedMX []string) (*EnterpriseDNSHealth, error) {
	d, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	if d == nil || d.TenantID != tenantID {
		return nil, ErrDomainNotFound
	}
	expectedMX = resolveExpectedMX(expectedMX, d.Name)

	health := &EnterpriseDNSHealth{
		DomainID:          d.ID,
		DomainName:        d.Name,
		OperationalStatus: string(d.Status),
	}

	snap, _ := s.verifRepo.GetLatest(ctx, d.ID)
	if snap != nil {
		health.DNSHealth = snap.Status
		health.HealthScore = snap.Score
		health.LastCheckedAt = snap.CheckedAt.Format(time.RFC3339)
		if snap.Evidence != "" {
			var dnsResult DNSResult
			if err := json.Unmarshal([]byte(snap.Evidence), &dnsResult); err == nil {
				health.MX = dnsResult.MX
				health.SPF = dnsResult.SPF
				health.DMARC = dnsResult.DMARC
				if dnsResult.DKIM != nil {
					health.DKIM = &DKIMHealthCheck{
						Selector:   dnsResult.DKIM.Selector,
						Status:     dnsResult.DKIM.Status,
						Expected:   dnsResult.DKIM.Expected,
						Observed:   dnsResult.DKIM.Observed,
						Reason:     dnsResult.DKIM.Reason,
						CheckedAt:  dnsResult.DKIM.CheckedAt,
						PublicTXT:  dnsResult.DKIM.PublicKey,
					}
					if d.DKIMEnabled && d.DKIMSelector != "" {
						health.DKIM.RecordName = d.DKIMSelector + "._domainkey." + d.Name
						health.DKIM.Configured = true
					}
				}
			}
		}
	} else {
		sel := d.DKIMSelector
		if sel == "" {
			sel = "default"
		}
		expectedDKIMRecord := ""
		if s.dkimRepo != nil {
			cfg, err := s.dkimRepo.GetByDomain(ctx, d.Name, nil)
			if err == nil && cfg != nil && cfg.PrivateKeyPEM != "" {
				if rec, ok := deriveExpectedDKIMRecord(cfg.PrivateKeyPEM, sel, d.Name); ok {
					expectedDKIMRecord = rec
				}
			}
		}
		result := s.inspector.InspectEnterprise(ctx, d.Name, expectedMX, sel, expectedDKIMRecord)
		health = result
		health.DomainID = d.ID
		health.OperationalStatus = string(d.Status)
	}

	if health.DKIM != nil && d.DKIMEnabled {
		health.DKIM.Configured = true
		if d.DKIMSelector != "" {
			health.DKIM.Selector = d.DKIMSelector
			health.DKIM.RecordName = d.DKIMSelector + "._domainkey." + d.Name
		}
	}

	return health, nil
}

// VerifyEnterpriseDNS runs a fresh DNS verification with enterprise MX config.
func (s *Service) VerifyEnterpriseDNS(ctx context.Context, tenantID uint, domainID uint, expectedMX []string) (*EnterpriseDNSHealth, error) {
	d, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	if d == nil || d.TenantID != tenantID {
		return nil, ErrDomainNotFound
	}
	expectedMX = resolveExpectedMX(expectedMX, d.Name)

	claimed, err := s.verifRepo.TryClaim(ctx, domainID, s.cooldown)
	if err != nil {
		return nil, fmt.Errorf("claim verification: %w", err)
	}
	if !claimed {
		return nil, ErrVerificationCooldown
	}

	sel := d.DKIMSelector
	if sel == "" {
		sel = "default"
	}

	result := s.inspector.InspectEnterprise(ctx, d.Name, expectedMX, sel, "")
	health := result
	health.DomainID = d.ID
	health.OperationalStatus = string(d.Status)
	if health.LastCheckedAt == "" {
		health.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
	}

	if health.DKIM != nil && d.DKIMEnabled {
		health.DKIM.Configured = true
		if d.DKIMSelector != "" {
			health.DKIM.Selector = d.DKIMSelector
			health.DKIM.RecordName = d.DKIMSelector + "._domainkey." + d.Name
		}
		if s.dkimRepo != nil {
			cfg, err := s.dkimRepo.GetByDomain(ctx, d.Name, nil)
			if err == nil && cfg != nil && cfg.PrivateKeyPEM != "" {
				expected, ok := deriveExpectedDKIMRecord(cfg.PrivateKeyPEM, health.DKIM.Selector, d.Name)
				if ok {
					health.DKIM.Expected = truncateForDisplay(expected, 120)
					dkimResult := s.inspector.CheckDKIM(ctx, d.Name, health.DKIM.Selector, expected)
					if dkimResult.Status == string(DNSStatusPass) {
						health.DKIM.MatchesDNS = true
					} else {
						if observed := normalizeDKIMTXT(dkimResult.Observed); observed == "" {
							dkimResult.Reason = "DKIM record not found"
						} else {
							dkimResult.Reason = "DKIM key mismatch — published key differs from current stored key"
						}
						health.DKIM.MatchesDNS = false
					}
					health.DKIM.Status = dkimResult.Status
					health.DKIM.Reason = dkimResult.Reason
					health.DKIM.Observed = dkimResult.Observed
				}
			}
		}
	}

	evidence, _ := json.Marshal(result)
	snap := &VerificationSnapshot{
		DomainID: domainID,
		Score:    health.HealthScore,
		Status:   health.DNSHealth,
		MXStatus: statusFieldEnterprise(health, func(h *EnterpriseDNSHealth) string {
			if h.MX != nil {
				return h.MX.Status
			}
			return ""
		}),
		SPFStatus: statusFieldEnterprise(health, func(h *EnterpriseDNSHealth) string {
			if h.SPF != nil {
				return h.SPF.Status
			}
			return ""
		}),
		DKIMStatus: statusFieldEnterprise(health, func(h *EnterpriseDNSHealth) string {
			if h.DKIM != nil {
				return h.DKIM.Status
			}
			return ""
		}),
		DMARCStatus: statusFieldEnterprise(health, func(h *EnterpriseDNSHealth) string {
			if h.DMARC != nil {
				return h.DMARC.Status
			}
			return ""
		}),
		Evidence: string(evidence),
	}

	if err := s.verifRepo.SaveAndRelease(ctx, snap, domainID); err != nil {
		return nil, fmt.Errorf("save verification: %w", err)
	}

	return health, nil
}

func statusFieldEnterprise(h *EnterpriseDNSHealth, fn func(*EnterpriseDNSHealth) string) string {
	if h == nil {
		return ""
	}
	return fn(h)
}

// deriveExpectedDKIMRecord returns the DKIM DNS TXT record value the
// tenant's stored private key should be publishing. It delegates entirely
// to dkim.DerivePublicKeyRecordValue — the single shared PEM-to-public-key
// implementation also used by internal/api/handlers/dns_ops.go — rather
// than duplicating the pem.Decode/x509.Parse*/MarshalPKIX sequence here.
// selector and domain are accepted for call-site symmetry with the
// generate/rotate flow but are not otherwise used: the record value format
// depends only on the key, not on where it will be published.
func deriveExpectedDKIMRecord(privPEM string, _ string, _ string) (string, bool) {
	return dkim.DerivePublicKeyRecordValue(privPEM)
}
