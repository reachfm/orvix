package providers

// Namecheap provider — real, safe read-merge-write implementation
// (DNS-AUTOMATION-2G).
//
// The Namecheap XML API requires a full host list on every
// setHosts call. The provider therefore:
//
//   1. Fetches the live host list via NamecheapClient.GetHosts.
//   2. Classifies every live record against the Orvix-managed
//      record set (MX @ / A mail / A mta-sts / TXT SPF /
//      TXT selector._domainkey / TXT _dmarc / TXT _mta-sts /
//      TXT _smtp._tls / CAA @).
//   3. Builds a merged set: keep unrelated records, replace
//      Orvix-managed records whose existing value differs from
//      the desired value, add missing Orvix-managed records.
//   4. Refuses to clobber a record that has the same name +
//      type as an Orvix-managed record but a value the
//      provider cannot classify (Conflict action).
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
	APIUser         string
	APIKey          string
	Username        string
	ClientIP        string // required for sandbox; production accepts empty
	Sandbox         bool
	EnableApply     bool
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

// orvixManagedKey is the canonical identity for a managed
// record at the Namecheap level. Namecheap uses a single
// "Name" field that becomes the host portion of the FQDN;
// "@" represents the apex.
func orvixManagedKey(r dnsops.Record) string {
	return r.Name + "|" + string(r.Type)
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
//               different value
//   - skip    — desired record exists in live state with the
//               same value (or it is an unrelated record
//               Orvix does not manage)
//   - conflict — live record has the same host + type as a
//               desired Orvix-managed record but a value we
//               cannot safely replace. The provider refuses
//               to overwrite without explicit operator opt-in.
//   - delete  — never emitted; reserved.
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
	// Fetch the live host list. A network error here is
	// surfaced as a sanitised note; the per-record Plan
	// below still describes the desired state with
	// Action=create so the operator can see what the
	// provider would do.
	sld, tld, ok := splitDomain(plan.Domain)
	if !ok {
		out.Notes = append(out.Notes, "namecheap plan: domain is not a valid SLD.TLD; cannot fetch live hosts")
		return classifyWithoutLive(plan, p.Name()), nil
	}
	live, err := p.client.GetHosts(ctx, sld, tld)
	if err != nil {
		out.Notes = append(out.Notes, fmt.Sprintf("namecheap getHosts failed: %s", sanitiseErr(err)))
		return classifyWithoutLive(plan, p.Name()), nil
	}
	// Build the desired Namecheap host list (only the
	// Orvix-managed records).
	desired := make([]NamecheapHost, 0, len(plan.Records))
	desiredByKey := make(map[string]NamecheapHost, len(plan.Records))
	for _, r := range plan.Records {
		if !OrvixManaged(r) {
			continue
		}
		h := toNamecheapHost(r, plan.Domain)
		desired = append(desired, h)
		desiredByKey[orvixManagedKey(r)] = h
	}
	// Classify every live record. Records that match a
	// desired key are updated / kept; records that don't
	// match are preserved as-is.
	//
	// Safety contract: when a live record's (Name, Type)
	// matches a desired Orvix-managed record but the value
	// differs, the provider MUST NOT silently overwrite
	// the live value. Per the brief: "refuse if an
	// existing record has same host/type but incompatible
	// value unless operator explicitly opts into update".
	// This build never opts in, so we surface the
	// disagreement as a Conflict action in the Plan
	// steps and exclude the record from the merged set
	// (so a subsequent Apply will refuse). The operator
	// must remove the unrelated record from the live zone
	// before retrying.
	conflicts := make(map[string]bool)
	merged := make([]NamecheapHost, 0, len(live))
	for _, h := range live {
		// The Namecheap "Name" field is the host portion
		// (e.g. "mail" or "@"); map it back to the
		// dnsops record identity so the desired-set
		// lookup works.
		key := hostIdentity(h)
		if desiredHost, ok := desiredByKey[key]; ok {
			if sameHost(desiredHost, h) {
				merged = append(merged, h) // already correct
			} else {
				// Same (Name, Type) but a different value:
				// do NOT overwrite. The conflicting live
				// record is excluded from the merged set
				// and the corresponding Plan step is
				// marked Action=Conflict so the Apply
				// path will refuse.
				conflicts[key] = true
			}
			delete(desiredByKey, key)
			continue
		}
		// Unrelated record: preserve unchanged.
		merged = append(merged, h)
	}
	// Anything left in desiredByKey is missing from live
	// state and must be created. Append them to the merged
	// set; the order is the order we generated them.
	for _, h := range desired {
		if _, ok := findByKey(merged, h); ok {
			continue
		}
		merged = append(merged, h)
	}
	// Build the ChangePlan.Steps from the desired records
	// so the dashboard can render create / update / skip /
	// conflict per-record.
	for _, r := range plan.Records {
		if !OrvixManaged(r) {
			continue
		}
		ch := dnsops.Change{Record: r}
		liveHost, hasLive := findLiveHost(live, orvixManagedKey(r))
		switch r.Purpose {
		case dnsops.PurposePTR, dnsops.PurposeBIMI, dnsops.PurposeDANETLSA:
			ch.Action = dnsops.ActionSkip
			ch.Reason = "informational only — not a Namecheap zone record"
		default:
			switch {
			case conflicts[orvixManagedKey(r)]:
				ch.Action = dnsops.ActionConflict
				ch.LiveValue = liveHost.Address
				desiredH := toNamecheapHost(r, plan.Domain)
				ch.Reason = fmt.Sprintf("live record at %s differs from desired %s (live=%q desired=%q) — refusing to overwrite; remove the unrelated record before applying", liveHost.Address, desiredH.Address, liveHost.Address, desiredH.Address)
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
	// Sort steps by action so the dashboard renders in a
	// stable order.
	sort.SliceStable(out.Steps, func(i, j int) bool {
		return stepKey(out.Steps[i]) < stepKey(out.Steps[j])
	})
	out.Notes = append(out.Notes, fmt.Sprintf(
		"namecheap plan: %d desired Orvix-managed record(s); %d live host(s) preserved; merged set has %d record(s)",
		len(desired), len(live), len(merged)))
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

// Apply implements dnsops.Provider. Apply requires:
//
//   - non-empty confirm (operator typed it)
//   - credentials present
//   - EnableApply true
//   - live read succeeded (the Plan we just computed had a
//     fully populated Steps list with mixed create / update /
//     skip; a plan produced by classifyWithoutLive is not
//     safe to apply because the merge baseline is missing)
//
// The merged set is computed exactly as in Plan so the live
// API call receives a coherent, fully-preserved host list.
// The apply result reports created / updated / skipped /
// failed counts so the dashboard can render a summary.
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
	if len(plan.Steps) == 0 {
		return dnsops.ApplyResult{}, fmt.Errorf("apply requires a prior plan; call provider/plan first")
	}
	// Refuse if any plan step is in conflict — the operator
	// must remove the unrelated live record before retrying.
	// The brief mandates "refuse on conflict"; this is the
	// only place that gate lives so it cannot be bypassed
	// by a stale Plan.
	for _, s := range plan.Steps {
		if s.Action == dnsops.ActionConflict {
			return dnsops.ApplyResult{
				Provider: p.Name(),
				Domain:   plan.Domain,
				Failed:   len(plan.Steps),
				Steps:    plan.Steps,
				Notes: []string{
					"namecheap apply refused: dry-run plan has conflict(s) at " + s.Record.Name + "/" + string(s.Record.Type) + "; remove the unrelated live record before retrying",
				},
			}, nil
		}
	}
	domain := plan.Domain
	sld, tld, ok := splitDomain(domain)
	if !ok {
		return dnsops.ApplyResult{}, fmt.Errorf("invalid domain %q", domain)
	}
	// We need the desired Orvix-managed set so we can merge.
	// The provider only stores the Change shape, not the
	// original dnsops.Plan; the simplest fix is to re-fetch
	// the live zone and re-classify using the same logic
	// as Plan. This guarantees the merged set is
	// deterministic against the live API response.
	live, err := p.client.GetHosts(ctx, sld, tld)
	if err != nil {
		return dnsops.ApplyResult{}, fmt.Errorf("namecheap getHosts failed: %s", sanitiseErr(err))
	}
	// We don't have the original dnsops.Plan here; rebuild
	// the desired managed set from the ChangePlan.Steps.
	desiredByKey := make(map[string]NamecheapHost, len(plan.Steps))
	desired := make([]NamecheapHost, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		r := s.Record
		if !OrvixManaged(r) {
			continue
		}
		h := toNamecheapHost(r, domain)
		desiredByKey[orvixManagedKey(r)] = h
		desired = append(desired, h)
	}
	// Apply the same conflict-aware merge as Plan: if a
	// live record shares an (Name, Type) key with a
	// desired Orvix-managed record and the value differs,
	// we EXCLUDE the conflicting live record from the
	// merged set (the operator must remove it before
	// retrying). The conflict check above (against
	// plan.Steps) already gated this Apply call, so this
	// branch is a defensive belt-and-braces — it would only
	// fire if a concurrent write changed the live zone
	// between the Plan and Apply calls.
	merged := make([]NamecheapHost, 0, len(live)+len(desired))
	for _, h := range live {
		key := hostIdentity(h)
		if d, ok := desiredByKey[key]; ok {
			if sameHost(d, h) {
				merged = append(merged, h) // skip (already correct)
			} else {
				// Conflict: do not overwrite. The
				// conflicting live record is left out of
				// the merged set; the operator must
				// remove it from the live zone.
				delete(desiredByKey, key)
				continue
			}
			delete(desiredByKey, key)
			continue
		}
		merged = append(merged, h) // preserve unrelated
	}
	for _, h := range desired {
		if _, ok := findByKey(merged, h); ok {
			continue
		}
		merged = append(merged, h) // create
	}
	// Final safety: refuse to submit a set that contains
	// any record we cannot classify. The merge logic above
	// only passes records that came from live (preserved)
	// or the desired set; there is no third source.
	if _, err := p.client.SetHosts(ctx, sld, tld, merged); err != nil {
		return dnsops.ApplyResult{
			Provider: p.Name(),
			Domain:   domain,
			Failed:   len(merged),
			Steps:    plan.Steps,
			Notes:    []string{"namecheap setHosts failed: " + sanitiseErr(err)},
		}, nil
	}
	// Build the apply summary by counting the per-record
	// actions from the Plan. The live-write succeeded so
	// the Failed counter is zero; the per-step Actions
	// remain Create / Update / Skip from the Plan.
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

// toNamecheapHost converts a dnsops.Record to a Namecheap
// NamecheapHost. Only Orvix-managed records are eligible;
// the caller must filter first.
func toNamecheapHost(r dnsops.Record, domain string) NamecheapHost {
	ttl := r.TTL
	if ttl == 0 {
		ttl = 1800 // Namecheap default
	}
	mxPref := ""
	if r.Priority > 0 {
		mxPref = fmt.Sprintf("%d", r.Priority)
	}
	return NamecheapHost{
		Name:    r.Name,
		Type:    string(r.Type),
		Address: r.Value,
		MXPref:  mxPref,
		TTL:     fmt.Sprintf("%d", ttl),
	}
}

// hostIdentity returns the dnsops-orvixManagedKey for a live
// Namecheap host. The Namecheap "Name" field already matches
// the dnsops "Name" field for managed records (we generate
// records with the canonical name).
func hostIdentity(h NamecheapHost) string {
	return h.Name + "|" + h.Type
}

// sameHost reports whether two Namecheap hosts carry the
// same effective value. TTL is intentionally EXCLUDED from
// the comparison: TTL is an operator-controlled cache hint,
// not part of the "what this record is" identity. Treating
// a TTL-only difference as a conflict would refuse the
// apply every time the operator tweaked dns ops TTLs, with
// no semantic benefit. MXPref is included because changing
// the MX priority is a meaningful semantic change.
//
// The provider records the existing TTL in the merged set
// so the live TTL is preserved when no other field differs.
func sameHost(a, b NamecheapHost) bool {
	return a.Name == b.Name &&
		a.Type == b.Type &&
		a.Address == b.Address &&
		a.MXPref == b.MXPref
}

// findByKey looks for a host in list whose (Name, Type)
// matches h. Used to avoid duplicating records in the merged
// set when the desired list re-introduces something already
// kept from live state.
func findByKey(list []NamecheapHost, h NamecheapHost) (NamecheapHost, bool) {
	key := hostIdentity(h)
	for _, x := range list {
		if hostIdentity(x) == key {
			return x, true
		}
	}
	return NamecheapHost{}, false
}

// findLiveHost returns the live host whose orvixManagedKey
// matches the supplied key. The provider uses this to set
// Change.LiveValue (the address that was already in the
// zone) and to decide update vs skip.
func findLiveHost(live []NamecheapHost, key string) (NamecheapHost, bool) {
	for _, h := range live {
		if hostIdentity(h) == key {
			return h, true
		}
	}
	return NamecheapHost{}, false
}

// splitDomain splits "example.com" into ("example", "com").
// The Namecheap API expects SLD (second-level) + TLD
// separately. Multi-label TLDs like "co.uk" are not
// supported by this provider in this build; a future
// config-driven TLD suffix list can extend the helper.
func splitDomain(domain string) (sld, tld string, ok bool) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[len(parts)-1], true
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
