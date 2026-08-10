package domain

import (
	"context"
	"strings"

	"github.com/orvix/orvix/internal/tlsmgmt"
)

// tlsStatusSource is the narrow interface this package needs from
// tlsmgmt.Service — reused, not duplicated. Defined here (consumer
// side) so domain admin depends on a port, not the concrete type.
type tlsStatusSource interface {
	LoadCertificates(ctx context.Context) ([]tlsmgmt.TLSCertificate, error)
	ListUploadedCertificates(ctx context.Context, tenantID int64) ([]tlsmgmt.TLSCertificate, error)
}

// SetTLSService wires the platform's hardened TLS/certificate lifecycle
// (upload, validation, expiry, reload) into the domain admin service so
// a domain's certificate status can be reported alongside its DNS/DKIM
// state, without re-implementing certificate parsing or expiry logic.
func (s *Service) SetTLSService(t tlsStatusSource) {
	s.tlsSvc = t
}

// DomainTLSStatusResult reports what the reused TLS lifecycle knows
// about a specific domain's certificate. Automated ACME issuance is
// not implemented in this build (see AdminSslAcmeStatus) — this is a
// status/expiry read, not a renewal trigger; RenewalRequired signals
// that the operator must act via the existing upload/reload endpoints.
type DomainTLSStatusResult struct {
	Configured      bool     `json:"configured"`
	Source          string   `json:"source"` // "uploaded", "configured", or "none"
	CommonName      string   `json:"common_name,omitempty"`
	SANs            []string `json:"sans,omitempty"`
	NotAfter        string   `json:"not_after,omitempty"`
	DaysRemaining   int      `json:"days_remaining,omitempty"`
	Status          string   `json:"status,omitempty"`
	RenewalRequired bool     `json:"renewal_required"`
	Note            string   `json:"note,omitempty"`
}

// DomainTLSStatus resolves the domain, then finds the certificate (if
// any) whose common name or SANs cover it — checking tenant-uploaded
// certificates first (more specific, customer-controlled), then the
// platform's configured/on-disk certificates (e.g. a wildcard cert
// covering many domains).
func (s *Service) DomainTLSStatus(ctx context.Context, id, tenantID uint) (*DomainTLSStatusResult, error) {
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}
	if s.tlsSvc == nil {
		return &DomainTLSStatusResult{Configured: false, Source: "none", Note: "TLS service unavailable"}, nil
	}

	if uploaded, err := s.tlsSvc.ListUploadedCertificates(ctx, int64(tenantID)); err == nil {
		if cert := matchDomainCert(uploaded, d.Name); cert != nil {
			return domainTLSResultFromCert(cert, "uploaded"), nil
		}
	}

	if configured, err := s.tlsSvc.LoadCertificates(ctx); err == nil {
		if cert := matchDomainCert(configured, d.Name); cert != nil {
			return domainTLSResultFromCert(cert, "configured"), nil
		}
	}

	return &DomainTLSStatusResult{
		Configured: false,
		Source:     "none",
		Note:       "no certificate found covering this domain — automated ACME issuance is not implemented in this build; upload one via /api/v1/admin/ssl/certificates",
	}, nil
}

func matchDomainCert(certs []tlsmgmt.TLSCertificate, domainName string) *tlsmgmt.TLSCertificate {
	want := strings.ToLower(strings.TrimSpace(domainName))
	for i := range certs {
		c := &certs[i]
		if certCoversName(c, want) {
			return c
		}
	}
	return nil
}

// certCoversName reports whether cert's CommonName or any SAN covers
// name — exact match or a leading-wildcard match (e.g. "*.example.com"
// covers "mail.example.com" but never the bare apex, matching standard
// wildcard certificate semantics).
func certCoversName(c *tlsmgmt.TLSCertificate, name string) bool {
	candidates := append([]string{c.CommonName}, c.SANs...)
	for _, cand := range candidates {
		cand = strings.ToLower(strings.TrimSpace(cand))
		if cand == "" {
			continue
		}
		if cand == name {
			return true
		}
		if strings.HasPrefix(cand, "*.") {
			suffix := cand[1:] // ".example.com"
			if strings.HasSuffix(name, suffix) && name != strings.TrimPrefix(suffix, ".") {
				return true
			}
		}
	}
	return false
}

func domainTLSResultFromCert(c *tlsmgmt.TLSCertificate, source string) *DomainTLSStatusResult {
	return &DomainTLSStatusResult{
		Configured:      true,
		Source:          source,
		CommonName:      c.CommonName,
		SANs:            c.SANs,
		NotAfter:        c.NotAfter.UTC().Format("2006-01-02T15:04:05Z"),
		DaysRemaining:   c.DaysRemaining,
		Status:          string(c.Status),
		RenewalRequired: c.Status == tlsmgmt.CertWarning || c.Status == tlsmgmt.CertExpired || c.Status == tlsmgmt.CertInvalid,
	}
}
