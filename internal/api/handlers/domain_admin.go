package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/auth"
	"strconv"
)

func (h *Handler) ListAdminDomains(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	var req struct {
		Search string `json:"search"`
		Status string `json:"status"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	c.Bind().Query(&req)

	var status *string
	if req.Status != "" {
		status = &req.Status
	}

	filter := domain.DomainFilter{
		TenantID: &tenantID,
		Status:   status,
		Search:   req.Search,
		Limit:    req.Limit,
		Offset:   req.Offset,
	}

	result, total, err := h.domainAdminSvc.ListDomains(c.Context(), filter)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if result == nil {
		result = []domain.AdminDomain{}
	}
	return c.JSON(fiber.Map{"domains": result, "total": total})
}

func (h *Handler) GetAdminDomain(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain id"})
	}
	id := uint(idVal)

	d, err := h.domainAdminSvc.GetDomain(c.Context(), id, tenantID)
	if err != nil {
		if err == domain.ErrDomainNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"domain": d})
}

func (h *Handler) CreateAdminDomain(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	var req domain.CreateDomainRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name is required"})
	}

	// Quota enforcement: check domain limit before creating.
	count, err := h.domainAdminSvc.CountByTenant(c.Context(), tenantID)
	if err == nil && h.quotaSvc != nil {
		if result := h.quotaSvc.CanCreateDomain(tenantID, int(count)); result != nil && !result.Allowed {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":   "domain quota exceeded: " + result.Reason,
				"limit":   result.Limit,
				"used":    result.Used,
				"allowed": false,
			})
		}
	}

	d, err := h.domainAdminSvc.CreateDomain(c.Context(), req, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if h.usageSvc != nil {
		h.usageSvc.SetDomainCount(tenantID, int(count)+1)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"domain": d})
}

func (h *Handler) UpdateAdminDomain(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain id"})
	}
	id := uint(idVal)

	var req domain.UpdateDomainRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	d, err := h.domainAdminSvc.UpdateDomain(c.Context(), id, tenantID, req)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"domain": d})
}

func (h *Handler) SetAdminDomainStatus(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain id"})
	}
	id := uint(idVal)

	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Status == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status is required"})
	}

	if err := h.domainAdminSvc.SetDomainStatus(c.Context(), id, tenantID, req.Status, req.Reason); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "ok"})
}
