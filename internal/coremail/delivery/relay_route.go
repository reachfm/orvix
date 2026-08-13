package delivery

import (
	"context"

	"github.com/orvix/orvix/internal/coremail/queue"
)

// RelayRoute is the delivery-path-facing shape of a routing decision.
// Deliberately a plain struct with primitive fields (no dependency on
// internal/platform/relay's types) — the delivery package is lower in
// the dependency graph and must not import a platform bounded
// context; the runtime wiring layer adapts relay.SelectedRoute into
// this shape.
type RelayRoute struct {
	Direct       bool
	ProviderID   uint
	ProviderName string
	Host         string
	Port         int
	// ConnSecurity mirrors relay.ConnSecurity's string values
	// ("none"/"starttls"/"implicit_tls") without importing the type.
	ConnSecurity string
	Username     string
	// Password is the DECRYPTED credential for this one delivery
	// attempt only — never logged, never persisted, discarded after
	// the dial. Supplied by the RelaySelector's Resolve step, which is
	// the only place decryption happens.
	Password string
}

// RelayRouteRequest carries the REAL routing context for one outbound
// message. It replaces the previous positional argument list, which forced
// callers to pass a bare sender DOMAIN into a parameter the relay service
// then treated as a full sender ADDRESS, and which had no way to carry the
// sending domain's id at all — so domain-scoped routing rules could never
// match and sender patterns could never be evaluated.
//
// Every field is sourced from the queue entry / mailbox record by the worker;
// none is inferred inside the relay service.
type RelayRouteRequest struct {
	TenantID uint
	// SenderAddress is the FULL envelope sender (local@domain), used for
	// sender-pattern matching.
	SenderAddress string
	// SenderDomain is the sending domain, used for domain-scoped matching
	// when no DomainID is known.
	SenderDomain string
	// DomainID is the sending domain's row id, used by domain-scoped rules.
	// Zero means "unknown", which must never be treated as "matches any".
	DomainID uint
	// SenderMailAccessMode is the sending identity's mail-access policy.
	SenderMailAccessMode string
	RecipientDomain      string
	// Seed drives deterministic weighted selection.
	Seed int64
}

// RelayRouteDecision is the outcome of routing. It exists so the worker can
// tell apart "policy says deliver direct" from "routing could not be
// determined" — the previous contract collapsed both into a nil/err pair
// that the worker treated as "fall through to direct MX", which silently
// downgraded mandatory relay routes to unauthenticated direct delivery.
type RelayRouteDecision struct {
	// Route is the selected relay route (or an explicit Direct route).
	Route *RelayRoute
	// Fallbacks are additional providers to try IN ORDER within this same
	// delivery attempt if Route fails. Ordered, deduplicated and
	// scope-checked by the relay service; the worker walks them verbatim.
	Fallbacks []RelayRoute
}

// RelaySelector is the narrow port the delivery worker depends on to
// decide, per outbound message, whether to relay (and through which
// provider) or deliver direct-to-MX — and to report back what
// happened, for circuit-breaker bookkeeping. A nil RelaySelector on
// DeliveryWorker preserves the exact pre-existing direct-to-MX
// behavior with zero code path change (see deliverRemote).
//
// FAIL-CLOSED CONTRACT: an error from SelectRoute is NEVER permission to
// deliver direct. The worker defers the message instead. Direct delivery
// happens only when SelectRoute succeeds AND the returned route is
// explicitly Direct.
type RelaySelector interface {
	SelectRoute(ctx context.Context, req RelayRouteRequest) (*RelayRouteDecision, error)
	// RecordAttemptResult persists circuit-breaker bookkeeping. It returns
	// an error so a persistence failure is visible to the caller rather
	// than silently discarded; the worker decides what a bookkeeping
	// failure means for an already-completed SMTP delivery.
	RecordAttemptResult(ctx context.Context, providerID uint, success bool) error
	// Deliver performs the actual authenticated SMTP dial to route's
	// provider. Implemented by the adapter (which alone imports
	// internal/platform/relay), so no relay-specific SMTP client code
	// — and no decrypted-credential handling — lives in this package.
	Deliver(ctx context.Context, route *RelayRoute, from string, to []string, data []byte) RelayDeliverResult
}

// RelayDeliverResult mirrors just enough of DeliveryResult for the
// worker to make its retry/temp-fail decision uniformly with direct
// delivery, without this package needing to know about relay.DeliverResult.
type RelayDeliverResult struct {
	Success  bool
	TempFail bool
	// Ambiguous is true when the message body was sent to the relay but
	// the final acceptance response was never received (timeout or
	// connection drop after DATA). The recipient MAY have received the
	// message; the delivery chain must NOT immediately re-send through a
	// fallback provider (F6).
	Ambiguous bool
	StatusMsg string
}

// SuppressionChecker is the deliverability control plane's real-path
// enforcement port (Milestone 9). nil disables the check entirely —
// preserving pre-existing behavior for any deployment that hasn't
// wired it.
type SuppressionChecker interface {
	IsSuppressed(ctx context.Context, tenantID uint, address string) (bool, error)
}

// DeliverabilityRecorder is the reputation-signal recording port. Like
// SuppressionChecker, nil disables recording without affecting
// delivery behavior.
type DeliverabilityRecorder interface {
	RecordOutcome(ctx context.Context, entry *queue.QueueEntry, tenantID uint, relayProviderName string, result *DeliveryResult, attemptNumber int)
}
