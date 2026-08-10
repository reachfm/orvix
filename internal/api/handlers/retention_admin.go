package handlers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/platform/retention"
	"go.uber.org/zap"
)

// ── Policies ──────────────────────────────────────────────────────

// PostRetentionPolicy creates a retention policy at a given hierarchy
// level (platform/tenant/domain/mailbox).
func (h *Handler) PostRetentionPolicy(c fiber.Ctx) error {
	if h.retentionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention service not available"})
	}
	var req retention.Policy
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	p, err := h.retentionSvc.CreatePolicy(c.Context(), req)
	if err != nil {
		return retentionActionError(c, err)
	}
	h.writeAuditLog(c, "retention.policy.create", fmt.Sprintf("level:%s|tenant:%d|domain:%d|mailbox:%d", p.Level, p.TenantID, p.DomainID, p.MailboxID))
	return c.Status(fiber.StatusCreated).JSON(p)
}

// GetRetentionEffectivePolicy resolves the single most-specific policy
// applicable to the given scope — the hierarchy resolution an operator
// or the purge planner actually uses.
func (h *Handler) GetRetentionEffectivePolicy(c fiber.Ctx) error {
	if h.retentionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention service not available"})
	}
	tenantID := parseUintQuery(c, "tenant_id")
	domainID := parseUintQuery(c, "domain_id")
	mailboxID := parseUintQuery(c, "mailbox_id")
	category := c.Query("category")
	p, err := h.retentionSvc.ResolvePolicy(c.Context(), tenantID, domainID, mailboxID, category)
	if err != nil {
		h.logger.Error("resolve retention policy failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to resolve retention policy"})
	}
	if p == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no applicable retention policy"})
	}
	return c.JSON(p)
}

// ── Legal holds ──────────────────────────────────────────────────

func (h *Handler) PostRetentionLegalHold(c fiber.Ctx) error {
	if h.retentionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention service not available"})
	}
	var req struct {
		ScopeKind string     `json:"scope_kind"`
		ScopeID   uint       `json:"scope_id"`
		CaseRef   string     `json:"case_ref"`
		Reason    string     `json:"reason"`
		EndsAt    *time.Time `json:"ends_at"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.ScopeKind == "" || req.ScopeID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "scope_kind and scope_id are required"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	hold, err := h.retentionSvc.PlaceLegalHold(c.Context(), req.ScopeKind, req.ScopeID, req.CaseRef, req.Reason, actorID, req.EndsAt)
	if err != nil {
		return retentionActionError(c, err)
	}
	h.writeAuditLog(c, "retention.legal_hold.place", fmt.Sprintf("scope:%s:%d|case:%s", req.ScopeKind, req.ScopeID, req.CaseRef))
	return c.Status(fiber.StatusCreated).JSON(hold)
}

// GetRetentionLegalHolds lists active legal holds for a scope.
func (h *Handler) GetRetentionLegalHolds(c fiber.Ctx) error {
	if h.retentionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention service not available"})
	}
	scopeKind := c.Query("scope_kind")
	scopeID := parseUintQuery(c, "scope_id")
	if scopeKind == "" || scopeID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "scope_kind and scope_id query params are required"})
	}
	holds, err := h.retentionSvc.ListActiveHolds(c.Context(), scopeKind, scopeID)
	if err != nil {
		h.logger.Error("list legal holds failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list legal holds"})
	}
	return c.JSON(fiber.Map{"holds": holds})
}

func (h *Handler) PostRetentionLegalHoldRelease(c fiber.Ctx) error {
	if h.retentionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention service not available"})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid legal hold id"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	if err := h.retentionSvc.ReleaseLegalHold(c.Context(), uint(idVal), actorID); err != nil {
		return retentionActionError(c, err)
	}
	h.writeAuditLog(c, "retention.legal_hold.release", fmt.Sprintf("hold_id:%d", idVal))
	return c.JSON(fiber.Map{"status": "released"})
}

// ── Purge (dry-run + confirmed execution) ─────────────────────────

func (h *Handler) PostRetentionPurgePlan(c fiber.Ctx) error {
	if h.retentionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention service not available"})
	}
	var req struct {
		ScopeKind string    `json:"scope_kind"`
		ScopeID   uint      `json:"scope_id"`
		OlderThan time.Time `json:"older_than"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.ScopeKind == "" || req.ScopeID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "scope_kind and scope_id are required"})
	}
	if req.OlderThan.IsZero() {
		req.OlderThan = time.Now().UTC()
	}
	plan, err := h.retentionSvc.PlanPurge(c.Context(), req.ScopeKind, req.ScopeID, req.OlderThan)
	if err != nil {
		h.logger.Error("plan purge failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to plan purge"})
	}
	return c.JSON(plan)
}

