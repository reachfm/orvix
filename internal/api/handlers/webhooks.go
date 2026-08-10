package handlers

import (
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/webhooks"
)

func (h *Handler) webhookService() *webhooks.Service {
	if h.webhookSvc == nil {
		sqlDB, _ := h.db.DB()
		h.webhookSvc = webhooks.NewService(webhooks.NewRepository(sqlDB), nil)
	}
	return h.webhookSvc
}

func (h *Handler) CreateWebhookSubscription(c fiber.Ctx) error {
	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Scope  string   `json:"scope"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	tenantID, _ := c.Locals("tenant_id").(uint)
	scope := webhooks.SubscriptionScope(req.Scope)
	sub, err := h.webhookService().CreateSubscription(c.Context(), tenantID, scope, req.URL, req.Events, nil)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAudit(c, "webhook.subscription.create", fmt.Sprintf("sub:%d url:%s", sub.ID, sub.URL))
	return c.Status(fiber.StatusCreated).JSON(sub)
}

func (h *Handler) ListWebhookSubscriptions(c fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(uint)
	scope := c.Query("scope")
	onlyActive := c.Query("active") == "true"
	svc := h.webhookService()
	subs, err := svc.ListSubscriptions(c.Context(), tenantID, scope, onlyActive)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"subscriptions": subs})
}

func (h *Handler) GetWebhookSubscription(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 64)
	svc := h.webhookService()
	sub, err := svc.GetSubscription(c.Context(), uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
	}
	return c.JSON(sub)
}

func (h *Handler) GetWebhookDeliveryHistory(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 64)
	limit := 50
	svc := h.webhookService()
	history, err := svc.DeliveryHistory(c.Context(), uint(id), limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"deliveries": history})
}

func (h *Handler) RetryWebhookDelivery(c fiber.Ctx) error {
	return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{"error": "not yet implemented"})
}
