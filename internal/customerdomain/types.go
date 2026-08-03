package customerdomain

import (
	"time"
)

// DomainOverview is the customer-facing domain summary.
type DomainOverview struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	MailboxCount int       `json:"mailbox_count"`
	DNSHealth    string    `json:"dns_health"`
	HealthScore  int       `json:"health_score"`
	LastChecked  *string   `json:"last_checked,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DomainDetail is the customer-facing domain detail view.
type DomainDetail struct {
	ID              uint       `json:"id"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	Plan            string     `json:"plan"`
	Description     string     `json:"description,omitempty"`
	MaxMailboxes    int        `json:"max_mailboxes"`
	MaxAliases      int        `json:"max_aliases"`
	MaxQuotaMB      int64      `json:"max_quota_mb"`
	MailboxCount    int        `json:"mailbox_count"`
	DKIMEnabled     bool       `json:"dkim_enabled"`
	DKIMSelector    string     `json:"dkim_selector,omitempty"`
	DMARCEnabled    bool       `json:"dmarc_enabled"`
	MTASTSEnabled   bool       `json:"mtasts_enabled"`
	HealthScore     int        `json:"health_score"`
	DNSHealth       string     `json:"dns_health"`
	LatestDNSResult *DNSResult `json:"latest_dns_result,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// DNSResult is a structured DNS inspection outcome.
type DNSResult struct {
	MX    *MXCheck    `json:"mx"`
	SPF   *SPFCheck   `json:"spf"`
	DKIM  *DKIMCheck  `json:"dkim"`
	DMARC *DMARCCheck `json:"dmarc"`
}

// MXCheck is the MX record inspection result.
type MXCheck struct {
	Status    string   `json:"status"`
	Observed  []string `json:"observed,omitempty"`
	Expected  string   `json:"expected,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	CheckedAt string   `json:"checked_at"`
	Guidance  string   `json:"guidance,omitempty"`
}

// SPFCheck is the SPF record inspection result.
type SPFCheck struct {
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
	Guidance  string `json:"guidance,omitempty"`
}

// DKIMCheck is the DKIM record inspection result.
type DKIMCheck struct {
	Selector  string `json:"selector,omitempty"`
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
	PublicKey string `json:"public_key,omitempty"`
	Guidance  string `json:"guidance,omitempty"`
}

// DMARCCheck is the DMARC record inspection result.
type DMARCCheck struct {
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
	Guidance  string `json:"guidance,omitempty"`
}

// DNSStatus represents the check outcome.
type DNSStatus string

const (
	DNSStatusPass    DNSStatus = "pass"
	DNSStatusWarning DNSStatus = "warning"
	DNSStatusFail    DNSStatus = "fail"
	DNSStatusUnknown DNSStatus = "unknown"
	// DNSStatusPending marks a record whose expected value was only just
	// published (e.g. a freshly rotated DKIM key) and cannot yet be
	// expected to resolve.
	DNSStatusPending DNSStatus = "pending"
	// DNSStatusNotChecked marks a record ORVIX knows about but did not
	// look up during this inspection.
	DNSStatusNotChecked DNSStatus = "not_checked"
	// DNSStatusOptional marks a record that improves the deployment but is
	// not required by ORVIX. It NEVER contributes to the health score.
	DNSStatusOptional DNSStatus = "optional"
	// DNSStatusNotApplicable marks a record that cannot apply to this
	// deployment at all (e.g. TLSA when DANE is not configured anywhere in
	// the product). It NEVER contributes to the health score.
	DNSStatusNotApplicable DNSStatus = "not_applicable"
	// DNSStatusConfigRequired marks a record whose REQUIRED value could not
	// be determined from configuration. Such a record is indeterminate and
	// must never be able to read as passing.
	DNSStatusConfigRequired DNSStatus = "configuration_required"
)

