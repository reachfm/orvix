package handlers

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/customerdomain"
	"go.uber.org/zap"
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

	// Validate and normalize the domain name up front so an obviously bad name
	// is rejected before a transaction is opened. The service re-validates the
	// name inside the transaction — this is a fast path, not the guard.
	normalized, err := domain.ValidateDomainName(req.Name)
	if err != nil {
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeInvalidDomainName, "Invalid domain name.")
	}
	req.Name = normalized

	// Quota enforcement: check domain limit before creating. The provisioning
	// transaction performs its own authoritative, row-locked plan check; this
	// one produces the richer limit/used payload existing clients consume.
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

	// ONE atomic provisioning operation: domain + limits + optional DKIM +
	// audit, committed or rolled back together. The legacy flat request body
	// {"name":"..."} flows through this same path unchanged.
	result, err := h.domainAdminSvc.ProvisionDomain(c.Context(), req, tenantID)
	if err != nil {
		return respondProvisioningError(c, err)
	}
	if h.usageSvc != nil && !result.Idempotent {
		h.usageSvc.SetDomainCount(tenantID, int(count)+1)
	}

	// The response body carries ONLY publishable data. DKIM is represented by
	// its selector, DNS record name and PUBLIC TXT value; the private key
	// never leaves the database.
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"domain":           result.Domain,
		"effective_limits": result.EffectiveLimits,
		"dkim":             result.DKIM,
		"plan":             result.Plan,
		"dns":              result.DNS,
		"idempotent":       result.Idempotent,
	})
}

// respondProvisioningError maps every typed provisioning error to a stable
// machine-readable code and an actionable HTTP status. Unmapped errors collapse
// to a generic 500 so a raw database error is never surfaced to a caller.
func respondProvisioningError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, domain.ErrDomainAlreadyExists):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainAlreadyExists, "Domain already exists.")
	case errors.Is(err, domain.ErrInvalidDomainName):
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeInvalidDomainName, "Invalid domain name.")
	case errors.Is(err, domain.ErrInvalidDomainStatus):
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeDomainStatusInvalid, "Initial status must be active or disabled.")
	case errors.Is(err, domain.ErrDescriptionTooLong):
		return respondAPIError(c, fiber.StatusUnprocessableEntity, domain.CodeDescriptionTooLong, "Description must be 500 characters or fewer.")
	case errors.Is(err, domain.ErrInvalidDKIMSelector):
		return respondAPIError(c, fiber.StatusUnprocessableEntity, domain.CodeInvalidDKIMSelector, "Invalid DKIM selector.")
	case errors.Is(err, domain.ErrLimitContradiction):
		return respondAPIError(c, fiber.StatusUnprocessableEntity, domain.CodeLimitContradiction, err.Error())
	case errors.Is(err, domain.ErrLimitExceedsPlan):
		return respondAPIError(c, fiber.StatusUnprocessableEntity, domain.CodeLimitExceedsPlan, err.Error())
	case errors.Is(err, domain.ErrInvalidLimit):
		return respondAPIError(c, fiber.StatusUnprocessableEntity, domain.CodeInvalidLimit, err.Error())
	case errors.Is(err, domain.ErrDomainLimitReached):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainLimitReached, "Domain limit reached for your plan.")
	case errors.Is(err, domain.ErrPlanUnavailable):
		// Fail CLOSED: unknown plan data must never provision a domain.
		return respondAPIError(c, fiber.StatusConflict, domain.CodePlanUnavailable, "Organization plan data is unavailable; provisioning is blocked.")
	case errors.Is(err, domain.ErrDKIMAlreadyConfigured):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDKIMAlreadyConfigured, "DKIM is already configured for this domain.")
	case errors.Is(err, domain.ErrDomainForbidden):
		return respondAPIError(c, fiber.StatusForbidden, "FORBIDDEN", "Access denied.")
	default:
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
}

