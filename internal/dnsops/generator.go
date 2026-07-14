package dnsops

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Generator produces a deterministic desired-state DNS plan for a
// domain. The generator does NOT call out to DNS — that is the
// Verifier's job. The generator's contract is:
//
//   - Same canonical MTA-STS policy body always yields the same policy id;
//     changing the policy body yields a new id.
//   - No records use placeholder DKIM material. If dkimPubKey is empty,
//     the DKIM row is still emitted but with a "not generated" reason;
//     callers (admin UI) must surface that as a "Generate DKIM key"
//     action rather than rendering a fake public key.
//   - No record carries a private key, provider token, or any secret.
type Generator struct {
	// NowFunc is overridable in tests for future time-sensitive fields.
	// It must not affect MTAPolicyID, which is derived from policy content.
	NowFunc func() time.Time
}

// NewGenerator returns a Generator with the default NowFunc.
func NewGenerator() *Generator {
	return &Generator{NowFunc: func() time.Time { return time.Now().UTC() }}
}

// Inputs collects everything the generator needs.
type Inputs struct {
	Domain     string // apex domain, e.g. "orvix.email"
	MailHost   string // e.g. "mail.orvix.email"
	ServerIPv4 string // canonical dotted IPv4, e.g. "65.75.203.74"
	ServerIPv6 string // canonical IPv6, "" if not configured
	// ListenerBind is the SMTP / POP3 / IMAP listener bind host
	// from coremail.smtp_host. It is INFORMATIONAL ONLY and is
	// echoed in Plan.ListenerBind so the dashboard can show the
	// two concepts (public DNS vs listener bind) side by side.
	// A / AAAA / SPF records MUST use ServerIPv4 / ServerIPv6,
	// NOT ListenerBind (the listener bind host defaults to
	// 0.0.0.0 and has nothing to do with the public DNS plan).
	ListenerBind   string
	DKIMSelector   string // e.g. "orvix"; default "default" when empty
	DKIMKeyID      string // opaque key id (returned in Plan.DKIMKeyID)
	DKIMPubKey     string // base64 DER public key, no PEM headers (empty → "not generated")
	ReportMailbox  string // e.g. "dmarc@orvix.email"; default derived
	MTAMode        string // "testing" or "enforce"; default "testing"
	DNSSECDetected bool   // if true, DANE/TLSA readiness row is added
}

// Validate returns an error if any required input is missing or
// malformed. The Domain check is the FQDN shape used everywhere else
// in Orvix (lowercase, no scheme, no wildcard, no whitespace, at
// least one dot, no empty labels).
func (in Inputs) Validate() error {
	if err := validateDomain(in.Domain); err != nil {
		return err
	}
	if strings.TrimSpace(in.MailHost) == "" {
		return fmt.Errorf("mail_host is required")
	}
	if strings.TrimSpace(in.ServerIPv4) == "" {
		return fmt.Errorf("server_ipv4 is required")
	}
	return nil
}

func validateDomain(d string) error {
	d = strings.ToLower(strings.TrimSpace(d))
	if d == "" {
		return fmt.Errorf("domain is required")
	}
	if strings.Contains(d, "://") || strings.Contains(d, "/") {
		return fmt.Errorf("invalid domain: no protocol or path allowed")
	}
	if strings.Contains(d, " ") || strings.Contains(d, "*") {
		return fmt.Errorf("invalid domain: no spaces or wildcards")
	}
	if strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return fmt.Errorf("invalid domain: must not start or end with a dot")
	}
	parts := strings.Split(d, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid domain: must be a fully qualified domain name")
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("invalid domain: consecutive dots")
		}
	}
	return nil
}

