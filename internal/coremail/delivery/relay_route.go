package delivery

import "context"

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

// RelaySelector is the narrow port the delivery worker depends on to
// decide, per outbound message, whether to relay (and through which
// provider) or deliver direct-to-MX — and to report back what
// happened, for circuit-breaker bookkeeping. A nil RelaySelector on
// DeliveryWorker preserves the exact pre-existing direct-to-MX
// behavior with zero code path change (see deliverRemote).
type RelaySelector interface {
	SelectRoute(ctx context.Context, tenantID uint, senderDomain, senderMailAccessMode, recipientDomain string, seed int64) (*RelayRoute, error)
	RecordAttemptResult(ctx context.Context, providerID uint, success bool)
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
	Success   bool
	TempFail  bool
	StatusMsg string
}
