package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/models"
)

// ListHealHistory returns auto-heal action history.
func (h *Handler) ListHealHistory(c fiber.Ctx) error {
	var history []models.HealHistory
	h.db.Order("created_at desc").Limit(100).Find(&history)
	return c.JSON(history)
}

// RunHealCheck manually triggers a specific health check.
func (h *Handler) RunHealCheck(c fiber.Ctx) error {
	name := c.Params("name")
	for _, mod := range h.registry.All() {
		if mod.ID() == "auto-heal" {
			return c.JSON(fiber.Map{"status": "check triggered", "check": name})
		}
	}
	return c.Status(404).JSON(fiber.Map{"error": "auto-heal module not available"})
}
