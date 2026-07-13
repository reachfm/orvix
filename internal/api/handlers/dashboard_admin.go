package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
)

func (h *Handler) CustomerDashboard(c fiber.Ctx) error {
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(err.(*fiber.Error).Code).JSON(fiber.Map{"error": err.Error()})
	}

	d, err := h.dashboardSvc.CustomerDashboard(c.Context(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(d)
}

func (h *Handler) PlatformDashboard(c fiber.Ctx) error {
	d, err := h.dashboardSvc.PlatformDashboard(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(d)
}
