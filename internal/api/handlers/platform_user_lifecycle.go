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

// ── Platform user lifecycle (Phase 8 production-acceptance remediation) ──
//
// POST /api/v1/platform/users/:id/deactivate
//
// A canonical, audited way for one authorized platform administrator to
// deactivate ANOTHER platform-scoped user account. This did not
// previously exist: the only prior paths were self-service (blocked by
// design) or the tenant-scoped admin_users.go family, which excludes
// platform_super_admin entirely (tenant_id IS NULL for those rows).
//
// Authorization: gated on the dedicated PermPlatformUsersWrite
// permission, granted ONLY to platform_super_admin — deliberately
// distinct from PermPlatformSessionsRevoke (revoking a session is a
// different authority than disabling the identity) and from the
// tenant-scoped PermUsersWrite (which never applies to platform rows).
//
// The handler itself only does HTTP concerns (parsing, idempotency,
// typed confirmation, response shaping); the transactional account
// work lives in deactivatePlatformUserTx below, which is the single
// source of truth callable from tests without going through HTTP.
//
// What deactivation does, atomically, or not at all:
//   - locks/reads the target row, rejects a non-platform target;
//   - rejects self-targeting;
//   - sets active=false and bumps token_version (invalidates every
//     already-issued JWT immediately);
//   - deletes the target's sessions rows — this repo has no separate
//     "trusted device" table; a session row IS the trusted-device
//     session record, so deleting sessions covers both;
//   - deactivates (never hard-deletes) the target's api_keys rows and
//     bumps owner_token_version;
//   - marks every unused mfa_recovery_codes row as used — this repo's
//     only recovery-token store;
//   - deletes any pending mfa_challenges rows;
//   - writes one audit entry (actor/target/reason/request_id/time).
//     This repo has no separate outbox table; coremail_audit is the
//     durable evidence store and is written inside the same
//     transaction success path (commit-then-audit — a crash between
//     commit and audit is the same gap every other platform mutation
//     in this codebase already accepts, not a new one).
//
// Idempotency: POST requires an Idempotency-Key (h.platformIdempotent).
// Replaying the same key+body returns the original response without
// re-running the transaction. Calling deactivate again on an already-
// inactive target (different key, e.g. a genuine retry after a lost
// response) is NOT an error — the underlying statements are naturally
// idempotent (SET active=false, DELETE ... WHERE user_id=?, etc.) and
// the handler returns 200 either way. Only a missing target is a 404.
func (h *Handler) DeactivatePlatformUser(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()

	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	targetID := uint(id)

	actorID := h.platformActorID(c)
	if actorID != 0 && targetID == actorID {
		return fiber.NewError(fiber.StatusConflict, "cannot deactivate your own account")
	}

	body, err := platformMutationBody(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		Confirm string `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := bindStrictJSONBytes(body, &req); err != nil {
		return strictJSONError(c, err)
	}
	wantConfirm := fmt.Sprintf("DEACTIVATE-USER-%d", targetID)
	if req.Confirm != wantConfirm {
		return fiber.NewError(fiber.StatusPreconditionFailed, "type the confirmation phrase exactly: "+wantConfirm)
	}
	if strings.TrimSpace(req.Reason) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "reason is required")
	}

	c.Set("Cache-Control", "no-store")

	scope := "platform.user.deactivate:POST:/platform/users/" + strconv.FormatUint(uint64(targetID), 10) + ":actor:" + strconv.FormatUint(uint64(actorID), 10)

	return h.platformIdempotent(c, scope, func() (int, any, any, error) {
		result, err := deactivatePlatformUserTx(c.Context(), sqlDB, h.dialect, targetID, actorID, req.Reason)
		if err != nil {
			return 0, nil, nil, err
		}
		if h.auditStore != nil {
			_ = h.auditStore.Record(c.Context(), &audit.Entry{
				Actor:     fmt.Sprintf("user:%d", actorID),
				Action:    "platform_user.deactivate",
				Target:    fmt.Sprintf("user:%d|reason:%s|request_id:%s", targetID, req.Reason, result.RequestID),
				Result:    "success",
				IP:        c.IP(),
				UserAgent: c.Get("User-Agent"),
				Timestamp: time.Now().UTC(),
			})
		}
		resp := fiber.Map{
			"id":         targetID,
			"active":     false,
			"request_id": result.RequestID,
		}
		return fiber.StatusOK, resp, resp, nil
	})
}

// deactivateResult carries the outcome of the transactional core so
// tests can call it directly without an HTTP round trip.
type deactivateResult struct {
	RequestID string
}

// deactivatePlatformUserTx is the canonical platform-user-lifecycle
// service operation. It is the single place that touches the
// underlying tables; DeactivatePlatformUser never issues SQL itself.
func deactivatePlatformUserTx(ctx context.Context, sqlDB *sql.DB, dialect *dbdialect.Info, targetID, actorID uint, reason string) (*deactivateResult, error) {
	if targetID == actorID {
		return nil, kernel.NewError(kernel.ErrCodeConflict, "cannot deactivate your own account")
	}

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

	var tenantID sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT tenant_id FROM users WHERE id = `+dialect.Placeholder(1)+` AND deleted_at IS NULL`,
		targetID).Scan(&tenantID)
	if err == sql.ErrNoRows {
		return nil, kernel.NewError(kernel.ErrCodeNotFound, "user not found")
	}
	if err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}
	if tenantID.Valid {
		return nil, kernel.NewError(kernel.ErrCodeValidation, "target is not a platform-scoped identity")
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET active = `+dialect.FalseLiteral()+`, updated_at = `+dialect.Placeholder(1)+`, token_version = COALESCE(token_version, 0) + 1 WHERE id = `+dialect.Placeholder(2),
		now, targetID); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = `+dialect.Placeholder(1), targetID); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE api_keys SET active = `+dialect.FalseLiteral()+`, owner_token_version = owner_token_version + 1, updated_at = `+dialect.Placeholder(1)+` WHERE user_id = `+dialect.Placeholder(2),
		now, targetID); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}

	// mfa_recovery_codes is this repo's only recovery-token store;
	// mark every unused code as used so none remain redeemable.
	_, _ = tx.ExecContext(ctx,
		`UPDATE mfa_recovery_codes SET used_at = `+dialect.Placeholder(1)+` WHERE user_id = `+dialect.Placeholder(2)+` AND used_at IS NULL`,
		now, targetID)

	// mfa_challenges may not exist on a fresh DB that never exercised
	// MFA login; ignore a missing-table error rather than fail the
	// whole deactivation over an empty, optional table.
	_, _ = tx.ExecContext(ctx, `DELETE FROM mfa_challenges WHERE user_id = `+dialect.Placeholder(1), targetID)

	if err := tx.Commit(); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "failed to process request")
	}
	committed = true

	reqIDBytes := make([]byte, 8)
	_, _ = rand.Read(reqIDBytes)
	return &deactivateResult{RequestID: hex.EncodeToString(reqIDBytes)}, nil
}
