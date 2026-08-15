package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── Platform domain lifecycle (Phase 8 production-acceptance remediation) ──
//
// POST /api/v1/platform/domains/:tenant_id/:id/deactivate
//
// A canonical, audited platform domain deactivation/soft-delete
// workflow. Distinct from the existing tenant-scoped
// domain.Service.DeleteDomain: that one is a genuine hard soft-delete
// (sets deleted_at, purges the live DKIM config) intended for a tenant
// admin removing their own domain outright. This endpoint is a
// reversible-in-principle "deactivate" — it sets status=deactivated
// and deactivated_at, but never touches deleted_at and never purges
// DKIM config or DKIM/audit history, because deactivation must not
// destroy evidence a later investigation or reactivation might need.
//
// Authorization: gated on the dedicated PermPlatformDomainsDeactivate
// permission (granted only to platform_super_admin and its legacy
// super_admin analog) — distinct from the general PermDomainsWrite
// that platform domain create/update already reuse from the tenant
// surface, because deactivation is destructive-adjacent and warrants
// its own authority (same reasoning as PermPlatformUsersWrite).
//
// The handler only does HTTP concerns; deactivatePlatformDomainTx is
// the single source of truth for the transactional work, callable
// directly from tests.
//
// What deactivation does, atomically, or not at all:
//   - re-verifies tenant ownership INSIDE the SQL predicate (the path
//     tenant_id is never trusted without also filtering by it);
//   - applies optimistic concurrency via expected_version — a stale
//     version is a 409, mirroring the existing mailbox
//     mail_access_mode convention;
//   - refuses while protected dependencies exist: active mailboxes,
//     active aliases, or mail still in flight in the queue (pending/
//     leased/deferred) for this domain — each refusal names which
//     dependency blocked it;
//   - sets status='deactivated', deactivated_at=now,
//     deactivation_reason=<reason>, bumps version;
//   - never touches deleted_at, coremail_dkim_config, or any history
//     table — DKIM material and history survive deactivation intact;
//   - writes one audit entry (actor/tenant/target/reason/request_id).
//
// Idempotency: POST requires an Idempotency-Key (h.platformIdempotent).
// Calling deactivate again on an already-deactivated domain (a
// different key — e.g. a genuine retry) is not an error: the
// operation is a no-op success rather than re-running the dependency
// checks and version bump a second time.
func (h *Handler) DeactivatePlatformDomain(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()

	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	domainID := uint(id)

	body, err := platformMutationBody(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		Confirm         string `json:"confirm"`
		Reason          string `json:"reason"`
		ExpectedVersion int    `json:"expected_version"`
	}
	if err := bindStrictJSONBytes(body, &req); err != nil {
		return strictJSONError(c, err)
	}
	wantConfirm := fmt.Sprintf("DEACTIVATE-DOMAIN-%d", domainID)
	if req.Confirm != wantConfirm {
		return fiber.NewError(fiber.StatusPreconditionFailed, "type the confirmation phrase exactly: "+wantConfirm)
	}
	if strings.TrimSpace(req.Reason) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "reason is required")
	}
	if req.ExpectedVersion < 1 {
		return fiber.NewError(fiber.StatusBadRequest, "a positive expected_version is required")
	}

	c.Set("Cache-Control", "no-store")

	actorID := h.platformActorID(c)
	scope := "platform.domain.deactivate:POST:/platform/domains/" + strconv.FormatUint(uint64(tenantID), 10) + "/" + strconv.FormatUint(uint64(domainID), 10) + ":actor:" + strconv.FormatUint(uint64(actorID), 10)

	return h.platformIdempotent(c, scope, func() (int, any, any, error) {
		result, err := deactivatePlatformDomainTx(c.Context(), sqlDB, h.dialect, tenantID, domainID, req.ExpectedVersion, req.Reason)
		if err != nil {
			return 0, nil, nil, err
		}
		if h.auditStore != nil {
			_ = h.auditStore.Record(c.Context(), &audit.Entry{
				Actor:     fmt.Sprintf("user:%d", actorID),
				Action:    "platform_domain.deactivate",
				Target:    fmt.Sprintf("domain:%d|tenant:%d|reason:%s|request_id:%s", domainID, tenantID, req.Reason, result.RequestID),
				Result:    "success",
				TenantID:  tenantID,
				IP:        c.IP(),
				UserAgent: c.Get("User-Agent"),
				Timestamp: time.Now().UTC(),
			})
		}
		resp := fiber.Map{
			"id":         domainID,
			"tenant_id":  tenantID,
			"status":     "deactivated",
			"version":    result.NewVersion,
			"request_id": result.RequestID,
		}
		return fiber.StatusOK, resp, resp, nil
	})
}

