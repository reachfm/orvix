package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/admin/organization"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/billing"
	"go.uber.org/zap"
	"strconv"
)

func (h *Handler) ListOrganizations(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization admin service not available"})
	}
	var req struct {
		Search string `json:"search"`
		Active *bool  `json:"active"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	c.Bind().Query(&req)

	filter := organization.OrganizationFilter{
		Search: req.Search,
		Active: req.Active,
		Limit:  req.Limit,
		Offset: req.Offset,
	}

	result, total, err := h.orgAdminSvc.ListOrganizations(c.Context(), filter)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if result == nil {
		result = []organization.Organization{}
	}
	return c.JSON(fiber.Map{"organizations": result, "total": total})
}

func (h *Handler) GetOrganization(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization admin service not available"})
	}
	// This handler is mounted only on the tenant-scoped /enterprise
	// group (admin/operator + tenant context). An organization IS a
	// tenant, so a caller may only read their own organization; the
	// service GetByID is not tenant-scoped, so we must enforce it here.
	// Cross-tenant reads go through the platform routes, which are
	// gated to admin/superadmin only. Without this check an operator
	// could enumerate any tenant's organization metadata by id (IDOR).
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid organization id"})
	}
	id := uint(idVal)

	// Return 404 (not 403) on a cross-tenant id so we do not leak the
	// existence of other tenants.
	if id != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	org, err := h.orgAdminSvc.GetOrganization(c.Context(), id)
	if err != nil {
		if err == organization.ErrOrganizationNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"organization": org})
}

func (h *Handler) CreateOrganization(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization admin service not available"})
	}
	var req organization.CreateOrganizationRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Slug == "" || req.Domain == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "slug and domain are required"})
	}

	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}

	org, err := h.orgAdminSvc.CreateOrganization(c.Context(), req, tenantID)
	if err != nil {
		if err == organization.ErrOrganizationExists {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "organization already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	// Organization.ID is the canonical tenant ID for billing.
	// Provision a Free subscription for the new organization.
	// If subscription provisioning fails, log the error but do
	// not rollback the organization — the billing init path
	// will backfill missing subscriptions on restart.
	if h.billingSvc != nil {
		if _, subErr := h.billingSvc.GetSubscription(org.ID); subErr != nil {
			if _, createErr := h.billingSvc.CreateSubscription(org.ID, billing.PlanFree, billing.IntervalMonthly, 0); createErr != nil {
				h.logger.Error("subscription provisioning failed for new organization",
					zap.Uint("org_id", org.ID),
					zap.Error(createErr))
			}
		}
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"organization": org})
}

func (h *Handler) UpdateOrganization(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization admin service not available"})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid organization id"})
	}
	id := uint(idVal)

	var req organization.UpdateOrganizationRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	org, err := h.orgAdminSvc.UpdateOrganization(c.Context(), id, req)
	if err != nil {
		if err == organization.ErrOrganizationNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"organization": org})
}

func (h *Handler) SetOrganizationActive(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization admin service not available"})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid organization id"})
	}
	id := uint(idVal)

	var req struct {
		Active bool   `json:"active"`
		Reason string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	if err := h.orgAdminSvc.SetOrganizationActive(c.Context(), id, req.Active, req.Reason); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "ok"})
}

func (h *Handler) GetOrganizationDetail(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization admin service not available"})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid organization id"})
	}
	id := uint(idVal)

	detail, err := h.orgAdminSvc.GetOrganizationDetail(c.Context(), id)
	if err != nil {
		if err == organization.ErrOrganizationNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(detail)
}
