package auth

import (
	"encoding/json"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// AccountExtractor pulls the account identifier a request is targeting, so the
// account and combo dimensions can be enforced. It returns "" when the
// endpoint carries no account (MFA verification), leaving only the IP budget.
type AccountExtractor func(c fiber.Ctx) string

// CredentialAccountFromBody reads the account identifier from a JSON login /
// signup / password-reset body.
//
// Only identifier fields are ever read. The password (and any other secret in
// the body) is never touched, never logged, and never becomes part of a key.
// A malformed body yields "" — the request still gets the IP budget, and the
// handler will reject it on its own terms.
func CredentialAccountFromBody(c fiber.Ctx) string {
	body := c.Body()
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Email    string `json:"email"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if payload.Username != "" {
		return payload.Username
	}
	return payload.Email
}

// NoAccount is the extractor for endpoints with no account identifier.
func NoAccount(fiber.Ctx) string { return "" }

// AuthLimitMiddleware enforces the multi-dimensional budget on a credential
// endpoint.
//
// The client address comes from c.IP(). That is the ONLY supported source:
// Fiber resolves it from the configured trusted-proxy model (walking the
// X-Forwarded-For chain right-to-left, skipping trusted hops, and returning
// the first untrusted address; falling back to the direct peer when the
// immediate peer is not a trusted proxy). Reading a forwarding header directly
// here would reintroduce exactly the spoofing bypass that model exists to
// prevent.
func AuthLimitMiddleware(limiter *AuthLimiter, extract AccountExtractor, logger *zap.Logger) fiber.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if extract == nil {
		extract = NoAccount
	}
	return func(c fiber.Ctx) error {
		if limiter == nil {
			return c.Next()
		}
		account := extract(c)
		decision := limiter.Check(c.Context(), c.IP(), account)
		if decision.Allowed {
			return c.Next()
		}

		// The dimension that tripped is logged for operators but never
		// returned: "your account is rate limited" versus "your IP is rate
		// limited" tells an attacker whether the account they guessed is
		// real and under attack. One response shape for every dimension.
		logger.Warn("authentication attempt throttled",
			zap.String("dimension", decision.Dimension),
			zap.Bool("degraded", decision.Degraded),
			zap.String("path", c.Path()))

		retry := int(decision.RetryAfter.Seconds())
		c.Set("Retry-After", strconv.Itoa(retry))
		c.Set("Cache-Control", "no-store")
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "too many attempts, try again later",
			"code":  "rate_limited",
		})
	}
}
