package handlers

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/support"
	"go.uber.org/zap"
)

// stableSupportError maps the canonical support-ticket service errors
// to the typed envelope the rest of the API uses (code + HTTP status).
//
// AUTHENTICATION_REQUIRED is handled by the route middleware; this map
// only covers errors the repository itself surfaces.
func stableSupportError(c fiber.Ctx, err error, logger *zap.Logger, action string) error {
	switch {
	case errors.Is(err, support.ErrTicketNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"code":    "NOT_FOUND",
			"message": "Support ticket not found",
		})
	case errors.Is(err, support.ErrMessageEmpty):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"code":    "VALIDATION_ERROR",
			"message": "Ticket subject, category, and description are required",
		})
	case errors.Is(err, support.ErrPriorityUnknown):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"code":    "VALIDATION_ERROR",
			"message": "Priority must be one of: low, normal, high, urgent",
		})
	case errors.Is(err, support.ErrInvalidTransition):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"code":    "INVALID_STATE_TRANSITION",
			"message": "The requested transition is not allowed from the ticket's current status",
		})
	default:
		logger.Error("support handler: unexpected error", zap.String("action", action), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"code":    "INTERNAL_ERROR",
			"message": "An unexpected error occurred processing the support ticket",
		})
	}
}

// supportRepoOr500 returns the wired support repository or fails the
// request with 503 if it has not been wired (which only happens in
// tests that construct a Handler without going through the router).
func (h *Handler) supportRepoOr500(c fiber.Ctx) (*support.Repository, error) {
	if h.supportRepo != nil {
		return h.supportRepo, nil
	}
	if h.db == nil {
		return nil, c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"code":    "DEPENDENCY_UNAVAILABLE",
			"message": "Support service is not configured",
		})
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return nil, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"code":    "INTERNAL_ERROR",
			"message": "Failed to access database",
		})
	}
	r := support.NewRepository(sqlDB, h.db)
	h.supportRepo = r
	return r, nil
}

