package dnsverify

import (
	"net"
	"strings"
	"time"
)

// Resolver performs DNS lookups. Uses net.DefaultResolver by default.
type Resolver interface {
	LookupTXT(host string) ([]string, error)
	LookupMX(host string) ([]*net.MX, error)
	LookupHost(host string) ([]string, error)
	LookupAddr(addr string) ([]string, error)
}

type defaultResolver struct{}

func (defaultResolver) LookupTXT(host string) ([]string, error) { return net.LookupTXT(host) }
func (defaultResolver) LookupMX(host string) ([]*net.MX, error) { return net.LookupMX(host) }
func (defaultResolver) LookupHost(host string) ([]string, error) { return net.LookupHost(host) }
func (defaultResolver) LookupAddr(addr string) ([]string, error) { return net.LookupAddr(addr) }

// Service performs real DNS verification for deliverability.
type Service struct {
	resolver   Resolver
	dkimSel string
}

// NewService creates a DNS verification service with a configurable DKIM selector.
func NewService(dkimSelector string) *Service {
	if dkimSelector == "" {
		dkimSelector = "default"
	}
	return &Service{
		resolver:   defaultResolver{},
		dkimSel: dkimSelector,
	}
}

// SetResolver allows injecting a custom resolver (used in tests).
func (s *Service) SetResolver(r Resolver) { s.resolver = r }

// GenerateReport produces a complete deliverability report for a domain.
func (s *Service) GenerateReport(domain string) *DomainDNSReport {
	report := &DomainDNSReport{
		Domain:      domain,
		GeneratedAt: time.Now().UTC(),
	}

	report.SPF = s.checkSPF(domain)
	report.DKIM = s.checkDKIM(domain)
	report.DMARC = s.checkDMARC(domain)
	report.MX = s.checkMX(domain)
	report.A = s.checkA(domain)
	report.AAAA = s.checkAAAA(domain)

	if len(report.A.Records) > 0 {
		report.PTR = s.checkPTR(report.A.Records[0])
	}

	report.Overall = s.computeOverall(report)
	return report
}

// checkSPF looks up TXT records for SPF.
func (s *Service) checkSPF(domain string) SPFResult {
	txts, err := s.resolver.LookupTXT(domain)
	if err != nil {
		return SPFResult{Present: false, Valid: false}
	}
	for _, txt := range txts {
		cleaned := strings.TrimSpace(txt)
		if strings.HasPrefix(cleaned, "v=spf1") {
			return SPFResult{Present: true, Valid: true, Record: cleaned}
		}
	}
	return SPFResult{Present: false, Valid: false}
}

// checkDKIM looks up the DKIM TXT record.
func (s *Service) checkDKIM(domain string) DKIMResult {
	dkimDomain := s.dkimSel + "._domainkey." + domain
	txts, err := s.resolver.LookupTXT(dkimDomain)
	if err != nil {
		return DKIMResult{Present: false, Selector: s.dkimSel}
	}
	for _, txt := range txts {
		cleaned := strings.TrimSpace(txt)
		if strings.Contains(cleaned, "v=DKIM1") || strings.Contains(cleaned, "p=") {
			return DKIMResult{Present: true, Selector: s.dkimSel, Record: cleaned}
		}
	}
	return DKIMResult{Present: false, Selector: s.dkimSel}
}

// checkDMARC looks up the _dmarc TXT record.
func (s *Service) checkDMARC(domain string) DMARCResult {
	dmarcDomain := "_dmarc." + domain
	txts, err := s.resolver.LookupTXT(dmarcDomain)
	if err != nil {
		return DMARCResult{Present: false, Valid: false}
	}
	for _, txt := range txts {
		cleaned := strings.TrimSpace(txt)
		if strings.HasPrefix(cleaned, "v=DMARC1") {
			return DMARCResult{Present: true, Valid: true, Record: cleaned}
		}
	}
	return DMARCResult{Present: false, Valid: false}
}

// checkMX looks up MX records.
func (s *Service) checkMX(domain string) MXResult {
	mxs, err := s.resolver.LookupMX(domain)
	if err != nil || len(mxs) == 0 {
		return MXResult{Present: false}
	}
	records := make([]MXRecord, len(mxs))
	for i, mx := range mxs {
		records[i] = MXRecord{Host: mx.Host, Pref: mx.Pref}
	}
	return MXResult{Present: true, Records: records}
}

// checkA looks up A records.
func (s *Service) checkA(domain string) AResult {
	ips, err := s.resolver.LookupHost(domain)
	if err != nil || len(ips) == 0 {
		return AResult{Present: false}
	}
	return AResult{Present: true, Records: ips}
}

// checkAAAA looks up AAAA records.
func (s *Service) checkAAAA(domain string) AAAAResult {
	// LookupHost returns both A and AAAA. We use net.DefaultResolver which
	// doesn't distinguish. For AAAA-only, we attempt to parse as IPv6.
	// This is a simplification — real AAAA check requires a separate lookup.
	// Since Go's standard resolver doesn't support AAAA-only queries without
	// a custom dialer, we use a heuristic: check if any returned IP is IPv6.
	ips, err := s.resolver.LookupHost(domain)
	if err != nil {
		return AAAAResult{Present: false}
	}
	var v6 []string
	for _, ip := range ips {
		if net.ParseIP(ip) != nil && net.ParseIP(ip).To4() == nil {
			v6 = append(v6, ip)
		}
	}
	if len(v6) > 0 {
		return AAAAResult{Present: true, Records: v6}
	}
	return AAAAResult{Present: false}
}

// checkPTR does reverse DNS lookup.
func (s *Service) checkPTR(ip string) PTRResult {
	names, err := s.resolver.LookupAddr(ip)
	if err != nil || len(names) == 0 {
		return PTRResult{Present: false, Valid: false}
	}
	return PTRResult{Present: true, Valid: true, Records: names}
}

// computeOverall determines the overall deliverability status.
func (s *Service) computeOverall(r *DomainDNSReport) OverallStatus {
	failures := 0
	warnings := 0

	if !r.SPF.Present { failures++ }
	if !r.DKIM.Present { warnings++ }
	if !r.DMARC.Present { warnings++ }
	if !r.MX.Present { failures++ }
	if !r.A.Present { failures++ }
	if r.PTR.Present && !r.PTR.Valid { warnings++ }

	switch {
	case failures > 0:
		return StatusFailed
	case warnings > 0:
		return StatusWarning
	default:
		return StatusHealthy
	}
}
