// Package dnsops implements DNS / DKIM operations for the Orvix admin.
//
// The package is intentionally read-only and provider-agnostic at the core:
//   - The Generator produces a deterministic desired-state plan from a
//     domain, a server IPv4 (and optional IPv6), and a DKIM selector + public
//     key. The plan covers MX, A (and optional AAAA), SPF, DKIM, DMARC,
//     MTA-STS, TLS-RPT, and CAA.
//   - The Verifier looks each desired record up via a Resolver
//     (net.DefaultResolver in production, an in-memory map in tests) and
//     assigns per-record Status (verified / missing / mismatch / conflict /
//     multiple_spf / not_checked / unsupported / error). It never marks a
//     record verified without a real DNS answer that matches.
//   - The Provider abstraction lets the same plan be turned into a change
//     plan for either manual copy/paste, Cloudflare, or Namecheap. Tokens
//     stay in server-side config; they are never returned to clients and
//     never logged.
//
// Security contract:
//   - No shell-out. We use net.DefaultResolver or the supplied Resolver.
//   - No env read in this package; only the Service layer above reads cfg.
//   - No private DKIM key material in any struct returned to a caller.
//   - No provider token in any struct returned to a caller.
//   - All DNS ops are admin-protected at the handler layer; this package
//     is neutral on auth.
package dnsops

// RecordType enumerates supported DNS record types in the core model.
type RecordType string

const (
	RecordA     RecordType = "A"
	RecordAAAA  RecordType = "AAAA"
	RecordMX    RecordType = "MX"
	RecordTXT   RecordType = "TXT"
	RecordCNAME RecordType = "CNAME"
	RecordCAA   RecordType = "CAA"
	// RecordTLSA is reserved for DANE readiness. The generator
	// never emits TLSA automatically (DANE requires DNSSEC + a
	// pinned certificate model); we only carry the type so the
	// model can describe readiness rows in future.
	RecordTLSA RecordType = "TLSA"
)

// Purpose describes the role of a record in the plan. Used by the
// dashboard to label and group rows.
type Purpose string

const (
	PurposeMX       Purpose = "mx"
	PurposeMailA    Purpose = "mail_a"
	PurposeMailAAAA Purpose = "mail_aaaa"
	PurposeSPF      Purpose = "spf"
	PurposeDKIM     Purpose = "dkim"
	PurposeDMARC    Purpose = "dmarc"
	PurposeMTASTS   Purpose = "mta_sts"
	// PurposeMTASTSValue carries the TXT portion of the mta-sts
	// policy descriptor (_mta-sts TXT, v=STSv1; id=...). When
	// the operator wants to read both records as one logical
	// block we still keep them separate in the Records list.
	PurposeMTASTSValue Purpose = "mta_sts_value"
	// PurposeMTASTSHost is the public host record (mta-sts.<domain>
	// A / AAAA) that points to the mail server so the public
	// policy endpoint at https://mta-sts.<domain>/.well-known/mta-sts.txt
	// is reachable. The endpoint itself is served by Orvix; the
	// A / AAAA record is what makes the hostname resolve.
	PurposeMTASTSHost Purpose = "mta_sts_host"
	PurposeTLSRPT     Purpose = "tls_rpt"
	PurposeCAA        Purpose = "caa"
	PurposePTR        Purpose = "ptr"
	PurposeBIMI       Purpose = "bimi"
	PurposeDANETLSA   Purpose = "dane_tlsa"
)

// Status is the verification outcome of a record.
type Status string

const (
	StatusVerified    Status = "verified"
	StatusMissing     Status = "missing"
	StatusMismatch    Status = "mismatch"
	StatusConflict    Status = "conflict"
	StatusMultipleSPF Status = "multiple_spf"
	StatusNotChecked  Status = "not_checked"
	StatusUnsupported Status = "unsupported"
	StatusError       Status = "error"
	StatusNotFound    Status = "not_found"
)

// Action is what a provider would do with a desired record vs the
// existing live state. The action is computed by the provider's Plan()
// pass, NOT by the generator — providers look at live provider-side
// records and decide per-record.
type Action string

const (
	ActionCreate   Action = "create"
	ActionUpdate   Action = "update"
	ActionSkip     Action = "skip"
	ActionConflict Action = "conflict"
	ActionDelete   Action = "delete" // never emitted by default; reserved
)

// Record is the canonical DNS record model used end-to-end (plan,
// verifier, provider). It is JSON-serialised; consumer code (admin UI,
// audit log) reads the same shape.
type Record struct {
	Name     string     `json:"name"`               // e.g. "@", "mail", "_dmarc"
	Type     RecordType `json:"type"`               // A, AAAA, MX, TXT, CNAME, CAA, TLSA
	Value    string     `json:"value"`              // primary value (host, IP, or TXT string)
	TTL      int        `json:"ttl"`                // seconds; 0 means "provider default"
	Priority int        `json:"priority,omitempty"` // MX priority (10 by default)
	Flag     int        `json:"flag,omitempty"`     // CAA flag (0 or 128)
	Tag      string     `json:"tag,omitempty"`      // CAA tag ("issue", "iodef")
	Required bool       `json:"required"`           // true for must-publish records
	Purpose  Purpose    `json:"purpose"`            // semantic purpose

	// Verification — populated by the Verifier. The generator leaves
	// these at their zero values. Status==StatusVerified implies
	// Verified==true; anything else sets Reason.
	Status   Status `json:"status"`
	Verified bool   `json:"verified"`
	Reason   string `json:"reason,omitempty"`
}