// scoredDNSStatus reports whether a status participates in the health score
// and in the overall pass/fail rollup. optional/not_applicable records are
// deliberately excluded so a deployment that legitimately does not use them
// is not penalised, and so they can never inflate a score either.
func scoredDNSStatus(status string) bool {
	switch status {
	case string(DNSStatusOptional), string(DNSStatusNotApplicable), "":
		return false
	}
	return true
}

// DNSRecordCheck is the generic, first-class result for the record types
// added beyond the original six summary checks (host addressing, MX host
// resolution, rDNS, autodiscover/autoconfig, TLSA). It carries everything a
// row in the admin DNS modal needs: identity, required value, observed
// value, status, machine reason and human repair guidance.
type DNSRecordCheck struct {
	// Name is the fully-qualified DNS name that was queried (or, for PTR,
	// the IP whose reverse zone was queried).
	Name string `json:"name"`
	// Type is the DNS RR type as displayed ("A", "AAAA", "PTR", "CNAME",
	// "TLSA", "MX-host").
	Type string `json:"type"`
	// Status is one of the DNSStatus* values above.
	Status string `json:"status"`
	// Expected is the required value. It is always populated unless Status
	// is not_applicable, in which case it is intentionally empty.
	Expected string `json:"expected,omitempty"`
	// Observed holds every value actually resolved, in resolution order.
	Observed []string `json:"observed,omitempty"`
	// Reason is the precise machine-oriented explanation of Status.
	Reason string `json:"reason,omitempty"`
	// Guidance is operator-facing repair text naming the concrete record to
	// create and its concrete value.
	Guidance string `json:"guidance,omitempty"`
	// Optional is true when this record is nice-to-have rather than
	// required; it mirrors Status being optional/not_applicable and exists
	// so the frontend can style the row without string-matching.
	Optional  bool   `json:"optional"`
	CheckedAt string `json:"checked_at"`
}

// DomainListRequest is the paginated list input.
type DomainListRequest struct {
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Search string `json:"search,omitempty"`
	Status string `json:"status,omitempty"`
}

// DomainListResponse is the paginated list output.
type DomainListResponse struct {
	Domains []DomainOverview `json:"domains"`
	Total   int64            `json:"total"`
	Offset  int              `json:"offset"`
	Limit   int              `json:"limit"`
}

