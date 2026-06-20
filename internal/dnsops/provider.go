package dnsops

import "context"

// Provider turns a Plan into a ChangePlan. The Provider does not
// perform DNS verification — that is the Verifier's job. The Provider
// may (and for non-manual providers, must) read live provider-side
// records to decide create / update / skip / conflict.
//
// Security contract:
//   - The Provider NEVER returns a provider token (api_key, token,
//     secret) to the caller. Tokens are read from server config only.
//   - The Provider NEVER logs raw provider responses — error
//     messages are sanitised to a single line with no token-shaped
//     substrings.
//   - Apply requires an explicit confirmation token (any non-empty
//     string supplied by the caller via Apply(ctx, plan, confirm)).
//     Providers that cannot reach an external API (e.g. the manual
//     provider) return an ApplyResult with Failed=len(plan.Steps)
//     and a single explanatory Note. This is the only safe
//     behaviour — silently "succeeding" would be worse than
//     refusing.
type Provider interface {
	// Name is a short identifier shown in the UI and audit logs.
	Name() string

	// Plan returns what the provider would do, given the desired
	// plan and (optionally) the live state the provider can read.
	// Plan must be safe to call repeatedly (idempotent).
	Plan(ctx context.Context, plan *Plan) (ChangePlan, error)

	// Apply executes the change plan. confirm is the operator-
	// supplied confirmation string. The manual provider always
	// returns a non-empty Failed count because it does not reach
	// an external API.
	Apply(ctx context.Context, plan ChangePlan, confirm string) (ApplyResult, error)
}

// Confirmer is the audit-log sink for the Service layer (the
// runtime wires this to the existing audit.Store; tests can plug a
// no-op or in-memory impl). Providers themselves do not depend on
// a Confirmer — the Service.ApplyProvider records the attempt.
type Confirmer interface {
	Log(action, target string)
}
