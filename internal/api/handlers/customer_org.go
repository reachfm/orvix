package handlers

import (
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
)

func (h *Handler) GetCurrentOrganization(c fiber.Ctx) error {
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	org, err := h.orgAdminSvc.GetOrganization(c.Context(), tenantID)
	if h.orgAdminSvc == nil || err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}
	return c.JSON(org)
}

func (h *Handler) ListInvitations(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	invites, err := h.orgAdminSvc.ListInvitations(c.Context(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list invitations"})
	}
	return c.JSON(invites)
}

func (h *Handler) CreateInvitation(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email is required"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	userID, _ := c.Locals("user_id").(uint)
	inv, token, err := h.orgAdminSvc.CreateInvitation(c.Context(), tenantID, userID, req.Email, req.Role, 7)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "invitation.create", fmt.Sprintf("tenant:%d email:%s", tenantID, req.Email))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"invitation": inv, "token": token})
}

func (h *Handler) RevokeInvitation(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid invitation id"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	if err := h.orgAdminSvc.RevokeInvitation(c.Context(), uint(id), tenantID); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "invitation.revoke", fmt.Sprintf("id:%d", id))
	return c.JSON(fiber.Map{"status": "revoked"})
}

func (h *Handler) ListMembers(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	members, err := h.orgAdminSvc.ListMembers(c.Context(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list members"})
	}
	return c.JSON(members)
}

func (h *Handler) UpdateMemberRole(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	userIDStr := c.Params("id")
	memberID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid user id"})
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	if err := h.orgAdminSvc.UpdateMemberRole(c.Context(), uint(memberID), tenantID, req.Role); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "member.role_update", fmt.Sprintf("user:%d role:%s", memberID, req.Role))
	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) RemoveMember(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	userIDStr := c.Params("id")
	memberID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid user id"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	if err := h.orgAdminSvc.RemoveMember(c.Context(), uint(memberID), tenantID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "member.remove", fmt.Sprintf("user:%d", memberID))
	return c.JSON(fiber.Map{"status": "removed"})
}

func (h *Handler) RequestOwnershipTransfer(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	var req struct {
		TargetUserID uint `json:"target_user_id"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.TargetUserID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "target_user_id is required"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	userID, _ := c.Locals("user_id").(uint)
	transfer, token, err := h.orgAdminSvc.RequestOwnershipTransfer(c.Context(), tenantID, userID, req.TargetUserID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "ownership.request", fmt.Sprintf("tenant:%d target:%d", tenantID, req.TargetUserID))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"transfer": transfer, "token": token})
}

func (h *Handler) AcceptOwnershipTransfer(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	userID, _ := c.Locals("user_id").(uint)
	if err := h.orgAdminSvc.AcceptOwnershipTransfer(c.Context(), req.Token, userID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "ownership.accept", fmt.Sprintf("user:%d", userID))
	return c.JSON(fiber.Map{"status": "accepted"})
}

func (h *Handler) CancelOwnershipTransfer(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	userID, _ := c.Locals("user_id").(uint)
	if err := h.orgAdminSvc.CancelOwnershipTransfer(c.Context(), 0, tenantID, userID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "ownership.cancel", fmt.Sprintf("tenant:%d", tenantID))
	return c.JSON(fiber.Map{"status": "cancelled"})
}

func (h *Handler) SuspensionStatus(c fiber.Ctx) error {
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	status, err := h.orgAdminSvc.GetSuspensionStatus(c.Context(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get suspension status"})
	}
	if status == nil {
		return c.JSON(fiber.Map{"suspended": false})
	}
	return c.JSON(fiber.Map{"suspended": true, "reason": status.Reason, "suspended_at": status.SuspendedAt})
}

func (h *Handler) RequestDeletion(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	userID, _ := c.Locals("user_id").(uint)
	if err := h.orgAdminSvc.RequestDeletion(c.Context(), tenantID, userID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "deletion.request", fmt.Sprintf("tenant:%d", tenantID))
	return c.JSON(fiber.Map{"status": "deletion_requested"})
}

func (h *Handler) CancelDeletion(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "organization service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	if err := h.orgAdminSvc.CancelDeletion(c.Context(), tenantID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "deletion.cancel", fmt.Sprintf("tenant:%d", tenantID))
	return c.JSON(fiber.Map{"status": "deletion_cancelled"})
}