// VerificationSnapshot holds a persisted verification result.
type VerificationSnapshot struct {
	ID          uint      `json:"id"`
	DomainID    uint      `json:"domain_id"`
	Score       int       `json:"score"`
	Status      string    `json:"status"`
	MXStatus    string    `json:"mx_status,omitempty"`
	SPFStatus   string    `json:"spf_status,omitempty"`
	DKIMStatus  string    `json:"dkim_status,omitempty"`
	DMARCStatus string    `json:"dmarc_status,omitempty"`
	Evidence    string    `json:"evidence,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// EnterpriseDNSHealth is the unified, canonical DNS health response shared
// by GET /enterprise/domains/:id/dns and POST .../dns/verify — both
// endpoints return exactly this shape so the frontend never has to special-
// case which one answered a request.
type EnterpriseDNSHealth struct {
	DomainID          uint   `json:"domain_id"`
	DomainName        string `json:"domain_name"`
	OperationalStatus string `json:"operational_status"`
	DNSHealth         string `json:"dns_health"`
	HealthScore       int    `json:"health_score"`
	LastCheckedAt     string `json:"last_checked_at,omitempty"`
	// CooldownUntil/RetryAfterSeconds are populated whenever a prior
	// verification exists and its cooldown window has not yet elapsed —
	// on both GET (informational) and POST (whether this call itself
	// performed a fresh check or was blocked by cooldown).
	CooldownUntil     string `json:"cooldown_until,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	// Complete is true only when every record below was successfully
	// reconstructed from a real check (fresh or persisted). A missing,
	// corrupt, or partially-reconstructable snapshot sets this false, and
	// HealthScore/DNSHealth are ALWAYS recomputed from whatever records
	// are actually present (never taken verbatim from a stale persisted
	// scalar) — see recomputeEnterpriseHealth in service.go. This is the
	// fix for the "100% Pass with every record Not checked" defect: a
	// nil record can never contribute a passing score.
	Complete     bool             `json:"complete"`
	MX           *MXCheck         `json:"mx"`
	SPF          *SPFCheck        `json:"spf"`
	DKIM         *DKIMHealthCheck `json:"dkim"`
	DMARC        *DMARCCheck      `json:"dmarc"`
	MTASTS       *MTASTSCheck     `json:"mtasts"`
	TLSRPT       *TLSRPTCheck     `json:"tlsrpt"`
	MTASTSPolicy *MTASTSPolicy    `json:"mtasts_policy,omitempty"`

	// ── Expanded record inventory ────────────────────────────────────────
	// These are additive: the six fields above keep their exact prior shape
	// and meaning for backward compatibility with existing clients.

	// MailHostA / MailHostAAAA are the forward-address records of the
	// primary expected mail host (the first entry of expected_mx, or
	// "mail.<domain>"). AAAA is optional: IPv6 is not required to run
	// ORVIX, so a missing AAAA is reported as optional, not as a failure.
	MailHostA    *DNSRecordCheck `json:"mail_host_a"`
	MailHostAAAA *DNSRecordCheck `json:"mail_host_aaaa"`
	// MXHosts resolves EVERY hostname returned by the domain's MX lookup to
	// A/AAAA. An MX that does not resolve is a hard delivery failure, so
	// these are required records.
	MXHosts []*DNSRecordCheck `json:"mx_hosts"`
	// PTR is the reverse lookup of the primary mail host's first IPv4
	// address. Required: most receivers reject mail from hosts with no
	// matching rDNS.
	PTR *DNSRecordCheck `json:"ptr"`
	// Autodiscover / Autoconfig are client-provisioning conveniences.
	// ORVIX serves both protocols from its own web host (see
	// internal/api/router.go: /autodiscover/autodiscover.xml and
	// /.well-known/autoconfig/mail/config-v1.1.xml), so mail flow and
	// client setup both work without these delegation records. They are
	// therefore OPTIONAL and never counted against the score.
	Autodiscover *DNSRecordCheck `json:"autodiscover"`
	Autoconfig   *DNSRecordCheck `json:"autoconfig"`
	// AutodiscoverSRV is the live _autodiscover._tcp.<domain> SRV record
	// compared field-by-field (priority, weight, port, target) against the
	// operator-configured expectation in CanonicalExpectations. Like the
	// CNAME delegation rows it is OPTIONAL — ORVIX serves autodiscover
	// itself — but unlike them it is genuinely resolved and compared
	// rather than assumed.
	AutodiscoverSRV *DNSRecordCheck `json:"autodiscover_srv"`
	// TLSA is always not_applicable: ORVIX has no DANE/TLSA configuration
	// surface anywhere in internal/config, so there is no requirement to
	// assert. It is reported explicitly rather than omitted so the operator
	// can see it was considered.
	TLSA *DNSRecordCheck `json:"tlsa"`
}

// DKIMHealthCheck extends DKIMCheck with admin-specific fields.
type DKIMHealthCheck struct {
	Selector   string `json:"selector"`
	Status     string `json:"status"`
	Expected   string `json:"expected,omitempty"`
	Observed   string `json:"observed,omitempty"`
	Reason     string `json:"reason,omitempty"`
	CheckedAt  string `json:"checked_at"`
	RecordName string `json:"record_name"`
	Configured bool   `json:"configured"`
	PublicTXT  string `json:"public_txt,omitempty"`
	MatchesDNS bool   `json:"matches_dns"`
	Guidance   string `json:"guidance,omitempty"`
}

// MTASTSCheck is the MTA-STS inspection result.
type MTASTSCheck struct {
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
	Expected  string `json:"expected,omitempty"`
	Guidance  string `json:"guidance,omitempty"`
}

// TLSRPTCheck is the TLS-RPT inspection result.
type TLSRPTCheck struct {
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
	Expected  string `json:"expected,omitempty"`
	Guidance  string `json:"guidance,omitempty"`
}
