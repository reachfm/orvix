package customerdomain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dnsops"
)

// DNSInspector performs bounded DNS checks for a domain.
type DNSInspector struct {
	dns           dnsops.Resolver
	timeout       time.Duration
	nowFunc       func() time.Time
	mtastsFetcher *MTASTSFetcher
}

// NewDNSInspector creates a DNS inspector backed by the given resolver.
func NewDNSInspector(dns dnsops.Resolver) *DNSInspector {
	return &DNSInspector{
		dns:     dns,
		timeout: 15 * time.Second,
		nowFunc: func() time.Time { return time.Now().UTC() },
	}
}

// WithClock sets a deterministic clock for testing.
func (i *DNSInspector) WithClock(now func() time.Time) *DNSInspector {
	i.nowFunc = now
	return i
}

// WithMTASTSFetcher sets the HTTPS policy fetcher for MTA-STS policy checks.
func (i *DNSInspector) WithMTASTSFetcher(f *MTASTSFetcher) *DNSInspector {
	i.mtastsFetcher = f
	return i
}

// Inspect runs all DNS checks for a domain and returns structured results.
func (i *DNSInspector) Inspect(ctx context.Context, domain string, expectedMX string, dkimSelector string, expectedDKIMRecord string) *DNSResult {
	now := i.nowFunc().Format(time.RFC3339)
	result := &DNSResult{}

	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	result.MX = i.checkMX(ctx, domain, []string{expectedMX}, now)
	result.SPF = i.checkSPF(ctx, domain, now)
	result.DKIM = i.checkDKIM(ctx, domain, dkimSelector, expectedDKIMRecord, now)
	result.DMARC = i.checkDMARC(ctx, domain, now)

	return result
}

func (i *DNSInspector) checkMX(ctx context.Context, domain string, expectedHosts []string, now string) *MXCheck {
	normalized := make([]string, 0, len(expectedHosts))
	for _, h := range expectedHosts {
		h = strings.TrimSuffix(h, ".")
		if h != "" {
			normalized = append(normalized, h)
		}
	}

	mx, err := i.dns.LookupMX(ctx, domain)
	if err != nil {
		if isDNSNotFound(err) {
			return &MXCheck{Status: string(DNSStatusFail), Reason: "no MX records found", CheckedAt: now}
		}
		if isDNSTimeout(err) {
			return &MXCheck{Status: string(DNSStatusUnknown), Reason: "dns timeout", CheckedAt: now}
		}
		return &MXCheck{Status: string(DNSStatusUnknown), Reason: fmt.Sprintf("dns error: %v", err), CheckedAt: now}
	}
	if len(mx) == 0 {
		return &MXCheck{Status: string(DNSStatusFail), Reason: "no MX records found", CheckedAt: now}
	}

	observed := make([]string, 0, len(mx))
	observedHosts := make(map[string]bool)
	for _, m := range mx {
		host := strings.TrimSuffix(m.Host, ".")
		observed = append(observed, fmt.Sprintf("%s:%d", host, m.Pref))
		observedHosts[host] = true
	}

	if len(normalized) == 0 {
		fallback := "mail." + domain
		normalized = append(normalized, fallback)
	}

	expectedDisplay := strings.Join(normalized, ", ")

	matched := 0
	for _, host := range normalized {
		if observedHosts[host] {
			matched++
		}
	}

	if matched == len(normalized) {
		return &MXCheck{Status: string(DNSStatusPass), Observed: observed, Expected: expectedDisplay, CheckedAt: now}
	}
	if matched > 0 {
		return &MXCheck{Status: string(DNSStatusWarning), Observed: observed, Expected: expectedDisplay, Reason: "not all expected MX hosts found", CheckedAt: now}
	}
	return &MXCheck{Status: string(DNSStatusFail), Observed: observed, Expected: expectedDisplay, Reason: "expected MX host not found", CheckedAt: now}
}

