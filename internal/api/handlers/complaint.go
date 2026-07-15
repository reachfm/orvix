package handlers

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/abuse"
	"github.com/orvix/orvix/internal/billing"
	"go.uber.org/zap"
)

const maxComplaintPayloadBytes = 1 << 20

func (h *Handler) ReceiveComplaintWebhook(c fiber.Ctx) error {
	if h.billingWebhook == nil || h.paymentProvider == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "complaint processing not configured"})
	}
	if h.abuseSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "abuse service not configured"})
	}

	timestamp := c.Get(paymentTimestampHeader)
	if timestamp == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing timestamp"})
	}
	signature := c.Get(paymentSignatureHeader)
	if signature == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing signature"})
	}

	rawPayload := c.Body()
	if len(rawPayload) == 0 || len(rawPayload) > maxComplaintPayloadBytes {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid payload"})
	}

	event, err := h.paymentProvider.VerifyWebhook(rawPayload, timestamp, signature)
	if err != nil {
		h.logger.Warn("complaint verification failed", zap.Error(err))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "verification failed"})
	}
	if event.ProviderEventID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing event id"})
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
			return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "already_processed"})
		}
		h.logger.Error("complaint event record failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "event recording failed"})
	}

	if event.ProviderSubID != "" && h.billingSvc != nil {
		if sub, subErr := h.billingSvc.GetSubscriptionByProviderID(event.ProviderSubID); subErr == nil {
			sig := &abuse.AbuseSignal{
				TenantID:    sub.TenantID,
				SignalType:  abuse.SignalSpamComplaint,
				Severity:    abuse.SeverityWarning,
				Description: "provider complaint event received: " + event.Type,
				DetectedAt:  time.Now().UTC(),
			}
			if err := h.abuseSvc.RecordSignal(c.Context(), sig); err != nil {
				h.logger.Error("complaint signal creation failed", zap.Error(err))
			}
		}
	}

	if err := h.billingWebhook.MarkProcessed(c.Context(), event.ProviderEventID, rec.Provider, nil); err != nil {
		h.logger.Error("complaint mark processed failed", zap.Error(err))
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "received", "event_id": event.ProviderEventID})
}
