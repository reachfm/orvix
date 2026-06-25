package handlers

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/coremail/push"
)

func (h *Handler) PushSubscribe(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": reason})
	}
	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256DH string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Endpoint == "" || req.Keys.P256DH == "" || req.Keys.Auth == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "endpoint, p256dh, and auth are required"})
	}
	if h.pushNotifier == nil || !h.pushNotifier.IsEnabled() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "push notifications not available"})
	}
	existing, _ := h.pushNotifier.Repo.GetByEndpoint(c.Context(), req.Endpoint)
	if existing != nil {
		if existing.MailboxID != ctx.Mailbox.ID {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "subscription belongs to another user"})
		}
		existing.P256DHKey = req.Keys.P256DH
		existing.AuthKey = req.Keys.Auth
		existing.UserAgent = c.Get("User-Agent")
		existing.DisabledAt = nil
		if err := h.pushNotifier.Repo.Update(c.Context(), existing); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update subscription"})
		}
		return c.JSON(fiber.Map{"status": "updated", "id": existing.ID})
	}
	sub := &push.PushSubscription{
		MailboxID: ctx.Mailbox.ID,
		Endpoint:  req.Endpoint,
		P256DHKey: req.Keys.P256DH,
		AuthKey:   req.Keys.Auth,
		UserAgent: c.Get("User-Agent"),
	}
	if err := h.pushNotifier.Repo.Create(c.Context(), sub); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create subscription"})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "subscribed", "id": sub.ID})
}

func (h *Handler) PushUnsubscribe(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": reason})
	}
	var req struct {
		ID       *uint  `json:"id"`
		Endpoint string `json:"endpoint"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if h.pushNotifier == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "push notifications not available"})
	}
	if req.Endpoint != "" {
		sub, err := h.pushNotifier.Repo.GetByEndpoint(c.Context(), req.Endpoint)
		if err != nil || sub == nil || sub.MailboxID != ctx.Mailbox.ID {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
		}
		if err := h.pushNotifier.Repo.Disable(c.Context(), sub.ID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to unsubscribe"})
		}
		return c.JSON(fiber.Map{"status": "unsubscribed"})
	}
	if req.ID != nil {
		if err := h.pushNotifier.Repo.Disable(c.Context(), *req.ID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to unsubscribe"})
		}
		return c.JSON(fiber.Map{"status": "unsubscribed"})
	}
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id or endpoint required"})
}

func (h *Handler) PushStatus(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": reason})
	}
	enabled := h.pushNotifier != nil && h.pushNotifier.IsEnabled()
	resp := fiber.Map{
		"enabled":           enabled,
		"active_count":      0,
		"subscriptions":     []push.PushSubscription{},
		"permission_status": c.Get("X-Push-Permission", "unknown"),
	}
	if enabled {
		resp["vapid_public_key"] = h.pushNotifier.VAPID.PublicKey
		disabled := false
		subs, err := h.pushNotifier.Repo.ListByMailbox(c.Context(), ctx.Mailbox.ID, &push.PushSubscriptionFilter{Disabled: &disabled})
		if err == nil {
			resp["active_count"] = len(subs)
			resp["subscriptions"] = subs
		}
	}
	return c.JSON(resp)
}

func (h *Handler) PushTest(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": reason})
	}
	if h.pushNotifier == nil || !h.pushNotifier.IsEnabled() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "push notifications not configured"})
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Endpoint == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "endpoint required"})
	}
	sub, err := h.pushNotifier.Repo.GetByEndpoint(c.Context(), req.Endpoint)
	if err != nil || sub == nil || sub.MailboxID != ctx.Mailbox.ID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
	}
	h.pushNotifier.NotifyMailboxMessage(c.Context(), ctx.Mailbox.ID, "test-"+time.Now().UTC().Format("20060102150405"), ctx.Mailbox.Email, "Orvix Push Test")
	return c.JSON(fiber.Map{"status": "sent"})
}
