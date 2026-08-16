package handlers

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail/mime"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/supportaccess"
)

// ── Platform mailbox support view (audited, read-only) ─────────────
//
// A Platform Super Admin can inspect a customer's mailbox — folders,
// messages, attachments — WITHOUT ever reading/resetting the
// mailbox's password, minting a normal webmail JWT/access_token, or
// creating a customer login session. The operator's own platform
// session/cookie is never touched: they remain authenticated as
// themselves throughout.
//
// Session model: supportaccess.MailboxViewSession — a random opaque
// session id bound to exactly one operator, one tenant, one mailbox,
// the "mailbox_view" scope, a ticket reference, a reason, and a hard
// expiry (default 30m, max 60m). The session becomes unusable the
// moment it expires, is ended, or is revoked; it is never a reusable
// bearer credential and is never logged in full elsewhere. Every read
// through the session is individually authorized and audited.

func (h *Handler) mailboxSessionRepository() *supportaccess.MailboxSessionRepository {
	if h.mailboxSessionRepo == nil {
		sqlDB, _ := h.db.DB()
		h.mailboxSessionRepo = supportaccess.NewMailboxSessionRepository(sqlDB)
		_ = h.mailboxSessionRepo.EnsureSchema(context.Background())
	}
	return h.mailboxSessionRepo
}

func (h *Handler) writeSupportMailboxAudit(c fiber.Ctx, action, target string, tenantID uint) {
	if h.auditStore == nil {
		return
	}
	role, _ := c.Locals("role").(string)
	_ = h.auditStore.Record(c.Context(), &audit.Entry{
		Actor:     fmt.Sprintf("user:%d", h.platformActorID(c)),
		Role:      role,
		Action:    action,
		Target:    target,
		Result:    "success",
		TenantID:  tenantID,
		IP:        c.IP(),
		UserAgent: c.Get("User-Agent"),
		Timestamp: time.Now().UTC(),
	})
}

// StartMailboxSupportView — POST /platform/mailboxes/:tenant_id/:id/support-view
//
// Body: {"ticket_ref","reason","duration_minutes","confirm":"ACCESS-MAILBOX-<id>"}.
// The mailbox password is never read for this operation; the response
// never contains a password, hash, or reusable bearer token — only an
// opaque session id scoped to (operator, tenant, mailbox, mailbox_view).
func (h *Handler) StartMailboxSupportView(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	mailboxID, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}

	var req struct {
		TicketRef       string `json:"ticket_ref"`
		Reason          string `json:"reason"`
		DurationMinutes int    `json:"duration_minutes"`
		Confirm         string `json:"confirm"`
	}
	if err := bindStrictJSON(c, &req); err != nil {
		return strictJSONError(c, err)
	}
	wantConfirm := fmt.Sprintf("ACCESS-MAILBOX-%d", mailboxID)
	if req.Confirm != wantConfirm {
		return fiber.NewError(fiber.StatusPreconditionFailed, "type the confirmation phrase exactly: "+wantConfirm)
	}
	if strings.TrimSpace(req.TicketRef) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "ticket_ref is required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "reason is required")
	}

	duration := supportaccess.MailboxViewSessionDefaultDuration
	if req.DurationMinutes > 0 {
		d := time.Duration(req.DurationMinutes) * time.Minute
		if d > supportaccess.MailboxViewSessionMaxDuration {
			return fiber.NewError(fiber.StatusBadRequest, "duration_minutes may not exceed 60")
		}
		duration = d
	}

	// Tenant-scoped lookup: confirms the mailbox exists AND belongs to
	// this tenant in one call (mailcontrol.Service already 404s on a
	// cross-tenant id — never leaks existence across tenants). The
	// mailbox's password/hash is never part of PlatformMailbox.
	mailbox, err := svc.GetMailbox(c.Context(), mailboxID, tenantID)
	if err != nil {
		return errorResponse(c, err)
	}

	c.Set("Cache-Control", "no-store")

	session := &supportaccess.MailboxViewSession{
		OperatorID:      h.platformActorID(c),
		TargetTenantID:  tenantID,
		TargetMailboxID: mailboxID,
		Scope:           supportaccess.MailboxViewScope,
		TicketRef:       strings.TrimSpace(req.TicketRef),
		Reason:          strings.TrimSpace(req.Reason),
		ExpiresAt:       time.Now().UTC().Add(duration),
	}
	if err := h.mailboxSessionRepository().Insert(c.Context(), session); err != nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeInternal, "failed to start support session"))
	}

	h.writeSupportMailboxAudit(c, "support.mailbox_view.start",
		fmt.Sprintf("mailbox:%d|tenant:%d|session:%s|ticket:%s", mailboxID, tenantID, session.ID, session.TicketRef), tenantID)

	return c.JSON(fiber.Map{
		"session_id": session.ID,
		"tenant_id":  tenantID,
		"mailbox_id": mailboxID,
		"email":      mailbox.Email,
		"mode":       "read_only",
		"expires_at": session.ExpiresAt,
	})
}

