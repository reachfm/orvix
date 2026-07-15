package handlers

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/billing"
	"go.uber.org/zap"
)

const maxWebhookPayloadBytes = 1 << 20 // 1 MB
const paymentTimestampHeader = "X-Payment-Timestamp"
const paymentSignatureHeader = "X-Payment-Signature"

func (h *Handler) ReceivePaymentWebhook(c fiber.Ctx) error {
	if h.billingWebhook == nil || h.paymentProvider == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "payment processing not configured"})
	}

	timestamp := c.Get(paymentTimestampHeader)
	if timestamp == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing payment timestamp"})
	}
	signature := c.Get(paymentSignatureHeader)
	if signature == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing payment signature"})
	}

	rawPayload := c.Body()
	if len(rawPayload) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "empty payload"})
	}
	if len(rawPayload) > maxWebhookPayloadBytes {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "payload too large"})
	}

	event, err := h.paymentProvider.VerifyWebhook(rawPayload, timestamp, signature)
	if err != nil {
		h.logger.Warn("webhook verification failed", zap.Error(err))
		if errors.Is(err, billing.ErrWebhookInvalid) || errors.Is(err, billing.ErrWebhookSignatureInvalid) || errors.Is(err, billing.ErrWebhookTimestampMalformed) || errors.Is(err, billing.ErrWebhookTimestampExpired) || errors.Is(err, billing.ErrNoProviderConfigured) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid signature"})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "webhook verification failed"})
	}
	if event.ProviderEventID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing provider event id"})
	}

	rec := &billing.WebhookEventRecord{
		ID:             event.ProviderEventID,
		Provider:       "stripe",
		EventType:      event.Type,
		ProviderSubID:  event.ProviderSubID,
		RawPayload:     rawPayload,
		Signature:      signature,
		ReceivedAt:     time.Now().UTC(),
		IdempotencyKey: event.ProviderEventID,
	}

	if err := h.billingWebhook.RecordEvent(c.Context(), rec); err != nil {
		if errors.Is(err, billing.ErrWebhookAlreadyProcessed) {
			return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "already_processed", "event_id": event.ProviderEventID})
		}
		h.logger.Error("webhook record failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "event recording failed"})
	}

	if event.PaymentStatus == "paid" && event.SubscriptionStatus == "active" && event.ProviderSubID != "" && h.billingSvc != nil {
		if sub, subErr := h.billingSvc.GetSubscriptionByProviderID(event.ProviderSubID); subErr == nil {
			h.billingSvc.TransitionState(sub.TenantID, billing.SubActive)
		}
	}

	processingErr := h.billingWebhook.MarkProcessed(c.Context(), event.ProviderEventID, nil)
	if processingErr != nil {
		h.logger.Error("webhook mark processed failed", zap.Error(processingErr))
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "received", "event_id": event.ProviderEventID})
}
