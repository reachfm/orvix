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
	dns     dnsops.Resolver
	timeout time.Duration
	nowFunc func() time.Time
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

// Inspect runs all DNS checks for a domain and returns structured results.
func (i *DNSInspector) Inspect(ctx context.Context, domain string, expectedMX string, dkimSelector string, expectedDKIMRecord string) *DNSResult {
	now := i.nowFunc().Format(time.RFC3339)
	result := &DNSResult{}

	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	result.MX = i.checkMX(ctx, domain, expectedMX, now)
	result.SPF = i.checkSPF(ctx, domain, now)
	result.DKIM = i.checkDKIM(ctx, domain, dkimSelector, expectedDKIMRecord, now)
	result.DMARC = i.checkDMARC(ctx, domain, now)

	return result
}

func (i *DNSInspector) checkMX(ctx context.Context, domain, expectedMX, now string) *MXCheck {
	expectedMX = strings.TrimSuffix(expectedMX, ".")
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
	matched := false
	for _, m := range mx {
		host := strings.TrimSuffix(m.Host, ".")
		observed = append(observed, fmt.Sprintf("%s:%d", host, m.Pref))
		if host == expectedMX {
			matched = true
		}
	}

	expected := expectedMX
	if expected == "" {
		expected = "mail." + domain
	}

	if matched {
		return &MXCheck{Status: string(DNSStatusPass), Observed: observed, Expected: expected, CheckedAt: now}
	}
	return &MXCheck{Status: string(DNSStatusWarning), Observed: observed, Expected: expected, Reason: "expected MX host not found", CheckedAt: now}
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

func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
