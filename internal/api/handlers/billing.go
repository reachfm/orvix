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
		PlanID          billing.PlanID          `json:"plan_id"`
		BillingInterval billing.BillingInterval `json:"billing_interval"`
		TrialDays       int                     `json:"trial_days"`
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

// GetBillingState returns the coherent commercial state for the
// authenticated tenant (GET /enterprise/billing/state): subscription,
// plan, billing period, live usage, invoice state, and the payment/
// provider configuration. EVERY field is read from the real
// billing/usage/invoice stores — no fabricated MRR, cards, or paid
// invoices. When no payment provider is configured,
// payment_provider.configured=false is surfaced honestly ("provider
// not configured"). Missing rows are null/empty, never placeholders.
func (h *Handler) GetBillingState(c fiber.Ctx) error {
	tenantID := c.Locals("tenant_id").(uint)
	out := fiber.Map{"tenant_id": tenantID}

	if h.billingSvc != nil {
		if sub, subErr := h.billingSvc.GetSubscription(tenantID); subErr == nil && sub != nil {
			out["subscription"] = sub
			if plan, planErr := h.billingSvc.GetPlan(sub.PlanID); planErr == nil && plan != nil {
				out["plan"] = plan
			}
		} else {
			out["subscription"] = nil
			out["plan"] = nil
		}
	} else {
		out["subscription"] = nil
		out["plan"] = nil
	}

	if h.usageSvc != nil {
		if usage, usageErr := h.usageSvc.GetCurrentUsage(tenantID); usageErr == nil && usage != nil {
			out["usage"] = usage
		} else {
			out["usage"] = nil
		}
	} else {
		out["usage"] = nil
	}

	invoices := []billing.InvoiceRecord{}
	if h.invoiceSvc != nil {
		if list, _, invErr := h.invoiceSvc.ListTenantInvoices(c.Context(), &billing.InvoiceFilter{TenantID: tenantID, Limit: 50}); invErr == nil {
			invoices = list
		}
	}
	out["invoices"] = invoices

	provider := ""
	enabled := false
	configured := false
	note := ""
	if h.cfg != nil && h.cfg.Payment.Provider != "" {
		provider = h.cfg.Payment.Provider
		enabled = h.cfg.Payment.Enabled
		configured = h.cfg.Payment.Enabled
		if !h.cfg.Payment.Enabled {
			note = "payment provider is configured but disabled"
		}
	} else {
		note = "payment provider not configured"
	}
	out["payment_provider"] = fiber.Map{
		"provider":   provider,
		"enabled":    enabled,
		"configured": configured,
		"note":       note,
	}

	return c.JSON(out)
}
