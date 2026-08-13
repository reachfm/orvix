package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/trustmgmt"
	"go.uber.org/zap"
)

// H-6 authentication-abuse integration helpers.
//
// These functions are the single place where the login flow talks to the
// trust/lockout engine and the authentication limiter, so every credential
// endpoint shares one behaviour:
//
//   - A locked account/IP is rejected with the SAME 401 as a wrong password
//     and unknown account, after spending one real verify so the response is
//     timing-indistinguishable from a wrong-password rejection (the endpoint
//     must not be an oracle for lockout state).
//   - Failed verification (unknown account, wrong password) accumulates
//     failures on the account, IP and pair keys in the trust engine, so a
//     distributed spray is eventually locked out regardless of source.
//   - A genuine success clears the account/pair lockout state and the
//     limiter's account/pair budgets, but NEVER the IP budget — otherwise a
//     valid account on a shared address would let an attacker reset the IP
//     counter at will and keep spraying other accounts from the same host.
//
// Endpoints that reject a request for POLICY reasons (MFA verification
// failures, allow_webmail=false, disabled mailbox) do NOT touch the trust
// engine: the account is not being credential-sprayed, and locking a legit
// owner out because of a policy state they cannot change is an availability
// failure. Each of those paths is bounded by its own mechanism (per-challenge
// attempt cap, IP budget).

// SetAuthLimiter wires the multi-dimensional authentication limiter into the
// handler for success-path resets (credential endpoints mount it via the
// router's authThrottle/authThrottleIP middleware).
func (h *Handler) SetAuthLimiter(l *auth.AuthLimiter) {
	h.authLimiter = l
}

// lockoutKeys derives the canonical lockout key set for an attempt.
func (h *Handler) lockoutKeys(email string, ip string) trustmgmt.LockoutKeys {
	return trustmgmt.AuthKeys(email, ip)
}

// denyIfLockedOut reports whether any lockout key for this attempt is
// currently locked. When it returns true the caller MUST respond with the
// generic 401 "invalid credentials" shape and MUST NOT proceed with password
// verification, token issuance, or any account-specific response.
//
// Timing equalization: one real Argon2id verify is spent against the same
// hash a wrong-password attempt would spend, so the locked path and the
// wrong-password path are indistinguishable by response time. The dummy
// password is a constant — the cost is determined by the hash parameters,
// not by the candidate.
func (h *Handler) denyIfLockedOut(c fiber.Ctx, email, passwordHash string) bool {
	if h.trustService == nil {
		return false
	}
	keys := h.lockoutKeys(email, c.IP())
	if !h.trustService.IsAnyLockedOut(c.Context(), keys) {
		return false
	}
	// Timing equalization against the known-account wrong-password path.
	auth.VerifyPasswordWithRehash("orvix-lockout-dummy-password", passwordHash)
	h.logger.Warn("login attempt denied by trust lockout",
		zap.String("email", email),
		zap.String("ip", c.IP()))
	return true
}

// recordLoginFailure records a credential failure in the security monitor AND
// the trust engine. Call it for unknown-account and wrong-password outcomes
// only (see the policy-denial note at the top of this file).
func (h *Handler) recordLoginFailure(c fiber.Ctx, email string) {
	h.security.RecordFailedLogin(c.Context(), c.IP(), email)
	if h.trustService != nil {
		h.trustService.RecordAuthFailureFor(c.Context(), h.lockoutKeys(email, c.IP()))
	}
}

// recordLoginSuccess records a genuine login: security monitor, trust-engine
// success clear, and account/budget reset for the limiter.
func (h *Handler) recordLoginSuccess(c fiber.Ctx, email string) {
	h.security.RecordSuccessfulLogin(c.IP())
	if h.trustService != nil {
		h.trustService.RecordAuthSuccessFor(c.Context(), h.lockoutKeys(email, c.IP()))
	}
	if h.authLimiter != nil {
		h.authLimiter.ResetAccount(c.Context(), c.IP(), email)
	}
}
