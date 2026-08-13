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
	ErrNameRequired          = errors.New("relay name is required")
	ErrRelayNameConflict     = errors.New("a relay with this name already exists in the same scope")
)