// Plan is the desired DNS state for a domain.
type Plan struct {
	Domain     string `json:"domain"`
	MailHost   string `json:"mail_host"`   // e.g. "mail.orvix.email"
	ServerIPv4 string `json:"server_ipv4"` // canonical IPv4 string; "" if unknown
	ServerIPv6 string `json:"server_ipv6"` // canonical IPv6 string; "" if not configured
	// ListenerBind is the SMTP/POP3/IMAP listener bind host from
	// coremail.smtp_host. It is INFORMATIONAL ONLY — the dashboard
	// surfaces it alongside ServerIPv4 so the operator can see the
	// two concepts separately, but A / SPF / AAAA records MUST
	// use ServerIPv4 / ServerIPv6, NEVER ListenerBind (which is
	// a bind address, not a public DNS value, and typically
	// 0.0.0.0 on production installs).
	ListenerBind  string   `json:"listener_bind,omitempty"`
	DKIMSelector  string   `json:"dkim_selector"`         // e.g. "orvix" or "default"
	DKIMKeyID     string   `json:"dkim_key_id,omitempty"` // opaque key id for the active pair
	ReportMailbox string   `json:"report_mailbox"`        // e.g. "dmarc@orvix.email"
	MTAPolicyID   string   `json:"mta_sts_policy_id"`     // content-derived policy id; same policy body => same id
	MTAMode       string   `json:"mta_sts_mode"`          // "testing" or "enforce"
	Records       []Record `json:"records"`

	// Read-only text blocks surfaced to the dashboard so the
	// operator can copy/paste them at the DNS provider. The DKIM
	// public TXT is also embedded in Records[] above.
	MTAPolicyFile string `json:"mta_sts_policy_file,omitempty"` // mta-sts.txt body
	PTRHint       string `json:"ptr_hint,omitempty"`            // expected PTR value

	// MTAStsHostName is the public hostname that the operator
	// must publish an A / AAAA record for so the public MTA-STS
	// policy endpoint at /.well-known/mta-sts.txt is reachable.
	// Format: "mta-sts.<domain>". Empty when the domain is empty.
	MTAStsHostName string `json:"mta_sts_hostname,omitempty"`
	// MTAPolicyURL is the absolute URL where the policy file is
	// served. Built from cfg.DNS (scheme) when the public MTA-STS
	// host is configured; falls back to "https://" + MTAStsHostName.
	MTAPolicyURL string `json:"mta_sts_policy_url,omitempty"`
}

// IsComplete returns true if every Required record is verified. Used
// by the dashboard to render the readiness badge.
func (p *Plan) IsComplete() bool {
	if p == nil {
		return false
	}
	for _, r := range p.Records {
		if r.Required && r.Status != StatusVerified {
			return false
		}
	}
	return true
}

// RequiredRecords returns just the required subset (MX, A, SPF, DKIM,
// DMARC, MTA-STS, TLS-RPT). CAA and PTR are optional.
func (p *Plan) RequiredRecords() []Record {
	if p == nil {
		return nil
	}
	out := make([]Record, 0, len(p.Records))
	for _, r := range p.Records {
		if r.Required {
			out = append(out, r)
		}
	}
	return out
}

// ChangePlan is what a provider returns from a Plan() call. It is the
// per-record next-step list, plus a top-level note. Apply() consumes
// the same ChangePlan plus an explicit confirmation.
type ChangePlan struct {
	Provider string   `json:"provider"` // "manual", "cloudflare", "namecheap"
	Domain   string   `json:"domain"`
	Steps    []Change `json:"steps"`
	Notes    []string `json:"notes,omitempty"` // e.g. "no token configured", "no live read"
}

// Change is one desired record action.
type Change struct {
	Record Record `json:"record"`
	Action Action `json:"action"`
	Reason string `json:"reason,omitempty"`
	// LiveValue is what the provider reports as already present
	// (empty if the provider could not read live state, e.g. no
	// token configured). When LiveValue != "" and Action==Skip, the
	// provider confirmed the record already matches the plan.
	LiveValue string `json:"live_value,omitempty"`
}

// ApplyResult is what a provider returns from Apply(). Note that
// Apply() always requires an explicit confirmation token in the call
// site; this struct is the result.
type ApplyResult struct {
	Provider string   `json:"provider"`
	Domain   string   `json:"domain"`
	Applied  int      `json:"applied"`
	Skipped  int      `json:"skipped"`
	Failed   int      `json:"failed"`
	Steps    []Change `json:"steps"`
	Notes    []string `json:"notes,omitempty"`
}

// VerifyReport is what the Verifier returns. It is a copy of the Plan
// with per-record Status/Verified/Reason populated.
type VerifyReport struct {
	Plan      Plan     `json:"plan"`
	Warnings  []string `json:"warnings,omitempty"` // top-level notes (multiple_spf, ptr_unverified, ...)
	Verified  bool     `json:"verified"`           // all Required records verified
	CheckedAt string   `json:"checked_at"`         // RFC3339 timestamp
}
