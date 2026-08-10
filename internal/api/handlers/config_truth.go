package handlers

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/configtruth"
)

func (h *Handler) configTruthService() *configtruth.Service {
	if h.configTruthSvc == nil {
		sqlDB, _ := h.db.DB()
		h.configTruthSvc = configtruth.NewService(configtruth.NewRepository(sqlDB))
		h.registerConfigDefaults()
	}
	return h.configTruthSvc
}

// registerConfigDefaults registers known configuration fields.
func (h *Handler) registerConfigDefaults() {
	svc := h.configTruthSvc
	svc.RegisterField(configtruth.Field{Key: "security.password_min_len", Section: "security", Type: "int", RestartRequired: true})
	svc.RegisterField(configtruth.Field{Key: "security.session_ttl_seconds", Section: "security", Type: "int", RestartRequired: true})
	svc.RegisterField(configtruth.Field{Key: "backup.retention_count", Section: "backup", Type: "int", RestartRequired: false})
	svc.RegisterField(configtruth.Field{Key: "backup.scheduler_enabled", Section: "backup", Type: "bool", RestartRequired: false})
	svc.RegisterField(configtruth.Field{Key: "jwt.secret", Section: "security", Type: "string", RestartRequired: true, Secret: true})
}

// GetConfigurationSetting returns the authoritative view of one setting.
func (h *Handler) GetConfigurationSetting(c fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "key is required"})
	}
	setting, err := h.configTruthService().Get(c.Context(), key)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(setting)
}

// ListConfigurationSettings returns all known settings.
func (h *Handler) ListConfigurationSettings(c fiber.Ctx) error {
	settings, err := h.configTruthService().List(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"settings": settings})
}

// MutateConfigurationSetting validates and applies a mutation.
func (h *Handler) MutateConfigurationSetting(c fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "key is required"})
	}
	var req struct {
		Value   any    `json:"value"`
		Version int    `json:"version"`
		Reason  string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	result, err := h.configTruthService().Mutate(c.Context(), key, configtruth.MutationRequest{
		Value:   req.Value,
		Version: req.Version,
		ActorID: actorID,
		Reason:  req.Reason,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAudit(c, "config.mutate", fmt.Sprintf("key:%s applied=%v state=%s", key, result.Applied, result.State))
	return c.JSON(result)
}
