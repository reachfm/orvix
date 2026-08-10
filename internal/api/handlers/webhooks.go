package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/webhooks"
)

type webhookSubscriptionRequest struct {
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Active  *bool    `json:"active,omitempty"`
	Version int      `json:"version,omitempty"`
}

func (h *Handler) webhookService() *webhooks.Service {
	if h.webhookSvc != nil {
		return h.webhookSvc
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return nil
	}
	svc := webhooks.NewService(webhooks.NewRepository(sqlDB), nil)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		return nil
	}
	h.webhookSvc = svc
	return svc
}

func webhookTenantID(c fiber.Ctx) (uint, error) { return auth.RequireTenantID(c) }

func webhookID(c fiber.Ctx, name string) (uint, error) {
	id, err := strconv.ParseUint(c.Params(name), 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return uint(id), nil
}

func webhookError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, webhooks.ErrNotFound):
		return c.Status(404).JSON(fiber.Map{"error": "webhook resource not found", "code": "WEBHOOK_NOT_FOUND"})
	case errors.Is(err, webhooks.ErrInvalidURL), errors.Is(err, webhooks.ErrInvalidEvent), errors.Is(err, webhooks.ErrInvalidStatus), errors.Is(err, webhooks.ErrTenantRequired):
		return c.Status(422).JSON(fiber.Map{"error": "invalid webhook configuration", "code": "WEBHOOK_VALIDATION_FAILED"})
	case strings.Contains(err.Error(), "concurrent modification"):
		return c.Status(409).JSON(fiber.Map{"error": "webhook subscription changed; reload and retry", "code": "WEBHOOK_VERSION_CONFLICT"})
	case strings.Contains(err.Error(), "not replayable"):
		return c.Status(409).JSON(fiber.Map{"error": "delivery is not replayable", "code": "WEBHOOK_NOT_REPLAYABLE"})
	default:
		return c.Status(500).JSON(fiber.Map{"error": "webhook operation failed", "code": "WEBHOOK_INTERNAL_ERROR"})
	}
}

func (h *Handler) requireWebhookService(c fiber.Ctx) (*webhooks.Service, error) {
	svc := h.webhookService()
	if svc == nil {
		return nil, c.Status(503).JSON(fiber.Map{"error": "webhook service unavailable", "code": "WEBHOOK_UNAVAILABLE"})
	}
	return svc, nil
}

func (h *Handler) CreateWebhookSubscription(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	var req webhookSubscriptionRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	sub, secret, err := svc.CreateSubscriptionWithSecret(c.Context(), tenantID, webhooks.ScopeTenant, req.URL, req.Events, nil)
	if err != nil {
		return webhookError(c, err)
	}
	h.writeAudit(c, "webhook.subscription.create", fmt.Sprintf("subscription:%d", sub.ID))
	return c.Status(201).JSON(fiber.Map{"subscription": sub, "secret": secret})
}

func (h *Handler) ListWebhookSubscriptions(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	subs, err := svc.ListSubscriptions(c.Context(), tenantID, string(webhooks.ScopeTenant), c.Query("active") == "true")
	if err != nil {
		return webhookError(c, err)
	}
	return c.JSON(fiber.Map{"subscriptions": subs})
}

func (h *Handler) GetWebhookSubscription(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid subscription id"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	sub, err := svc.GetSubscriptionForTenant(c.Context(), id, tenantID)
	if err != nil {
		return webhookError(c, err)
	}
	return c.JSON(fiber.Map{"subscription": sub})
}

func (h *Handler) UpdateWebhookSubscription(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid subscription id"})
	}
	var req webhookSubscriptionRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	sub, err := svc.UpdateSubscription(c.Context(), id, tenantID, req.URL, req.Events, req.Active, req.Version)
	if err != nil {
		return webhookError(c, err)
	}
	h.writeAudit(c, "webhook.subscription.update", fmt.Sprintf("subscription:%d", id))
	return c.JSON(fiber.Map{"subscription": sub})
}

func (h *Handler) DisableWebhookSubscription(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid subscription id"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	sub, err := svc.Disable(c.Context(), id, tenantID)
	if err != nil {
		return webhookError(c, err)
	}
	h.writeAudit(c, "webhook.subscription.disable", fmt.Sprintf("subscription:%d", id))
	return c.JSON(fiber.Map{"subscription": sub})
}

func (h *Handler) DeleteWebhookSubscription(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid subscription id"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	if err := svc.Delete(c.Context(), id, tenantID); err != nil {
		return webhookError(c, err)
	}
	h.writeAudit(c, "webhook.subscription.delete", fmt.Sprintf("subscription:%d", id))
	return c.SendStatus(204)
}

func (h *Handler) GetWebhookDeliveryHistory(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid subscription id"})
	}
	page, pageSize := 1, 50
	if v, e := strconv.Atoi(c.Query("page", "1")); e == nil && v > 0 {
		page = v
	} else {
		return c.Status(400).JSON(fiber.Map{"error": "invalid page"})
	}
	if v, e := strconv.Atoi(c.Query("page_size", "50")); e == nil && v > 0 && v <= 100 {
		pageSize = v
	} else {
		return c.Status(400).JSON(fiber.Map{"error": "invalid page_size"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	history, err := svc.DeliveryHistoryFiltered(c.Context(), id, tenantID, c.Query("status"), pageSize, (page-1)*pageSize)
	if err != nil {
		return webhookError(c, err)
	}
	return c.JSON(fiber.Map{"deliveries": history, "page": fiber.Map{"number": page, "size": pageSize}})
}

func (h *Handler) GetWebhookDelivery(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid delivery id"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	delivery, attempts, err := svc.DeliveryForTenant(c.Context(), id, tenantID)
	if err != nil {
		return webhookError(c, err)
	}
	return c.JSON(fiber.Map{"delivery": delivery, "attempts": attempts})
}

func (h *Handler) ReplayWebhookDelivery(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid delivery id"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	delivery, err := svc.ReplayForTenant(c.Context(), id, tenantID)
	if err != nil {
		return webhookError(c, err)
	}
	h.writeAudit(c, "webhook.delivery.replay", fmt.Sprintf("delivery:%d replay:%d", id, delivery.ID))
	return c.Status(201).JSON(fiber.Map{"delivery": delivery})
}

func (h *Handler) RetryWebhookDelivery(c fiber.Ctx) error { return h.ReplayWebhookDelivery(c) }

func (h *Handler) RotateWebhookSecret(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid subscription id"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	sub, secret, err := svc.RotateSecretForTenant(c.Context(), id, tenantID)
	if err != nil {
		return webhookError(c, err)
	}
	h.writeAudit(c, "webhook.subscription.secret.rotate", fmt.Sprintf("subscription:%d", id))
	return c.JSON(fiber.Map{"subscription": sub, "secret": secret})
}

func (h *Handler) ReactivateWebhookSubscription(c fiber.Ctx) error {
	tenantID, err := webhookTenantID(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": "tenant context required"})
	}
	id, err := webhookID(c, "id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid subscription id"})
	}
	svc, serviceErr := h.requireWebhookService(c)
	if serviceErr != nil {
		return serviceErr
	}
	sub, err := svc.ReactivateForTenant(c.Context(), id, tenantID)
	if err != nil {
		return webhookError(c, err)
	}
	h.writeAudit(c, "webhook.subscription.reactivate", fmt.Sprintf("subscription:%d", id))
	return c.JSON(fiber.Map{"subscription": sub})
}
