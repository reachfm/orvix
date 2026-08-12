package handlers

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/platform/deliverability"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/platform/mailcontrol"
)

// SetMailControlService wires the platform mail-control service.
func (h *Handler) SetMailControlService(svc *mailcontrol.Service) {
	h.mailControlSvc = svc
}

// SetDeliverabilityService wires the platform deliverability/suppression
// service (Milestone 9 bounded context).
func (h *Handler) SetDeliverabilityService(svc *deliverability.Service) {
	h.deliverabilitySvc = svc
}

func (h *Handler) deliverability() (*deliverability.Service, error) {
	if h.deliverabilitySvc == nil {
		return nil, kernel.NewError(kernel.ErrCodeUnavailable, "deliverability service is unavailable")
	}
	return h.deliverabilitySvc, nil
}

func (h *Handler) mailControl() (*mailcontrol.Service, error) {
	if h.mailControlSvc == nil {
		return nil, kernel.NewError(kernel.ErrCodeUnavailable, "platform mail control is unavailable")
	}
	return h.mailControlSvc, nil
}

// platformActorID returns the authenticated platform operator id.
func (h *Handler) platformActorID(c fiber.Ctx) uint {
	id, _ := c.Locals("user_id").(uint)
	return id
}

func parseTenantParam(c fiber.Ctx) (uint, error) {
	v, err := strconv.ParseUint(c.Params("tenant_id"), 10, 64)
	if err != nil || v == 0 {
		return 0, kernel.NewError(kernel.ErrCodeValidation, "a valid tenant_id is required")
	}
	return uint(v), nil
}

func parseIDParam(c fiber.Ctx, name string) (uint, error) {
	v, err := strconv.ParseUint(c.Params(name), 10, 64)
	if err != nil || v == 0 {
		return 0, kernel.NewError(kernel.ErrCodeValidation, "a valid "+name+" is required")
	}
	return uint(v), nil
}

func mailControlPage(c fiber.Ctx) (limit, offset int) {
	limit = queryIntDefault(c, "limit", 25)
	if limit < 1 || limit > 200 {
		limit = 25
	}
	offset = queryIntDefault(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func queryIntDefault(c fiber.Ctx, key string, def int) int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// ── Platform domains ───────────────────────────────────────────────

func (h *Handler) ListPlatformDomains(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	limit, offset := mailControlPage(c)
	out, err := svc.ListDomains(c.Context(), mailcontrol.PlatformDomainFilter{
		TenantID: tenantID, Search: strings.TrimSpace(c.Query("q")), Status: strings.TrimSpace(c.Query("status")),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) GetPlatformDomain(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	out, err := svc.GetDomain(c.Context(), id, tenantID)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) SetPlatformDomainStatus(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Status) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body", "code": "VALIDATION_FAILED"})
	}
	if err := svc.SetDomainStatus(c.Context(), id, tenantID, req.Status, req.Reason, h.platformActorID(c)); err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id})
}

func (h *Handler) SetPlatformDomainMailAccessMode(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		MailAccessMode string `json:"mail_access_mode"`
	}
	if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.MailAccessMode) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body", "code": "VALIDATION_FAILED"})
	}
	if err := svc.SetMailAccessMode(c.Context(), id, tenantID, req.MailAccessMode, h.platformActorID(c)); err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id, "mail_access_mode": req.MailAccessMode})
}

// ── Platform mailboxes ─────────────────────────────────────────────

func (h *Handler) ListPlatformMailboxes(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	limit, offset := mailControlPage(c)
	var domainID uint
	if raw := strings.TrimSpace(c.Query("domain_id")); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			domainID = uint(n)
		}
	}
	out, err := svc.ListMailboxes(c.Context(), mailcontrol.PlatformMailboxFilter{
		TenantID: tenantID, DomainID: domainID, Search: strings.TrimSpace(c.Query("q")),
		Status: strings.TrimSpace(c.Query("status")), Limit: limit, Offset: offset,
	})
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) GetPlatformMailbox(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	out, err := svc.GetMailbox(c.Context(), id, tenantID)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) SetPlatformMailboxStatus(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Status) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body", "code": "VALIDATION_FAILED"})
	}
	if err := svc.UpdateMailboxStatus(c.Context(), id, tenantID, req.Status, req.Reason, h.platformActorID(c)); err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id})
}

