package handlers

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/admin/organization"
	platformSvc "github.com/orvix/orvix/internal/admin/platform"
	"github.com/orvix/orvix/internal/platform/kernel"
)

func (h *Handler) ListPlatformOrganizations(c fiber.Ctx) error {
	if h.platformAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "platform admin service not available"})
	}
	var req struct {
		Search string `json:"search"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	c.Bind().Query(&req)

	result, total, err := h.platformAdminSvc.ListOrganizationSummaries(c.Context(), req.Search, req.Limit, req.Offset)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if result == nil {
		result = []platformSvc.OrganizationSummary{}
	}
	return c.JSON(fiber.Map{"organizations": result, "total": total})
}

func (h *Handler) GetPlatformOrganization(c fiber.Ctx) error {
	if h.platformAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "platform admin service not available"})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid organization id"})
	}
	id := uint(idVal)

	detail, err := h.platformAdminSvc.GetOrganizationDetail(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}
	return c.JSON(detail)
}

// CreatePlatformOrganization is the Platform Super Admin organization
// creation endpoint (POST /api/v1/platform/organizations) — closes the
// documented MISSING_BACKEND capability. Product semantics:
//
//   - owner_email is REQUIRED: the initial owner is established through
//     the real tenant_admin invitation/activation model (a one-time
//     token, hashed at rest), never by inventing an owner user or
//     password. An ownerless ACTIVE organization is impossible by
//     construction — the org row, its owner invitation, and the audit
//     record commit in ONE transaction.
//   - plan/subscription is initialized consistently with self-signup
//     (default: free plan, monthly).
//   - Idempotency-Key is required; a replay returns the stored result.
//     The one-time invite token is never persisted anywhere (only its
//     SHA-256 hash lives in org_invitations) and is therefore returned
//     ONLY in the live response, never in a replayed body.
func (h *Handler) CreatePlatformOrganization(c fiber.Ctx) error {
	if h.platformAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "platform admin service not available"})
	}
	// Sensitive mutation: the live response carries a one-time invite
	// token, so it must never be cached (applies to the live response
	// AND any idempotent replay).
	c.Set("Cache-Control", "no-store")
	var req platformSvc.CreateOrganizationRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if strings.TrimSpace(req.OwnerEmail) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "owner_email is required"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	return h.platformIdempotent(c, "platform.organizations.create", func() (int, any, any, error) {
		result, err := h.platformAdminSvc.CreateOrganization(c.Context(), req, actorID)
		if err != nil {
			if err == organization.ErrOrganizationExists {
				return 0, nil, nil, kernel.NewError(kernel.ErrCodeConflict, "an organization with this slug already exists")
			}
			if err == organization.ErrOrganizationDomainExists {
				return 0, nil, nil, kernel.NewError(kernel.ErrCodeConflict, "an organization with this domain already exists")
			}
			if strings.Contains(err.Error(), "owner_email") {
				return 0, nil, nil, kernel.NewError(kernel.ErrCodeValidation, err.Error())
			}
			return 0, nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "create organization", err)
		}
		// Stored body: NEVER persists the one-time invite token. A replay
		// returns the organization + invitation metadata; the token itself
		// is single-use and shown only in the live response.
		stored := fiber.Map{
			"organization": result.Organization,
			"invitation": fiber.Map{
				"id": result.Invitation.ID, "organization_id": result.Invitation.OrganizationID,
				"email": result.Invitation.Email, "role": result.Invitation.Role,
				"status": result.Invitation.Status, "expires_at": result.Invitation.ExpiresAt,
			},
		}
		live := fiber.Map{
			"organization": result.Organization,
			"invitation":   result.Invitation,
			"invite_token": result.InviteToken,
			"warning":      "Save this invitation token now - it will not be shown again",
		}
		return fiber.StatusCreated, stored, live, nil
	})
}
