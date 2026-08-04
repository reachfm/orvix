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

	// expectations is the single canonical source for every required
	// record value (SPF, DMARC, autodiscover SRV). Its zero value
	// reproduces the historical hard-coded behaviour, so an inspector
	// built without WithExpectations still works.
	expectations CanonicalExpectations
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

// WithExpectations installs the operator-configured canonical DNS
// expectations (from coremail.expected_spf / expected_dmarc_* /
// autodiscover_srv_*). Every consumer — verifier, `expected` field,
// guidance text and downloaded record file — reads from this one value.
func (i *DNSInspector) WithExpectations(e CanonicalExpectations) *DNSInspector {
	i.expectations = e
	return i
}

// Expectations exposes the canonical expectations so handlers and tests can
// assert that the value they configured is the value the UI receives.
func (i *DNSInspector) Expectations() CanonicalExpectations { return i.expectations }

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

	// Compare the LIVE record against the one canonical expectation. This is
	// consumer (a) of CanonicalExpectations: the verifier and the `expected`
	// value shown in the UI must be the same string, or the console can tell
	// an operator their record is correct while requiring a different one.
	//
	// Only an operator-CONFIGURED policy is enforced. With no expected_spf
	// set the expectation is the generic domain-derived fallback, which is
	// not authoritative enough to warn against — that preserves the previous
	// behaviour for deployments that have not configured this yet.
	expected := i.expectations.SPF(domain)
	if i.expectations.SPFIsConfigured() && !spfEquivalent(spf, expected) {
		return &SPFCheck{
			Status:    string(DNSStatusWarning),
			Observed:  spf,
			Expected:  expected,
			Reason:    "published SPF record does not match the configured ORVIX policy",
			CheckedAt: now,
		}
	}
	return &SPFCheck{Status: string(DNSStatusPass), Observed: spf, Expected: expected, CheckedAt: now}
}