func (i *DNSInspector) checkSPF(ctx context.Context, domain, now string) *SPFCheck {
	records, err := i.dns.LookupTXT(ctx, domain)
	if err != nil {
		if isDNSNotFound(err) {
			return &SPFCheck{Status: string(DNSStatusFail), Reason: "no SPF record found", CheckedAt: now}
		}
		return &SPFCheck{Status: string(DNSStatusUnknown), Reason: fmt.Sprintf("dns error: %v", err), CheckedAt: now}
	}

	var spf string
	for _, r := range records {
		if strings.HasPrefix(strings.ToLower(r), "v=spf1") {
			if spf != "" {
				return &SPFCheck{Status: string(DNSStatusFail), Observed: spf, Reason: "multiple SPF records found", CheckedAt: now}
			}
			spf = r
		}
	}

	if spf == "" {
		return &SPFCheck{Status: string(DNSStatusFail), Reason: "no SPF record found", CheckedAt: now}
	}
	return &SPFCheck{Status: string(DNSStatusPass), Observed: spf, CheckedAt: now}
}

func (i *DNSInspector) checkDKIM(ctx context.Context, domain, selector, expectedRecord, now string) *DKIMCheck {
	if selector == "" {
		selector = "default"
	}
	dkimDomain := fmt.Sprintf("%s._domainkey.%s", selector, domain)
	records, err := i.dns.LookupTXT(ctx, dkimDomain)
	if err != nil {
		if isDNSNotFound(err) {
			return &DKIMCheck{Selector: selector, Status: string(DNSStatusFail), Reason: "DKIM record not found", CheckedAt: now}
		}
		return &DKIMCheck{Selector: selector, Status: string(DNSStatusUnknown), Reason: fmt.Sprintf("dns error: %v", err), CheckedAt: now}
	}

	observed := strings.Join(records, "")
	if expectedRecord != "" && observed != expectedRecord {
		return &DKIMCheck{Selector: selector, Status: string(DNSStatusFail), Observed: truncateForDisplay(observed, 120), Expected: truncateForDisplay(expectedRecord, 120), Reason: "DKIM record mismatch", CheckedAt: now}
	}
	return &DKIMCheck{Selector: selector, Status: string(DNSStatusPass), Observed: truncateForDisplay(observed, 120), CheckedAt: now}
}

// CheckDKIM is the public wrapper around checkDKIM for callers that
// have a derived expected record (post-service wiring).
func (i *DNSInspector) CheckDKIM(ctx context.Context, domain, selector, expectedRecord string) *DKIMCheck {
	now := i.nowFunc().Format(time.RFC3339)
	return i.checkDKIM(ctx, domain, selector, expectedRecord, now)
}

func normalizeDKIMTXT(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	s = strings.Trim(s, `"`)
	return s
}

func (i *DNSInspector) checkDMARC(ctx context.Context, domain, now string) *DMARCCheck {
	dmarcDomain := "_dmarc." + domain
	records, err := i.dns.LookupTXT(ctx, dmarcDomain)
	if err != nil {
		if isDNSNotFound(err) {
			return &DMARCCheck{Status: string(DNSStatusFail), Reason: "DMARC record not found", CheckedAt: now}
		}
		return &DMARCCheck{Status: string(DNSStatusUnknown), Reason: fmt.Sprintf("dns error: %v", err), CheckedAt: now}
	}

	var dmarc string
	for _, r := range records {
		if strings.HasPrefix(strings.ToLower(r), "v=dmarc1") {
			dmarc = r
			break
		}
	}

	if dmarc == "" {
		return &DMARCCheck{Status: string(DNSStatusFail), Reason: "DMARC record not found", CheckedAt: now}
	}

	p := "none"
	if strings.Contains(strings.ToLower(dmarc), "p=reject") {
		p = "reject"
	} else if strings.Contains(strings.ToLower(dmarc), "p=quarantine") {
		p = "quarantine"
	}

	if p == "none" {
		return &DMARCCheck{Status: string(DNSStatusWarning), Observed: truncateForDisplay(dmarc, 200), Reason: "DMARC policy is p=none (no enforcement)", CheckedAt: now}
	}
	return &DMARCCheck{Status: string(DNSStatusPass), Observed: truncateForDisplay(dmarc, 200), CheckedAt: now}
}

func isDNSNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such host") || strings.Contains(msg, "not found") || strings.Contains(msg, "nxdomain")
}

func isDNSTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "i/o timeout")
}