// GetOrganizationCapacity returns the organization plan summary the
// provisioning wizard needs: plan name, effective ceilings, live usage and
// remaining capacity. Unlimited dimensions are reported with an explicit
// *_unlimited flag and a null remaining value — never a misleading 0.
//
// It is a READ endpoint scoped to the authenticated tenant; a caller can never
// request another tenant's capacity because the tenant comes from the session,
// not the request. It fails closed (409 PLAN_UNAVAILABLE) when the plan row
// cannot be read, which is the same condition that blocks provisioning.
func (h *Handler) GetOrganizationCapacity(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	summary, err := h.domainAdminSvc.GetPlanSummary(c.Context(), tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrPlanUnavailable) {
			return respondAPIError(c, fiber.StatusConflict, domain.CodePlanUnavailable, "Organization plan data is unavailable.")
		}
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
	return c.JSON(fiber.Map{"capacity": summary})
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

// PostAdminDomainDKIMRevoke handles POST /admin/domains/:id/dkim/revoke.
func (h *Handler) PostAdminDomainDKIMRevoke(c fiber.Ctx) error {
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
	if err := h.domainAdminSvc.RevokeDKIM(c.Context(), uint(idVal), tenantID); err != nil {
		return domainServiceError(c, err)
	}
	return c.JSON(fiber.Map{"revoked": true})
}

// GetAdminDomainDKIMHistory handles GET /admin/domains/:id/dkim/history.
func (h *Handler) GetAdminDomainDKIMHistory(c fiber.Ctx) error {
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
	hist, err := h.domainAdminSvc.ListDKIMHistory(c.Context(), uint(idVal), tenantID)
	if err != nil {
		return domainServiceError(c, err)
	}
	if hist == nil {
		hist = []domain.DKIMSelectorHistoryEntry{}
	}
	return c.JSON(fiber.Map{"history": hist})
}

// GetAdminDomainTLSStatus handles GET /admin/domains/:id/tls.
func (h *Handler) GetAdminDomainTLSStatus(c fiber.Ctx) error {
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
	result, err := h.domainAdminSvc.DomainTLSStatus(c.Context(), uint(idVal), tenantID)
	if err != nil {
		return domainServiceError(c, err)
	}
	return c.JSON(fiber.Map{"tls": result})
}

// GetAdminDomainMailAccessMode handles GET /admin/domains/:id/mail-access-mode.
func (h *Handler) GetAdminDomainMailAccessMode(c fiber.Ctx) error {
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
	mode, err := h.domainAdminSvc.GetMailAccessMode(c.Context(), uint(idVal), tenantID)
	if err != nil {
		return domainServiceError(c, err)
	}
	return c.JSON(fiber.Map{"mail_access_mode": mode})
}

// PostAdminDomainMailAccessMode handles POST /admin/domains/:id/mail-access-mode.
func (h *Handler) PostAdminDomainMailAccessMode(c fiber.Ctx) error {
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
	var req struct {
		Mode string `json:"mail_access_mode"`
	}
	c.Bind().JSON(&req)
	if err := h.domainAdminSvc.SetMailAccessMode(c.Context(), uint(idVal), tenantID, req.Mode); err != nil {
		if err == domain.ErrInvalidMailAccessMode {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return domainServiceError(c, err)
	}
	return c.JSON(fiber.Map{"mail_access_mode": req.Mode})
}

// GetEnterpriseDomainDNS returns DNS health for an enterprise domain.
// GET /enterprise/domains/:id/dns
func (h *Handler) GetEnterpriseDomainDNS(c fiber.Ctx) error {
	if h.customerDomainSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain dns service not available"})
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

	// expectedMX is passed through as configured (may be empty). The
	// fallback default ("mail.<domain-name>") is applied inside the
	// service, which resolves the real domain name from id+tenantID —
	// the handler only has the numeric :id, so building the fallback
	// hostname here would incorrectly embed the id, not the domain name.
	health, err := h.customerDomainSvc.GetEnterpriseDNS(c.Context(), tenantID, id, h.cfg.CoreMail.ExpectedMX)
	if err != nil {
		if errors.Is(err, customerdomain.ErrDomainNotFound) {
			return respondAPIError(c, fiber.StatusNotFound, "DOMAIN_NOT_FOUND", "Domain not found.")
		}
		h.logger.Error("get enterprise domain dns", zap.Error(err))
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
	return c.JSON(health)
}

// VerifyEnterpriseDomainDNS runs live DNS verification for an enterprise domain.
// POST /enterprise/domains/:id/dns/verify
func (h *Handler) VerifyEnterpriseDomainDNS(c fiber.Ctx) error {
	if h.customerDomainSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "domain dns service not available"})
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

	// See GetEnterpriseDomainDNS above: the fallback is applied in the
	// service once the real domain name is known.
	health, err := h.customerDomainSvc.VerifyEnterpriseDNS(c.Context(), tenantID, id, h.cfg.CoreMail.ExpectedMX)
	if err != nil {
		if errors.Is(err, customerdomain.ErrDomainNotFound) {
			return respondAPIError(c, fiber.StatusNotFound, "DOMAIN_NOT_FOUND", "Domain not found.")
		}
		if errors.Is(err, customerdomain.ErrVerificationCooldown) {
			// health here (if non-nil) is the last successful snapshot —
			// the 429 body must still carry it so the client never has to
			// discard good data just because this particular call was
			// rate-limited. An empty snapshot (never verified before)
			// legitimately has no body to attach.
			if health != nil {
				if health.RetryAfterSeconds > 0 {
					c.Set("Retry-After", strconv.Itoa(health.RetryAfterSeconds))
				}
				return c.Status(fiber.StatusTooManyRequests).JSON(health)
			}
			return respondAPIError(c, fiber.StatusTooManyRequests, "VERIFICATION_COOLDOWN", "Verification cooldown active, try again later.")
		}
		h.logger.Error("verify enterprise domain dns", zap.Error(err))
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
	return c.JSON(health)
}
