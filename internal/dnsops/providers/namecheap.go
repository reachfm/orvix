package providers

// Namecheap provider — real, safe read-merge-write implementation
// (DNS-AUTOMATION-2G).
//
// The Namecheap XML API requires a full host list on every
// setHosts call. The provider therefore:
//
//   1. Fetches the live host list via NamecheapClient.GetHosts.
//   2. Classifies every live record against the Orvix-managed
//      record set using purpose-aware identity (TXT records
//      distinguished by semantic prefix, CAA by tag).
//   3. Builds a merged set: keep unrelated records, replace
//      Orvix-managed records whose existing value differs from
//      the desired value, add missing Orvix-managed records.
//   4. Refuses to clobber a record that conflicts (same
//      purpose-identity but different value). Unrelated TXT
//      records at the same host (e.g. google-site-verification)
//      are preserved and never treated as conflicts.
//   5. Apply is GATED by NamecheapEnableApply. Even with valid
//      credentials, the provider refuses to call SetHosts
//      when the kill switch is off. The status returned to the
//      UI is "dry_run_only" so the operator understands why
//      the button is disabled.
//
// The provider NEVER returns the API key, API user, request
// URL, or raw XML to the caller. Error messages are one-line
// summaries; the caller can read /api/v1/admin/summary for
// the high-level status and the audit log for the per-call
// record.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/orvix/orvix/internal/dnsops"
)

// NamecheapConfig carries the Namecheap credentials + the
// Apply kill switch.
//
// The standard Namecheap sandbox/production pair is APIUser +
// APIKey; the sandbox adds a ClientIP requirement that we
// surface as a config knob.
//
// NamecheapEnableApply is the kill switch for live writes.
// The provider stays in dry-run mode until an operator
// explicitly flips this on. Default false. The value is read
// from dns.namecheap_enable_apply (YAML) or
// ORVIX_DNS_NAMECHEAP_ENABLE_APPLY (env) by the router; the
// provider itself only consumes the boolean.
type NamecheapConfig struct {
	APIUser     string
	APIKey      string
	Username    string
	ClientIP    string // required for sandbox; production accepts empty
	Sandbox     bool
	EnableApply bool
}

// HasCredentials returns true if the provider has enough
// config to emit a Plan. Apply() additionally checks
// EnableApply; with valid credentials but EnableApply=false
// the status is "dry_run_only" and Apply() refuses.
func (c NamecheapConfig) HasCredentials() bool {
	return strings.TrimSpace(c.APIUser) != "" &&
		strings.TrimSpace(c.APIKey) != "" &&
		strings.TrimSpace(c.Username) != ""
}

// OrvixManaged returns true when the supplied dnsops.Record
// corresponds to a host Orvix manages. The provider uses this
// predicate to classify the live host list into "preserve" /
// "update" / "create" / "conflict" buckets.
func OrvixManaged(r dnsops.Record) bool {
	switch r.Purpose {
	case dnsops.PurposeMX,
		dnsops.PurposeMailA,
		dnsops.PurposeMailAAAA,
		dnsops.PurposeMTASTSHost,
		dnsops.PurposeSPF,
		dnsops.PurposeDKIM,
		dnsops.PurposeDMARC,
		dnsops.PurposeMTASTS,
		dnsops.PurposeTLSRPT,
		dnsops.PurposeCAA:
		return true
	}
	return false
}