// spfEquivalent compares two SPF records ignoring case and redundant
// whitespace. Mechanism ORDER is significant in SPF evaluation, so it is
// deliberately NOT normalised away.
func spfEquivalent(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
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

	logicalRecords := splitDKIMTXTRecords(records)
	if len(logicalRecords) > 1 {
		return &DKIMCheck{Selector: selector, Status: string(DNSStatusFail), Observed: truncateForDisplay(strings.Join(records, " | "), 120), Reason: fmt.Sprintf("multiple conflicting DKIM records published at %s (%d records)", dkimDomain, len(logicalRecords)), CheckedAt: now}
	}
	observed := strings.Join(logicalRecords[0], "")

	// Never compare the raw record strings. Parse tags and compare the
	// decoded p= public-key bytes, so reordered tags, harmless whitespace,
	// and additional optional tags in either record do not produce a
	// false mismatch — and so a malformed/missing/revoked/duplicate p=
	// tag is always rejected explicitly rather than silently accepted.
	if expectedRecord != "" {
		match, err := dkimRecordsMatch(observed, expectedRecord)
		if err != nil {
			return &DKIMCheck{Selector: selector, Status: string(DNSStatusFail), Observed: truncateForDisplay(observed, 120), Expected: truncateForDisplay(expectedRecord, 120), Reason: "DKIM record invalid: " + err.Error(), CheckedAt: now}
		}
		if !match {
			return &DKIMCheck{Selector: selector, Status: string(DNSStatusFail), Observed: truncateForDisplay(observed, 120), Expected: truncateForDisplay(expectedRecord, 120), Reason: "DKIM record mismatch", CheckedAt: now}
		}
		return &DKIMCheck{Selector: selector, Status: string(DNSStatusPass), Observed: truncateForDisplay(observed, 120), CheckedAt: now}
	}

	// No expected value was supplied: still validate the published record
	// is well-formed and not revoked, rather than accepting any garbage
	// that happens to be present at the selector name.
	if _, err := dkimPublicKeyBytes(observed); err != nil {
		return &DKIMCheck{Selector: selector, Status: string(DNSStatusFail), Observed: truncateForDisplay(observed, 120), Reason: "DKIM record invalid: " + err.Error(), CheckedAt: now}
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

	// Consumer (a) of CanonicalExpectations for DMARC: the enforcement level
	// actually published is compared against the one configured policy.
	// p=reject is stricter than a configured p=quarantine, so it is accepted
	// rather than flagged — only a WEAKER-than-required policy warns.
	// Only an operator-CONFIGURED policy is enforced. With no expectation
	// configured, ORVIX does not invent one and grade the live record
	// against it — the "p=none means no enforcement" warning above is a
	// universal DMARC truth and still applies, but a stricter requirement
	// is not asserted on an upgraded install that never opted in.
	wantPolicy := strings.TrimSpace(i.expectations.DMARCPolicy)
	if wantPolicy == "reject" && p != "reject" {
		return &DMARCCheck{
			Status:    string(DNSStatusWarning),
			Observed:  truncateForDisplay(dmarc, 200),
			Reason:    "published DMARC policy p=" + p + " is weaker than the configured policy p=reject",
			CheckedAt: now,
		}
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

	// A valid MTA-STS TXT record on its own proves nothing: the policy it
	// points at must actually be served over HTTPS and parse. Until that is
	// confirmed, MTA-STS can never be recorded as a full pass — including
	// when no fetcher is wired at all, which previously left the check at
	// "pass" and allowed an unverified deployment to reach 100%.
	var mtastsPolicy *MTASTSPolicy
	if mtastsResult.Status == "pass" {
		switch {
		case i.mtastsFetcher == nil:
			mtastsResult.Status = "warning"
			mtastsResult.Reason = "MTA-STS TXT valid but HTTPS policy unverified: no policy fetcher configured"
		default:
			p, err := i.mtastsFetcher.Fetch(ctx, domain)
			if err != nil || p == nil || !p.Valid {
				mtastsResult.Reason = "MTA-STS TXT valid but HTTPS policy " + reasonFromError(err, p)
				if strings.HasSuffix(mtastsResult.Reason, "HTTPS policy ") {
					mtastsResult.Reason = "MTA-STS TXT valid but HTTPS policy unverified: policy unavailable"
				}
				mtastsResult.Status = "warning"
			} else {
				mtastsPolicy = p
			}
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

	mailHost := primaryMailHost(expectedMX, domain)
	mailHostA := i.checkHostA(ctx, mailHost, now)

	health := &EnterpriseDNSHealth{
		DomainName:   domain,
		MX:           mxResult,
		SPF:          spfResult,
		DKIM:         dkimCheck,
		DMARC:        dmarcResult,
		MTASTS:       mtastsResult,
		TLSRPT:       tlsrptResult,
		MTASTSPolicy: mtastsPolicy,

		MailHostA:    mailHostA,
		MailHostAAAA: i.checkHostAAAA(ctx, mailHost, now),
		MXHosts:      i.checkMXHostResolution(ctx, domain, now),
		PTR:          i.checkPTR(ctx, mailHost, mailHostA, now),
		Autodiscover: i.checkDelegationCNAME(ctx, "autodiscover."+domain, mailHost, "Outlook autodiscover", now),
		Autoconfig:   i.checkDelegationCNAME(ctx, "autoconfig."+domain, mailHost, "Thunderbird autoconfig", now),
		TLSA:         i.checkTLSA(mailHost, now),

		AutodiscoverSRV: i.checkAutodiscoverSRV(ctx, domain, mailHost, now),
	}

	i.applyCanonicalExpectations(health, domain, mailHost)

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

// applyCanonicalExpectations guarantees every required record carries a real
// `expected` value and concrete repair guidance, and that a record whose
// required value is genuinely indeterminate is marked
// configuration_required rather than being allowed to read as a pass.
func (i *DNSInspector) applyCanonicalExpectations(health *EnterpriseDNSHealth, domain, mailHost string) {
	e := i.expectations

	if c := health.MX; c != nil {
		if c.Expected == "" {
			c.Expected = mailHost
		}
		c.Guidance = fmt.Sprintf("Add an MX record for %s pointing to %s with priority 10.", domain, c.Expected)
	}

	if c := health.SPF; c != nil {
		// Consumers (b) and (c): the `expected` field returned to the UI
		// and the repair guidance both read the SAME
		// CanonicalExpectations value the verifier compared against, so
		// they can never diverge. The frontend's downloadable record file
		// (consumer (d)) renders this same `expected` field.
		if !e.SPFIsConfigured() {
			// No invented requirement. The observed record is preserved
			// exactly as published, so an operator upgrading from a YAML
			// that predates expected_spf still sees their real policy and
			// is never told to replace a valid record with a generic one.
			// ORVIX simply has nothing authoritative to compare against.
			c.Expected = ""
			c.Status = string(DNSStatusConfigRequired)
			c.Reason = "server configuration " + ConfigKeySPF + " is not set, so ORVIX cannot state a required SPF policy for this domain"
			c.Guidance = "Set " + ConfigKeySPF + " in the ORVIX server configuration to this deployment's real SPF policy, then re-check. Until it is set, ORVIX will neither claim this record passes nor ask you to change the record currently published."
		} else {
			c.Expected = e.SPF(domain)
			c.Guidance = fmt.Sprintf("Publish a single TXT record at %s with the value %q. Exactly one SPF record may exist per domain.", domain, c.Expected)
		}
	}

	if c := health.DMARC; c != nil {
		// Consumers (b)/(c)/(d) again — one source, four readers.
		if !e.DMARCIsConfigured() {
			// dmarc@<domain> is not provisioned by ORVIX. Publishing it
			// would send aggregate reports into a black hole, so it is
			// never presented as the required value.
			c.Expected = ""
			c.Status = string(DNSStatusConfigRequired)
			c.Reason = "server configuration " + ConfigKeyDMARCRUA + " is not set, so ORVIX cannot state a required DMARC record for this domain"
			c.Guidance = "Set " + ConfigKeyDMARCRUA + " to a reporting mailbox you actually receive (and optionally " + "coremail.expected_dmarc_policy" + "), then re-check. ORVIX does not provision a dmarc@ address and will not invent one."
		} else {
			c.Expected = e.DMARC(domain)
			c.Guidance = fmt.Sprintf("Add a TXT record at _dmarc.%s with the value %q.", domain, c.Expected)
		}
	}

	if c := health.MTASTS; c != nil {
		if c.Expected == "" {
			c.Expected = canonicalMTASTS()
		}
		c.Guidance = fmt.Sprintf("Add a TXT record at _mta-sts.%s with the value %q, and serve the matching policy document over HTTPS at https://mta-sts.%s/.well-known/mta-sts.txt.", domain, c.Expected, domain)
	}

	if c := health.TLSRPT; c != nil {
		if c.Expected == "" {
			c.Expected = canonicalTLSRPT(domain)
		}
		c.Guidance = fmt.Sprintf("Add a TXT record at _smtp._tls.%s with the value %q.", domain, c.Expected)
	}

	if c := health.DKIM; c != nil {
		name := c.RecordName
		if name == "" {
			sel := c.Selector
			if sel == "" {
				sel = "default"
			}
			name = sel + "._domainkey." + domain
		}
		if c.Expected == "" && c.PublicTXT != "" {
			c.Expected = c.PublicTXT
		}
		c.Guidance = fmt.Sprintf("Publish the DKIM public key as a TXT record at %s. Use the exact value shown in the Required column; do not re-wrap or re-quote it.", name)
	}
}

func computeEnterpriseHealthScore(health *EnterpriseDNSHealth) HealthScoreResult {
	result := HealthScoreResult{
		Breakdown: make(map[string]ScoreComponent),
	}

	possible := 0
	earnedTotal := 0

	add := func(name string, weight int, status string) {
		// optional / not_applicable records are excluded from BOTH the
		// numerator and the denominator: they can neither penalise a
		// deployment that legitimately does not use them nor inflate one
		// that does.
		if !scoredDNSStatus(status) {
			result.Breakdown[name] = ScoreComponent{Weight: 0, Earned: 0, Status: status}
			return
		}
		earned := 0
		switch status {
		case "pass":
			earned = weight
		case "warning":
			earned = weight / 2
		}
		result.Breakdown[name] = ScoreComponent{Weight: weight, Earned: earned, Status: status}
		possible += weight
		earnedTotal += earned
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

	add("mx", 20, mx)
	add("spf", 12, spf)
	add("dkim", 20, dkim)
	add("dmarc", 12, dmarc)
	add("mtasts", 8, mtasts)
	add("tlsrpt", 8, tlsrpt)

	// Expanded record inventory. These are only scored when they were
	// actually computed (a health object hydrated from a snapshot written
	// before this field existed has them nil, and a nil record must not
	// silently change the score of an old snapshot).
	if health.MailHostA != nil {
		add("mail_host_a", 8, health.MailHostA.Status)
	}
	if health.MailHostAAAA != nil {
		add("mail_host_aaaa", 0, health.MailHostAAAA.Status)
	}
	for idx, h := range health.MXHosts {
		if h == nil {
			continue
		}
		add(fmt.Sprintf("mx_host_%d", idx), 8/max(1, len(health.MXHosts)), h.Status)
	}
	if health.PTR != nil {
		add("ptr", 4, health.PTR.Status)
	}
	if health.Autodiscover != nil {
		add("autodiscover", 0, health.Autodiscover.Status)
	}
	if health.Autoconfig != nil {
		add("autoconfig", 0, health.Autoconfig.Status)
	}
	if health.TLSA != nil {
		add("tlsa", 0, health.TLSA.Status)
	}
	// Weight 0: the autodiscover SRV record is a convenience, and ORVIX
	// serves autodiscover directly, so its absence must not reduce the
	// score. It is still added to the breakdown so the row is accounted
	// for rather than silently dropped.
	if health.AutodiscoverSRV != nil {
		add("autodiscover_srv", 0, health.AutodiscoverSRV.Status)
	}

	if possible <= 0 {
		result.Score = 0
		return result
	}
	result.Score = earnedTotal * 100 / possible

	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score > 100 {
		result.Score = 100
	}
	return result
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
	// Expanded inventory participates in the rollup on exactly the same
	// terms as the score: optional/not_applicable records are filtered out
	// below, so an absent AAAA or autodiscover CNAME never degrades the
	// overall status, while an unresolvable MX host or missing rDNS does.
	if health.MailHostA != nil {
		statuses = append(statuses, health.MailHostA.Status)
	}
	if health.MailHostAAAA != nil {
		statuses = append(statuses, health.MailHostAAAA.Status)
	}
	for _, h := range health.MXHosts {
		if h != nil {
			statuses = append(statuses, h.Status)
		}
	}
	if health.PTR != nil {
		statuses = append(statuses, health.PTR.Status)
	}
	if health.Autodiscover != nil {
		statuses = append(statuses, health.Autodiscover.Status)
	}
	if health.Autoconfig != nil {
		statuses = append(statuses, health.Autoconfig.Status)
	}
	if health.TLSA != nil {
		statuses = append(statuses, health.TLSA.Status)
	}

	scored := statuses[:0:0]
	for _, s := range statuses {
		if scoredDNSStatus(s) {
			scored = append(scored, s)
		}
	}
	statuses = scored

	for _, s := range statuses {
		if s == "fail" || s == "unknown" || s == string(DNSStatusConfigRequired) {
			return s
		}
	}
	for _, s := range statuses {
		if s == "warning" || s == string(DNSStatusPending) || s == string(DNSStatusNotChecked) {
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
