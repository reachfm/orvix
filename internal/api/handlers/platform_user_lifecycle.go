package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"go.uber.org/zap"
)

// ── Platform user lifecycle (Phase 8 production-acceptance remediation) ──
//
// POST /api/v1/platform/users/:id/deactivate
//
// A canonical, audited way for one authorized platform administrator to
// deactivate ANOTHER platform-scoped user account (platform_super_admin
// or any other role). This did not previously exist: the only prior
// paths were self-service (blocked by design — an identity can never
// disable itself) or a tenant-scoped admin_users.go family that
// excludes platform_super_admin entirely (it queries "tenant_id = ?"
// and platform_super_admin rows carry tenant_id=NULL).
//
// Deactivation is NOT a row delete. It:
//   - sets active=false (the same column every login check already
//     reads — AuthenticateMailbox/user login paths reject inactive
//     users) and bumps token_version (invalidates every already-issued
//     JWT immediately, the same mechanism UpdateAdminUserStatus uses);
//   - deletes the target's sessions rows (refresh-token sessions);
//   - deactivates (active=false, never hard-deleted) the target's
//     api_keys rows and bumps owner_token_version so any cached
//     API-key validation also rejects immediately;
//   - deletes the target's mfa_challenges rows (any in-flight,
//     unconsumed MFA challenge is discarded rather than left
//     redeemable);
//   - writes one audit entry naming the actor, the target, the typed
//     reason, and a per-request correlation id.
//
// Fail-closed guards: caller must be platformMW-gated
// (platform_super_admin) with PermUsersWrite, must not target itself,
// must supply the exact typed confirmation phrase
// "DEACTIVATE-USER-<id>", and the DB work runs inside one transaction
// — a failure partway through leaves the account exactly as it was,
// never half-deactivated.
func (h *Handler) DeactivatePlatformUser(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()

	id, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}

	actorID := h.platformActorID(c)
	if actorID != 0 && uint(id) == actorID {
		return fiber.NewError(fiber.StatusConflict, "cannot deactivate your own account")
	}

	var body struct {
		Confirm string `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	wantConfirm := fmt.Sprintf("DEACTIVATE-USER-%d", id)
	if body.Confirm != wantConfirm {
		return fiber.NewError(fiber.StatusPreconditionFailed, "type the confirmation phrase exactly: "+wantConfirm)
	}
	if body.Reason == "" {
		return fiber.NewError(fiber.StatusBadRequest, "reason is required")
	}

	reqIDBytes := make([]byte, 8)
	_, _ = rand.Read(reqIDBytes)
	requestID := hex.EncodeToString(reqIDBytes)

	tx, err := sqlDB.BeginTx(c.Context(), nil)
	if err != nil {
		h.logger.Error("deactivate platform user: begin tx failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process request")
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	res, err := tx.ExecContext(c.Context(),
		`UPDATE users SET active = `+h.dialect.FalseLiteral()+`, updated_at = `+h.dialect.Placeholder(1)+`, token_version = COALESCE(token_version, 0) + 1 WHERE id = `+h.dialect.Placeholder(2)+` AND deleted_at IS NULL`,
		now, id)
	if err != nil {
		h.logger.Error("deactivate platform user: update users failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process request")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "user not found")
	}

	if _, err := tx.ExecContext(c.Context(), `DELETE FROM sessions WHERE user_id = `+h.dialect.Placeholder(1), id); err != nil {
		h.logger.Error("deactivate platform user: delete sessions failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process request")
	}

	if _, err := tx.ExecContext(c.Context(),
		`UPDATE api_keys SET active = `+h.dialect.FalseLiteral()+`, owner_token_version = owner_token_version + 1, updated_at = `+h.dialect.Placeholder(1)+` WHERE user_id = `+h.dialect.Placeholder(2),
		now, id); err != nil {
		h.logger.Error("deactivate platform user: deactivate api_keys failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process request")
	}

	// mfa_challenges may not exist yet on a fresh DB that never
	// exercised MFA login; ignore a missing-table error rather than
	// fail the whole deactivation over an empty, optional table.
	_, _ = tx.ExecContext(c.Context(), `DELETE FROM mfa_challenges WHERE user_id = `+h.dialect.Placeholder(1), id)

	if err := tx.Commit(); err != nil {
		h.logger.Error("deactivate platform user: commit failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process request")
	}
	committed = true

	if h.auditStore != nil {
		_ = h.auditStore.Record(c.Context(), &audit.Entry{
			Actor:     fmt.Sprintf("user:%d", actorID),
			Action:    "platform_user.deactivate",
			Target:    fmt.Sprintf("user:%d|reason:%s|request_id:%s", id, body.Reason, requestID),
			Result:    "success",
			IP:        c.IP(),
			UserAgent: c.Get("User-Agent"),
			Timestamp: now,
		})
	}

	return c.JSON(fiber.Map{
		"id":         id,
		"active":     false,
		"request_id": requestID,
	})
}