// PostRetentionPurgeExecute requires the exact typed confirmation
// phrase and a reason, rechecks the legal hold immediately before
// executing (not just at plan time — ExecutePurge itself rechecks
// per-batch too), and is idempotent on the caller-supplied
// idempotency key.
func (h *Handler) PostRetentionPurgeExecute(c fiber.Ctx) error {
	if h.retentionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention service not available"})
	}
	var req struct {
		ScopeKind string    `json:"scope_kind"`
		ScopeID   uint      `json:"scope_id"`
		OlderThan time.Time `json:"older_than"`
		Confirm   string    `json:"confirm"`
		Reason    string    `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.ScopeKind == "" || req.ScopeID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "scope_kind and scope_id are required"})
	}
	if strings.TrimSpace(req.Reason) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "reason is required"})
	}
	if req.Confirm != retention.PurgeConfirmationPhrase {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "purge requires typed confirmation: " + retention.PurgeConfirmationPhrase})
	}
	if req.OlderThan.IsZero() {
		req.OlderThan = time.Now().UTC()
	}
	idemKey := strings.TrimSpace(c.Get("Idempotency-Key"))
	actorID, _ := c.Locals("user_id").(uint)
	count, err := h.retentionSvc.ExecutePurge(c.Context(), req.ScopeKind, req.ScopeID, req.OlderThan, req.Confirm, idemKey, actorID)
	if err != nil {
		return retentionActionError(c, err)
	}
	h.writeAuditLog(c, "retention.purge.execute", fmt.Sprintf("scope:%s:%d|reason:%s|purged:%d", req.ScopeKind, req.ScopeID, req.Reason, count))
	return c.JSON(fiber.Map{"purged": count})
}

// ── Recovery ───────────────────────────────────────────────────────
//
// Recovery of a purge target is only meaningful BEFORE the hard purge
// runs — once ExecutePurge's PurgeBatchEligible has removed rows they
// are gone, by design (a "soft-deleted, recovery window not yet
// expired" mailbox is not yet purge-eligible in the first place, see
// internal/admin/mailbox.CountEligibleForPurge / PurgeBatchEligible's
// WHERE clause). The real undo capability for a still-recoverable
// soft-deleted mailbox already exists at the mailbox admin layer
// (mailboxAdminSvc.RestoreMailbox, wired to POST
// /enterprise/mailboxes/:id/restore) — this endpoint exposes the SAME
// capability under the retention namespace (so retention chain-of-
// custody records the recovery as a retention-lifecycle event) rather
// than duplicating the undelete logic.
func (h *Handler) PostRetentionRecoverMailbox(c fiber.Ctx) error {
	if h.retentionSvc == nil || h.mailboxAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention/mailbox admin service not available"})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	m, err := h.mailboxAdminSvc.RestoreMailbox(c.Context(), uint(idVal), tenantID)
	if err != nil {
		h.logger.Error("retention mailbox recovery failed", zap.Uint64("mailbox_id", idVal), zap.Error(err))
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	}
	actorID, _ := c.Locals("user_id").(uint)
	_ = h.retentionSvc.RecordCustody(c.Context(), "recover", "mailbox", uint(idVal), actorID, 1, nil)
	h.writeAuditLog(c, "retention.mailbox.recover", fmt.Sprintf("mailbox_id:%d", idVal))
	return c.JSON(m)
}

// ── Chain of custody ──────────────────────────────────────────────

// GetRetentionCustody returns chain-of-custody evidence records for a
// scope — IDs/metadata/hashes only, never message bodies or other
// sensitive content (ChainOfCustodyEvent carries no such field).
func (h *Handler) GetRetentionCustody(c fiber.Ctx) error {
	if h.retentionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "retention service not available"})
	}
	scopeKind := c.Query("scope_kind")
	scopeID := parseUintQuery(c, "scope_id")
	if scopeKind == "" || scopeID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "scope_kind and scope_id query params are required"})
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	events, err := h.retentionSvc.ListCustodyEvents(c.Context(), scopeKind, scopeID, limit, offset)
	if err != nil {
		h.logger.Error("list chain of custody failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list chain of custody records"})
	}
	return c.JSON(fiber.Map{"events": events})
}

// ── helpers ────────────────────────────────────────────────────────

func parseUintQuery(c fiber.Ctx, key string) uint {
	v, _ := strconv.ParseUint(c.Query(key), 10, 64)
	return uint(v)
}

func retentionActionError(c fiber.Ctx, err error) error {
	switch err {
	case retention.ErrLegalHoldActive:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	case retention.ErrHoldNotFound:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	case retention.ErrConfirmationRequired:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	case retention.ErrInvalidPolicy:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
}
