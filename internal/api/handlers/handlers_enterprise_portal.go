package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
	"go.uber.org/zap"
)

func (h *Handler) ListEnterpriseAPIKeys(c fiber.Ctx) error {
	return h.ListAPIKeys(c)
}

func (h *Handler) CreateEnterpriseAPIKey(c fiber.Ctx) error {
	return h.CreateAPIKey(c)
}

func (h *Handler) DeleteEnterpriseAPIKey(c fiber.Ctx) error {
	return h.DeleteAPIKey(c)
}

func (h *Handler) RotateEnterpriseAPIKey(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}
	idStr := c.Params("id")
	var id uint
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid key id"})
	}

	if h.apikeys == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "API key manager not available"})
	}

	tenantID := c.Locals("tenant_id").(uint)
	role := string(c.Locals("role").(auth.Role))

	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	var scopes []string
	if err := c.Bind().JSON(&req); err == nil {
		scopes = req.Scopes
	}
	name := req.Name
	if name == "" {
		name = fmt.Sprintf("key-%d", id)
	}

	fullKey, record, err := h.apikeys.Generate(name, userID, tenantID, role, scopes, 365)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	_ = id

	h.writeAuditLog(c, "apikey.rotate", fmt.Sprintf("name:%s", record.Name))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"api_key":    fullKey,
		"key_prefix": record.KeyPrefix,
		"name":       record.Name,
		"expires_at": record.ExpiresAt,
		"warning":    "Save this key now - it will not be shown again",
	})
}

func (h *Handler) ListEnterpriseAuditLogs(c fiber.Ctx) error {
	if h.auditStore == nil {
		return c.JSON([]struct{}{})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(err.(*fiber.Error).Code).JSON(fiber.Map{"error": err.Error()})
	}

	q := &audit.Query{
		TenantID: tenantID,
		Limit:    100,
	}
	if actor := c.Query("actor"); actor != "" {
		q.Actor = actor
	}
	if action := c.Query("action"); action != "" {
		q.Action = action
	}
	if target := c.Query("target"); target != "" {
		q.Target = target
	}
	if since := c.Query("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			q.Since = &t
		}
	}
	if until := c.Query("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			q.Until = &t
		}
	}

	logs, _, err := h.auditStore.Search(c.Context(), q)
	if err != nil {
		h.logger.Error("failed to list enterprise audit logs", zap.Error(err))
		return c.JSON([]struct{}{})
	}
	type safeEntry struct {
		ID        int64  `json:"id"`
		Action    string `json:"action"`
		Actor     string `json:"actor"`
		Target    string `json:"target"`
		Result    string `json:"result"`
		Timestamp string `json:"timestamp"`
	}
	result := make([]safeEntry, 0, len(logs))
	for _, e := range logs {
		result = append(result, safeEntry{
			ID:        e.ID,
			Action:    e.Action,
			Actor:     e.Actor,
			Target:    e.Target,
			Result:    e.Result,
			Timestamp: e.Timestamp.Format(time.RFC3339),
		})
	}
	return c.JSON(result)
}
