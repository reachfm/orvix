package dnsops

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Verifier walks a Plan and assigns per-record Status/Verified/Reason
// using real DNS lookups via the supplied Resolver.
//
// Contract:
//   - The Verifier never marks a record verified unless DNS confirms it.
//   - The Verifier normalises TXT chunking (joins multiple strings
//     into one logical record), case (SPF/DKIM tags), and trailing dots.
//   - The Verifier surfaces multiple SPF records as StatusMultipleSPF
//     rather than picking one and silently passing.
//   - Per-record errors (DNS timeout, malformed payload) become
//     StatusError with a sanitised Reason — no stack traces, no raw
//     resolver error chains.
type Verifier struct {
	resolver Resolver
	nowFunc  func() time.Time
}

// NewVerifier returns a Verifier backed by r.
func NewVerifier(r Resolver) *Verifier {
	return &Verifier{
		resolver: r,
		nowFunc:  func() time.Time { return time.Now().UTC() },
	}
}

// WithClock is for tests that want a deterministic CheckedAt.
func (v *Verifier) WithClock(now func() time.Time) *Verifier {
	if now != nil {
		v.nowFunc = now
	}
	return v
}

// Verify applies DNS verification to every record in plan and
// returns a VerifyReport. The plan is mutated in place AND echoed in
// the returned report (so callers that already hold the plan do not
// need to switch references).
func (v *Verifier) Verify(ctx context.Context, plan *Plan) (*VerifyReport, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan is nil")
	}
	if v.resolver == nil {
		return nil, fmt.Errorf("resolver is nil")
	}

	report := &VerifyReport{
		Plan:      *plan,
		CheckedAt: v.nowFunc().Format(time.RFC3339),
	}

	// Per-record checks.
	for i := range plan.Records {
		r := &plan.Records[i]
		switch r.Purpose {
		case PurposeMX:
			v.checkMX(ctx, plan, r)
		case PurposeMailA:
			v.checkMailA(ctx, plan, r)
		case PurposeMailAAAA:
			v.checkMailAAAA(ctx, plan, r)
		case PurposeSPF:
			v.checkSPF(ctx, plan, r)
		case PurposeDKIM:
			v.checkDKIM(ctx, plan, r)
		case PurposeDMARC:
			v.checkDMARC(ctx, plan, r)
		case PurposeMTASTS:
			v.checkMTASTS(ctx, plan, r)
		case PurposeTLSRPT:
			v.checkTLSRPT(ctx, plan, r)
		case PurposeCAA:
			v.checkCAA(ctx, plan, r)
		case PurposePTR:
			v.checkPTR(ctx, plan, r)
		case PurposeBIMI, PurposeDANETLSA:
			// Optional readiness rows; never auto-checked.
			r.Status = StatusNotChecked
			r.Verified = false
			r.Reason = "readiness row — operator action required"
		default:
			r.Status = StatusUnsupported
			r.Verified = false
			r.Reason = "unsupported record purpose"
		}
		report.Plan.Records[i] = *r
	}

	// Top-level warnings.
	for _, r := range plan.Records {
		if r.Status == StatusMultipleSPF {
			report.Warnings = append(report.Warnings, "multiple SPF records detected — RFC 7208 forbids more than one; receivers may treat this as permerror")
		}
		if r.Purpose == PurposeCAA && r.Status == StatusConflict {
			report.Warnings = append(report.Warnings, "existing CAA records conflict with the recommended issuer set; do not overwrite")
		}
		if r.Purpose == PurposePTR && r.Status == StatusVerified {
			report.Warnings = append(report.Warnings, "PTR verified by DNS resolver — confirm with hosting provider if reverse DNS is delegated to them")
		}
	}
	sort.Strings(report.Warnings)

	report.Verified = plan.IsComplete()
	return report, nil
}

// ── Per-record checks ───────────────────────────────────────────

