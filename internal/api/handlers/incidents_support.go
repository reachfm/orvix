package handlers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/capability"
	"github.com/orvix/orvix/internal/incident"
	"github.com/orvix/orvix/internal/supportaccess"

	// middleware provides support-access enforcement.
	"github.com/orvix/orvix/internal/api/middleware"
)

// ── Incident Management (platform-only) ───────────────────────────

func (h *Handler) incidentService() *incident.Service {
	if h.incidentSvc == nil {
		sqlDB, _ := h.db.DB()
		h.incidentSvc = incident.NewService(incident.NewRepository(sqlDB))
		_ = h.incidentSvc.EnsureSchema(context.Background())
	}
	return h.incidentSvc
}

func (h *Handler) CreateIncident(c fiber.Ctx) error {
	var req struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Severity    string   `json:"severity"`
		Services    []string `json:"services"`
		Regions     []string `json:"regions"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Title == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title is required"})
	}
	inc, err := h.incidentService().Create(c.Context(), req.Title, req.Description, incident.Severity(req.Severity), req.Services, req.Regions, nil)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAudit(c, "incident.create", fmt.Sprintf("title:%s", req.Title))
	return c.Status(fiber.StatusCreated).JSON(inc)
}

func (h *Handler) UpdateIncident(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid incident id"})
	}
	var req struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	operator := fmt.Sprintf("user:%v", c.Locals("user_id"))
	inc, err := h.incidentService().Update(c.Context(), uint(id), incident.Status(req.Status), req.Message, operator)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAudit(c, "incident.update", fmt.Sprintf("id:%d status:%s", id, req.Status))
	return c.JSON(inc)
}

func (h *Handler) GetIncident(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 64)
	inc, err := h.incidentService().Get(c.Context(), uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "incident not found"})
	}
	return c.JSON(inc)
}

func (h *Handler) ListIncidents(c fiber.Ctx) error {
	status := c.Query("status")
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	list, err := h.incidentService().List(c.Context(), status, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"incidents": list})
}

func (h *Handler) GetIncidentTimeline(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 64)
	tl, err := h.incidentService().Timeline(c.Context(), uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"events": tl})
}

func (h *Handler) GetPublicStatus(c fiber.Ctx) error {
	st, err := h.incidentService().PublicStatus(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(st)
}

// ── Support Access (platform-only) ────────────────────────────────

func (h *Handler) supportAccessService() *supportaccess.Service {
	if h.supportAccessSvc == nil {
		sqlDB, _ := h.db.DB()
		h.supportAccessSvc = supportaccess.NewService(supportaccess.NewRepository(sqlDB))
		_ = h.supportAccessSvc.EnsureSchema(context.Background())
	}
	return h.supportAccessSvc
}

func (h *Handler) CreateSupportAccessGrant(c fiber.Ctx) error {
	var req struct {
		TicketRef           string `json:"ticket_ref"`
		Reason              string `json:"reason"`
		TargetTenantID      uint   `json:"target_tenant_id"`
		PermissionScope     string `json:"permission_scope"`
		DurationHours       int    `json:"duration_hours"`
		EmergencyBreakGlass bool   `json:"emergency_break_glass"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	grantedByID, _ := c.Locals("user_id").(uint)
	dur := time.Duration(req.DurationHours) * time.Hour
	if dur <= 0 {
		dur = 4 * time.Hour
	}
	g, err := h.supportAccessService().RequestGrant(c.Context(), req.TicketRef, req.Reason, req.TargetTenantID, grantedByID, req.PermissionScope, dur, req.EmergencyBreakGlass)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAudit(c, "support.access.grant.requested", fmt.Sprintf("tenant:%d ticket:%s", req.TargetTenantID, req.TicketRef))
	return c.Status(fiber.StatusCreated).JSON(g)
}

func (h *Handler) ActivateSupportAccessGrant(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 64)
	g, err := h.supportAccessService().ActivateGrant(c.Context(), uint(id))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAudit(c, "support.access.grant.activated", fmt.Sprintf("grant:%d tenant:%d", id, g.TargetTenantID))
	return c.JSON(g)
}

func (h *Handler) RevokeSupportAccessGrant(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 64)
	var req struct {
		Reason string `json:"reason"`
	}
	c.Bind().JSON(&req)
	g, err := h.supportAccessService().RevokeGrant(c.Context(), uint(id), req.Reason)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAudit(c, "support.access.grant.revoked", fmt.Sprintf("grant:%d reason:%s", id, req.Reason))
	return c.JSON(g)
}

