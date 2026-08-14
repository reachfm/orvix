package handlers

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/coremail/mailpolicy"
	"github.com/orvix/orvix/internal/platform/deliverability"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/platform/mailcontrol"
	"go.uber.org/zap"
)

// SetMailControlService wires the platform mail-control service.
func (h *Handler) SetMailControlService(svc *mailcontrol.Service) {
	h.mailControlSvc = svc
}

// SetMailAccessPolicy wires the canonical mailbox-level mail-access
// policy into the webmail send path. Wired by the router; nil
// disables enforcement (pre-policy test harnesses keep their
// behavior).
func (h *Handler) SetMailAccessPolicy(p *mailpolicy.Policy) {
	h.mailPolicy = p
}

// SetDeliverabilityService wires the platform deliverability/suppression
// service (Milestone 9 bounded context).
func (h *Handler) SetDeliverabilityService(svc *deliverability.Service) {
	h.deliverabilitySvc = svc
}

// SetPlatformIdempotencyStore wires the idempotency store used by
// platform control-plane mutations. nil disables idempotency with 503.
func (h *Handler) SetPlatformIdempotencyStore(s *kernel.IdempotencyStore) {
	h.platformIdem = s
}

func (h *Handler) deliverability() (*deliverability.Service, error) {
	if h.deliverabilitySvc == nil {
		return nil, kernel.NewError(kernel.ErrCodeUnavailable, "deliverability service is unavailable")
	}
	return h.deliverabilitySvc, nil
}

// StartDeliverabilityScheduler runs the bounded background jobs of the
// deliverability control plane: suppression-expiry reconciliation and
// signal retention purge. These jobs exist specifically so expiry is
// never resolved by request-time table scans — the delivery path
// performs only the single indexed point lookup.
func (h *Handler) StartDeliverabilityScheduler(ctx context.Context, interval time.Duration) {
	if h.deliverabilitySvc == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := h.deliverabilitySvc.ReconcileExpired(ctx); err != nil {
					h.logger.Warn("deliverability expiry reconciliation failed", zap.Error(err))
				} else if n > 0 {
					h.logger.Info("deliverability expiry reconciliation", zap.Int64("expired", n))
				}
				if n, err := h.deliverabilitySvc.PurgeOldSignals(ctx, 90*24*time.Hour); err != nil {
					h.logger.Warn("deliverability signal retention purge failed", zap.Error(err))
				} else if n > 0 {
					h.logger.Info("deliverability signal retention purge", zap.Int64("purged", n))
				}
			}
		}
	}()
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
	offset := queryIntDefault(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	f := deliverability.SuppressionFilter{
		TenantID: tenantID,
		Domain:   strings.TrimSpace(c.Query("domain")),
		Search:   strings.TrimSpace(c.Query("q")),
		Reason:   strings.TrimSpace(c.Query("reason")),
		Source:   strings.TrimSpace(c.Query("source")),
		State:    deliverability.SuppressionState(strings.TrimSpace(c.Query("state"))),
		Limit:    limit,
		Offset:   offset,
	}
	parseRFC3339 := func(key string) *time.Time {
		raw := strings.TrimSpace(c.Query(key))
		if raw == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil
		}
		u := t.UTC()
		return &u
	}
	f.CreatedFrom = parseRFC3339("created_from")
	f.CreatedTo = parseRFC3339("created_to")
	f.ExpiryFrom = parseRFC3339("expiry_from")
	f.ExpiryTo = parseRFC3339("expiry_to")
	list, total, err := svc.ListSuppressions(c.Context(), f)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"suppressions": list, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) GetPlatformSuppression(c fiber.Ctx) error {
	svc, err := h.deliverability()
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
	sup, err := svc.GetSuppression(c.Context(), id, tenantID)
	if err != nil {
		if errors.Is(err, deliverability.ErrSuppressionNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "suppression not found", "code": "NOT_FOUND"})
		}
		return errorResponse(c, err)
	}
	return c.JSON(sup)
}

func (h *Handler) GetPlatformSuppressionHistory(c fiber.Ctx) error {
	svc, err := h.deliverability()
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
	limit := queryIntDefault(c, "limit", 50)
	if limit < 1 || limit > 200 {
		limit = 50
	}
	events, err := svc.ListSuppressionEvents(c.Context(), id, tenantID, limit)
	if err != nil {
		if errors.Is(err, deliverability.ErrSuppressionNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "suppression not found", "code": "NOT_FOUND"})
		}
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"suppression_id": id, "events": events})
}

func (h *Handler) ReleasePlatformSuppression(c fiber.Ctx) error {
	svc, err := h.deliverability()
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
		Reason string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body", "code": "VALIDATION_FAILED"})
	}
	if err := svc.ReleaseSuppression(c.Context(), id, tenantID, h.platformActorID(c), req.Reason); err != nil {
		if errors.Is(err, deliverability.ErrSuppressionNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "suppression not found", "code": "NOT_FOUND"})
		}
		if errors.Is(err, deliverability.ErrSuppressionNotActive) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "suppression is not active", "code": "CONFLICT"})
		}
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id, "state": string(deliverability.SuppressionReleased)})
}