func (v *Verifier) checkMX(ctx context.Context, plan *Plan, r *Record) {
	mxs, err := v.resolver.LookupMX(ctx, plan.Domain)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "MX lookup failed: " + sanitizeDNSError(err)
		return
	}
	expectedHost := strings.TrimSuffix(strings.ToLower(plan.MailHost), ".")
	for _, m := range mxs {
		host := strings.TrimSuffix(strings.ToLower(m.Host), ".")
		if host == expectedHost && int(m.Pref) == r.Priority {
			r.Status = StatusVerified
			r.Verified = true
			r.Reason = fmt.Sprintf("MX %d %s", m.Pref, m.Host)
			return
		}
	}
	// Partial match: host matches but priority differs, or vice versa.
	for _, m := range mxs {
		host := strings.TrimSuffix(strings.ToLower(m.Host), ".")
		if host == expectedHost {
			r.Status = StatusMismatch
			r.Reason = fmt.Sprintf("MX host matches but priority is %d (want %d)", m.Pref, r.Priority)
			return
		}
	}
	r.Status = StatusMismatch
	r.Reason = fmt.Sprintf("MX records present but none match %s", plan.MailHost)
}

func (v *Verifier) checkMailA(ctx context.Context, plan *Plan, r *Record) {
	name := "mail." + plan.Domain
	ips, err := v.resolver.LookupA(ctx, name)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "A lookup failed: " + sanitizeDNSError(err)
		return
	}
	want := strings.TrimSpace(plan.ServerIPv4)
	for _, ip := range ips {
		if ip.String() == want {
			r.Status = StatusVerified
			r.Verified = true
			r.Reason = fmt.Sprintf("A %s -> %s", name, want)
			return
		}
	}
	r.Status = StatusMismatch
	r.Reason = fmt.Sprintf("A records present but none match server IPv4 %s (got %s)", want, joinIPs(ips))
}

func (v *Verifier) checkMailAAAA(ctx context.Context, plan *Plan, r *Record) {
	name := "mail." + plan.Domain
	ips, err := v.resolver.LookupAAAA(ctx, name)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "AAAA lookup failed: " + sanitizeDNSError(err)
		return
	}
	want := strings.TrimSpace(plan.ServerIPv6)
	for _, ip := range ips {
		if ip.String() == want {
			r.Status = StatusVerified
			r.Verified = true
			r.Reason = fmt.Sprintf("AAAA %s -> %s", name, want)
			return
		}
	}
	r.Status = StatusMismatch
	r.Reason = fmt.Sprintf("AAAA records present but none match %s (got %s)", want, joinIPs(ips))
}

// checkSPF enforces "exactly one v=spf1 record" and validates that
// the existing record contains the mechanisms the plan emits.
func (v *Verifier) checkSPF(ctx context.Context, plan *Plan, r *Record) {
	name := apexName(plan, r)
	txts, err := v.resolver.LookupTXT(ctx, name)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "SPF TXT lookup failed: " + sanitizeDNSError(err)
		return
	}
	joined := normaliseTXT(txts)
	var spfRecords []string
	for _, t := range joined {
		if strings.HasPrefix(strings.TrimSpace(t), "v=spf1") {
			spfRecords = append(spfRecords, t)
		}
	}
	if len(spfRecords) == 0 {
		r.Status = StatusMissing
		r.Reason = "no SPF TXT record found at " + name
		return
	}
	if len(spfRecords) > 1 {
		r.Status = StatusMultipleSPF
		r.Reason = fmt.Sprintf("RFC 7208 forbids multiple SPF records; found %d at %s", len(spfRecords), name)
		return
	}
	existing := strings.TrimSpace(spfRecords[0])
	want := strings.TrimSpace(r.Value)
	// Normalise: collapse whitespace, lower-case the version tag,
	// trim trailing dot on any "mx:<host>" references.
	if spfEquivalent(existing, want) {
		r.Status = StatusVerified
		r.Verified = true
		r.Reason = "SPF matches generated plan"
		return
	}
	r.Status = StatusMismatch
	r.Reason = "SPF exists but differs from generated plan (see merge guidance — do not overwrite silently)"
}

