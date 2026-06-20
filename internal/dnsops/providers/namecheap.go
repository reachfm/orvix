package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/dnsops"
)

// NamecheapConfig carries the Namecheap credentials. The standard
// sandbox/production pair is APIUser + APIKey; the sandbox adds a
// ClientIP requirement that we surface as a config knob.
//
// As with the Cloudflare provider, Apply is intentionally disabled
// in this build. The Plan path emits a dry-run list without making
// any HTTP request.
type NamecheapConfig struct {
	APIUser  string
	APIKey   string
	Username string
	ClientIP string // required for sandbox; production accepts empty
	Sandbox  bool
}

// HasCredentials returns true if the provider has enough config to
// emit a Plan. Apply() refuses regardless because live API writes
// are out of scope for this build.
func (c NamecheapConfig) HasCredentials() bool {
	return strings.TrimSpace(c.APIUser) != "" &&
		strings.TrimSpace(c.APIKey) != "" &&
		strings.TrimSpace(c.Username) != ""
}

// NamecheapProvider implements dnsops.Provider against the Namecheap
// API. Same posture as CloudflareProvider: Plan emits a dry-run
// change list with ActionCreate / ActionSkip; Apply always refuses.
type NamecheapProvider struct {
	cfg      NamecheapConfig
	resolver dnsops.Resolver
}

// NewNamecheapProvider returns a NamecheapProvider. Resolver is used
// only for the optional live read.
func NewNamecheapProvider(cfg NamecheapConfig, r dnsops.Resolver) *NamecheapProvider {
	return &NamecheapProvider{cfg: cfg, resolver: r}
}

// Compile-time assertion: NamecheapProvider satisfies dnsops.Provider.
var _ dnsops.Provider = (*NamecheapProvider)(nil)

// Name implements dnsops.Provider.
func (p *NamecheapProvider) Name() string { return "namecheap" }

// Plan implements dnsops.Provider.
func (p *NamecheapProvider) Plan(_ context.Context, plan *dnsops.Plan) (dnsops.ChangePlan, error) {
	if plan == nil {
		return dnsops.ChangePlan{}, fmt.Errorf("plan is nil")
	}
	if !p.cfg.HasCredentials() {
		return dnsops.ChangePlan{
			Provider: p.Name(),
			Domain:   plan.Domain,
			Notes:    []string{"provider not configured — set namecheap api_user / api_key / username in server config to enable"},
			Steps:    nil,
		}, nil
	}
	mode := "production"
	if p.cfg.Sandbox {
		mode = "sandbox"
	}
	out := dnsops.ChangePlan{
		Provider: p.Name(),
		Domain:   plan.Domain,
		Notes: []string{
			fmt.Sprintf("Namecheap credentials detected (%s mode); dry-run plan emitted. Apply is intentionally disabled in this build and requires manual confirmation.", mode),
		},
	}
	for _, r := range plan.Records {
		ch := dnsops.Change{Record: r, Action: dnsops.ActionCreate}
		switch r.Purpose {
		case dnsops.PurposePTR, dnsops.PurposeBIMI, dnsops.PurposeDANETLSA:
			ch.Action = dnsops.ActionSkip
			ch.Reason = "informational only — not a Namecheap zone record"
		default:
			ch.Reason = "would create " + string(r.Type) + " " + r.Name + " (Namecheap API not called in this build)"
		}
		out.Steps = append(out.Steps, ch)
	}
	return out, nil
}

// Apply implements dnsops.Provider. Apply always refuses in this
// build; see CloudflareProvider.Apply for the rationale.
func (p *NamecheapProvider) Apply(_ context.Context, plan dnsops.ChangePlan, confirm string) (dnsops.ApplyResult, error) {
	if strings.TrimSpace(confirm) == "" {
		return dnsops.ApplyResult{}, fmt.Errorf("apply requires a non-empty confirmation")
	}
	if !p.cfg.HasCredentials() {
		return dnsops.ApplyResult{}, fmt.Errorf("namecheap provider not configured")
	}
	return dnsops.ApplyResult{
		Provider: p.Name(),
		Domain:   plan.Domain,
		Failed:   len(plan.Steps),
		Steps:    plan.Steps,
		Notes: []string{
			"namecheap apply is intentionally disabled in this build; dry-run plan only",
		},
	}, nil
}