func (h *Handler) ReactivatePlatformSuppression(c fiber.Ctx) error {
	svc, err := h.deliverability()
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
		Reason    string     `json:"reason"`
		Source    string     `json:"source"`
		Notes     string     `json:"notes"`
		ExpiresAt *time.Time `json:"expires_at"`
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
	if err := svc.ReactivateSuppression(c.Context(), id, tenantID, h.platformActorID(c), reason, source, req.Notes, req.ExpiresAt); err != nil {
		if errors.Is(err, deliverability.ErrSuppressionNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "suppression not found", "code": "NOT_FOUND"})
		}
		if errors.Is(err, deliverability.ErrSuppressionActive) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "suppression is already active", "code": "CONFLICT"})
		}
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id, "state": string(deliverability.SuppressionActive)})
}

// DeletePlatformSuppression handles DELETE
// /api/v1/platform/suppressions/:tenant_id/:id — release semantics
// (history preserved) with typed confirmation.
func (h *Handler) DeletePlatformSuppression(c fiber.Ctx) error {
	svc, err := h.deliverability()
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
	if err := typedConfirm(c, "RELEASE-SUPPRESSION-"+strconv.FormatUint(uint64(id), 10)); err != nil {
		return err
	}
	if err := svc.ReleaseSuppression(c.Context(), id, tenantID, h.platformActorID(c), "operator release"); err != nil {
		if errors.Is(err, deliverability.ErrSuppressionNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "suppression not found", "code": "NOT_FOUND"})
		}
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id})
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
		Address   string     `json:"address"`
		Reason    string     `json:"reason"`
		Source    string     `json:"source"`
		Notes     string     `json:"notes,omitempty"`
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
	windowStart, err := time.Parse(time.RFC3339, c.Query("start"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "start must be RFC3339", "code": "VALIDATION_FAILED"})
	}
	windowEnd, err := time.Parse(time.RFC3339, c.Query("end"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "end must be RFC3339", "code": "VALIDATION_FAILED"})
	}
	// Backward-compatible dimension view (legacy contract).
	dim := deliverability.Dimension(strings.TrimSpace(c.Query("dimension")))
	dimValue := strings.TrimSpace(c.Query("value"))
	if dimValue == "" {
		dimValue = strconv.FormatUint(uint64(tenantID), 10)
	}
	if dim == "" {
		dim = deliverability.DimensionTenant
	}
	var window *deliverability.WindowMetrics
	if dim == deliverability.DimensionTenant || dim == deliverability.DimensionSendingDomain ||
		dim == deliverability.DimensionRecipientDomain || dim == deliverability.DimensionRelayProvider {
		m, err := svc.Metrics(c.Context(), dim, dimValue, windowStart, windowEnd)
		if err != nil {
			return errorResponse(c, err)
		}
		window = m
	} else {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "dimension must be tenant, sending_domain, recipient_domain, or relay_provider", "code": "VALIDATION_FAILED"})
	}
	summary, err := svc.MetricsSummary(c.Context(), tenantID, windowStart, windowEnd)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{
		"window":        window,
		"summary":       summary,
		"volume":        summary.Volume,
		"delivered":     summary.Delivered,
		"bounced":       summary.Bounced,
		"complaints":    summary.Complaints,
		"delivery_rate": summary.DeliveryRate,
		"bounce_rate":   summary.BounceRate,
		"complaint_rate": func() float64 {
			if summary.Volume > 0 {
				return float64(summary.Complaints) / float64(summary.Volume)
			}
			return 0
		}(),
	})
}

// ListPlatformDeliverabilityEvents handles
// GET /api/v1/platform/deliverability/:tenant_id/events — the
// tenant's real delivery evidence with filters, bounded pagination,
// and safe projections.
func (h *Handler) ListPlatformDeliverabilityEvents(c fiber.Ctx) error {
	svc, err := h.deliverability()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	limit := queryIntDefault(c, "limit", 100)
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := queryIntDefault(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	f := deliverability.EventFilter{
		TenantID: tenantID,
		Domain:   strings.TrimSpace(c.Query("domain")),
		Type:     deliverability.SignalType(strings.TrimSpace(c.Query("type"))),
		Provider: strings.TrimSpace(c.Query("provider")),
		Limit:    limit,
		Offset:   offset,
	}
	if f.Type != "" && !f.Type.IsValid() {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid event type", "code": "VALIDATION_FAILED"})
	}
	if raw := strings.TrimSpace(c.Query("start")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "start must be RFC3339", "code": "VALIDATION_FAILED"})
		}
		u := t.UTC()
		f.Start = &u
	}
	if raw := strings.TrimSpace(c.Query("end")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "end must be RFC3339", "code": "VALIDATION_FAILED"})
		}
		u := t.UTC()
		f.End = &u
	}
	events, total, err := svc.ListEvents(c.Context(), f)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"events": events, "total": total, "limit": limit, "offset": offset})
}

// GetPlatformDeliverabilityEvent handles
// GET /api/v1/platform/deliverability/:tenant_id/events/:id — one
// safe event projection, tenant-scoped.
func (h *Handler) GetPlatformDeliverabilityEvent(c fiber.Ctx) error {
	svc, err := h.deliverability()
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
	ev, err := svc.GetEvent(c.Context(), id, tenantID)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(ev)
}