// InspectEnterprise runs all DNS checks including MTA-STS and TLS-RPT for the admin panel.
func (i *DNSInspector) InspectEnterprise(ctx context.Context, domain string, expectedMX []string, dkimSelector string, expectedDKIMRecord string) *EnterpriseDNSHealth {
	now := i.nowFunc().Format(time.RFC3339)
	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	mxResult := i.checkMX(ctx, domain, expectedMX, now)
	spfResult := i.checkSPF(ctx, domain, now)
	dkimRaw := i.checkDKIM(ctx, domain, dkimSelector, expectedDKIMRecord, now)
	dmarcResult := i.checkDMARC(ctx, domain, now)
	mtastsResult := i.checkMTASTS(ctx, domain, now)
	tlsrptResult := i.checkTLSRPT(ctx, domain, now)

	var mtastsPolicy *MTASTSPolicy
	if i.mtastsFetcher != nil && mtastsResult.Status == "pass" {
		p, err := i.mtastsFetcher.Fetch(ctx, domain)
		if err != nil || p == nil || !p.Valid {
			mtastsResult.Reason = "MTA-STS TXT valid but HTTPS policy " + reasonFromError(err, p)
			if mtastsResult.Reason == "" {
				mtastsResult.Reason = "MTA-STS TXT valid but HTTPS policy unavailable"
			}
			mtastsResult.Status = "warning"
		} else {
			mtastsPolicy = p
		}
	}

	dkimCheck := &DKIMHealthCheck{
		Selector:  dkimRaw.Selector,
		Status:    dkimRaw.Status,
		Expected:  dkimRaw.Expected,
		Observed:  dkimRaw.Observed,
		Reason:    dkimRaw.Reason,
		CheckedAt: dkimRaw.CheckedAt,
	}

	if dkimSelector != "" {
		dkimCheck.RecordName = dkimSelector + "._domainkey." + domain
		dkimCheck.Configured = true
		if dkimRaw.Status == string(DNSStatusPass) && expectedDKIMRecord != "" && dkimRaw.Observed == expectedDKIMRecord {
			dkimCheck.MatchesDNS = true
		}
		if dkimRaw.PublicKey != "" {
			dkimCheck.PublicTXT = truncateForDisplay(dkimRaw.PublicKey, 120)
		}
	}

	health := &EnterpriseDNSHealth{
		DomainName:   domain,
		MX:           mxResult,
		SPF:          spfResult,
		DKIM:         dkimCheck,
		DMARC:        dmarcResult,
		MTASTS:       mtastsResult,
		TLSRPT:       tlsrptResult,
		MTASTSPolicy: mtastsPolicy,
	}

	score := computeEnterpriseHealthScore(health)
	health.HealthScore = score.Score
	health.DNSHealth = enterpriseOverallStatus(health)
	health.LastCheckedAt = now

	return health
}

func (i *DNSInspector) checkMTASTS(ctx context.Context, domain, now string) *MTASTSCheck {
	mtastsDomain := "_mta-sts." + domain
	records, err := i.dns.LookupTXT(ctx, mtastsDomain)
	if err != nil {
		if isDNSNotFound(err) {
			return &MTASTSCheck{Status: string(DNSStatusFail), Reason: "MTA-STS record not found", CheckedAt: now}
		}
		return &MTASTSCheck{Status: string(DNSStatusUnknown), Reason: fmt.Sprintf("dns error: %v", err), CheckedAt: now}
	}

	var mtasts string
	for _, r := range records {
		if strings.HasPrefix(strings.ToLower(r), "v=stsv1") {
			mtasts = r
			break
		}
	}

	if mtasts == "" {
		return &MTASTSCheck{Status: string(DNSStatusFail), Reason: "no valid MTA-STS record found", CheckedAt: now}
	}

	hasID := strings.Contains(strings.ToLower(mtasts), "id=")
	if !hasID {
		return &MTASTSCheck{Status: string(DNSStatusWarning), Observed: truncateForDisplay(mtasts, 200), Reason: "MTA-STS record missing id tag", CheckedAt: now}
	}

	return &MTASTSCheck{Status: string(DNSStatusPass), Observed: truncateForDisplay(mtasts, 200), CheckedAt: now}
}

