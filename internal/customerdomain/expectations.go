package customerdomain

import (
	"fmt"
	"strings"
)

// CanonicalExpectations is the SINGLE source of truth for every "required
// value" the admin DNS console shows, checks, explains and downloads.
//
// The reviewer-confirmed defect this type exists to prevent: the expected SPF
// and DMARC values were previously hard-coded inside applyCanonicalExpectations
// as "v=spf1 mx -all" and "v=DMARC1; p=quarantine; rua=mailto:dmarc@<domain>".
// That silently replaced the real, previously-established ORVIX policy
// ("v=spf1 ip4:65.75.203.74 include:spf.orvix.email -all") with an invented
// generic one, and fabricated a dmarc@ mailbox that is not provisioned.
//
// All four consumers read from here and nowhere else:
//
//	(a) the verifier/scorer      — checkSPF / checkDMARC compare the LIVE
//	                               record against Expectations.SPF()/DMARC()
//	(b) the `expected` field     — applyCanonicalExpectations copies the same
//	                               strings into the API response
//	(c) the repair guidance      — built from the same strings
//	(d) the downloaded zone file — the frontend renders the `expected` field
//	                               from (b); it never derives its own value
//
// The zero value is valid and reproduces the historical (pre-config)
// behaviour, so an operator who has not yet populated coremail.expected_spf
// keeps working exactly as before.
type CanonicalExpectations struct {
	// SPFRecord is the literal SPF TXT value (coremail.expected_spf).
	SPFRecord string

	// DMARCPolicy is the `p=` value (coremail.expected_dmarc_policy).
	DMARCPolicy string

	// DMARCRUA is the full aggregate-report destination including the
	// "mailto:" scheme (coremail.expected_dmarc_rua). Empty means "no
	// operator-configured address" and the placeholder path is used.
	DMARCRUA string

	// Autodiscover SRV expectations (coremail.autodiscover_srv_*).
	SRVTarget   string
	SRVPort     int
	SRVPriority int
	SRVWeight   int
}

// DefaultDMARCPolicy is used when the operator configured none. quarantine is
// the safe middle ground: it protects the domain without silently discarding
// mail during a misconfigured rollout the way p=reject would.
const DefaultDMARCPolicy = "quarantine"

// DefaultAutodiscoverSRVPort is the standard port for Outlook autodiscover
// carried over HTTPS, which is what ORVIX serves
// (/autodiscover/autodiscover.xml on the web host).
const DefaultAutodiscoverSRVPort = 443

// placeholderDMARCRUALocal is the local part of the DOCUMENTED PLACEHOLDER
// used when no rua address is configured. It is deliberately not silently
// treated as correct: SPFAndDMARCGuidance flags it so an operator is told to
// configure a real mailbox. ORVIX never provisions this address.
const placeholderDMARCRUALocal = "dmarc"

// SPF returns the required SPF TXT value for domain.
//
// Backward compatibility: when no expected_spf is configured we fall back to
// "v=spf1 mx -all". That fallback is correct-but-generic — it authorises
// exactly the domain's own MX hosts — and preserves the behaviour of
// deployments that predate the expected_spf setting. It is NOT the real ORVIX
// policy; ORVIX deployments must set coremail.expected_spf.
func (e CanonicalExpectations) SPF(domain string) string {
	if v := strings.TrimSpace(e.SPFRecord); v != "" {
		return v
	}
	return "v=spf1 mx -all"
}

// SPFIsConfigured reports whether the returned SPF value came from operator
// configuration rather than the generic fallback.
func (e CanonicalExpectations) SPFIsConfigured() bool {
	return strings.TrimSpace(e.SPFRecord) != ""
}

// DMARCRUAValue returns the rua destination for domain and whether it is a
// real, operator-configured address (true) or the documented placeholder
// (false).
func (e CanonicalExpectations) DMARCRUAValue(domain string) (string, bool) {
	if v := strings.TrimSpace(e.DMARCRUA); v != "" {
		if !strings.Contains(v, ":") {
			v = "mailto:" + v
		}
		return v, true
	}
	return fmt.Sprintf("mailto:%s@%s", placeholderDMARCRUALocal, domain), false
}

// DMARC returns the required DMARC TXT value for domain.
func (e CanonicalExpectations) DMARC(domain string) string {
	policy := strings.TrimSpace(e.DMARCPolicy)
	if policy == "" {
		policy = DefaultDMARCPolicy
	}
	rua, _ := e.DMARCRUAValue(domain)
	return fmt.Sprintf("v=DMARC1; p=%s; rua=%s", policy, rua)
}

// AutodiscoverSRVName is the queried name for the autodiscover SRV record.
func AutodiscoverSRVName(domain string) string {
	return "_autodiscover._tcp." + domain
}

// AutodiscoverSRV returns the expected SRV target/port/priority/weight and
// whether an expectation could be determined at all.
//
// We can always determine one: ORVIX itself serves
// /autodiscover/autodiscover.xml (registered in internal/api/router.go), so
// the target defaults to the domain's own primary mail host over 443 rather
// than being fabricated from an unrelated hostname. ok is false only when
// even the mail host is unknown, in which case the row is reported
// not_applicable instead of inventing a target.
func (e CanonicalExpectations) AutodiscoverSRV(mailHost string) (target string, port, priority, weight int, ok bool) {
	target = strings.TrimSuffix(strings.TrimSpace(e.SRVTarget), ".")
	if target == "" {
		target = strings.TrimSuffix(strings.TrimSpace(mailHost), ".")
	}
	if target == "" {
		return "", 0, 0, 0, false
	}
	port = e.SRVPort
	if port <= 0 {
		port = DefaultAutodiscoverSRVPort
	}
	return target, port, e.SRVPriority, e.SRVWeight, true
}

// AutodiscoverSRVExpectedString renders the expectation in zone-file order
// (priority weight port target.) so the `expected` column, the guidance text
// and the downloaded record file are byte-identical.
func (e CanonicalExpectations) AutodiscoverSRVExpectedString(mailHost string) (string, bool) {
	target, port, priority, weight, ok := e.AutodiscoverSRV(mailHost)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d %d %d %s.", priority, weight, port, target), true
}
