package handlers

import (
	"database/sql"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/admin/organization"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
	"go.uber.org/zap"
)

// AcceptInvitationHTTP redeems a one-time organization invitation
// (POST /auth/invitations/accept). This is the PUBLIC activation path for
// invited members — most importantly the PSA-created organization owner:
// the org starts in pending_activation (tenants.active=0) and this route
// is the ONLY transition to active. It is public because the invitee has
// no account yet; the one-time token is the credential. Throttled like
// every other credential-accepting endpoint.
//
// Contract:
//   - Body: {token, password, name?}. The email comes from the invitation
//     row, NEVER from the request — the token authorizes that exact
//     address to join.
//   - Password is strength-checked and bcrypt-hashed (same policy as
//     signup).
//   - Platform identities (platform_super_admin / superadmin emails) are
//     protected exactly like signup: an invitation to a platform identity
//     email can never mint a tenant account under it.
//   - An existing active user with the invited email is a stable CONFLICT
//     (the token cannot hijack an existing account).
//   - Revoked/expired/already-used tokens get stable INVALID_STATE_TRANSITION
//     codes; unknown tokens get NOT_FOUND (no existence disclosure).
//   - The org is activated (active=1) unless a deliberate suspension
//     record exists.
//   - On success the one-time token is consumed; a replay returns
//     INVALID_STATE_TRANSITION (never a secret, never a duplicate user).
func (h *Handler) AcceptInvitationHTTP(c fiber.Ctx) error {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body", "code": string(kernel.ErrCodeValidation),
		})
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "token is required", "code": string(kernel.ErrCodeValidation),
		})
	}
	if err := passwordStrength(req.Password); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(), "code": string(kernel.ErrCodeValidation),
		})
	}

	// The invited email must not be a platform identity — same protection
	// as SignupStart/SignupVerify. Checked before any write.
	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("invitations/accept: failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	dial := h.sqlDialect()
	invitedEmail, err := h.invitationEmailByToken(sqlDB, dial, token)
	if err != nil {
		h.logger.Error("invitations/accept: resolve invitation email", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if invitedEmail != "" {
		if blocked, berr := h.emailBelongsToPlatformIdentity(sqlDB, dial, invitedEmail); berr != nil {
			h.logger.Error("invitations/accept: platform identity check", zap.Error(berr))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
		} else if blocked {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "unable to accept this invitation", "code": string(kernel.ErrCodeConflict),
			})
		}
	}

	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available", "code": string(kernel.ErrCodeUnavailable)})
	}
	result, err := h.orgAdminSvc.AcceptInvitation(c.Context(), token, req.Password)
	if err != nil {
		switch err {
		case organization.ErrInvitationNotFound:
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "invitation not found", "code": string(kernel.ErrCodeNotFound)})
		case organization.ErrInvitationExpired:
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "invitation has expired", "code": string(kernel.ErrCodeStateTransition)})
		case organization.ErrInvitationRevoked:
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "invitation was revoked", "code": string(kernel.ErrCodeStateTransition)})
		case organization.ErrInvitationAlreadyUsed:
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "invitation already accepted", "code": string(kernel.ErrCodeStateTransition)})
		case organization.ErrEmailAlreadyInUse:
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "an account with this email already exists", "code": string(kernel.ErrCodeConflict)})
		case organization.ErrWeakPassword:
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": string(kernel.ErrCodeValidation)})
		}
		h.logger.Error("invitations/accept: acceptance failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	h.logger.Info("invitation accepted", zap.Uint("user_id", result.UserID), zap.Uint("organization_id", result.OrganizationID))
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status":              "accepted",
		"user_id":             result.UserID,
		"organization_id":     result.OrganizationID,
		"email":               result.Email,
		"role":                result.Role,
		"organization_active": result.OrganizationActive,
	})
}

// invitationEmailByToken resolves the invited email for a raw token by
// hashing it and looking up the org_invitations row. Returns "" for an
// unknown token (the caller then lets the service produce the uniform
// NOT_FOUND). It exists so the platform-identity check can run BEFORE the
// accept transaction without duplicating hash logic in the handler.
func (h *Handler) invitationEmailByToken(sqlDB *sql.DB, dial *dbdialect.Info, rawToken string) (string, error) {
	if len(rawToken) == 0 || len(rawToken) > 128 {
		return "", nil
	}
	var email string
	err := sqlDB.QueryRow(
		"SELECT email FROM org_invitations WHERE token_hash="+dial.Placeholder(1),
		organization.HashToken(rawToken),
	).Scan(&email)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return email, nil
}
