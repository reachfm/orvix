package relay

import "errors"

var (
	ErrProviderNotFound    = errors.New("relay provider not found")
	ErrPoolNotFound        = errors.New("relay pool not found")
	ErrNoRouteAvailable    = errors.New("no relay provider available")
	ErrInvalidConnSecurity = errors.New("invalid connection security mode")
	ErrVersionConflict     = errors.New("relay resource was modified concurrently")
	ErrRateLimited         = errors.New("relay provider rate limit exceeded")
	ErrPolicyBlocked       = errors.New("mail access policy blocks this route")
	ErrOverrideExpired     = errors.New("emergency override has expired")
	ErrUnsafeTarget        = errors.New("unsafe relay target")
	// ErrCredentialUnavailable is returned when a provider's stored
	// credential cannot be decrypted. The generic message never carries the
	// ciphertext, key path, or any part of the secret.
	ErrCredentialUnavailable = errors.New("relay credential unavailable")
	// ErrProviderUnavailable signals an INFRASTRUCTURE failure while
	// resolving a route (repository/store error). It is retryable and must
	// never be interpreted as permission to deliver direct-to-MX.
	ErrProviderUnavailable = errors.New("relay provider lookup unavailable")
	// ErrCrossTenantProvider signals that a routing rule referenced a
	// provider owned by a different tenant. Configuration integrity failure;
	// permanent, and never a direct-delivery downgrade.
	ErrCrossTenantProvider = errors.New("relay provider belongs to another tenant")
	// ErrCrossTenantPool signals that a provider, rule, or override
	// referenced a pool owned by a different tenant — or that a tenant
	// attempted to attach a provider to a platform-global pool (default
	// deny). Configuration integrity failure; permanent.
	ErrCrossTenantPool = errors.New("relay pool belongs to another tenant")
	// ErrGlobalPoolRequiresPlatform signals a tenant attempting to create
	// or use a platform-global pool. Platform-managed resources are
	// created through the platform surface only.
	ErrGlobalPoolRequiresPlatform = errors.New("global relay pools are platform-managed")
	// ErrInsecureCredentialTransport signals that a provider is configured to
	// send SMTP AUTH over a channel that is not a verified TLS session
	// (plaintext, or opportunistic/unvalidated TLS). The credential is
	// refused BEFORE decryption, so the secret is never materialized.
	ErrInsecureCredentialTransport = errors.New("relay credential requires verified TLS")
	// ErrOverrideNotFound is returned when an emergency override cannot be
	// revoked: absent, already inactive, or owned by another tenant. All three
	// are refusals rather than silent successes.
	ErrOverrideNotFound  = errors.New("emergency override not found or already inactive")
	ErrNameRequired      = errors.New("relay name is required")
	ErrRelayNameConflict = errors.New("a relay with this name already exists in the same scope")
)

// IsRetryableRouteError reports whether a routing failure is TEMPORARY
// (infrastructure) rather than permanent (configuration/policy). The delivery
// worker uses this to choose deferral vs. bounce. Critically, neither answer
// permits a downgrade to direct-to-MX delivery.
func IsRetryableRouteError(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrProviderUnavailable),
		errors.Is(err, ErrCredentialUnavailable),
		errors.Is(err, ErrRateLimited),
		errors.Is(err, ErrNoRouteAvailable):
		return true
	default:
		// Configuration and policy failures (unknown provider, cross-tenant
		// reference, unsafe target, insecure credential transport, blocked by
		// policy) will not fix themselves on retry.
		return false
	}
}
