package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/customerdomain"
	"go.uber.org/zap"
)

// customerDomainReady reports whether the customer domain service is
// wired. When it is nil (e.g. verification-table init failed at router
// construction) the endpoints must return a deterministic 503 instead
// of dereferencing a nil service and panicking into a 500.
func (h *Handler) customerDomainReady(c fiber.Ctx) bool {
	if h.customerDomainSvc == nil {
		_ = c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "customer domain service not available"})
		return false
	}
	return true
}

// ListCustomerDomains returns paginated domains for the calling tenant.
func (h *Handler) ListCustomerDomains(c fiber.Ctx) error {
	if !h.customerDomainReady(c) {
		return nil
	}
	tenantID := h.tenantIDUint(c)

	var req customerdomain.DomainListRequest
	req.Offset = queryInt(c, "offset", 0)
	req.Limit = queryInt(c, "limit", 20)
	req.Search = c.Query("search", "")
	req.Status = c.Query("status", "")

	resp, err := h.customerDomainSvc.ListDomains(c.Context(), tenantID, req)
	if err != nil {
		h.logger.Error("list customer domains", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(resp)
}

// GetCustomerDomain returns detailed information for a specific domain.
func (h *Handler) GetCustomerDomain(c fiber.Ctx) error {
	if !h.customerDomainReady(c) {
		return nil
	}
	tenantID := h.tenantIDUint(c)
	domainID, err := strconv.ParseUint(c.Params("domain_id"), 10, 64)
	if err != nil || domainID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain id"})
	}

	detail, err := h.customerDomainSvc.GetDomain(c.Context(), tenantID, uint(domainID))
	if err == customerdomain.ErrDomainNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}
	if err != nil {
		h.logger.Error("get customer domain", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(detail)
}

// GetCustomerDomainDNS returns DNS inspection results for a domain.
func (h *Handler) GetCustomerDomainDNS(c fiber.Ctx) error {
	if !h.customerDomainReady(c) {
		return nil
	}
	tenantID := h.tenantIDUint(c)
	domainID, err := strconv.ParseUint(c.Params("domain_id"), 10, 64)
	if err != nil || domainID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain id"})
	}

	result, err := h.customerDomainSvc.GetDNS(c.Context(), tenantID, uint(domainID))
	if err == customerdomain.ErrDomainNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}
	if err != nil {
		h.logger.Error("get customer domain dns", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(result)
}

// VerifyCustomerDomain triggers a bounded DNS verification refresh.
func (h *Handler) VerifyCustomerDomain(c fiber.Ctx) error {
	if !h.customerDomainReady(c) {
		return nil
	}
	tenantID := h.tenantIDUint(c)
	domainID, err := strconv.ParseUint(c.Params("domain_id"), 10, 64)
	if err != nil || domainID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain id"})
	}

	err = h.customerDomainSvc.VerifyDomain(c.Context(), tenantID, uint(domainID))
	if err == customerdomain.ErrDomainNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}
	if err == customerdomain.ErrVerificationCooldown {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "verification cooldown active, try again later"})
	}
	if err != nil {
		h.logger.Error("verify customer domain", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	// Return the latest snapshot after verification. Guard against a
	// nil snapshot (e.g. the snapshot read failed) so we return a clean
	// response instead of panicking on snap.Score.
	snap, snapErr := h.customerDomainSvc.GetLatestSnapshot(c.Context(), tenantID, uint(domainID))
	if snapErr != nil || snap == nil {
		return c.JSON(fiber.Map{
			"status":  "verified",
			"message": "domain verification complete",
		})
	}
	return c.JSON(fiber.Map{
		"status":  "verified",
		"score":   snap.Score,
		"message": "domain verification complete",
	})
}

// tenantIDUint extracts the tenant ID from the request context.
func (h *Handler) tenantIDUint(c fiber.Ctx) uint {
	if v, ok := c.Locals("tenant_id").(uint); ok && v > 0 {
		return v
	}
	return 1
}

func queryInt(c fiber.Ctx, key string, defaultVal int) int {
	s := c.Query(key, "")
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return defaultVal
	}
	return n
}