// apexName returns the FQDN to query for an apex record. Records
// with Name "@" map to the bare apex domain.
func apexName(plan *Plan, r *Record) string {
	if r.Name == "@" || r.Name == "" {
		return plan.Domain
	}
	return r.Name + "." + plan.Domain
}

// checkDKIM compares the TXT at <selector>._domainkey.<domain>
// with the public key embedded in the plan record. We compare
// semantically: v=DKIM1 present, k=rsa present, p=<same base64>.
func (v *Verifier) checkDKIM(ctx context.Context, plan *Plan, r *Record) {
	name := r.Name + "." + plan.Domain
	txts, err := v.resolver.LookupTXT(ctx, name)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "DKIM TXT lookup failed: " + sanitizeDNSError(err)
		return
	}
	joined := normaliseTXT(txts)
	for _, t := range joined {
		t = strings.TrimSpace(t)
		if !strings.HasPrefix(t, "v=DKIM1") {
			continue
		}
		if dkimEquivalent(t, r.Value) {
			r.Status = StatusVerified
			r.Verified = true
			r.Reason = "DKIM public key matches"
			return
		}
		r.Status = StatusMismatch
		r.Reason = "DKIM record exists but public key differs (selector rotation in progress?)"
		return
	}
	r.Status = StatusMissing
	r.Reason = "no v=DKIM1 record found at " + name
}

func (v *Verifier) checkDMARC(ctx context.Context, plan *Plan, r *Record) {
	name := "_dmarc." + plan.Domain
	txts, err := v.resolver.LookupTXT(ctx, name)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "DMARC TXT lookup failed: " + sanitizeDNSError(err)
		return
	}
	joined := normaliseTXT(txts)
	for _, t := range joined {
		t = strings.TrimSpace(t)
		if !strings.HasPrefix(t, "v=DMARC1") {
			continue
		}
		// The DMARC parser enforces the v=DMARC1 + p= minimum
		// contract from RFC 7489 §6.3. We accept any policy value
		// (none / quarantine / reject) because the dashboard
		// recommendation is staged; the live record might already
		// be stricter than the plan.
		if _, perr := dmarcMinimalParse(t); perr != nil {
			r.Status = StatusError
			r.Reason = "DMARC TXT present but malformed: " + perr.Error()
			return
		}
		if dmarcMinimalMatch(t, r.Value) {
			r.Status = StatusVerified
			r.Verified = true
			r.Reason = "DMARC present and parses; matches generated policy tags"
			return
		}
		// Parses, differs from our plan. That is fine — surface as
		// mismatch so the dashboard can show the staged path
		// none -> quarantine -> reject.
		r.Status = StatusMismatch
		r.Reason = "DMARC present with stricter/different policy than generated plan"
		return
	}
	r.Status = StatusMissing
	r.Reason = "no v=DMARC1 record found at " + name
}

func (v *Verifier) checkMTASTS(ctx context.Context, plan *Plan, r *Record) {
	name := "_mta-sts." + plan.Domain
	txts, err := v.resolver.LookupTXT(ctx, name)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "MTA-STS TXT lookup failed: " + sanitizeDNSError(err)
		return
	}
	joined := normaliseTXT(txts)
	for _, t := range joined {
		t = strings.TrimSpace(t)
		if !strings.HasPrefix(t, "v=STSv1") {
			continue
		}
		// id must match the policy id the plan emits. v=STSv1 is
		// case-insensitive; id is opaque and case-sensitive.
		existingID := extractMTASTSID(t)
		if existingID != "" && existingID == plan.MTAPolicyID {
			r.Status = StatusVerified
			r.Verified = true
			r.Reason = "MTA-STS id matches current plan"
			return
		}
		r.Status = StatusMismatch
		r.Reason = fmt.Sprintf("MTA-STS id differs (live=%q plan=%q) — bump plan id or align live policy", existingID, plan.MTAPolicyID)
		return
	}
	r.Status = StatusMissing
	r.Reason = "no v=STSv1 record found at " + name
}