func (h *Handler) SetPlatformMailboxQuota(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		QuotaMB int64 `json:"quota_mb"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.QuotaMB <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "quota_mb must be a positive integer", "code": "VALIDATION_FAILED"})
	}
	if err := svc.UpdateMailboxQuota(c.Context(), id, tenantID, req.QuotaMB, h.platformActorID(c)); err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id, "quota_mb": req.QuotaMB})
}

func (h *Handler) ResetPlatformMailboxPassword(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	pw, err := svc.ResetMailboxPassword(c.Context(), id, tenantID, h.platformActorID(c))
	if err != nil {
		return errorResponse(c, err)
	}
	// The generated password is returned exactly once; the operator
	// must copy it now. It is never logged, cached, or retrievable
	// again.
	return c.JSON(fiber.Map{"status": "ok", "id": id, "generated_password": pw, "show_once": true})
}

func (h *Handler) DeletePlatformMailbox(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	// Typed confirmation is required for this destructive action.
	confirmation := strings.TrimSpace(c.Get("X-Confirm"))
	if confirmation == "" || confirmation != mailcontrol.ConfirmMailboxPurge+strconv.FormatUint(uint64(id), 10) {
		return c.Status(fiber.StatusPreconditionRequired).JSON(fiber.Map{"error": "typed confirmation required", "code": "PRECONDITION_FAILED"})
	}
	if err := svc.SoftDeleteMailbox(c.Context(), id, tenantID, h.platformActorID(c)); err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id})
}

// ── Platform aliases ───────────────────────────────────────────────

func (h *Handler) ListPlatformAliases(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	limit, offset := mailControlPage(c)
	var domainID uint
	if raw := strings.TrimSpace(c.Query("domain_id")); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			domainID = uint(n)
		}
	}
	out, err := svc.ListAliases(c.Context(), mailcontrol.PlatformAliasFilter{
		TenantID: tenantID, DomainID: domainID, Search: strings.TrimSpace(c.Query("q")),
		Destination: strings.TrimSpace(c.Query("to")), Limit: limit, Offset: offset,
	})
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) GetPlatformAlias(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	out, err := svc.GetAlias(c.Context(), id, tenantID)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) CreatePlatformAlias(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		DomainID uint   `json:"domain_id"`
		FromAddr string `json:"from_addr"`
		ToAddr   string `json:"to_addr"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body", "code": "VALIDATION_FAILED"})
	}
	out, err := svc.CreateAlias(c.Context(), tenantID, req.DomainID, req.FromAddr, req.ToAddr, h.platformActorID(c))
	if err != nil {
		return errorResponse(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

func (h *Handler) DeletePlatformAlias(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	if err := svc.DeleteAlias(c.Context(), id, tenantID, h.platformActorID(c)); err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id})
}

// ── Platform groups ────────────────────────────────────────────────

func (h *Handler) ListPlatformGroups(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	limit, offset := mailControlPage(c)
	out, err := svc.ListGroups(c.Context(), mailcontrol.PlatformGroupFilter{
		TenantID: tenantID, Search: strings.TrimSpace(c.Query("q")), Limit: limit, Offset: offset,
	})
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) GetPlatformGroup(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	out, err := svc.GetGroup(c.Context(), id, tenantID)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) ListPlatformGroupMembers(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	members, err := svc.ListGroupMembers(c.Context(), id, tenantID)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"group_id": id, "members": members})
}

// ── Platform bulk mailbox operations ───────────────────────────────

func (h *Handler) BulkPlatformMailboxStatus(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req mailcontrol.BulkMailboxRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body", "code": "VALIDATION_FAILED"})
	}
	req.TenantID = tenantID
	out, err := svc.BulkMailboxStatus(c.Context(), req, h.platformActorID(c))
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(out)
}

// -- Platform suppression management -------------------------------

func (h *Handler) ListPlatformSuppressions(c fiber.Ctx) error {
	svc, err := h.deliverability()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	limit := queryIntDefault(c, "limit", 50)
	if limit < 1 || limit > 500 {
		limit = 50
	}
	afterID := uint(queryIntDefault(c, "after_id", 0))
	list, err := svc.ListSuppressions(c.Context(), tenantID, limit, afterID)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"suppressions": list})
}

func (h *Handler) AddPlatformSuppression(c fiber.Ctx) error {
	svc, err := h.deliverability()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		Address   string `json:"address"`
		Reason    string `json:"reason"`
		Source    string `json:"source"`
		Notes     string `json:"notes,omitempty"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body", "code": "VALIDATION_FAILED"})
	}
	reason := deliverability.SuppressionReason(strings.ToLower(strings.TrimSpace(req.Reason)))
	if reason != deliverability.SuppressionHardBounce && reason != deliverability.SuppressionComplaint && reason != deliverability.SuppressionManual {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": "reason must be hard_bounce, complaint, or manual", "code": "VALIDATION_FAILED"})
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "platform_operator"
	}
	sup, err := svc.AddSuppression(c.Context(), tenantID, req.Address, reason, source, h.platformActorID(c), req.Notes, req.ExpiresAt)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(sup)
}

func (h *Handler) RemovePlatformSuppression(c fiber.Ctx) error {
	svc, err := h.deliverability()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	address := strings.TrimSpace(c.Query("address"))
	if address == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "address query param required", "code": "VALIDATION_FAILED"})
	}
	if err := svc.RemoveSuppression(c.Context(), tenantID, address, h.platformActorID(c)); err != nil {
		if errors.Is(err, deliverability.ErrSuppressionNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "suppression not found", "code": "NOT_FOUND"})
		}
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "address": address})
}

// -- Platform deliverability metrics --------------------------------

func (h *Handler) GetPlatformDeliverabilityMetrics(c fiber.Ctx) error {
	svc, err := h.deliverability()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	dim := deliverability.Dimension(strings.TrimSpace(c.Query("dimension")))
	if dim != deliverability.DimensionTenant && dim != deliverability.DimensionSendingDomain &&
		dim != deliverability.DimensionRecipientDomain && dim != deliverability.DimensionRelayProvider {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "dimension must be tenant, sending_domain, recipient_domain, or relay_provider", "code": "VALIDATION_FAILED"})
	}
	dimValue := strings.TrimSpace(c.Query("value"))
	if dimValue == "" {
		dimValue = strconv.FormatUint(uint64(tenantID), 10)
	}
	windowStart, err := time.Parse(time.RFC3339, c.Query("start"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "start must be RFC3339", "code": "VALIDATION_FAILED"})
	}
	windowEnd, err := time.Parse(time.RFC3339, c.Query("end"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "end must be RFC3339", "code": "VALIDATION_FAILED"})
	}
	m, err := svc.Metrics(c.Context(), dim, dimValue, windowStart, windowEnd)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(m)
}
