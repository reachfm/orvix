package admin_handlers

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/orvixemail/orvix/internal/models"
)

// CreateTenant creates a new tenant (organization) in the platform with an admin user.
// Super Admin only.
// Request: {"name":"Acme","plan":"pro","email":"admin@acme.com","password":"secret123"}
func (h *Handler) CreateTenant(c *fiber.Ctx) error {
	var req struct {
		Name     string `json:"name"`
		Plan     string `json:"plan"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.Name == "" || req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name, email, and password are required",
		})
	}

	if req.Plan == "" {
		req.Plan = "trial"
	}

	tenant := newTenantFromPlan(req.Name, req.Plan)
	if err := h.cfg.DB.Create(&tenant).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": fmt.Sprintf("tenant could not be created: %v", err),
		})
	}

	hash, err := h.cfg.Auth.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to hash password",
		})
	}

	admin := models.User{
		TenantID:     tenant.ID,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         "tenant_admin",
		IsAdmin:      true,
		IsActive:     true,
	}
	if err := h.cfg.DB.Create(&admin).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("admin user created but database error: %v", err),
		})
	}

	admin.PasswordHash = ""

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"tenant": tenant,
		"admin":  admin,
	})
}

// GetTenant retrieves a tenant by ID with its current status and limits.
// Super Admin only.
func (h *Handler) GetTenant(c *fiber.Ctx) error {
	id := c.Params("id", "0")
	if id == "0" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "tenant id is required",
		})
	}

	var tenant models.Tenant
	if err := h.cfg.DB.First(&tenant, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "tenant not found",
		})
	}

	return c.JSON(tenant)
}

// SuspendTenant suspends a tenant, disabling all domains and mailboxes under it.
// Super Admin only.
func (h *Handler) SuspendTenant(c *fiber.Ctx) error {
	return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
		"error": "SuspendTenant not yet implemented",
	})
}