// loadAuthorizedMailboxSession validates that the given session id is
// usable RIGHT NOW by the CURRENTLY authenticated operator against the
// exact tenant/mailbox in the URL. Any mismatch — wrong operator,
// wrong tenant, wrong mailbox, expired, ended, revoked, or simply
// absent — fails closed with a 403/404 that never distinguishes
// "doesn't exist" from "not yours" (no existence leak across
// operators or tenants).
func (h *Handler) loadAuthorizedMailboxSession(c fiber.Ctx, tenantID, mailboxID uint) (*supportaccess.MailboxViewSession, error) {
	sessionID := c.Params("session_id")
	if strings.TrimSpace(sessionID) == "" {
		return nil, kernel.NewError(kernel.ErrCodeNotFound, "support session not found")
	}
	session, err := h.mailboxSessionRepository().Get(c.Context(), sessionID)
	if err != nil {
		return nil, kernel.NewError(kernel.ErrCodeNotFound, "support session not found")
	}
	operatorID := h.platformActorID(c)
	if session.OperatorID != operatorID || session.TargetTenantID != tenantID || session.TargetMailboxID != mailboxID {
		// Deliberately the same NotFound as "session id does not
		// exist" — never confirm a session exists for someone else.
		return nil, kernel.NewError(kernel.ErrCodeNotFound, "support session not found")
	}
	if err := session.Active(time.Now().UTC()); err != nil {
		return nil, kernel.NewError(kernel.ErrCodeConflict, err.Error())
	}
	return session, nil
}

// ListMailboxSupportFolders — GET .../support-view/:session_id/folders
func (h *Handler) ListMailboxSupportFolders(c fiber.Ctx) error {
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	mailboxID, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	session, err := h.loadAuthorizedMailboxSession(c, tenantID, mailboxID)
	if err != nil {
		return errorResponse(c, err)
	}
	if h.mailStore == nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeUnavailable, "mail store is unavailable"))
	}
	c.Set("Cache-Control", "no-store")
	folders, err := h.mailStore.Folders.ListByMailbox(c.Context(), mailboxID, nil)
	if err != nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeInternal, "failed to list folders"))
	}
	h.writeSupportMailboxAudit(c, "support.mailbox_view.folders_read",
		fmt.Sprintf("mailbox:%d|tenant:%d|session:%s", mailboxID, tenantID, session.ID), tenantID)
	return c.JSON(fiber.Map{"folders": folders})
}

// ListMailboxSupportMessages — GET .../support-view/:session_id/messages
func (h *Handler) ListMailboxSupportMessages(c fiber.Ctx) error {
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	mailboxID, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	session, err := h.loadAuthorizedMailboxSession(c, tenantID, mailboxID)
	if err != nil {
		return errorResponse(c, err)
	}
	if h.mailStore == nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeUnavailable, "mail store is unavailable"))
	}
	limit := queryIntDefault(c, "limit", 50)
	if limit < 1 || limit > 200 {
		limit = 50
	}
	msgFilter := storage.MessageFilter{
		MailboxID: mailboxID,
		Search:    c.Query("q"),
		Limit:     limit,
		Offset:    queryIntDefault(c, "offset", 0),
	}
	if msgFilter.Search != "" {
		msgFilter.SearchSubject = true
		msgFilter.SearchFrom = true
		msgFilter.SearchTo = true
	}
	if folderIDStr := c.Query("folder_id"); folderIDStr != "" {
		if fid, err := strconv.ParseUint(folderIDStr, 10, 64); err == nil && fid > 0 {
			f := uint(fid)
			msgFilter.FolderID = &f
		}
	}

	c.Set("Cache-Control", "no-store")
	messages, total, err := h.mailStore.ListMessages(c.Context(), msgFilter, nil)
	if err != nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeInternal, "failed to list messages"))
	}
	h.writeSupportMailboxAudit(c, "support.mailbox_view.messages_read",
		fmt.Sprintf("mailbox:%d|tenant:%d|session:%s", mailboxID, tenantID, session.ID), tenantID)
	return c.JSON(fiber.Map{"messages": messages, "total": total})
}

