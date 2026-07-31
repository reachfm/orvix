package handlers

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/auth"
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
			return respondAPIError(c, fiber.StatusNotFound, domain.CodeDomainNotFound, "Domain not found.")
		}
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
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
		return respondAPIError(c, fiber.StatusBadRequest, "INVALID_REQUEST", "Invalid request body.")
	}
	if req.Name == "" {
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeInvalidDomainName, "Domain name is required.")
	}

	// Validate and normalize domain name.
	normalized, err := domain.ValidateDomainName(req.Name)
	if err != nil {
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeInvalidDomainName, "Invalid domain name.")
	}
	req.Name = normalized

	// Check for duplicate domain name. Domain names are globally unique DNS
	// names, so the lookup is global; the bare existence flag never reveals
	// which tenant owns the name. The database unique constraint is the final
	// concurrency-safe guard (mapped by the service to the same code).
	exists, err := h.domainAdminSvc.DomainExistsGlobal(c.Context(), normalized)
	if err == nil && exists {
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainAlreadyExists, "Domain already exists.")
	}

	// Quota enforcement: check domain limit before creating.
	count, err := h.domainAdminSvc.CountByTenant(c.Context(), tenantID)
	if err == nil && h.quotaSvc != nil {
		if result := h.quotaSvc.CanCreateDomain(tenantID, int(count)); result != nil && !result.Allowed {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    domain.CodeDomainLimitReached,
				"message": "Domain limit reached for your plan.",
				"limit":   result.Limit,
				"used":    result.Used,
				"allowed": false,
			})
		}
	}

	d, err := h.domainAdminSvc.CreateDomain(c.Context(), req, tenantID)
	if err != nil {
		if err == domain.ErrDomainAlreadyExists {
			return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainAlreadyExists, "Domain already exists.")
		}
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
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
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeDomainStatusInvalid, "Status is required.")
	}
	if _, ok := domain.ParseDomainStatus(req.Status); !ok {
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeDomainStatusInvalid, "Unsupported domain status.")
	}

	if err := h.domainAdminSvc.SetDomainStatus(c.Context(), id, tenantID, req.Status, req.Reason); err != nil {
		if errors.Is(err, domain.ErrInvalidDomainStatus) {
			return respondAPIError(c, fiber.StatusBadRequest, domain.CodeDomainStatusInvalid, "Unsupported domain status.")
		}
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
	return c.JSON(fiber.Map{"status": "ok"})
}

func (h *Handler) DeleteAdminDomain(c fiber.Ctx) error {
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

	// Load domain to check if it exists
	d, err := h.domainAdminSvc.GetDomain(c.Context(), id, tenantID)
	if err != nil {
		if err == domain.ErrDomainNotFound {
			return respondAPIError(c, fiber.StatusNotFound, domain.CodeDomainNotFound, "Domain not found.")
		}
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
	if d == nil {
		return respondAPIError(c, fiber.StatusNotFound, domain.CodeDomainNotFound, "Domain not found.")
	}

	if err := h.domainAdminSvc.DeleteDomain(c.Context(), id, tenantID); err != nil {
		switch {
		case errors.Is(err, domain.ErrDomainNotFound):
			return respondAPIError(c, fiber.StatusNotFound, domain.CodeDomainNotFound, "Domain not found.")
		case errors.Is(err, domain.ErrDomainHasMailboxes):
			return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainHasMailboxes, "Domain has mailboxes and cannot be deleted.")
		case errors.Is(err, domain.ErrDomainHasDependencies):
			return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainHasDependencies, "Domain has dependencies and cannot be deleted.")
		default:
			return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
		}
	}

	// Update usage
	if h.usageSvc != nil {
		count, _ := h.domainAdminSvc.CountByTenant(c.Context(), tenantID)
		h.usageSvc.SetDomainCount(tenantID, int(count))
	}

	return c.JSON(fiber.Map{"status": "deleted", "domain": d.Name})
}

func (h *Handler) GetAdminDomainDKIM(c fiber.Ctx) error {
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

	result, err := h.domainAdminSvc.GetDKIM(c.Context(), id, tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrDomainNotFound) {
			return respondAPIError(c, fiber.StatusNotFound, domain.CodeDomainNotFound, "Domain not found.")
		}
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
	if result == nil {
		return c.JSON(fiber.Map{"dkim": nil})
	}
	return c.JSON(fiber.Map{"dkim": result})
}

func (h *Handler) PostAdminDomainDKIMGenerate(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return respondAPIError(c, fiber.StatusBadRequest, "INVALID_DOMAIN_ID", "Invalid domain id.")
	}
	id := uint(idVal)

	var req struct {
		Selector string `json:"selector"`
	}
	c.Bind().JSON(&req)
	if req.Selector == "" {
		req.Selector = "mail"
	}

	result, err := h.domainAdminSvc.GenerateDKIM(c.Context(), id, tenantID, req.Selector)
	if err != nil {
		return domainServiceError(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"dkim": result})
}

func (h *Handler) VerifyEnterpriseDomain(c fiber.Ctx) error {
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "DOMAIN_NOT_FOUND"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	// Use DNS verification via dnsops if available
	verified := false
	mxStatus := "not_checked"
	spfStatus := "not_checked"
	dkimStatus := "not_checked"
	dmarcStatus := "not_checked"

	if h.dnsOps != nil {
		ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
		defer cancel()

		inputs, err := h.dnsOpsInputsForDomain(ctx, d.Name)
		if err == nil && inputs.ServerIPv4 != "" {
			plan, err := h.dnsOps.Generate(inputs)
			if err == nil {
				report, err := h.dnsOps.Verify(ctx, plan)
				if err == nil && report != nil {
					verified = report.Verified
					for _, rec := range report.Plan.Records {
						switch rec.Type {
						case "MX":
							mxStatus = string(rec.Status)
						case "SPF":
							spfStatus = string(rec.Status)
						case "DKIM":
							dkimStatus = string(rec.Status)
						case "DMARC":
							dmarcStatus = string(rec.Status)
						}
					}
				}
			}
		}
	}

	return c.JSON(fiber.Map{
		"domain":   d.Name,
		"verified": verified,
		"records": fiber.Map{
			"mx":    fiber.Map{"status": mxStatus},
			"spf":   fiber.Map{"status": spfStatus},
			"dkim":  fiber.Map{"status": dkimStatus},
			"dmarc": fiber.Map{"status": dmarcStatus},
		},
	})
}

func (h *Handler) PostAdminDomainDKIMRotate(c fiber.Ctx) error {
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
		Selector string `json:"selector"`
	}
	c.Bind().JSON(&req)
	if req.Selector == "" {
		req.Selector = "mail"
	}

	result, err := h.domainAdminSvc.RotateDKIM(c.Context(), id, tenantID, req.Selector)
	if err != nil {
		return domainServiceError(c, err)
	}

	return c.JSON(fiber.Map{"dkim": result})
}