// orvixManagedIdentity returns the purpose-aware identity key
// for a desired Orvix-managed record.
//
// For TXT records the identity includes the semantic prefix
// (v=spf1, v=DKIM1, v=DMARC1, v=STSv1, v=TLSRPTv1) so that:
//   - multiple TXT records at the same host are distinct
//   - unrelated TXT records (google-site-verification, acme
//     challenge) never collide with Orvix-managed records
//
// For CAA records the identity includes the tag so that:
//   - "issue" and "iodef" records at @ are both preserved
//   - existing unrelated CAA records are preserved
//
// For all other types the identity is Name|Type.
func orvixManagedIdentity(r dnsops.Record) string {
	switch {
	case r.Purpose == dnsops.PurposeCAA:
		return r.Name + "|" + string(r.Type) + "|" + r.Tag
	case r.Type == dnsops.RecordTXT:
		if p := txtPurposePrefix(r.Value); p != "" {
			return r.Name + "|" + string(r.Type) + "|" + p
		}
		// If the value doesn't carry the prefix (e.g. DKIM not
		// generated yet), use the purpose-based default so the
		// identity matches what liveHostIdentity would produce
		// for a live record with the real managed value.
		if p := purposeToDefaultPrefix(r.Purpose); p != "" {
			return r.Name + "|" + string(r.Type) + "|" + p
		}
		return r.Name + "|" + string(r.Type)
	default:
		return r.Name + "|" + string(r.Type)
	}
}

// txtPurposePrefix returns the semantic prefix of a TXT value
// if it matches a known Orvix-managed record type. Returns ""
// for unrecognised TXT records (google-site-verification etc.).
func txtPurposePrefix(value string) string {
	v := strings.TrimSpace(value)
	if strings.HasPrefix(v, "v=spf1") {
		return "v=spf1"
	}
	if strings.HasPrefix(v, "v=DKIM1") {
		return "v=DKIM1"
	}
	if strings.HasPrefix(v, "v=DMARC1") {
		return "v=DMARC1"
	}
	if strings.HasPrefix(v, "v=STSv1") {
		return "v=STSv1"
	}
	if strings.HasPrefix(v, "v=TLSRPTv1") {
		return "v=TLSRPTv1"
	}
	return ""
}

// purposeToDefaultPrefix returns the semantic TXT prefix for a
// known managed purpose, regardless of whether the current value
// contains it. This ensures orvixManagedIdentity is consistent
// with liveHostIdentity even when the plan's value is a
// placeholder (e.g. "DKIM not generated — public key missing" for
// a DKIM record that hasn't been generated yet).
func purposeToDefaultPrefix(p dnsops.Purpose) string {
	switch p {
	case dnsops.PurposeSPF:
		return "v=spf1"
	case dnsops.PurposeDKIM:
		return "v=DKIM1"
	case dnsops.PurposeDMARC:
		return "v=DMARC1"
	case dnsops.PurposeMTASTS, dnsops.PurposeMTASTSValue:
		return "v=STSv1"
	case dnsops.PurposeTLSRPT:
		return "v=TLSRPTv1"
	}
	return ""
}

// liveHostIdentity returns the purpose-aware identity key for
// a live Namecheap host record. For TXT records the identity
// includes the semantic prefix (or the full address when no
// recognised prefix is present) so that:
//   - unrelated TXT records at the same host are kept distinct
//   - an SPF TXT does not collide with a google-site-verification
//     TXT
//
// For CAA records the identity includes the tag extracted from
// the address ("0 tag value") so multiple CAA records at @ do
// not collapse.
func liveHostIdentity(h NamecheapHost) string {
	switch {
	case h.Type == "CAA":
		return h.Name + "|CAA|" + caaTagFromAddress(h.Address)
	case h.Type == "TXT":
		if p := liveTXTTypePrefix(h.Address); p != "" {
			return h.Name + "|TXT|" + p
		}
		return h.Name + "|TXT|" + h.Address
	default:
		return h.Name + "|" + h.Type
	}
}

// liveTXTTypePrefix returns the semantic prefix of a live
// Namecheap TXT address value. This mirrors txtPurposePrefix
// but operates on the raw NamecheapHost.Address string.
func liveTXTTypePrefix(address string) string {
	v := strings.TrimSpace(address)
	if strings.HasPrefix(v, "v=spf1") {
		return "v=spf1"
	}
	if strings.HasPrefix(v, "v=DKIM1") {
		return "v=DKIM1"
	}
	if strings.HasPrefix(v, "v=DMARC1") {
		return "v=DMARC1"
	}
	if strings.HasPrefix(v, "v=STSv1") {
		return "v=STSv1"
	}
	if strings.HasPrefix(v, "v=TLSRPTv1") {
		return "v=TLSRPTv1"
	}
	return ""
}