// GetMailboxSupportMessage — GET .../support-view/:session_id/messages/:message_id
//
// Pure read: uses MailStore.GetMetadata + MailStore.GetRFC822 directly
// and never calls Messages.UpdateFlags — reading a message through
// the support view must never mark it seen or otherwise mutate
// customer mailbox state.
func (h *Handler) GetMailboxSupportMessage(c fiber.Ctx) error {
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	mailboxID, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	session, err := h.loadAuthorizedMailboxSession(c, tenantID, mailboxID)
	if err != nil {
		return errorResponse(c, err)
	}
	messageID, err := parseIDParam(c, "message_id")
	if err != nil {
		return errorResponse(c, err)
	}
	if h.mailStore == nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeUnavailable, "mail store is unavailable"))
	}
	meta, err := h.mailStore.GetMetadata(c.Context(), messageID, nil)
	if err != nil || meta.MailboxID != mailboxID {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeNotFound, "message not found"))
	}
	raw, err := h.mailStore.GetRFC822(c.Context(), messageID, nil)
	if err != nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeInternal, "failed to load message body"))
	}
	attachments, err := h.mailStore.Attachments.ListByMessage(c.Context(), messageID, nil)
	if err != nil {
		attachments = nil
	}

	// Server-side MIME parse — the support viewer must never render
	// raw MIME source (boundaries, quoted-printable escapes) as the
	// display body, same fix as the normal webmail reading pane.
	bodies := mime.ExtractBodies(raw)

	c.Set("Cache-Control", "no-store")
	h.writeSupportMailboxAudit(c, "support.mailbox_view.message_read",
		fmt.Sprintf("mailbox:%d|tenant:%d|session:%s|message:%d", mailboxID, tenantID, session.ID, messageID), tenantID)
	return c.JSON(fiber.Map{
		"message":           meta,
		"text_body":         bodies.TextBody,
		"html_body":         bodies.HTMLBody,
		"has_html":          bodies.HasHTML,
		"has_remote_images": bodies.HasRemoteImages,
		"attachments":       attachments,
	})
}

// GetMailboxSupportAttachment — GET .../support-view/:session_id/messages/:message_id/attachments/:attachment_id
func (h *Handler) GetMailboxSupportAttachment(c fiber.Ctx) error {
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	mailboxID, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	session, err := h.loadAuthorizedMailboxSession(c, tenantID, mailboxID)
	if err != nil {
		return errorResponse(c, err)
	}
	messageID, err := parseIDParam(c, "message_id")
	if err != nil {
		return errorResponse(c, err)
	}
	attachmentID, err := parseIDParam(c, "attachment_id")
	if err != nil {
		return errorResponse(c, err)
	}
	if h.mailStore == nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeUnavailable, "mail store is unavailable"))
	}
	meta, err := h.mailStore.GetMetadata(c.Context(), messageID, nil)
	if err != nil || meta.MailboxID != mailboxID {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeNotFound, "message not found"))
	}
	att, err := h.mailStore.Attachments.GetByID(c.Context(), attachmentID, nil)
	if err != nil || att.MessageID != messageID {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeNotFound, "attachment not found"))
	}

	// Read into memory and Send rather than SendFile so the file handle
	// is closed before the response is flushed (avoids Windows
	// file-locking issues and matches the existing webmail attachment
	// download pattern).
	data, err := os.ReadFile(att.StoragePath)
	if err != nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeInternal, "read attachment failed"))
	}

	c.Set("Cache-Control", "no-store")
	h.writeSupportMailboxAudit(c, "support.mailbox_view.attachment_read",
		fmt.Sprintf("mailbox:%d|tenant:%d|session:%s|message:%d|attachment:%d", mailboxID, tenantID, session.ID, messageID, attachmentID), tenantID)
	c.Set("Content-Type", att.ContentType)
	c.Set("Content-Disposition", "attachment; filename=\""+att.Filename+"\"")
	return c.Send(data)
}

// EndMailboxSupportView — POST .../support-view/:session_id/end
func (h *Handler) EndMailboxSupportView(c fiber.Ctx) error {
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	mailboxID, err := parseIDParam(c, "id")
	if err != nil {
		return errorResponse(c, err)
	}
	sessionID := c.Params("session_id")
	session, err := h.mailboxSessionRepository().Get(c.Context(), sessionID)
	if err != nil {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeNotFound, "support session not found"))
	}
	operatorID := h.platformActorID(c)
	if session.OperatorID != operatorID || session.TargetTenantID != tenantID || session.TargetMailboxID != mailboxID {
		return errorResponse(c, kernel.NewError(kernel.ErrCodeNotFound, "support session not found"))
	}
	if err := h.mailboxSessionRepository().End(c.Context(), session); err != nil {
		// Already ended/revoked/expired is not an error from the
		// operator's point of view — ending an already-inactive
		// session is idempotent from the UI's perspective.
		return c.JSON(fiber.Map{"session_id": session.ID, "ended": true})
	}
	h.writeSupportMailboxAudit(c, "support.mailbox_view.end",
		fmt.Sprintf("mailbox:%d|tenant:%d|session:%s", mailboxID, tenantID, session.ID), tenantID)
	return c.JSON(fiber.Map{"session_id": session.ID, "ended": true})
}