func (i *DNSInspector) checkTLSRPT(ctx context.Context, domain, now string) *TLSRPTCheck {
	tlsrptDomain := "_smtp._tls." + domain
	records, err := i.dns.LookupTXT(ctx, tlsrptDomain)
	if err != nil {
		if isDNSNotFound(err) {
			return &TLSRPTCheck{Status: string(DNSStatusFail), Reason: "TLS-RPT record not found", CheckedAt: now}
		}
		return &TLSRPTCheck{Status: string(DNSStatusUnknown), Reason: fmt.Sprintf("dns error: %v", err), CheckedAt: now}
	}

	var tlsrpt string
	for _, r := range records {
		if strings.HasPrefix(strings.ToLower(r), "v=tlsrptv1") {
			tlsrpt = r
			break
		}
	}

	if tlsrpt == "" {
		return &TLSRPTCheck{Status: string(DNSStatusFail), Reason: "no valid TLS-RPT record found", CheckedAt: now}
	}

	hasRUA := strings.Contains(strings.ToLower(tlsrpt), "rua=")
	if !hasRUA {
		return &TLSRPTCheck{Status: string(DNSStatusWarning), Observed: truncateForDisplay(tlsrpt, 200), Reason: "TLS-RPT record missing rua tag", CheckedAt: now}
	}

	return &TLSRPTCheck{Status: string(DNSStatusPass), Observed: truncateForDisplay(tlsrpt, 200), CheckedAt: now}
}

func computeEnterpriseHealthScore(health *EnterpriseDNSHealth) HealthScoreResult {
	result := HealthScoreResult{
		Breakdown: make(map[string]ScoreComponent),
	}

	add := func(name string, weight int, status string) {
		earned := 0
		switch status {
		case "pass":
			earned = weight
		case "warning":
			earned = weight / 2
		}
		result.Breakdown[name] = ScoreComponent{Weight: weight, Earned: earned, Status: status}
		result.Score += earned
	}

	mx := "unknown"
	if health.MX != nil {
		mx = health.MX.Status
	}
	spf := "unknown"
	if health.SPF != nil {
		spf = health.SPF.Status
	}
	dkim := "unknown"
	if health.DKIM != nil {
		dkim = health.DKIM.Status
	}
	dmarc := "unknown"
	if health.DMARC != nil {
		dmarc = health.DMARC.Status
	}
	mtasts := "unknown"
	if health.MTASTS != nil {
		mtasts = health.MTASTS.Status
	}
	tlsrpt := "unknown"
	if health.TLSRPT != nil {
		tlsrpt = health.TLSRPT.Status
	}

	add("mx", 25, mx)
	add("spf", 15, spf)
	add("dkim", 25, dkim)
	add("dmarc", 15, dmarc)
	add("mtasts", 10, mtasts)
	add("tlsrpt", 10, tlsrpt)

	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score > 100 {
		result.Score = 100
	}
	return result
}

func enterpriseOverallStatus(health *EnterpriseDNSHealth) string {
	statuses := []string{}
	if health.MX != nil {
		statuses = append(statuses, health.MX.Status)
	}
	if health.SPF != nil {
		statuses = append(statuses, health.SPF.Status)
	}
	if health.DKIM != nil {
		statuses = append(statuses, health.DKIM.Status)
	}
	if health.DMARC != nil {
		statuses = append(statuses, health.DMARC.Status)
	}
	if health.MTASTS != nil {
		statuses = append(statuses, health.MTASTS.Status)
	}
	if health.TLSRPT != nil {
		statuses = append(statuses, health.TLSRPT.Status)
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

func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func reasonFromError(err error, p *MTASTSPolicy) string {
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
			return "policy_unverified: HTTPS timeout"
		}
		if strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") {
			return "policy_unverified: endpoint unavailable"
		}
		if strings.Contains(msg, "unexpected status") {
			return "policy_unverified: " + msg
		}
		if strings.Contains(msg, "tls") || strings.Contains(msg, "certificate") {
			return "policy_unverified: TLS error"
		}
		return "policy_unverified: " + msg
	}
	if p != nil && p.Error != "" {
		return "policy_unverified: " + p.Error
	}
	return ""
}