// Generate produces the desired-state Plan. It does not perform DNS
// lookups; per-record Status/Verified/Reason remain zero values.
func (g *Generator) Generate(in Inputs) (*Plan, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	selector := in.DKIMSelector
	if selector == "" {
		selector = "default"
	}
	report := in.ReportMailbox
	if report == "" {
		report = "dmarc@" + in.Domain
	}
	mtaMode := strings.ToLower(strings.TrimSpace(in.MTAMode))
	if mtaMode != "testing" && mtaMode != "enforce" {
		mtaMode = "testing"
	}

	// Build MTA-STS policy body first so its stable hash can be
	// used as the policy id (consistent across runs, days, and
	// restarts for the same policy content).
	mtaBody := mtaStsPolicyFile(in.MailHost, mtaMode)
	mtaPolicyID := mtaStsPolicyID(mtaBody)

	p := &Plan{
		Domain:        strings.ToLower(strings.TrimSpace(in.Domain)),
		MailHost:      strings.ToLower(strings.TrimSpace(in.MailHost)),
		ServerIPv4:    strings.TrimSpace(in.ServerIPv4),
		ServerIPv6:    strings.TrimSpace(in.ServerIPv6),
		ListenerBind:  strings.TrimSpace(in.ListenerBind),
		DKIMSelector:  selector,
		DKIMKeyID:     in.DKIMKeyID,
		ReportMailbox: report,
		MTAMode:       mtaMode,
		MTAPolicyID:   mtaPolicyID,
	}

	// MX — required.
	p.Records = append(p.Records, Record{
		Name:     "@",
		Type:     RecordMX,
		Value:    p.MailHost,
		TTL:      3600,
		Priority: 10,
		Required: true,
		Purpose:  PurposeMX,
	})

	// A — required.
	p.Records = append(p.Records, Record{
		Name:     "mail",
		Type:     RecordA,
		Value:    p.ServerIPv4,
		TTL:      3600,
		Required: true,
		Purpose:  PurposeMailA,
	})

	// AAAA — optional, only when ServerIPv6 is set.
	if p.ServerIPv6 != "" {
		p.Records = append(p.Records, Record{
			Name:     "mail",
			Type:     RecordAAAA,
			Value:    p.ServerIPv6,
			TTL:      3600,
			Required: false,
			Purpose:  PurposeMailAAAA,
		})
	}

	// mta-sts host (A / AAAA) — points the mta-sts.<domain>
	// hostname at the same public IPs as mail.<domain>. Required
	// so the public MTA-STS policy endpoint at
	// https://mta-sts.<domain>/.well-known/mta-sts.txt is
	// reachable. The Plan carries the policy URL / body so the
	// dashboard can render them.
	p.MTAStsHostName = "mta-sts." + p.Domain
	p.MTAPolicyURL = "https://" + p.MTAStsHostName + "/.well-known/mta-sts.txt"
	p.Records = append(p.Records, Record{
		Name:     "mta-sts",
		Type:     RecordA,
		Value:    p.ServerIPv4,
		TTL:      3600,
		Required: true,
		Purpose:  PurposeMTASTSHost,
	})
	if p.ServerIPv6 != "" {
		p.Records = append(p.Records, Record{
			Name:     "mta-sts",
			Type:     RecordAAAA,
			Value:    p.ServerIPv6,
			TTL:      3600,
			Required: false,
			Purpose:  PurposeMTASTSHost,
		})
	}

	// SPF — required. v=spf1 mx ip4:<ipv4> [-all|~all].
	// We use -all for the strict default; an operator can soften it.
	spf := fmt.Sprintf("v=spf1 mx ip4:%s -all", p.ServerIPv4)
	if p.ServerIPv6 != "" {
		spf = fmt.Sprintf("v=spf1 mx ip4:%s ip6:%s -all", p.ServerIPv4, p.ServerIPv6)
	}
	p.Records = append(p.Records, Record{
		Name:     "@",
		Type:     RecordTXT,
		Value:    spf,
		TTL:      3600,
		Required: true,
		Purpose:  PurposeSPF,
	})

	// DKIM — required if PubKey is set; otherwise emitted as
	// "not generated" so the dashboard can offer a Generate action.
	dkimName := fmt.Sprintf("%s._domainkey", selector)
	if strings.TrimSpace(in.DKIMPubKey) == "" {
		p.Records = append(p.Records, Record{
			Name:     dkimName,
			Type:     RecordTXT,
			Value:    "DKIM not generated — public key missing",
			TTL:      3600,
			Required: true,
			Purpose:  PurposeDKIM,
			Status:   StatusNotChecked,
			Reason:   "dkim key not generated for this domain",
		})
	} else {
		p.Records = append(p.Records, Record{
			Name:     dkimName,
			Type:     RecordTXT,
			Value:    fmt.Sprintf("v=DKIM1; k=rsa; p=%s", in.DKIMPubKey),
			TTL:      3600,
			Required: true,
			Purpose:  PurposeDKIM,
		})
	}

	// DMARC — required, default safe policy p=none with rua to report mailbox.
	dmarc := fmt.Sprintf("v=DMARC1; p=none; rua=mailto:%s; adkim=s; aspf=s; pct=100", report)
	p.Records = append(p.Records, Record{
		Name:     "_dmarc",
		Type:     RecordTXT,
		Value:    dmarc,
		TTL:      3600,
		Required: true,
		Purpose:  PurposeDMARC,
	})

	// MTA-STS — required, mode=testing by default.
	p.Records = append(p.Records, Record{
		Name:     "_mta-sts",
		Type:     RecordTXT,
		Value:    fmt.Sprintf("v=STSv1; id=%s", p.MTAPolicyID),
		TTL:      3600,
		Required: true,
		Purpose:  PurposeMTASTS,
	})
	p.MTAPolicyFile = mtaBody

	// TLS-RPT — required.
	p.Records = append(p.Records, Record{
		Name:     "_smtp._tls",
		Type:     RecordTXT,
		Value:    fmt.Sprintf("v=TLSRPTv1; rua=mailto:%s", reportTLS(in.Domain, report)),
		TTL:      3600,
		Required: true,
		Purpose:  PurposeTLSRPT,
	})

	// CAA — recommended but not required. Two records: letsencrypt issuer
	// + iodef for incident reporting. Existing CAA at @ is preserved
	// (the verifier only warns, never overwrites).
	p.Records = append(p.Records, Record{
		Name:     "@",
		Type:     RecordCAA,
		Value:    "letsencrypt.org",
		TTL:      3600,
		Flag:     0,
		Tag:      "issue",
		Required: false,
		Purpose:  PurposeCAA,
	})
	p.Records = append(p.Records, Record{
		Name:     "@",
		Type:     RecordCAA,
		Value:    "mailto:postmaster@" + p.Domain,
		TTL:      3600,
		Flag:     0,
		Tag:      "iodef",
		Required: false,
		Purpose:  PurposeCAA,
	})

	// PTR — provider-side requirement, surfaced as a non-required
	// informational row. The expected forward-confirmed value is
	// mail.<domain>.
	p.PTRHint = p.MailHost + "."
	p.Records = append(p.Records, Record{
		Name:     p.ServerIPv4,
		Type:     "PTR",
		Value:    p.PTRHint,
		TTL:      0,
		Required: false,
		Purpose:  PurposePTR,
		Status:   StatusNotChecked,
		Reason:   "PTR/rDNS is set by your hosting provider, not as a DNS zone record",
	})

	// DANE/TLSA readiness — only if DNSSEC detected. We never
	// auto-generate a TLSA record; the row is informational and
	// surfaces as "future readiness".
	if in.DNSSECDetected {
		p.Records = append(p.Records, Record{
			Name:     fmt.Sprintf("_25._tcp.%s", p.MailHost),
			Type:     RecordTLSA,
			Value:    "DANE/TLSA readiness only — TLSA record requires DNSSEC + a pinned certificate model",
			TTL:      3600,
			Required: false,
			Purpose:  PurposeDANETLSA,
			Status:   StatusNotChecked,
			Reason:   "TLSA generation requires certificate pinning; not auto-generated",
		})
	}

	// BIMI readiness — informational only. We do not auto-generate
	// BIMI; the row exists so the dashboard can label it "not
	// configured".
	p.Records = append(p.Records, Record{
		Name:     "default._bimi",
		Type:     RecordTXT,
		Value:    "BIMI not configured — requires a VMC certificate and a square logo",
		TTL:      3600,
		Required: false,
		Purpose:  PurposeBIMI,
		Status:   StatusNotChecked,
		Reason:   "BIMI generation requires a VMC certificate and a square logo; not auto-generated",
	})

	return p, nil
}

// mtaStsPolicyFile returns the policy file body the operator must
// host at https://mta-sts.<domain>/.well-known/mta-sts.txt.
func mtaStsPolicyFile(mailHost, mode string) string {
	if mode != "enforce" {
		mode = "testing"
	}
	return fmt.Sprintf("version: STSv1\nmode: %s\nmx: %s\nmax_age: 86400\n", mode, mailHost)
}

// mtaStsPolicyID returns a stable content-derived id for the MTA-STS
// TXT record. The id is the first 16 hex characters of the SHA-256
// hash of the canonical policy file body. Same policy content yields
// the same id across runs, days, and process restarts.
func mtaStsPolicyID(policyBody string) string {
	h := sha256.Sum256([]byte(policyBody))
	return hex.EncodeToString(h[:8])
}

// reportTLS returns the TLS-RPT rua target. We default to the same
// report mailbox as DMARC unless it would equal dmarc@, in which
// case tlsrpt@ is preferred (RFC 8460 §3).
func reportTLS(domain, defaultRua string) string {
	if strings.HasPrefix(defaultRua, "dmarc@") || defaultRua == "" {
		return "tlsrpt@" + domain
	}
	return defaultRua
}
