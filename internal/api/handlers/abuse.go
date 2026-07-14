package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/abuse"
)

func (h *Handler) ListAbuseSignals(c fiber.Ctx) error {
	if h.abuseSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "abuse service not available"})
	}
	tenantID := c.Locals("tenant_id").(uint)
	signals, err := h.abuseSvc.ListActiveSignals(c.Context(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if signals == nil {
		signals = []abuse.AbuseSignal{}
	}
	return c.JSON(signals)
}

func (h *Handler) AcknowledgeAbuseSignal(c fiber.Ctx) error {
	if h.abuseSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "abuse service not available"})
	}
	var id uint
	c.Bind().URI(&struct {
		ID uint `uri:"id"`
	}{})
	if id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}
	if err := h.abuseSvc.AcknowledgeSignal(c.Context(), id); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "acknowledged"})
}

func (h *Handler) ResolveAbuseSignal(c fiber.Ctx) error {
	if h.abuseSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "abuse service not available"})
	}
	var req struct {
		ID uint `json:"id"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	userID := c.Locals("user_id").(uint)
	if err := h.abuseSvc.ResolveSignal(c.Context(), req.ID, userID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "resolved"})
}

func (h *Handler) CheckSendLimit(c fiber.Ctx) error {
	if h.rateLimitSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "rate limit service not available"})
	}
	tenantID := c.Locals("tenant_id").(uint)
	bucket, err := h.rateLimitSvc.CheckSendLimit(c.Context(), tenantID, 0)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(bucket)
}