type deactivateDomainResult struct {
	RequestID  string
	NewVersion int
}

// deactivatePlatformDomainTx is the canonical platform-domain-
// lifecycle service operation.
func deactivatePlatformDomainTx(ctx context.Context, sqlDB *sql.DB, dialect *dbdialect.Info, tenantID, domainID uint, expectedVersion int, reason string) (*deactivateDomainResult, error) {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var version int
	var deactivatedAt sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT version, deactivated_at FROM coremail_domains WHERE id = `+dialect.Placeholder(1)+` AND tenant_id = `+dialect.Placeholder(2)+` AND deleted_at IS NULL`,
		domainID, tenantID).Scan(&version, &deactivatedAt)
	if err == sql.ErrNoRows {
		return nil, kernel.NewError(kernel.ErrCodeNotFound, "domain not found")
	}
	if err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}

	// Already deactivated: idempotent success, no dependency re-check,
	// no version bump — a genuine retry (different Idempotency-Key)
	// against a domain some other request already deactivated must not
	// error just because the world moved on.
	if deactivatedAt.Valid {
		reqIDBytes := make([]byte, 8)
		_, _ = rand.Read(reqIDBytes)
		if err := tx.Commit(); err != nil {
			return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
		}
		committed = true
		return &deactivateDomainResult{RequestID: hex.EncodeToString(reqIDBytes), NewVersion: version}, nil
	}

	if version != expectedVersion {
		return nil, kernel.NewError(kernel.ErrCodeConflict, fmt.Sprintf("domain version is no longer %d", expectedVersion))
	}

	var mbCount int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = `+dialect.Placeholder(1)+` AND tenant_id = `+dialect.Placeholder(2)+` AND deleted_at IS NULL`,
		domainID, tenantID).Scan(&mbCount); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}
	if mbCount > 0 {
		return nil, kernel.NewError(kernel.ErrCodeConflict, "domain has active mailboxes; remove or reassign them before deactivation")
	}

	var alCount int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM coremail_aliases WHERE domain_id = `+dialect.Placeholder(1)+` AND tenant_id = `+dialect.Placeholder(2)+` AND deleted_at IS NULL`,
		domainID, tenantID).Scan(&alCount); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}
	if alCount > 0 {
		return nil, kernel.NewError(kernel.ErrCodeConflict, "domain has active aliases; remove them before deactivation")
	}

	var queuedCount int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM coremail_queue WHERE domain_id = `+dialect.Placeholder(1)+` AND status IN ('pending', 'leased', 'deferred')`,
		domainID).Scan(&queuedCount); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}
	if queuedCount > 0 {
		return nil, kernel.NewError(kernel.ErrCodeConflict, "domain has mail still in flight in the queue; wait for it to clear before deactivation")
	}

	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx,
		`UPDATE coremail_domains SET status = `+dialect.Placeholder(1)+`, deactivated_at = `+dialect.Placeholder(2)+`, deactivation_reason = `+dialect.Placeholder(3)+`, version = version + 1, updated_at = `+dialect.Placeholder(4)+` WHERE id = `+dialect.Placeholder(5)+` AND tenant_id = `+dialect.Placeholder(6)+` AND version = `+dialect.Placeholder(7),
		"deactivated", now, reason, now, domainID, tenantID, expectedVersion)
	if err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// The guarded UPDATE predicate includes version=expectedVersion;
		// zero rows here means a concurrent writer moved the version
		// between our SELECT and this UPDATE.
		return nil, kernel.NewError(kernel.ErrCodeConflict, fmt.Sprintf("domain version is no longer %d", expectedVersion))
	}

	if err := tx.Commit(); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}
	committed = true

	reqIDBytes := make([]byte, 8)
	_, _ = rand.Read(reqIDBytes)
	return &deactivateDomainResult{RequestID: hex.EncodeToString(reqIDBytes), NewVersion: expectedVersion + 1}, nil
}