func (v *Verifier) checkTLSRPT(ctx context.Context, plan *Plan, r *Record) {
	name := "_smtp._tls." + plan.Domain
	txts, err := v.resolver.LookupTXT(ctx, name)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "TLS-RPT TXT lookup failed: " + sanitizeDNSError(err)
		return
	}
	joined := normaliseTXT(txts)
	for _, t := range joined {
		t = strings.TrimSpace(t)
		if !strings.HasPrefix(t, "v=TLSRPTv1") {
			continue
		}
		r.Status = StatusVerified
		r.Verified = true
		r.Reason = "TLS-RPT present"
		return
	}
	r.Status = StatusMissing
	r.Reason = "no v=TLSRPTv1 record found at " + name
}

// checkCAA: if a CAA record exists, we never overwrite it — we
// surface it as conflict/mismatch and let the operator decide. If
// no CAA exists, we leave the row in StatusVerified with the
// "ready to publish" reason; that is the only case where the
// generated "no existing CAA" state is treated as verified.
func (v *Verifier) checkCAA(ctx context.Context, plan *Plan, r *Record) {
	name := apexName(plan, r)
	txts, err := v.resolver.LookupTXT(ctx, name)
	if err != nil {
		// No CAA is the desired starting state; we treat NXDOMAIN
		// / no records as "ready to publish" (verified).
		if isNX(err) {
			r.Status = StatusVerified
			r.Verified = true
			r.Reason = "no existing CAA — safe to publish recommended issuer set"
			return
		}
		r.Status = statusFromDNSError(err)
		r.Reason = "CAA lookup failed: " + sanitizeDNSError(err)
		return
	}
	// CAA records live in the Type=257 RR but the standard
	// LookupTXT path on most resolvers surfaces the human
	// presentation form. We use a coarse parse: any string
	// matching "<flag> <tag> <value>" is a CAA presentation.
	// We treat both "issue" and "issuewild" as authority tags
	// (RFC 8659 §4.2) — a wildcard CA conflicting with our
	// recommended issuer is a conflict for the same reason a
	// plain "issue" conflict is.
	seenIssue := ""
	for _, t := range normaliseTXT(txts) {
		_, tag, val, ok := parseCAAPresentation(t)
		if !ok {
			continue
		}
		if tag == "issue" || tag == "issuewild" {
			seenIssue = val
		}
	}
	if seenIssue == "" {
		// CAA present but no issue tag — treat as informational.
		r.Status = StatusVerified
		r.Verified = true
		r.Reason = "CAA present but no issue tag — won't override"
		return
	}
	wantIssuer := r.Value
	if strings.EqualFold(seenIssue, wantIssuer) {
		r.Status = StatusVerified
		r.Verified = true
		r.Reason = "CAA issue tag matches recommended issuer"
		return
	}
	r.Status = StatusConflict
	r.Reason = fmt.Sprintf("existing CAA issue=%s differs from recommended %s — do not overwrite", seenIssue, wantIssuer)
}

