package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/dnsops"
)

// CloudflareConfig carries the Cloudflare credentials needed for
// the provider to attempt a real Plan / Apply. Tokens are passed in
// memory only — never serialised, never returned to a caller.
type CloudflareConfig struct {
	APIToken  string // Cloudflare API token (Bearer); required
	ZoneID    string // zone id for the apex domain; required
	AccountID string // optional; reserved for multi-zone ops
}

// HasCredentials returns true when the provider has enough config to
// even attempt a Plan. We do NOT make any HTTP calls; we just check
// that both fields are present and non-empty.
func (c CloudflareConfig) HasCredentials() bool {
	return strings.TrimSpace(c.APIToken) != "" && strings.TrimSpace(c.ZoneID) != ""
}

// CloudflareProvider implements dnsops.Provider against the
// Cloudflare DNS API. SECURITY POSTURE for this build:
//
//   - The provider's Plan() call is dry-run only and emits a
//     deterministic Change list WITHOUT calling the Cloudflare
//     API. We do not have confidence in the live API contract in
//     this build; an incorrect record write could overwrite a
//     customer's live zone.
//   - The provider's Apply() always returns Failed=len(plan.Steps)
//     with a "provider automation requires manual confirmation"
//     note. The interface is real and ready to be wired up to a
//     future build that includes signed requests against
//     api.cloudflare.com. For now, the dashboard's Apply button
//     shows the same "manual confirmation required" status that
//     the manual provider shows.
//
// This is the safe default. The next build can flip the readiness
// flag once we've audited the Cloudflare v4 API and tested against
// a sandbox zone.
type CloudflareProvider struct {
	cfg      CloudflareConfig
	resolver dnsops.Resolver
}

// NewCloudflareProvider returns a CloudflareProvider. The resolver
// is used only for the optional live DNS read; the provider does not
// call Cloudflare's API in this build.
func NewCloudflareProvider(cfg CloudflareConfig, r dnsops.Resolver) *CloudflareProvider {
	return &CloudflareProvider{cfg: cfg, resolver: r}
}

// Compile-time assertion: CloudflareProvider satisfies dnsops.Provider.
var _ dnsops.Provider = (*CloudflareProvider)(nil)

// Name implements dnsops.Provider.
func (p *CloudflareProvider) Name() string { return "cloudflare" }

// Plan implements dnsops.Provider. With no credentials, Plan
// returns an empty change list and a single explanatory Note so the
// dashboard can render "provider not configured". With credentials
// configured, Plan still emits a dry-run plan but flags every step
// as ActionCreate and stamps the same "manual confirmation required"
// reason that Apply uses — so the UI can show the provider name
// without pretending it can write.
func (p *CloudflareProvider) Plan(_ context.Context, plan *dnsops.Plan) (dnsops.ChangePlan, error) {
	if plan == nil {
		return dnsops.ChangePlan{}, fmt.Errorf("plan is nil")
	}
	if !p.cfg.HasCredentials() {
		return dnsops.ChangePlan{
			Provider: p.Name(),
			Domain:   plan.Domain,
			Notes:    []string{"provider not configured — set cloudflare_api_token and zone_id in server config to enable"},
			Steps:    nil,
		}, nil
	}
	out := dnsops.ChangePlan{
		Provider: p.Name(),
		Domain:   plan.Domain,
		Notes: []string{
			"Cloudflare credentials detected; dry-run plan emitted. Apply is intentionally disabled in this build and requires manual confirmation.",
		},
	}
	for _, r := range plan.Records {
		ch := dnsops.Change{Record: r, Action: dnsops.ActionCreate}
		switch r.Purpose {
		case dnsops.PurposePTR, dnsops.PurposeBIMI, dnsops.PurposeDANETLSA:
			ch.Action = dnsops.ActionSkip
			ch.Reason = "informational only — not a Cloudflare zone record"
		default:
			ch.Reason = "would create " + string(r.Type) + " " + r.Name + " (Cloudflare API not called in this build)"
		}
		out.Steps = append(out.Steps, ch)
	}
	return out, nil
}

// Apply implements dnsops.Provider. Cloudflare Apply is intentionally
// disabled in this build — we do NOT make any HTTP request. The
// Service.ApplyProvider handles audit logging via its own
// Confirmer; the provider itself never holds one.
func (p *CloudflareProvider) Apply(ctx context.Context, plan dnsops.ChangePlan, confirm string) (dnsops.ApplyResult, error) {
	if strings.TrimSpace(confirm) == "" {
		return dnsops.ApplyResult{}, fmt.Errorf("apply requires a non-empty confirmation")
	}
	if !p.cfg.HasCredentials() {
		return dnsops.ApplyResult{}, fmt.Errorf("cloudflare provider not configured")
	}
	_ = ctx
	res := dnsops.ApplyResult{
		Provider: p.Name(),
		Domain:   plan.Domain,
		Failed:   len(plan.Steps),
		Steps:    plan.Steps,
		Notes: []string{
			"cloudflare apply is intentionally disabled in this build; dry-run plan only",
			"set ORVIX_DNS_CLOUDFLARE_APPLY_ENABLED=true once the live API path is audited",
		},
	}
	return res, nil
}