// SubmitSupportRequest creates a support ticket from the authenticated
// user. Backed by the canonical support.Repository; the previous GORM
// implementation has been replaced.
//
// Response: 201 Created with the full ticket row, including the
// auto-generated reference_id (e.g. SR-<user>-<nano>). Delivery attempt
// to the transactional mail sender is best-effort and honest: success
// sets `delivery_status: "sent"`, failure sets it to "failed" with the
// sender's error string in `delivery_error`. When no mail sender is
// configured, `delivery_status: "disabled"` makes the absence explicit
// rather than silently claiming success.
func (h *Handler) SubmitSupportRequest(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	email, _ := c.Locals("email").(string)
	tenantID, _ := c.Locals("tenant_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"code":    "AUTHENTICATION_REQUIRED",
			"message": "authentication required",
		})
	}

	var req struct {
		Category    string `json:"category"`
		Subject     string `json:"subject"`
		Description string `json:"description"`
		Message     string `json:"message"` // legacy alias for `description`
		Priority    string `json:"priority"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"code":    "VALIDATION_ERROR",
			"message": "invalid request",
		})
	}
	if strings.TrimSpace(req.Description) == "" {
		req.Description = req.Message
	}
	if strings.TrimSpace(req.Subject) == "" || strings.TrimSpace(req.Category) == "" || strings.TrimSpace(req.Description) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"code":    "VALIDATION_ERROR",
			"message": "category, subject, and description are required",
		})
	}

	repo, err := h.supportRepoOr500(c)
	if err != nil {
		return err
	}

	ticket, err := repo.CreateTicket(c.Context(), support.CreateTicketInput{
		TenantID:    tenantID,
		UserID:      userID,
		UserEmail:   email,
		Category:    req.Category,
		Subject:     req.Subject,
		Description: req.Description,
		Priority:    req.Priority,
	})
	if err != nil {
		return stableSupportError(c, err, h.logger, "create_ticket")
	}

	// Best-effort delivery attempt — the ticket is already persisted
	// regardless of mail outcome. The status reflects reality:
	//   "sent"     — sender succeeded
	//   "failed"   — sender returned an error (delivery_error set)
	//   "disabled" — no mail sender wired (we never claim sent)
	if h.mailSender != nil {
		body := "Support Request #" + ticket.ReferenceID + "\nCategory: " + ticket.Category + "\nSubject: " + ticket.Subject +
			"\nUser: " + email + " (ID: " + strconv.FormatUint(uint64(userID), 10) + ")\nTenant: " + strconv.FormatUint(uint64(tenantID), 10) +
			"\n\n" + ticket.Description
		if sendErr := h.mailSender.Send("noreply@orvix.email", "Orvix Support: "+ticket.Subject, body); sendErr != nil {
			_ = repo.MarkDeliveryStatus(c.Context(), ticket.ID, "failed", sendErr.Error())
			ticket.DeliveryStatus = "failed"
			ticket.DeliveryError = sendErr.Error()
		} else {
			_ = repo.MarkDeliveryStatus(c.Context(), ticket.ID, "sent", "")
			ticket.DeliveryStatus = "sent"
		}
	} else {
		_ = repo.MarkDeliveryStatus(c.Context(), ticket.ID, "disabled", "no transactional mail sender wired")
		ticket.DeliveryStatus = "disabled"
	}

	h.writeAuditLog(c, "support.request.create", "category:"+ticket.Category+" ref:"+ticket.ReferenceID)
	return c.Status(fiber.StatusCreated).JSON(ticket)
}

// ListOwnSupportRequests — tenant-side history. Tenant-scoped by
// JWT-tenant (the tenant_id from the session), so a tenant caller
// cannot enumerate another tenant's tickets even with a guessed id.
//
// Response envelope: { entries, total, limit, offset } — same shape
// the platform audit page uses, so the existing client envelope
// unwrap helpers keep working.
func (h *Handler) ListOwnSupportRequests(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	tenantID, _ := c.Locals("tenant_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"code":    "AUTHENTICATION_REQUIRED",
			"message": "authentication required",
		})
	}

	repo, err := h.supportRepoOr500(c)
	if err != nil {
		return err
	}

	limit := parseIntQuery(c, "limit", 50, 1, 200)
	offset := parseIntQuery(c, "offset", 0, 0, 1<<31-1)
	status := strings.TrimSpace(c.Query("status"))

	// Tenant callers see only their own tickets; this is the
	// tenant-side history view. Owners / admins can see all tickets in
	// their tenant — but that surface is intentionally NOT exposed here
	// because the mission spec scopes tenant-side history to the
	// creator. Tenant-wide visibility (Org Admin viewing all tickets)
	// would be a different surface.
	list, total, err := repo.ListTickets(c.Context(), support.ListFilter{
		TenantID: tenantID,
		OwnerID:  userID,
		Status:   status,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return stableSupportError(c, err, h.logger, "list_own_tickets")
	}
	return c.JSON(fiber.Map{
		"entries": list,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// GetOwnSupportRequest — tenant-side detail (own ticket only).
func (h *Handler) GetOwnSupportRequest(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	tenantID, _ := c.Locals("tenant_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"code":    "AUTHENTICATION_REQUIRED",
			"message": "authentication required",
		})
	}

	repo, err := h.supportRepoOr500(c)
	if err != nil {
		return err
	}

	// :ref is the human-friendly reference id (e.g. SR-12-1234).
	ref := strings.TrimSpace(c.Params("ref"))
	ticket, err := repo.GetTicketByReference(c.Context(), ref, tenantID)
	if err != nil {
		return stableSupportError(c, err, h.logger, "get_own_ticket")
	}
	// Defense in depth: even with tenant-scoped query, only the
	// creator can read their own ticket.
	if ticket.UserID != userID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"code":    "NOT_FOUND",
			"message": "Support ticket not found",
		})
	}
	msgs, err := repo.ListMessages(c.Context(), ticket.ID, tenantID)
	if err != nil {
		return stableSupportError(c, err, h.logger, "list_messages_for_own_ticket")
	}
	return c.JSON(fiber.Map{
		"ticket":   ticket,
		"messages": msgs,
	})
}

// ReplyOnOwnSupportRequest — tenant adds a reply to their own ticket.
// Closes / resolved tickets reject further replies (INVALID_STATE_TRANSITION).
func (h *Handler) ReplyOnOwnSupportRequest(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	email, _ := c.Locals("email").(string)
	tenantID, _ := c.Locals("tenant_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"code":    "AUTHENTICATION_REQUIRED",
			"message": "authentication required",
		})
	}

	repo, err := h.supportRepoOr500(c)
	if err != nil {
		return err
	}

	ref := strings.TrimSpace(c.Params("ref"))
	ticket, err := repo.GetTicketByReference(c.Context(), ref, tenantID)
	if err != nil {
		return stableSupportError(c, err, h.logger, "lookup_ticket_for_reply")
	}
	if ticket.UserID != userID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"code":    "NOT_FOUND",
			"message": "Support ticket not found",
		})
	}
	if ticket.Status == "closed" || ticket.Status == "resolved" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"code":    "INVALID_STATE_TRANSITION",
			"message": "Cannot reply to a closed or resolved ticket",
		})
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"code":    "VALIDATION_ERROR",
			"message": "invalid request",
		})
	}
	msg, err := repo.AddReply(c.Context(), support.ReplyInput{
		TicketID:     ticket.ID,
		AuthorUserID: userID,
		AuthorEmail:  email,
		AuthorKind:   "tenant",
		Body:         req.Body,
	})
	if err != nil {
		return stableSupportError(c, err, h.logger, "add_tenant_reply")
	}
	h.writeAuditLog(c, "support.request.reply", "ticket:"+ticket.ReferenceID+" by:tenant")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"message": msg})
}

// ── Platform Support Inbox ────────────────────────────────────────
//
// The following handlers back the Platform Super Admin's Support
// Inbox (P23A in the platform parity matrix). They are mounted under
// /platform/support/tickets with the platformMW gate (PSA +
// superadmin + CSRF), so the route layer already rejects tenant
// callers. The handlers do not need to repeat the role check.

// ListPlatformSupportTickets — paginated, filtered list of every
// ticket across every tenant. Query params:
//
//	status    — exact match (open / in_progress / ...)
//	category  — exact match (general / billing / technical / security)
//	tenant_id — exact match (optional)
//	search    — subject / reference LIKE %x%
//	limit, offset — standard pagination
func (h *Handler) ListPlatformSupportTickets(c fiber.Ctx) error {
	repo, err := h.supportRepoOr500(c)
	if err != nil {
		return err
	}

	limit := parseIntQuery(c, "limit", 50, 1, 200)
	offset := parseIntQuery(c, "offset", 0, 0, 1<<31-1)
	status := strings.TrimSpace(c.Query("status"))
	category := strings.TrimSpace(c.Query("category"))
	search := strings.TrimSpace(c.Query("search"))

	var tenantID uint
	if t := strings.TrimSpace(c.Query("tenant_id")); t != "" {
		if v, perr := strconv.ParseUint(t, 10, 64); perr == nil {
			tenantID = uint(v)
		}
	}

	list, total, err := repo.ListTickets(c.Context(), support.ListFilter{
		TenantID: tenantID,
		Status:   status,
		Category: category,
		Search:   search,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return stableSupportError(c, err, h.logger, "platform_list_tickets")
	}
	return c.JSON(fiber.Map{
		"entries": list,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// GetPlatformSupportTicket — full detail (ticket + reply thread).
func (h *Handler) GetPlatformSupportTicket(c fiber.Ctx) error {
	repo, err := h.supportRepoOr500(c)
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(c.Params("ref"))
	ticket, err := repo.GetTicketByReference(c.Context(), ref, 0)
	if err != nil {
		return stableSupportError(c, err, h.logger, "platform_get_ticket")
	}
	msgs, err := repo.ListMessages(c.Context(), ticket.ID, 0)
	if err != nil {
		return stableSupportError(c, err, h.logger, "platform_get_ticket_messages")
	}
	return c.JSON(fiber.Map{
		"ticket":   ticket,
		"messages": msgs,
	})
}

// ReplyOnPlatformSupportTicket — platform operator replies on any
// ticket. Drives the canonical status transition (platform reply
// moves an open / waiting ticket to in_progress).
func (h *Handler) ReplyOnPlatformSupportTicket(c fiber.Ctx) error {
	operatorID, _ := c.Locals("user_id").(uint)
	email, _ := c.Locals("email").(string)
	if operatorID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"code":    "AUTHENTICATION_REQUIRED",
			"message": "authentication required",
		})
	}

	repo, err := h.supportRepoOr500(c)
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(c.Params("ref"))
	ticket, err := repo.GetTicketByReference(c.Context(), ref, 0)
	if err != nil {
		return stableSupportError(c, err, h.logger, "platform_lookup_for_reply")
	}
	if ticket.Status == "closed" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"code":    "INVALID_STATE_TRANSITION",
			"message": "Cannot reply to a closed ticket",
		})
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"code":    "VALIDATION_ERROR",
			"message": "invalid request",
		})
	}
	msg, err := repo.AddReply(c.Context(), support.ReplyInput{
		TicketID:     ticket.ID,
		AuthorUserID: operatorID,
		AuthorEmail:  email,
		AuthorKind:   "platform",
		Body:         req.Body,
	})
	if err != nil {
		return stableSupportError(c, err, h.logger, "platform_add_reply")
	}
	h.writeAuditLog(c, "support.request.platform_reply", "ticket:"+ticket.ReferenceID+" by:platform")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"message": msg})
}

// UpdatePlatformSupportTicketStatus — guarded transition (canonical
// graph only; closed is terminal).
func (h *Handler) UpdatePlatformSupportTicketStatus(c fiber.Ctx) error {
	repo, err := h.supportRepoOr500(c)
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(c.Params("ref"))
	var req struct {
		Status string `json:"status"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"code":    "VALIDATION_ERROR",
			"message": "invalid request",
		})
	}
	updated, err := repo.UpdateTicketStatus(c.Context(), 0, 0, req.Status)
	// UpdateTicketStatus with ticketID=0 returns ErrTicketNotFound; we
	// always pass the resolved id instead, so the only caller-visible
	// errors are ErrTicketNotFound (404) and ErrInvalidTransition (409).
	if err == nil {
		_ = updated
	}
	// Re-resolve by reference to fetch the numeric id.
	ticket, gerr := repo.GetTicketByReference(c.Context(), ref, 0)
	if gerr != nil {
		return stableSupportError(c, gerr, h.logger, "platform_status_lookup")
	}
	updated, err = repo.UpdateTicketStatus(c.Context(), ticket.ID, 0, req.Status)
	if err != nil {
		return stableSupportError(c, err, h.logger, "platform_status_update")
	}
	h.writeAuditLog(c, "support.request.status_change", "ticket:"+ticket.ReferenceID+" status:"+req.Status)
	return c.JSON(fiber.Map{"ticket": updated})
}

// parseIntQuery is a small helper that parses an integer query param
// with default + min + max bounds. Returns def when the param is
// absent or unparseable, clamps to [min, max] otherwise.
func parseIntQuery(c fiber.Ctx, key string, def, min, max int) int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// Ensure the auth package is referenced (otherwise goimports would
// drop it; the auth role gates the platform handlers).
var _ = auth.RolePlatformSuperAdmin
