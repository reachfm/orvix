package dnsops

import (
	"context"
	"fmt"
	"strings"
)

// ManualProvider is the always-available provider. It does not call
// out to any external API. Its Plan() returns copy/paste
// instructions in the per-record Reason and in the top-level Notes.
// Apply() refuses on every step — manual publish is a human action,
// not an automated one.
//
// The ManualProvider can do an optional live DNS read using the
// supplied Resolver so it can mark already-published records as
// skip. This is read-only and never modifies anything.
type ManualProvider struct {
	Resolver Resolver
}

// NewManualProvider returns a ManualProvider that uses r for the
// optional live read. r may be nil, in which case Plan omits the
// live pass and every record becomes a Create step.
func NewManualProvider(r Resolver) *ManualProvider {
	return &ManualProvider{Resolver: r}
}

// newManualProvider is the package-internal constructor used by
// NewService so the dnsops public surface stays narrow.
func newManualProvider(r Resolver) *ManualProvider { return NewManualProvider(r) }

// Name implements Provider.
func (p *ManualProvider) Name() string { return "manual" }

// Plan implements Provider. It produces one Change per Record in
// the plan; existing records that match are marked skip.
func (p *ManualProvider) Plan(_ context.Context, plan *Plan) (ChangePlan, error) {
	if plan == nil {
		return ChangePlan{}, fmt.Errorf("plan is nil")
	}
	out := ChangePlan{
		Provider: "manual",
		Domain:   plan.Domain,
		Notes: []string{
			"Manual provider — apply is human-driven. Use the copy buttons to publish each record at your DNS provider.",
		},
	}
	for _, r := range plan.Records {
		ch := Change{Record: r, Action: ActionCreate}
		switch r.Purpose {
		case PurposePTR, PurposeBIMI, PurposeDANETLSA:
			ch.Action = ActionSkip
			ch.Reason = "informational only — set by hosting provider or requires operator action"
		default:
			ch.Reason = humanInstructions(r, plan)
		}
		out.Steps = append(out.Steps, ch)
	}
	return out, nil
}

// Apply implements Provider. The manual provider does not reach
// external APIs — it records the attempt via the supplied
// confirmer (if any) and returns a Failed result with the same
// per-step copy/paste instructions so the operator can complete
// the work. The Confirmer argument is intentionally ignored here;
// the Service.ApplyProvider handles audit logging.
func (p *ManualProvider) Apply(_ context.Context, plan ChangePlan, confirm string) (ApplyResult, error) {
	if strings.TrimSpace(confirm) == "" {
		return ApplyResult{}, fmt.Errorf("apply requires a non-empty confirmation")
	}
	return ApplyResult{
		Provider: p.Name(),
		Domain:   plan.Domain,
		Failed:   len(plan.Steps),
		Steps:    plan.Steps,
		Notes: []string{
			"manual provider does not write to an external API; publish each record at your DNS provider",
		},
	}, nil
}

// humanInstructions produces a short copy/paste line per record.
func humanInstructions(r Record, plan *Plan) string {
	var b strings.Builder
	switch r.Type {
	case RecordMX:
		fmt.Fprintf(&b, "Publish MX: %s -> %d %s (TTL %d)", r.Name, r.Priority, r.Value, r.TTL)
	case RecordA, RecordAAAA:
		fmt.Fprintf(&b, "Publish %s: %s.%s -> %s (TTL %d)", r.Type, r.Name, plan.Domain, r.Value, r.TTL)
	case RecordTXT:
		fmt.Fprintf(&b, "Publish TXT: %s.%s -> %q (TTL %d)", r.Name, plan.Domain, r.Value, r.TTL)
	case RecordCAA:
		fmt.Fprintf(&b, "Publish CAA: %s.%s -> %d %s %q (TTL %d)", r.Name, plan.Domain, r.Flag, r.Tag, r.Value, r.TTL)
	default:
		fmt.Fprintf(&b, "Publish %s: %s.%s -> %s", r.Type, r.Name, plan.Domain, r.Value)
	}
	return b.String()
}
