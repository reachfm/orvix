package admin_handlers

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/orvixemail/orvix/internal/api/middleware"
	"github.com/orvixemail/orvix/internal/models"
)

// CreateTenantUser creates a new user under the caller's tenant scope
// and provisions the mailbox in the CoreMail engine.
func (h *Handler) CreateTenantUser(c *fiber.Ctx) error {
	tenantID := middleware.GetEffectiveTenant(c)
	if tenantID == 0 {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "no tenant context available",
		})
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "email and password are required",
		})
	}

	hash, err := h.cfg.Auth.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to hash password",
		})
	}

	user := models.User{
		TenantID:     tenantID,
		Email:        req.Email,
		Username:     req.Name,
		PasswordHash: hash,
		Role:         "user",
		IsActive:     true,
	}
	if err := h.cfg.DB.Create(&user).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": fmt.Sprintf("user could not be created: %v", err),
		})
	}

	// Provision mailbox in CoreMail engine (non-blocking for DB; error is logged)
	if h.cfg.MailAdapter != nil {
		if err := h.cfg.MailAdapter.ProvisionMailbox(req.Email, req.Password, 0); err != nil {
			h.cfg.Logger.Warnw("mailbox provisioned in DB but CoreMail engine failed",
				"email", req.Email,
				"error", err,
			)
		}
	}

	user.PasswordHash = ""
	return c.Status(fiber.StatusCreated).JSON(user)
}

// GetTenantUsers retrieves all users for the caller's tenant scope.
func (h *Handler) GetTenantUsers(c *fiber.Ctx) error {
	tenantID := middleware.GetEffectiveTenant(c)
	if tenantID == 0 {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "no tenant context available",
		})
	}

	var users []models.User
	if err := h.cfg.DB.Where("tenant_id = ?", tenantID).
		Omit("password_hash", "totp_secret", "backup_codes").
		Find(&users).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list users",
		})
	}

	return c.JSON(fiber.Map{
		"users":     users,
		"tenant_id": tenantID,
	})
}

// SuspendUser suspends a specific user within a tenant.
func (h *Handler) SuspendUser(c *fiber.Ctx) error {
	return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
		"error": "SuspendUser not yet implemented",
	})
}
