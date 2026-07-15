package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/billing"
	"go.uber.org/zap"
)

func (h *Handler) ReceivePaymentWebhook(c fiber.Ctx) error {
	if h.billingWebhook == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webhook service not available"})
	}

	eventType := c.Get("X-Webhook-Event")
	if eventType == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing webhook event type"})
	}

	rawPayload := c.Body()
	if len(rawPayload) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "empty payload"})
	}

	b := make([]byte, 8)
	rand.Read(b)
	eventID := hex.EncodeToString(b)

	rec := &billing.WebhookEventRecord{
		ID:         eventID,
		Provider:   "stripe",
		EventType:  eventType,
		RawPayload: rawPayload,
		ReceivedAt: time.Now().UTC(),
	}

	if err := h.billingWebhook.RecordEvent(c.Context(), rec); err != nil {
		h.logger.Error("webhook record failed", zap.Error(err))
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "event already processed or recording failed"})
	}

	if err := h.billingWebhook.MarkProcessed(c.Context(), eventID, nil); err != nil {
		h.logger.Error("webhook mark processed failed", zap.Error(err))
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "received", "event_id": eventID})
}
