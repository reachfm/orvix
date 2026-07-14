package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/auth/rbac"
	"strconv"
)

func (h *Handler) ListAdminMailboxes(c fiber.Ctx) error {
	if h.mailboxAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "mailbox admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	var req struct {
		Search   string `json:"search"`
		Status   string `json:"status"`
		DomainID uint   `json:"domain_id"`
		Limit    int    `json:"limit"`
		Offset   int    `json:"offset"`
	}
	c.Bind().JSON(&req)

	var status *mailbox.AdminMailboxStatus
	if req.Status != "" {
		s := mailbox.AdminMailboxStatus(req.Status)
		status = &s
	}

	var domainID *uint
	if req.DomainID > 0 {
		domainID = &req.DomainID
	}

	filter := mailbox.MailboxFilter{
		TenantID: &tenantID,
		DomainID: domainID,
		Status:   status,
		Search:   req.Search,
		Limit:    req.Limit,
		Offset:   req.Offset,
	}

	result, total, err := h.mailboxAdminSvc.ListMailboxes(c.Context(), filter)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if result == nil {
		result = []mailbox.AdminMailbox{}
	}
	return c.JSON(fiber.Map{"mailboxes": result, "total": total})
}

func (h *Handler) GetAdminMailbox(c fiber.Ctx) error {
	if h.mailboxAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "mailbox admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	id := uint(idVal)

	m, err := h.mailboxAdminSvc.GetMailbox(c.Context(), id, tenantID)
	if err != nil {
		if err == mailbox.ErrMailboxNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	return c.JSON(fiber.Map{"mailbox": m})
}

func (h *Handler) CreateAdminMailbox(c fiber.Ctx) error {
	if h.mailboxAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "mailbox admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	var req mailbox.CreateMailboxRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email is required"})
	}
	if req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "password is required"})
	}

	var domainID uint
	if domainIDVal := c.FormValue("domain_id"); domainIDVal != "" {
		_ = domainID
	}

	resp, err := h.mailboxAdminSvc.CreateMailbox(c.Context(), req, tenantID, domainID)
	if err != nil {
		if err == mailbox.ErrMailboxExists {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "mailbox already exists"})
		}
		if err == mailbox.ErrInvalidEmail {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	status := fiber.StatusCreated
	result := fiber.Map{
		"mailbox": resp.Mailbox,
	}
	if resp.Password != "" {
		result["password"] = resp.Password
	}
	return c.Status(status).JSON(result)
}

func (h *Handler) UpdateAdminMailbox(c fiber.Ctx) error {
	if h.mailboxAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "mailbox admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	id := uint(idVal)

	var req mailbox.UpdateMailboxRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	m, err := h.mailboxAdminSvc.UpdateMailbox(c.Context(), id, tenantID, req)
	if err != nil {
		if err == mailbox.ErrMailboxNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"mailbox": m})
}

func (h *Handler) SetAdminMailboxStatus(c fiber.Ctx) error {
	if h.mailboxAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "mailbox admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	id := uint(idVal)

	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Status == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status is required"})
	}

	status := mailbox.AdminMailboxStatus(req.Status)
	if err := h.mailboxAdminSvc.SetStatus(c.Context(), id, tenantID, status, req.Reason); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "ok", "mailbox_id": id, "new_status": status})
}

func (h *Handler) BulkSetAdminMailboxStatus(c fiber.Ctx) error {
	if h.mailboxAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "mailbox admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	var req struct {
		MailboxIDs []uint `json:"mailbox_ids"`
		Status     string `json:"status"`
		Reason     string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil || len(req.MailboxIDs) == 0 || req.Status == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mailbox_ids and status are required"})
	}

	status := mailbox.AdminMailboxStatus(req.Status)
	affected, err := h.mailboxAdminSvc.BulkSetStatus(c.Context(), req.MailboxIDs, tenantID, status, req.Reason)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "ok", "affected": affected})
}

func (h *Handler) ResetAdminMailboxPassword(c fiber.Ctx) error {
	if h.mailboxAdminSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "mailbox admin service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}

	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	id := uint(idVal)

	newPassword, err := h.mailboxAdminSvc.ResetPassword(c.Context(), id, tenantID)
	if err != nil {
		if err == mailbox.ErrMailboxNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "ok", "password": newPassword, "mailbox_id": id})
}

var _ = rbac.Permission("") // ensure import used
