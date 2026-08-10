package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	platformbilling "github.com/orvix/orvix/internal/platform/billing"
	"go.uber.org/zap"
)

// GetPlatformBillingBalance returns a tenant's current platform-credit
// balance — integer minor units + currency code only, exactly as
// stored; the handler never converts to/introduces a float.
func (h *Handler) GetPlatformBillingBalance(c fiber.Ctx) error {
	if h.platformBillSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "platform billing service not available"})
	}
	tenantID, err := parseUintParam(c, "tenant_id")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid tenant id"})
	}
	bal, err := h.platformBillSvc.GetBalance(c.Context(), tenantID)
	if err != nil {
		h.logger.Error("get platform billing balance failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get balance"})
	}
	if bal == nil {
		return c.JSON(fiber.Map{"tenant_id": tenantID, "balance_cents": 0, "currency": "", "version": 0})
	}
	return c.JSON(bal)
}

// PostPlatformBillingAdjustment records a manual credit/debit. Reason
// and actor are required; idempotency is enforced by the service layer
// via the caller-supplied idempotency key so a retried request never
// double-applies. CSRF is enforced by the router middleware chain for
// this route; every outcome (success or rejection) is audited by the
// service itself.
func (h *Handler) PostPlatformBillingAdjustment(c fiber.Ctx) error {
	if h.platformBillSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "platform billing service not available"})
	}
	tenantID, err := parseUintParam(c, "tenant_id")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid tenant id"})
	}
	var req struct {
		Type        string `json:"type"` // "credit" | "debit"
		AmountCents int64  `json:"amount_cents"`
		Currency    string `json:"currency"`
		Reason      string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if strings.TrimSpace(req.Reason) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "reason is required"})
	}
	if req.AmountCents <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "amount_cents must be a positive integer"})
	}
	adjType := platformbilling.AdjustmentDebit
	if req.Type == string(platformbilling.AdjustmentCredit) {
		adjType = platformbilling.AdjustmentCredit
	} else if req.Type != string(platformbilling.AdjustmentDebit) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "type must be 'credit' or 'debit'"})
	}
	idemKey := strings.TrimSpace(c.Get("Idempotency-Key"))
	actorID, _ := c.Locals("user_id").(uint)
	adj, err := h.platformBillSvc.ApplyAdjustment(c.Context(), tenantID, adjType, req.AmountCents, req.Currency, req.Reason, actorID, idemKey)
	if err != nil {
		return platformBillingActionError(c, err)
	}
	h.writeAuditLog(c, "platform_billing.adjustment.apply", fmt.Sprintf("tenant:%d|type:%s|amount:%d|currency:%s", tenantID, adjType, req.AmountCents, req.Currency))
	return c.Status(fiber.StatusCreated).JSON(adj)
}

// GetPlatformBillingAdjustments returns the recent adjustment history
// for a tenant, newest first.
func (h *Handler) GetPlatformBillingAdjustments(c fiber.Ctx) error {
	if h.platformBillSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "platform billing service not available"})
	}
	tenantID, err := parseUintParam(c, "tenant_id")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid tenant id"})
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	adjustments, err := h.platformBillSvc.ListAdjustments(c.Context(), tenantID, limit)
	if err != nil {
		h.logger.Error("list platform billing adjustments failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list adjustments"})
	}
	return c.JSON(fiber.Map{"adjustments": adjustments})
}

// GetPlatformBillingReconciliation is the minimal financial
// reconciliation report (Milestone 15 re-audit gap): it recomputes a
// tenant's ledger balance directly from the full adjustment history
// and reports any discrepancy against the maintained balance row —
// read-only, never auto-corrects. A genuine discrepancy is surfaced
// for an operator to investigate and correct via a new, reasoned
// adjustment, not silently patched by this endpoint.
func (h *Handler) GetPlatformBillingReconciliation(c fiber.Ctx) error {
	if h.platformBillSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "platform billing service not available"})
	}
	tenantID, err := parseUintParam(c, "tenant_id")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid tenant id"})
	}
	report, err := h.platformBillSvc.Reconcile(c.Context(), tenantID)
	if err != nil {
		h.logger.Error("platform billing reconciliation failed", zap.Uint("tenant_id", tenantID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to reconcile tenant ledger"})
	}
	return c.JSON(report)
}

func parseUintParam(c fiber.Ctx, name string) (uint, error) {
	v, err := strconv.ParseUint(c.Params(name), 10, 64)
	if err != nil || v == 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return uint(v), nil
}

func platformBillingActionError(c fiber.Ctx, err error) error {
	switch err {
	case platformbilling.ErrInvalidAmount, platformbilling.ErrReasonRequired, platformbilling.ErrCurrencyMismatch:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
}
