package middleware

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
)

// ExtractRole retrieves the role from Fiber locals set by AuthRequiredMiddleware.
func ExtractRole(c *fiber.Ctx) string {
	role, _ := c.Locals("role").(string)
	return role
}

// ExtractTenantID retrieves the tenant_id from Fiber locals set by AuthRequiredMiddleware.
func ExtractTenantID(c *fiber.Ctx) uint {
	tid, _ := c.Locals("tenant_id").(uint)
	return tid
}

// RequireSuperAdmin checks that the authenticated user has role "super_admin".
// Sets is_super_admin = true on the context for downstream handlers.
// Returns 403 Forbidden if the user lacks this role.
func RequireSuperAdmin() fiber.Handler {
	return func(c *fiber.Ctx) error {
		role := ExtractRole(c)
		if role != "super_admin" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":      "super_admin role required",
				"role":       role,
				"request_id": c.Locals("request_id"),
			})
		}
		c.Locals("is_super_admin", true)
		return c.Next()
	}
}

// RequireTenantAdmin checks that the authenticated user has role "super_admin" or "tenant_admin".
// - super_admin: can pass a ?tenant_id=N query param to act on a specific tenant.
//                If no query param, tenant_context is nil (platform-wide scope).
// - tenant_admin: tenant_context is locked to their own tenant_id.
// Returns 403 Forbidden if the user lacks either role.
func RequireTenantAdmin() fiber.Handler {
	return func(c *fiber.Ctx) error {
		role := ExtractRole(c)
		tenantID := ExtractTenantID(c)

		switch role {
		case "super_admin":
			c.Locals("is_super_admin", true)
			queryTID := c.QueryInt("tenant_id", 0)
			if queryTID > 0 {
				c.Locals("tenant_context", uint(queryTID))
			} else {
				c.Locals("tenant_context", nil)
			}
			return c.Next()

		case "tenant_admin":
			if tenantID == 0 {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error":      "tenant_admin has no tenant_id assigned",
					"request_id": c.Locals("request_id"),
				})
			}
			c.Locals("tenant_context", tenantID)
			return c.Next()

		default:
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":      "tenant_admin or super_admin role required",
				"role":       role,
				"request_id": c.Locals("request_id"),
			})
		}
	}
}

// RequireAdmin is a convenience alias for RequireSuperAdmin.
func RequireAdmin() fiber.Handler {
	return RequireSuperAdmin()
}

// GetEffectiveTenant returns the tenant_context from the context, or 0 if nil.
// Handlers use this to know which tenant to scope queries to.
func GetEffectiveTenant(c *fiber.Ctx) uint {
	tc := c.Locals("tenant_context")
	if tc == nil {
		return 0
	}
	tid, ok := tc.(uint)
	if !ok {
		return 0
	}
	return tid
}

// Errorf is a simple helper for consistent forbidden JSON responses.
func Errorf(c *fiber.Ctx, msg string, args ...interface{}) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error":      fmt.Sprintf(msg, args...),
		"request_id": c.Locals("request_id"),
	})
}