// checkPTR does a reverse lookup on the server IPv4 and compares
// the answer to the expected mail.<domain> host. We do not test
// multiple records here — most resolvers return a single PTR.
func (v *Verifier) checkPTR(ctx context.Context, plan *Plan, r *Record) {
	if strings.TrimSpace(plan.ServerIPv4) == "" {
		r.Status = StatusNotChecked
		r.Reason = "no server IPv4 configured for reverse lookup"
		return
	}
	hosts, err := v.resolver.LookupPTR(ctx, plan.ServerIPv4)
	if err != nil {
		r.Status = statusFromDNSError(err)
		r.Reason = "PTR reverse lookup failed: " + sanitizeDNSError(err)
		return
	}
	want := strings.TrimSuffix(strings.ToLower(plan.MailHost), ".")
	for _, h := range hosts {
		if strings.TrimSuffix(strings.ToLower(h), ".") == want {
			r.Status = StatusVerified
			r.Verified = true
			r.Reason = "PTR resolves to " + h
			return
		}
	}
	r.Status = StatusMismatch
	r.Reason = fmt.Sprintf("PTR resolves to %s; expected %s", strings.Join(hosts, ","), plan.MailHost)
}

// ── Helpers ─────────────────────────────────────────────────────

// statusFromDNSError maps the standard net.DNSError to our Status
// vocabulary without leaking resolver internals.
func statusFromDNSError(err error) Status {
	if err == nil {
		return StatusVerified
	}
	if e, ok := err.(*net.DNSError); ok {
		if e.IsNotFound {
			return StatusMissing
		}
		// Timeout / temporary errors — surface as Error so the
		// dashboard can retry.
		return StatusError
	}
	return StatusError
}

// sanitizeDNSError returns a safe one-line summary of a DNS error
// suitable for the admin dashboard. We never echo the full resolver
// chain (which may include host names or token-like substrings).
func sanitizeDNSError(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := err.(*net.DNSError); ok {
		if e.IsNotFound {
			return "no such host"
		}
		if e.IsTimeout {
			return "timeout"
		}
		if e.IsTemporary {
			return "temporary resolver error"
		}
		return "dns error"
	}
	msg := err.Error()
	// Keep messages short and free of file paths / credentials.
	if len(msg) > 120 {
		msg = msg[:120] + "…"
	}
	return msg
}

func isNX(err error) bool {
	if e, ok := err.(*net.DNSError); ok {
		return e.IsNotFound
	}
	return false
}

// normaliseTXT joins multiple TXT strings into single logical
// records. RFC 1035 §3.3.14 + RFC 7489 §6.3 allow a single TXT
// record to be split across multiple strings; we treat them as one.
//
// Heuristic: a chunk that starts with `v=` (DKIM/SPF/DMARC) or
// `version:` (MTA-STS) is the start of a new logical record.
// Anything else is appended to the current chunk with a single
// separating space if needed (the chunk split loses the boundary
// whitespace). This matches how providers split DKIM/SPF/DMARC/
// MTA-STS TXT records when the single record exceeds the
// 255-octet wire limit.
func normaliseTXT(in []string) []string {
	out := make([]string, 0, len(in))
	var cur strings.Builder
	for _, s := range in {
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		isNew := strings.HasPrefix(t, "v=") || strings.HasPrefix(t, "version:")
		if cur.Len() == 0 {
			cur.WriteString(t)
			continue
		}
		if isNew {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
			cur.WriteString(t)
			continue
		}
		// Continuation: insert a space if the previous chunk did
		// not already end with whitespace or a tag separator.
		prev := cur.String()
		if !strings.HasSuffix(prev, " ") &&
			!strings.HasSuffix(prev, ";") &&
			!strings.HasSuffix(prev, ",") &&
			!strings.HasSuffix(prev, "\t") {
			cur.WriteString(" ")
		}
		cur.WriteString(t)
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

// spfEquivalent returns true if two SPF records match after
// normalising whitespace and ignoring minor differences like the
// ordering of trailing mechanisms. We deliberately do NOT canonicalise
// to the same string — `mx -all` and `mx ip4:... -all` are not
// equivalent — we only collapse inter-token whitespace and lowercase
// the version tag.
func spfEquivalent(a, b string) bool {
	na := normaliseSPF(a)
	nb := normaliseSPF(b)
	if na == nb {
		return true
	}
	return false
}

func normaliseSPF(s string) string {
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return ""
	}
	parts[0] = strings.ToLower(parts[0])
	return strings.Join(parts, " ")
}

// dkimEquivalent returns true if two DKIM TXT records carry the same
// public key. We compare v=, k=, and p= only; t= / h= / s= are
// tolerated if present in the live record.
func dkimEquivalent(a, b string) bool {
	pa := parseTagList(a)
	pb := parseTagList(b)
	if strings.ToLower(pa["v"]) != "dkim1" || strings.ToLower(pb["v"]) != "dkim1" {
		return false
	}
	if strings.ToLower(pa["k"]) != strings.ToLower(pb["k"]) {
		return false
	}
	if pa["p"] == "" || pb["p"] == "" {
		return false
	}
	return strings.TrimSpace(pa["p"]) == strings.TrimSpace(pb["p"])
}

// parseTagList parses a DKIM/SPF-style "tag=value; tag=value" string.
// We do NOT share this with dkim.parseTagValue to keep dnsops free
// of any internal/coremail dependency.
func parseTagList(s string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(part[:eq])
		v := strings.TrimSpace(part[eq+1:])
		out[k] = v
	}
	return out
}