func (h *Handler) GetSupportAccessGrant(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 64)
	g, err := h.supportAccessService().Get(c.Context(), uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "grant not found"})
	}
	return c.JSON(g)
}

func (h *Handler) ListSupportAccessGrants(c fiber.Ctx) error {
	tenantID := uint(0)
	if tidStr := c.Query("tenant_id"); tidStr != "" {
		if tid, err := strconv.ParseUint(tidStr, 10, 64); err == nil {
			tenantID = uint(tid)
		}
	}
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	list, err := h.supportAccessService().List(c.Context(), tenantID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"grants": list})
}

// writeAudit records a lightweight audit entry.
func (h *Handler) writeAudit(c fiber.Ctx, action, target string) {
	if h.auditStore == nil {
		return
	}
	uid, _ := c.Locals("user_id").(uint)
	role, _ := c.Locals("role").(string)
	_ = h.auditStore.Record(c.Context(), &audit.Entry{
		Actor:     fmt.Sprintf("user:%d", uid),
		Role:      role,
		Action:    action,
		Target:    target,
		Result:    "success",
		IP:        c.IP(),
		UserAgent: string(c.Request().Header.UserAgent()),
	})
}

// ── Support-access enforcement middleware ───────────────────────────

// SupportAccessMiddleware returns the support-access enforcement
// middleware bound to this handler's support-access service.
func (h *Handler) SupportAccessMiddleware() fiber.Handler {
	return middleware.SupportAccess(h.supportAccessService())
}

// SupportAccessMiddlewareForScope is used only by real tenant-resource
// routes. Tenant users remain on the normal tenant middleware; platform
// support actors must present an active grant for the target tenant.
func (h *Handler) SupportAccessMiddlewareForScope(scope string) fiber.Handler {
	support := middleware.SupportAccessWithScope(h.supportAccessService(), scope)
	return func(c fiber.Ctx) error {
		role, _ := c.Locals("role").(auth.Role)
		switch role {
		case auth.RoleTenantAdmin, auth.RoleTenantOperator, auth.RoleTenantSupport, auth.RoleTenantReadOnly:
			if _, err := auth.RequireTenantID(c); err != nil {
				return err
			}
			return c.Next()
		case auth.RolePlatformSuperAdmin, auth.RoleSuperAdmin:
			err := support(c)
			if access, ok := middleware.SupportContext(c); ok {
				result := "success"
				if err != nil || c.Response().StatusCode() >= fiber.StatusBadRequest {
					result = "denied"
				}
				h.recordSupportAccess(c, access, scope, result)
			}
			return err
		default:
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "insufficient permissions"})
		}
	}
}

// recordSupportAccess records each completed grant-scoped operation. The
// record deliberately captures the actor, target tenant, granted scope and
// request correlation identifier without writing the operator's free-form
// support reason into the audit stream.
func (h *Handler) recordSupportAccess(c fiber.Ctx, access *middleware.SupportAccessContext, scope, result string) {
	if h.auditStore == nil || access == nil {
		return
	}
	role, _ := c.Locals("role").(auth.Role)
	target := fmt.Sprintf("tenant:%d grant:%d scope:%s", access.TenantID, access.Grant.ID, scope)
	if requestID := c.Get("X-Request-ID"); requestID != "" {
		target += " request:" + requestID
	}
	_ = h.auditStore.Record(c.Context(), &audit.Entry{
		Actor:     fmt.Sprintf("user:%d", access.OperatorID),
		Role:      string(role),
		Action:    "support.access.use",
		Target:    target,
		Result:    result,
		IP:        c.IP(),
		UserAgent: string(c.Request().Header.UserAgent()),
		TenantID:  access.TenantID,
	})
}

// ── Runtime capabilities ───────────────────────────────────────────

// GetCapabilities returns the runtime capability registry derived from
// actual registered modules and services.
func (h *Handler) GetCapabilities(c fiber.Ctx) error {
	svc := h.capabilityService()
	caps := svc.Capabilities()
	return c.JSON(fiber.Map{"capabilities": caps})
}

func (h *Handler) capabilityService() *capability.Service {
	if h.capabilitySvc == nil {
		h.capabilitySvc = capability.NewService(h.registry, nil)
	}
	return h.capabilitySvc
}
