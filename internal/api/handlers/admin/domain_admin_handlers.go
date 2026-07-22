package admin_handlers

import (
	"github.com/gofiber/fiber/v2"
)

// AddTenantDomain registers a new domain under a specific tenant.
// Tenant Admin (own tenant) or Super Admin (any tenant).
func AddTenantDomain(c *fiber.Ctx) error {
	return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
		"error": "AddTenantDomain not yet implemented",
	})
}

// VerifyDomain triggers a domain verification check (DNS records).
// Tenant Admin (own tenant) or Super Admin (any tenant).
func VerifyDomain(c *fiber.Ctx) error {
	return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
		"error": "VerifyDomain not yet implemented",
	})
}
