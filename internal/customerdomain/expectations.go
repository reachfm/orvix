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

// ConfigKeySPF, ConfigKeyDMARCRUA and ConfigKeySRVTarget are the exact YAML
// keys named back to the operator when an expectation is unconfigured, so the
// console says which setting to populate rather than "not configured".
const (
	ConfigKeySPF       = "coremail.expected_spf"
	ConfigKeyDMARCRUA  = "coremail.expected_dmarc_rua"
	ConfigKeySRVTarget = "coremail.autodiscover_srv_target"
)

// SPF returns the required SPF TXT value for domain, or "" when the operator
// has not configured one.
//
// It deliberately does NOT fall back to a domain-derived "v=spf1 mx -all".
// An installation upgraded from a YAML that predates expected_spf typically
// already publishes a REAL, valid policy (for example
// "v=spf1 ip4:… include:… -all"). Inventing a generic default would make the
// console display a required value the operator never chose, tell them via
// the guidance text and the downloadable record file to publish it, and
// contradict a correct existing record. Returning "" makes the gap explicit:
// callers mark the record configuration_required, keep displaying the
// observed value, and name the missing setting instead of guessing a policy.
func (e CanonicalExpectations) SPF(domain string) string {
	return strings.TrimSpace(e.SPFRecord)
}

// SPFIsConfigured reports whether an operator-configured SPF policy exists.
// Exact SPF matching is performed ONLY when this is true.
func (e CanonicalExpectations) SPFIsConfigured() bool {
	return strings.TrimSpace(e.SPFRecord) != ""
}

// DMARCRUAValue returns the operator-configured rua destination for domain
// and whether one exists. When unconfigured it returns "", false — it does
// NOT synthesise "mailto:dmarc@<domain>", because ORVIX never provisions that
// mailbox and presenting it as the required value sends operators to publish
// a reporting address that discards their DMARC aggregate reports.
func (e CanonicalExpectations) DMARCRUAValue(domain string) (string, bool) {
	v := strings.TrimSpace(e.DMARCRUA)
	if v == "" {
		return "", false
	}
	if !strings.Contains(v, ":") {
		v = "mailto:" + v
	}
	return v, true
}

// DMARCIsConfigured reports whether a complete DMARC expectation (a real
// reporting address) exists. Exact DMARC matching beyond the universal
// "p=none means no enforcement" rule is performed ONLY when this is true.
func (e CanonicalExpectations) DMARCIsConfigured() bool {
	_, ok := e.DMARCRUAValue("")
	return ok
}

// DMARC returns the required DMARC TXT value for domain, or "" when no real
// reporting address is configured. See DMARCRUAValue for why no placeholder
// address is fabricated.
func (e CanonicalExpectations) DMARC(domain string) string {
	rua, ok := e.DMARCRUAValue(domain)
	if !ok {
		return ""
	}
	policy := strings.TrimSpace(e.DMARCPolicy)
	if policy == "" {
		policy = DefaultDMARCPolicy
	}
	return fmt.Sprintf("v=DMARC1; p=%s; rua=%s", policy, rua)
}

// AutodiscoverSRVName is the queried name for the autodiscover SRV record.
func AutodiscoverSRVName(domain string) string {
	return "_autodiscover._tcp." + domain
}

// AutodiscoverSRV returns the expected SRV target/port/priority/weight and
// whether an operator-configured expectation exists.
//
// The target is NEVER derived from the mail host. An SRV record points at
// whichever host actually terminates autodiscover for the deployment, which
// is not necessarily the MX host, and comparing a live record against a
// guessed target produces a "wrong target" verdict against a record that may
// be perfectly correct. ok is false unless
// coremail.autodiscover_srv_target is set; callers then report the row as
// configuration_required rather than matching against an invented value.
//
// Port/priority/weight retain defaults only once a target IS configured —
// at that point the operator has opted in and 443 (the port ORVIX serves
// /autodiscover/autodiscover.xml on) is a real, checkable default.
func (e CanonicalExpectations) AutodiscoverSRV(mailHost string) (target string, port, priority, weight int, ok bool) {
	target = strings.TrimSuffix(strings.TrimSpace(e.SRVTarget), ".")
	if target == "" {
		return "", 0, 0, 0, false
	}
	port = e.SRVPort
	if port <= 0 {
		port = DefaultAutodiscoverSRVPort
	}
	return target, port, e.SRVPriority, e.SRVWeight, true
}

// SRVIsConfigured reports whether an operator-configured autodiscover SRV
// expectation exists. Exact SRV matching runs ONLY when this is true.
func (e CanonicalExpectations) SRVIsConfigured() bool {
	return strings.TrimSpace(e.SRVTarget) != ""
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
