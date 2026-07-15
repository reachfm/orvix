package handlers

import (
	"errors"
	"strconv"

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
	tenantID, ok := c.Locals("tenant_id").(uint)
	if !ok || tenantID == 0 {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	operatorID, _ := c.Locals("user_id").(uint)
	idStr := c.Params("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid signal id"})
	}
	if err := h.abuseSvc.AcknowledgeSignal(c.Context(), tenantID, uint(id), operatorID); err != nil {
		if errors.Is(err, abuse.ErrSignalNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "signal not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "acknowledged"})
}

func (h *Handler) ResolveAbuseSignal(c fiber.Ctx) error {
	if h.abuseSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "abuse service not available"})
	}
	tenantID, ok := c.Locals("tenant_id").(uint)
	if !ok || tenantID == 0 {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	operatorID, _ := c.Locals("user_id").(uint)
	idStr := c.Params("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid signal id"})
	}
	if err := h.abuseSvc.ResolveSignal(c.Context(), tenantID, uint(id), operatorID); err != nil {
		if errors.Is(err, abuse.ErrSignalNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "signal not found"})
		}
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