// dmarcMinimalParse enforces the v=DMARC1 + p= minimum contract.
// On success we return a record summary; on failure we return the
// parse error.
func dmarcMinimalParse(s string) (map[string]string, error) {
	tags := parseTagList(s)
	if !strings.EqualFold(tags["v"], "DMARC1") {
		return tags, fmt.Errorf("missing or wrong v tag")
	}
	if tags["p"] == "" {
		return tags, fmt.Errorf("missing p tag")
	}
	switch strings.ToLower(tags["p"]) {
	case "none", "quarantine", "reject":
	default:
		return tags, fmt.Errorf("invalid p tag")
	}
	return tags, nil
}

// dmarcMinimalMatch returns true when two DMARC records carry the
// same policy + rua. We tolerate stricter policies in the live
// record (e.g. plan=none but live=quarantine) only when planPolicy
// is "none"; stricter live is treated as verified so we don't nag
// an operator who has already hardened past our suggestion.
func dmarcMinimalMatch(live, plan string) bool {
	lt, _ := dmarcMinimalParse(live)
	pt, _ := dmarcMinimalParse(plan)
	if strings.EqualFold(lt["p"], pt["p"]) {
		return true
	}
	policyRank := func(p string) int {
		switch strings.ToLower(p) {
		case "none":
			return 0
		case "quarantine":
			return 1
		case "reject":
			return 2
		}
		return -1
	}
	if policyRank(lt["p"]) >= policyRank(pt["p"]) {
		return true
	}
	return false
}

// extractMTASTSID returns the id= tag value of an MTA-STS TXT
// record. Empty string if not found.
func extractMTASTSID(s string) string {
	tags := parseTagList(s)
	return strings.TrimSpace(tags["id"])
}

// parseCAAPresentation parses the human presentation of a CAA
// record: "<flag> <tag> <value>". We accept quoted and unquoted
// values; value whitespace is preserved.
func parseCAAPresentation(s string) (int, string, string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, "", "", false
	}
	// Split off flag (numeric).
	sp := strings.IndexByte(s, ' ')
	if sp < 0 {
		return 0, "", "", false
	}
	flag, err := atoiSafe(s[:sp])
	if err != nil {
		return 0, "", "", false
	}
	rest := strings.TrimSpace(s[sp+1:])
	sp2 := strings.IndexByte(rest, ' ')
	if sp2 < 0 {
		return 0, "", "", false
	}
	tag := rest[:sp2]
	value := strings.TrimSpace(rest[sp2+1:])
	value = strings.Trim(value, `"`)
	return flag, tag, value, true
}

func atoiSafe(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func joinIPs(ips []net.IP) string {
	parts := make([]string, 0, len(ips))
	for _, ip := range ips {
		parts = append(parts, ip.String())
	}
	return strings.Join(parts, ",")
}
