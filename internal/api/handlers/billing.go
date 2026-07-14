package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/billing"
)

func (h *Handler) ListBillingPlans(c fiber.Ctx) error {
	if h.billingSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "billing service not available"})
	}
	plans, err := h.billingSvc.ListPlans()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(plans)
}

func (h *Handler) GetBillingSubscription(c fiber.Ctx) error {
	if h.billingSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "billing service not available"})
	}
	tenantID := c.Locals("tenant_id").(uint)
	sub, err := h.billingSvc.GetSubscription(tenantID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(sub)
}

func (h *Handler) CreateBillingSubscription(c fiber.Ctx) error {
	if h.billingSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "billing service not available"})
	}
	var req struct {
		PlanID           billing.PlanID            `json:"plan_id"`
		BillingInterval  billing.BillingInterval   `json:"billing_interval"`
		TrialDays        int                       `json:"trial_days"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	tenantID := c.Locals("tenant_id").(uint)
	sub, err := h.billingSvc.CreateSubscription(tenantID, req.PlanID, req.BillingInterval, req.TrialDays)
	if err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(sub)
}

func (h *Handler) GetBillingUsage(c fiber.Ctx) error {
	if h.usageSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "usage service not available"})
	}
	tenantID := c.Locals("tenant_id").(uint)
	rec, err := h.usageSvc.GetCurrentUsage(tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rec)
}

func (h *Handler) CheckBillingQuota(c fiber.Ctx) error {
	if h.quotaSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "quota service not available"})
	}
	resource := c.Query("resource", "domains")
	usedStr := c.Query("used", "0")
	used, _ := strconv.Atoi(usedStr)
	tenantID := c.Locals("tenant_id").(uint)
	var result *billing.QuotaCheckResult
	switch resource {
	case "domains":
		result = h.quotaSvc.CanCreateDomain(tenantID, used)
	case "mailboxes":
		result = h.quotaSvc.CanCreateMailbox(tenantID, used)
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unknown resource type"})
	}
	return c.JSON(result)
}
