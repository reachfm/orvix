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
)
