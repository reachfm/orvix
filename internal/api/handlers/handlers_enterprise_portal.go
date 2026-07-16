package handlers

import (
	"fmt"
	"strings"
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

	// Look up the existing key to preserve its name and scopes.
	existingKeys, _ := h.apikeys.List(userID)
	var oldName string
	var oldScopes []string
	for _, k := range existingKeys {
		if k.ID == id {
			oldName = k.Name
			if k.Scopes != "" {
				oldScopes = parseScopes(k.Scopes)
			}
			break
		}
	}
	if oldName == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "API key not found"})
	}

	// Revoke old key atomically before generating new.
	if err := h.apikeys.RevokeScoped(id, userID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	var req struct {
		Scopes []string `json:"scopes"`
	}
	var scopes []string
	if err := c.Bind().JSON(&req); err == nil && len(req.Scopes) > 0 {
		scopes = req.Scopes
	} else {
		scopes = oldScopes
	}

	fullKey, record, err := h.apikeys.Generate(oldName, userID, tenantID, role, scopes, 365)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	h.writeAuditLog(c, "apikey.rotate", fmt.Sprintf("name:%s old_id:%d new_id:%d", record.Name, id, record.ID))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"api_key":    fullKey,
		"key_prefix": record.KeyPrefix,
		"name":       record.Name,
		"expires_at": record.ExpiresAt,
		"warning":    "Save this key now - it will not be shown again",
	})
}

func parseScopes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
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
