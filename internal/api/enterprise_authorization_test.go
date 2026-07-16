package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
)

func TestEnterpriseReadRequiresTenantContext(t *testing.T) {
	app := fiber.New()
	requireTenantCtx := func(c fiber.Ctx) error {
		if _, ok := c.Locals("tenant_id").(uint); !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
		}
		return c.Next()
	}
	mockCSRF := func(c fiber.Ctx) error { return c.Next() }

	enterpriseRead := app.Group("/enterprise", requireTenantCtx, mockCSRF)
	enterpriseRead.Get("/dashboard", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true})
	})

	// No tenant_id in context → 403.
	req := httptest.NewRequest("GET", "/enterprise/dashboard", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("no tenant context: expected 403, got %d", resp.StatusCode)
	}
}

func TestEnterpriseReadAllowsRoleUser(t *testing.T) {
	app := fiber.New()
	requireTenantCtx := func(c fiber.Ctx) error {
		if _, ok := c.Locals("tenant_id").(uint); !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
		}
		return c.Next()
	}
	mockCSRF := func(c fiber.Ctx) error { return c.Next() }

	// Set user context on the app level before the enterprise group.
	app.Use(func(c fiber.Ctx) error {
		c.Locals("role", auth.RoleUser)
		c.Locals("tenant_id", uint(1))
		c.Locals("user_id", uint(1))
		return c.Next()
	})

	enterpriseRead := app.Group("/enterprise", requireTenantCtx, mockCSRF)
	enterpriseRead.Get("/dashboard", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true})
	})

	req := httptest.NewRequest("GET", "/enterprise/dashboard", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("RoleUser with tenant context: expected 200, got %d", resp.StatusCode)
	}
}

func TestEnterpriseReadAllowsRoleReadOnly(t *testing.T) {
	app := fiber.New()
	requireTenantCtx := func(c fiber.Ctx) error {
		if _, ok := c.Locals("tenant_id").(uint); !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
		}
		return c.Next()
	}
	mockCSRF := func(c fiber.Ctx) error { return c.Next() }

	app.Use(func(c fiber.Ctx) error {
		c.Locals("role", auth.RoleReadOnly)
		c.Locals("tenant_id", uint(1))
		c.Locals("user_id", uint(1))
		return c.Next()
	})

	enterpriseRead := app.Group("/enterprise", requireTenantCtx, mockCSRF)
	enterpriseRead.Get("/audit/logs", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true})
	})

	req := httptest.NewRequest("GET", "/enterprise/audit/logs", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("RoleReadOnly with tenant context: expected 200, got %d", resp.StatusCode)
	}
}
