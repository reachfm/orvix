package middleware

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/supportaccess"
)

// SupportAccessContext is stored in fiber Locals for downstream handlers.
type SupportAccessContext struct {
	Grant      *supportaccess.AccessGrant
	OperatorID uint
	TenantID   uint
	Scopes     []string
}

// SupportAccess returns middleware that enforces a valid, active,
// unexpired support-access grant for the target tenant before the
// request reaches a tenant-scoped handler.
//
// The support operator authenticates with their normal platform
// credentials. The target tenant is supplied via the
// X-Support-Tenant-ID header. The middleware resolves the active
// grant and fails closed if it is missing, expired, revoked, or
// cross-tenant.
func SupportAccess(svc *supportaccess.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		actor, ok := auth.ActorFromCtx(c)
		if !ok || actor.UserID == 0 {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "authentication required",
			})
		}
		tenantID, err := requireSupportTenant(c)
		if err != nil {
			return err
		}
		grant, err := svc.GrantForOperator(c.Context(), actor.UserID, tenantID)
		if err != nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "no active support access grant for this tenant",
			})
		}
		if err := svc.ValidateAccess(c.Context(), tenantID); err != nil {
			switch err {
			case supportaccess.ErrExpired:
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error": "support access has expired",
				})
			case supportaccess.ErrRevoked:
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error": "support access has been revoked",
				})
			default:
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error": "support access is not active",
				})
			}
		}
		c.Locals("support_access", &SupportAccessContext{
			Grant:      grant,
			OperatorID: actor.UserID,
			TenantID:   tenantID,
			Scopes:     grant.Scopes(),
		})
		return c.Next()
	}
}

// requireSupportTenant parses and validates the target tenant header.
func requireSupportTenant(c fiber.Ctx) (uint, error) {
	raw := c.Get("X-Support-Tenant-ID")
	if raw == "" {
		return 0, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "X-Support-Tenant-ID header is required",
		})
	}
	var tenantID uint
	if _, err := fmt.Sscanf(raw, "%d", &tenantID); err != nil || tenantID == 0 {
		return 0, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid X-Support-Tenant-ID",
		})
	}
	return tenantID, nil
}

// HasScope reports whether the current support context includes the
// given scope. Returns false if there is no support context.
func HasScope(c fiber.Ctx, scope string) bool {
	ctx, ok := c.Locals("support_access").(*SupportAccessContext)
	if !ok || ctx == nil {
		return false
	}
	for _, s := range ctx.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// RequireScope returns middleware that enforces a specific support
// scope. It must run after SupportAccess.
func RequireScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !HasScope(c, scope) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "support scope " + scope + " is required",
			})
		}
		return c.Next()
	}
}

// SupportContext returns the support access context if present.
func SupportContext(c fiber.Ctx) (*SupportAccessContext, bool) {
	ctx, ok := c.Locals("support_access").(*SupportAccessContext)
	return ctx, ok
}