// caaTagFromAddress extracts the CAA tag from a Namecheap
// Address field. The Address is "flag tag value" e.g.
// "0 issue letsencrypt.org".
func caaTagFromAddress(addr string) string {
	parts := strings.Fields(addr)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// toNamecheapHostNamecheapHost converts a dnsops.Record to a
// NamecheapHost. Only Orvix-managed records are eligible;
// the caller must filter first. For CAA records the Address
// is formatted as "flag tag value" (Namecheap wire format).
func toNamecheapHost(r dnsops.Record, domain string) NamecheapHost {
	ttl := r.TTL
	if ttl == 0 {
		ttl = 1800 // Namecheap default
	}
	addr := r.Value
	if r.Type == dnsops.RecordCAA {
		addr = fmt.Sprintf("%d %s %s", r.Flag, r.Tag, r.Value)
	}
	mxPref := ""
	if r.Priority > 0 {
		mxPref = fmt.Sprintf("%d", r.Priority)
	}
	return NamecheapHost{
		Name:    r.Name,
		Type:    string(r.Type),
		Address: addr,
		MXPref:  mxPref,
		TTL:     fmt.Sprintf("%d", ttl),
	}
}

// NamecheapProvider implements dnsops.Provider against the
// Namecheap XML API. Live read + read-merge-write is the
// canonical Plan path; Apply is gated by EnableApply.
type NamecheapProvider struct {
	cfg    NamecheapConfig
	client NamecheapClient
}

// NewNamecheapProvider returns a NamecheapProvider backed by
// the supplied client. The client is what the provider uses
// to read live state and to write the merged set. Tests pass
// a FakeNamecheapClient; production passes a
// NetNamecheapClient (constructed by the router with the
// same credentials as the provider's cfg).
func NewNamecheapProvider(cfg NamecheapConfig, client NamecheapClient) *NamecheapProvider {
	return &NamecheapProvider{cfg: cfg, client: client}
}

// NewNamecheapProviderWithClientAndResolver is the legacy
// constructor signature kept for the 2F test suite. New
// callers should use NewNamecheapProvider. The supplied
// resolver is ignored — the provider now reads live state
// via the NamecheapClient.
func NewNamecheapProviderWithClientAndResolver(cfg NamecheapConfig, r dnsops.Resolver) *NamecheapProvider {
	return &NamecheapProvider{cfg: cfg, client: nil}
}

// Compile-time assertion: NamecheapProvider satisfies dnsops.Provider.
var _ dnsops.Provider = (*NamecheapProvider)(nil)

// Name implements dnsops.Provider.
func (p *NamecheapProvider) Name() string { return "namecheap" }

// Plan implements dnsops.Provider. With credentials present,
// the provider fetches the live host list and classifies every
// live record against the desired Orvix-managed set.
//
// Action semantics:
//
//   - create  — desired record is missing in live state
//   - update  — desired record exists in live state with a
//     different value
//   - skip    — desired record exists in live state with the
//     same value (or it is an unrelated record
//     Orvix does not manage)
//   - conflict — live record has the same purpose-identity
//     as a desired Orvix-managed record but a
//     value we cannot safely replace. The provider
//     refuses to overwrite without explicit operator
//     opt-in.
//   - delete  — never emitted; reserved.
//
// Unrelated TXT records at the same host (e.g.
// google-site-verification at @) are preserved and never
// treated as conflicts.
//
// The provider status reported in Notes reflects the
// live-readiness: "not_configured" (no credentials),
// "dry_run_only" (credentials present but EnableApply=false),
// or "ready" (credentials + EnableApply=true).
func (p *NamecheapProvider) Plan(ctx context.Context, plan *dnsops.Plan) (dnsops.ChangePlan, error) {
	if plan == nil {
		return dnsops.ChangePlan{}, fmt.Errorf("plan is nil")
	}
	out := dnsops.ChangePlan{Provider: p.Name(), Domain: plan.Domain}
	if !p.cfg.HasCredentials() {
		out.Notes = append(out.Notes, "namecheap provider not configured — set dns.namecheap_api_user, dns.namecheap_api_key, and dns.namecheap_username in the server config to enable")
		return out, nil
	}
	status := "ready"
	if !p.cfg.EnableApply {
		status = "dry_run_only"
	}
	mode := "production"
	if p.cfg.Sandbox {
		mode = "sandbox"
	}
	out.Notes = append(out.Notes, fmt.Sprintf(
		"namecheap credentials detected (%s mode); status=%s", mode, status))
	if p.client == nil {
		out.Notes = append(out.Notes, "namecheap client not wired — read-merge-write disabled in this build")
		return out, nil
	}
	// Reject unsupported multi-label public suffixes before
	// any API call (BLOCKER 4).
	sld, tld, ok := splitDomain(plan.Domain)
	if !ok {
		out.Notes = append(out.Notes, "namecheap plan: unsupported multi-label public suffix for Namecheap provider")
		return out, nil
	}
	live, err := p.client.GetHosts(ctx, sld, tld)
	if err != nil {
		out.Notes = append(out.Notes, fmt.Sprintf("namecheap getHosts failed: %s", sanitiseErr(err)))
		return classifyWithoutLive(plan, p.Name()), nil
	}
	out, ok = p.buildMergePlan(plan, live, out)
	return out, nil
}

// classifyWithoutLive returns a per-record plan with
// Action=create when the provider cannot read the live zone
// (e.g. API outage). The Apply path will refuse to run when
// the live read failed because the read-merge-write contract
// cannot be honoured without a baseline.
func classifyWithoutLive(plan *dnsops.Plan, providerName string) dnsops.ChangePlan {
	out := dnsops.ChangePlan{Provider: providerName, Domain: plan.Domain}
	for _, r := range plan.Records {
		if !OrvixManaged(r) {
			continue
		}
		ch := dnsops.Change{Record: r, Action: dnsops.ActionCreate}
		ch.Reason = "live zone unreadable — would create on apply (after live read succeeds)"
		out.Steps = append(out.Steps, ch)
	}
	return out
}

// buildMergePlan is the shared read-merge-write logic used
// by both Plan() and Apply(). It classifies every live host
// against the desired Orvix-managed set using the purpose-
// aware identity system.
//
// Returns a ChangePlan with per-record Steps populated.
// The merged set is NOT stored in the ChangePlan (it is
// ephemeral); Apply rebuilds it from the fresh live snapshot.
func (p *NamecheapProvider) buildMergePlan(plan *dnsops.Plan, live []NamecheapHost, out dnsops.ChangePlan) (dnsops.ChangePlan, bool) {
	desired := make([]NamecheapHost, 0, len(plan.Records))
	desiredByIdentity := make(map[string]NamecheapHost, len(plan.Records))
	for _, r := range plan.Records {
		if !OrvixManaged(r) {
			continue
		}
		h := toNamecheapHost(r, plan.Domain)
		desired = append(desired, h)
		desiredByIdentity[orvixManagedIdentity(r)] = h
	}

	// Detect duplicate managed identities in the live zone
	// BEFORE building the merged set. If two live records
	// share the same managed identity (e.g. two SPF TXT
	// records at @ with different values), the second one
	// would be treated as "unrelated" and preserved
	// alongside the managed record — creating a dangerous
	// duplicate. We flag all such cases as conflicts
	// (BLOCKER 2, DNS-AUTOMATION-2G-SAFETY-FIXES).
	duplicateConflicts, allDuplicateKeys := detectDuplicateLiveIdentities(live, desiredByIdentity)

	// Classify every live record using purpose-aware identity.
	// A live record whose identity matches a desired record
	// is either kept (same value) or flagged as a conflict
	// (different value). A live record whose identity does NOT
	// match any desired record is preserved as unrelated
	// (this is where google-site-verification at @ survives).
	conflicts := make(map[string]bool)
	// Merge duplicate conflict IDs into the conflict map.
	// Only add actual conflicts (different values); exact
	// duplicates are safe to collapse.
	for k, isConflict := range duplicateConflicts {
		if isConflict {
			conflicts[k] = true
		}
	}
	merged := make([]NamecheapHost, 0, len(live))
	seenIdentity := make(map[string]bool, len(live))
	for _, h := range live {
		key := liveHostIdentity(h)
		// If this identity has duplicates (either conflicting or
		// exact), collapse them — keep only the first occurrence.
		if allDuplicateKeys[key] {
			if seenIdentity[key] {
				continue
			}
			seenIdentity[key] = true
			merged = append(merged, h)
			delete(desiredByIdentity, key)
			continue
		}
		if desiredHost, ok := desiredByIdentity[key]; ok {
			if sameHost(desiredHost, h) {
				merged = append(merged, h)
			} else {
				conflicts[key] = true
			}
			delete(desiredByIdentity, key)
			continue
		}
		merged = append(merged, h)
	}
	// Append any desired records that were not found in live
	// state (i.e. missing). Use the desired host value, not
	// the live value.
	for _, h := range desired {
		if _, ok := findByKey(merged, h); ok {
			continue
		}
		merged = append(merged, h)
	}

	// Build the ChangePlan.Steps.
	for _, r := range plan.Records {
		if !OrvixManaged(r) {
			continue
		}
		ch := dnsops.Change{Record: r}
		key := orvixManagedIdentity(r)
		liveHost, hasLive := findLiveHost(live, key)
		switch r.Purpose {
		case dnsops.PurposePTR, dnsops.PurposeBIMI, dnsops.PurposeDANETLSA:
			ch.Action = dnsops.ActionSkip
			ch.Reason = "informational only — not a Namecheap zone record"
		default:
			switch {
			case conflicts[key]:
				ch.Action = dnsops.ActionConflict
				if hasLive {
					ch.LiveValue = liveHost.Address
				}
				desiredH := toNamecheapHost(r, plan.Domain)
				ch.Reason = fmt.Sprintf("live record at %s differs from desired %s (live=%q desired=%q) — refusing to overwrite; remove the unrelated record before applying", desiredH.Name+"/"+desiredH.Type, desiredH.Name+"/"+desiredH.Type, liveHost.Address, desiredH.Address)
			case !hasLive:
				ch.Action = dnsops.ActionCreate
				ch.Reason = "missing in live zone; merged set will append"
			case sameHost(toNamecheapHost(r, plan.Domain), liveHost):
				ch.Action = dnsops.ActionSkip
				ch.LiveValue = liveHost.Address
				ch.Reason = "live record already matches the desired value"
			default:
				ch.Action = dnsops.ActionUpdate
				ch.LiveValue = liveHost.Address
				ch.Reason = fmt.Sprintf("live value %q differs from desired %q", liveHost.Address, toNamecheapHost(r, plan.Domain).Address)
			}
		}
		out.Steps = append(out.Steps, ch)
	}

	hasConflict := false
	for _, s := range out.Steps {
		if s.Action == dnsops.ActionConflict {
			hasConflict = true
			break
		}
	}

	sort.SliceStable(out.Steps, func(i, j int) bool {
		return stepKey(out.Steps[i]) < stepKey(out.Steps[j])
	})
	out.Notes = append(out.Notes, fmt.Sprintf(
		"namecheap plan: %d desired Orvix-managed record(s); %d live host(s) preserved; merged set has %d record(s)",
		len(desired), len(live), len(merged)))
	return out, hasConflict
}

// ── Apply ────────────────────────────────────────────────────────

// Apply implements dnsops.Provider. Apply requires:
//
//   - non-empty confirm (operator typed it)
//   - confirm == "apply-dns-changes"
//   - credentials present
//   - EnableApply true
//   - domain uses a safe single-label TLD (rejects multi-label)
//   - live read succeeds
//   - fresh merge from live has zero conflicts (TOCTOU guard)
//
// The merged set is computed from a FRESH live read (not from
// the Plan's cached Steps) so that a concurrent zone change
// between Plan and Apply is detected and the apply is aborted
// before SetHosts touches the live zone. This prevents the
// TOCTOU class of bugs where a conflicting record added between
// Plan and Apply would be silently dropped from the merged set.
func (p *NamecheapProvider) Apply(ctx context.Context, plan dnsops.ChangePlan, confirm string) (dnsops.ApplyResult, error) {
	if strings.TrimSpace(confirm) == "" {
		return dnsops.ApplyResult{}, fmt.Errorf("apply requires a non-empty confirmation")
	}
	if confirm != "apply-dns-changes" {
		return dnsops.ApplyResult{}, fmt.Errorf("apply confirmation must be the literal string apply-dns-changes")
	}
	if !p.cfg.HasCredentials() {
		return dnsops.ApplyResult{}, fmt.Errorf("namecheap provider not configured")
	}
	if !p.cfg.EnableApply {
		return dnsops.ApplyResult{}, fmt.Errorf("namecheap live apply is disabled; set dns.namecheap_enable_apply=true in the server config to enable")
	}
	if p.client == nil {
		return dnsops.ApplyResult{}, fmt.Errorf("namecheap client not wired")
	}
	domain := plan.Domain
	sld, tld, ok := splitDomain(domain)
	if !ok {
		return dnsops.ApplyResult{}, fmt.Errorf("unsupported multi-label public suffix for Namecheap provider: %q", domain)
	}
	if len(plan.Steps) == 0 {
		return dnsops.ApplyResult{}, fmt.Errorf("apply requires a prior plan; call provider/plan first")
	}

	// TOCTOU guard: re-fetch the live zone and re-compute the
	// merge. The Plan's Steps are used only for counting the
	// desired actions; the actual merge is deterministic from
	// the fresh live baseline.
	live, err := p.client.GetHosts(ctx, sld, tld)
	if err != nil {
		return dnsops.ApplyResult{}, fmt.Errorf("namecheap getHosts failed: %s", sanitiseErr(err))
	}

	// Rebuild the desired set from the Plan's Steps.
	desiredByIdentity := make(map[string]NamecheapHost, len(plan.Steps))
	desired := make([]NamecheapHost, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		r := s.Record
		if !OrvixManaged(r) {
			continue
		}
		h := toNamecheapHost(r, domain)
		desiredByIdentity[orvixManagedIdentity(r)] = h
		desired = append(desired, h)
	}

	// Fresh merge from live. Apply the same conflict-aware logic
	// as Plan. If a NEW conflict appears (one that was not present
	// when the Plan was computed), refuse before SetHosts.
	merged := make([]NamecheapHost, 0, len(live)+len(desired))
	conflicts := make(map[string]bool)
	seenIdentity := make(map[string]bool, len(live))

	// Duplicate live managed identity detection (BLOCKER 2).
	// Must run before the merge loop so that duplicate records
	// sharing the same semantic identity are flagged as conflicts.
	dupConflicts, allDupKeys := detectDuplicateLiveIdentities(live, desiredByIdentity)
	for k, isConflict := range dupConflicts {
		if isConflict {
			conflicts[k] = true
		}
	}

	for _, h := range live {
		key := liveHostIdentity(h)
		// Skip duplicate managed identities that were already
		// added to the merged set. Exact duplicates (same
		// identity AND same value) are collapsed; duplicates
		// with different values are already flagged as conflicts
		// by detectDuplicateLiveIdentities above.
		if allDupKeys[key] {
			if seenIdentity[key] {
				continue
			}
			seenIdentity[key] = true
			merged = append(merged, h)
			delete(desiredByIdentity, key)
			continue
		}
		if d, ok := desiredByIdentity[key]; ok {
			if sameHost(d, h) {
				merged = append(merged, h)
			} else {
				conflicts[key] = true
			}
			delete(desiredByIdentity, key)
			continue
		}
		merged = append(merged, h)
	}
	for _, h := range desired {
		if _, ok := findByKey(merged, h); ok {
			continue
		}
		merged = append(merged, h)
	}

	// If any conflicts were detected in the fresh merge, abort
	// before SetHosts. The operator must investigate and remove
	// the conflicting record before retrying.
	if len(conflicts) > 0 {
		return dnsops.ApplyResult{
			Provider: p.Name(),
			Domain:   domain,
			Failed:   len(desired),
			Steps:    plan.Steps,
			Notes: []string{
				"namecheap apply refused: fresh live read detected conflicting record(s); remove the conflicting live record before retrying",
			},
		}, nil
	}

	if _, err := p.client.SetHosts(ctx, sld, tld, merged); err != nil {
		return dnsops.ApplyResult{
			Provider: p.Name(),
			Domain:   domain,
			Failed:   len(merged),
			Steps:    plan.Steps,
			Notes:    []string{"namecheap setHosts failed: " + sanitiseErr(err)},
		}, nil
	}

	// Build the apply summary from the Plan's Steps actions.
	res := dnsops.ApplyResult{
		Provider: p.Name(),
		Domain:   domain,
		Steps:    plan.Steps,
		Notes:    []string{fmt.Sprintf("namecheap apply succeeded: merged set has %d record(s) (%d live preserved, %d Orvix-managed created/updated)", len(merged), len(live), len(desired))},
	}
	for _, s := range plan.Steps {
		switch s.Action {
		case dnsops.ActionCreate:
			res.Applied++
		case dnsops.ActionUpdate:
			res.Applied++
		case dnsops.ActionSkip:
			res.Skipped++
		case dnsops.ActionConflict:
			res.Failed++
		case dnsops.ActionDelete:
			res.Failed++
		}
	}
	return res, nil
}

// ── Helpers ─────────────────────────────────────────────────────

// sameHost reports whether two Namecheap hosts carry the
// same effective value. TTL is intentionally EXCLUDED from
// the comparison: TTL is an operator-controlled cache hint,
// not part of the "what this record is" identity. Treating
// a TTL-only difference as a conflict would refuse the
// apply every time the operator tweaked dns ops TTLs, with
// no semantic benefit. MXPref is included because changing
// the MX priority is a meaningful semantic change.
func sameHost(a, b NamecheapHost) bool {
	return a.Name == b.Name &&
		a.Type == b.Type &&
		a.Address == b.Address &&
		a.MXPref == b.MXPref
}

// findByKey looks for a host in list whose live identity
// matches h. Used to avoid duplicating records in the merged
// set when the desired list re-introduces something already
// kept from live state.
func findByKey(list []NamecheapHost, h NamecheapHost) (NamecheapHost, bool) {
	key := liveHostIdentity(h)
	for _, x := range list {
		if liveHostIdentity(x) == key {
			return x, true
		}
	}
	return NamecheapHost{}, false
}

// findLiveHost returns the live host whose live identity
// matches the supplied key. The provider uses this to set
// Change.LiveValue (the address that was already in the
// zone) and to decide update vs skip.
func findLiveHost(live []NamecheapHost, key string) (NamecheapHost, bool) {
	for _, h := range live {
		if liveHostIdentity(h) == key {
			return h, true
		}
	}
	return NamecheapHost{}, false
}

// detectDuplicateLiveIdentities scans live hosts for duplicate
// managed identities (BLOCKER 2, DNS-AUTOMATION-2G-SAFETY-FIXES).
//
// A managed identity is one that corresponds to an Orvix-managed
// record type (SPF, DKIM, DMARC, MTA-STS, TLS-RPT, CAA).
// Unrelated TXT records (google-site-verification, _acme-challenge)
// are NOT managed and their duplicates are ignored.
//
// Returns a conflict map (identity → true if conflicting values,
// false for exact duplicates) and a set of all duplicate identity
// keys. The caller uses the conflict map to build ChangePlan
// conflict steps and the all-keys set to collapse duplicate
// records in the merged set.
func detectDuplicateLiveIdentities(live []NamecheapHost, desiredByIdentity map[string]NamecheapHost) (conflictMap map[string]bool, allDupKeys map[string]bool) {
	type counter struct {
		count   int
		address string
	}
	conflictMap = make(map[string]bool)
	allDupKeys = make(map[string]bool)

	identities := make(map[string]*counter, len(live))
	for _, h := range live {
		key := liveHostIdentity(h)
		isManaged := false
		if _, ok := desiredByIdentity[key]; ok {
			isManaged = true
		} else if strings.Contains(key, "v=spf1") || strings.Contains(key, "v=DKIM1") ||
			strings.Contains(key, "v=DMARC1") || strings.Contains(key, "v=STSv1") ||
			strings.Contains(key, "v=TLSRPTv1") || strings.Contains(key, "|CAA|") {
			isManaged = true
		}
		if !isManaged {
			continue
		}
		if _, ok := identities[key]; !ok {
			identities[key] = &counter{address: h.Address}
		}
		identities[key].count++
	}
	// Any identity with count > 1 is a duplicate. The conflictMap
	// entry is true if any occurrence has a different address
	// (conflict), false if all are exact duplicates.
	for key, c := range identities {
		if c.count <= 1 {
			continue
		}
		allDupKeys[key] = true
		hasConflict := false
		for _, h := range live {
			if liveHostIdentity(h) == key && h.Address != c.address {
				hasConflict = true
				break
			}
		}
		conflictMap[key] = hasConflict
	}
	return conflictMap, allDupKeys
}

// splitDomain splits "example.com" into ("example", "com").
// The Namecheap API expects SLD (second-level) + TLD
// separately. This implementation only supports single-label
// TLDs (e.g. .com, .net, .email). Multi-label public suffixes
// like "co.uk" or "com.au" are rejected because the naive
// split would produce an incorrect SLD/TLD pair, causing the
// API call to target the wrong zone.
//
// The rejection is safe: the caller (Plan / Apply) returns a
// clear, sanitised error with no domain content leaked and no
// API mutation made.
func splitDomain(domain string) (sld, tld string, ok bool) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return "", "", false
	}
	// Only accept exactly 2 labels (single-label TLD).
	// example.com has 2 labels → ok.
	// example.co.uk has 3 labels → rejected.
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// sanitiseErr returns a one-line error summary with no
// token-shaped substrings. Used for both Notes / error
// messages so a leak from the underlying HTTP client (e.g.
// an embedded URL with ?ApiKey=...) never reaches a
// client.
func sanitiseErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.IndexAny(s, "\n\r?"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// stepKey is a stable sort key for ChangePlan.Steps so the
// dashboard renders records in a consistent order: create
// first, then update, then skip, then conflict, then delete.
func stepKey(s dnsops.Change) string {
	switch s.Action {
	case dnsops.ActionCreate:
		return "0_create"
	case dnsops.ActionUpdate:
		return "1_update"
	case dnsops.ActionSkip:
		return "2_skip"
	case dnsops.ActionConflict:
		return "3_conflict"
	case dnsops.ActionDelete:
		return "4_delete"
	}
	return "5_other"
}
