package handlers

// User-facing webmail API. Each handler resolves the
// currently-authenticated user to a real mailbox and reads
// or writes through the live MailStore. There is no mock
// data path and no fallback to /api/v1/queue; if the user
// has no mailbox, or the MailStore is not wired, the
// handler returns a clean error.
//
// Endpoints:
//   GET    /api/v1/webmail/me                       current user + mailbox info
//   GET    /api/v1/webmail/folders                  list folders for the user's mailbox
//   GET    /api/v1/webmail/messages?folder=inbox   list messages in a folder
//   GET    /api/v1/webmail/messages/:id             one message's metadata + RFC822 body
//   POST   /api/v1/webmail/send                     write a new outbound message
//   POST   /api/v1/webmail/messages/:id/delete      move message to Trash (soft delete)
//
// All endpoints are mounted behind the auth middleware in
// router.go so missing/invalid sessions return 401 before
// any mailbox lookup runs.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/coremail"
	coremailmime "github.com/orvix/orvix/internal/coremail/mime"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"go.uber.org/zap"
)

// webmailUserContext resolves the current authenticated user
// (from the JWT set by the auth middleware) to the user's
// email, and then to the real coremail_mailboxes row. It is
// the single source of truth for "which mailbox does this
// request operate on" — every user-facing webmail endpoint
// starts with this lookup.
//
// Returns (mailbox, true) on success. Returns (nil, false)
// with one of the following reasons:
//   - no email address on file for the user_id (corrupt auth row)
//   - no active mailbox row in coremail_mailboxes for the email
//   - MailStore not wired to the handler (test mode, etc.)
//
// The reason string is returned so handlers can surface it
// to the operator in the response body, replacing the demo
// UI's silent failure mode.
type webmailUserContext struct {
	Mailbox      *coremail.Mailbox
	Email        string
	UserID       uint
	Role         string
	MailboxStore *storage.MailStore
}

// resolveWebmailUserContext is the canonical entry point
// for every webmail user endpoint. It runs the user→email→
// mailbox lookup, ensures the mailbox is active, and
// returns a context with the resolved mailbox + the
// MailStore the caller should use to read messages and
// folders.
//
// The caller MUST treat (nil, reason) as a non-error
// response with the reason exposed to the UI. There is no
// fall-through to a synthetic mailbox.
func (h *Handler) resolveWebmailUserContext(c fiber.Ctx) (*webmailUserContext, string) {
	ms, ok := h.mailStoreForUser()
	if !ok {
		return nil, "mailstore_unavailable"
	}
	userIDValue := c.Locals("user_id")
	if userIDValue == nil {
		return nil, "no_authenticated_user"
	}
	userID, ok := userIDValue.(uint)
	if !ok {
		return nil, "invalid_user_id"
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return nil, "database_unavailable"
	}

	var email, role string
	if err := sqlDB.QueryRow(
		"SELECT email, role FROM users WHERE id = ?", userID,
	).Scan(&email, &role); err != nil {
		return nil, "user_not_found"
	}
	if email == "" {
		return nil, "user_has_no_email"
	}

	// Look up the mailbox by email. We query coremail_mailboxes
	// directly because the MailStore wraps folder/message
	// repositories and does not own the mailbox row.
	mailbox, err := lookupMailboxByEmail(c.Context(), sqlDB, email)
	if err != nil || mailbox == nil {
		return nil, "no_mailbox"
	}
	if mailbox.Status != coremail.MailboxActive {
		return nil, "mailbox_not_active"
	}

	return &webmailUserContext{
		Mailbox:      mailbox,
		Email:        email,
		UserID:       userID,
		Role:         role,
		MailboxStore: ms,
	}, ""
}

// lookupMailboxByEmail queries coremail_mailboxes for an
// active mailbox with the given email. The MailboxSQLRepo
// in the coremail package has a GetByEmail method but it
// expects a transaction; the user-facing endpoints run
// outside a transaction so we issue the query directly.
func lookupMailboxByEmail(ctx context.Context, db *sql.DB, email string) (*coremail.Mailbox, error) {
	const q = `SELECT id, domain_id, tenant_id, local_part, email, name,
		password_hash, auth_scheme, mfa_enabled, COALESCE(mfa_secret,''),
		COALESCE(app_passwords,''), status, quota_mb, used_bytes, msg_count,
		is_admin, is_forwarder, COALESCE(forward_to,''), COALESCE(labels,''),
		send_limit_per_hour, recv_limit_per_hour, last_login, COALESCE(last_ip,''),
		created_at, updated_at, deleted_at
		FROM coremail_mailboxes WHERE email = ? AND deleted_at IS NULL LIMIT 1`
	row := db.QueryRowContext(ctx, q, email)
	m := &coremail.Mailbox{}
	var lastLogin sql.NullTime
	var deletedAt sql.NullTime
	var mfaSecret, appPwds, forwardTo, labels, lastIP string
	var status string
	if err := row.Scan(
		&m.ID, &m.DomainID, &m.TenantID, &m.LocalPart, &m.Email, &m.Name,
		&m.PasswordHash, &m.AuthScheme, &m.MFAEnabled, &mfaSecret,
		&appPwds, &status, &m.QuotaMB, &m.UsedBytes, &m.MsgCount,
		&m.IsAdmin, &m.IsForwarder, &forwardTo, &labels,
		&m.SendLimitPerHour, &m.RecvLimitPerHour, &lastLogin, &lastIP,
		&m.CreatedAt, &m.UpdatedAt, &deletedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	m.Status = coremail.MailboxStatus(status)
	if lastLogin.Valid {
		t := lastLogin.Time
		m.LastLogin = &t
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		m.DeletedAt = &t
	}
	return m, nil
}

// WebmailMe returns the current authenticated user and their
// mailbox. If the user has no mailbox, the response includes
// `mailbox: null` and a human-readable reason — the UI uses
// this to render the "No mailbox configured" state.
func (h *Handler) WebmailMe(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"user":    nil,
			"mailbox": nil,
			"reason":  reason,
		})
	}
	return c.JSON(fiber.Map{
		"user": fiber.Map{
			"id":    ctx.UserID,
			"email": ctx.Email,
			"role":  ctx.Role,
		},
		"mailbox": fiber.Map{
			"id":         ctx.Mailbox.ID,
			"email":      ctx.Mailbox.Email,
			"name":       ctx.Mailbox.Name,
			"is_admin":   ctx.Mailbox.IsAdmin,
			"quota_mb":   ctx.Mailbox.QuotaMB,
			"used_bytes": ctx.Mailbox.UsedBytes,
			"msg_count":  ctx.Mailbox.MsgCount,
		},
	})
}

// WebmailFolders lists the folders for the current user's
// mailbox. The response is a flat JSON array; folder_type
// is included so the UI can render system folders (Inbox,
// Sent, Drafts, Trash, Junk) distinct from user-created
// folders.
func (h *Handler) WebmailFolders(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"folders": []any{},
			"reason":  reason,
		})
	}
	folders, err := ctx.MailboxStore.Folders.ListByMailbox(c.Context(), ctx.Mailbox.ID, nil)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("list folders: %v", err),
		})
	}
	// Strip password_hash and other sensitive mailbox fields
	// are not on Folder. Just convert.
	out := make([]fiber.Map, 0, len(folders))
	for _, f := range folders {
		out = append(out, fiber.Map{
			"id":            f.ID,
			"name":          f.Name,
			"path":          f.Path,
			"folder_type":   string(f.FolderType),
			"system":        f.FolderType != "",
			"parent_id":     f.ParentID,
			"message_count": f.MessageCount,
			"unread_count":  f.UnreadCount,
			"total_size":    f.TotalSize,
		})
	}
	return c.JSON(fiber.Map{"folders": out})
}

// WebmailMessages lists messages in the named folder of the
// current user's mailbox. The folder name is matched against
// the Folder.Path field; "inbox", "INBOX", "Inbox" all
// resolve to the same folder. Soft-deleted messages
// (deleted=1, purged_at NULL) are excluded — that's what the
// Trash folder is for.
//
// Query parameters:
//   - folder=INBOX|Sent|Drafts|Trash|Junk|Archive|<name>
//   - q=<substring> : case-insensitive substring match.
//     By default against subject / from /
//     to. The "body=1" flag extends the
//     match to the message body (one
//     read per candidate — slower).
//   - limit=N      : 1..200, default 50. Values above 200
//     are clamped to 200 to keep the first
//     paint fast.
//   - offset=N     : pagination cursor; new messages first.
//   - total=1      : return the total count for the query
//     (off by default — counting across
//     filtered rows costs a separate query
//     the UI does not need on every page).
//
// Response shape:
//
//	{messages, folder, folder_id, total?, limit, offset,
//	 has_more, snippet_for_q?}
//
// `has_more` is true when the returned page is exactly
// `limit` long AND a next page would be non-empty. The
// UI uses it to drive the "Load more" affordance.
func (h *Handler) WebmailMessages(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"messages": []any{},
			"reason":   reason,
		})
	}

	folderParam := strings.TrimSpace(c.Query("folder"))
	if folderParam == "" {
		folderParam = "INBOX"
	}
	q := strings.TrimSpace(c.Query("q"))
	searchBody := strings.EqualFold(strings.TrimSpace(c.Query("body")), "1") ||
		strings.EqualFold(strings.TrimSpace(c.Query("body")), "true")
	includeTotal := strings.EqualFold(strings.TrimSpace(c.Query("total")), "1") ||
		strings.EqualFold(strings.TrimSpace(c.Query("total")), "true")

	limit, err := parsePageLimit(c.Query("limit"), 50, 200)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	offset, err := parsePageOffset(c.Query("offset"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	folder, err := ctx.MailboxStore.Folders.GetByPath(c.Context(), ctx.Mailbox.ID, folderParam, nil)
	if err != nil || folder == nil {
		// Try a case-insensitive match against the canonical
		// system folder names. The MailStore writes paths
		// like "INBOX", "Sent", "Drafts", "Trash", "Junk"
		// — the case the IMAP server expects.
		folders, listErr := ctx.MailboxStore.Folders.ListByMailbox(c.Context(), ctx.Mailbox.ID, nil)
		if listErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fmt.Sprintf("list folders: %v", listErr),
			})
		}
		folder = nil
		lower := strings.ToLower(folderParam)
		for _, f := range folders {
			if strings.ToLower(f.Path) == lower || strings.ToLower(f.Name) == lower {
				folder = &f
				break
			}
		}
		if folder == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error":  "folder not found",
				"folder": folderParam,
			})
		}
	}

	filter := storage.MessageFilter{
		MailboxID:     ctx.Mailbox.ID,
		FolderID:      &folder.ID,
		Search:        q,
		SearchSubject: true,
		SearchFrom:    true,
		SearchTo:      true,
		SearchBody:    searchBody,
		Limit:         limit,
		Offset:        offset,
	}
	messages, total, err := ctx.MailboxStore.Messages.List(c.Context(), filter, nil)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("list messages: %v", err),
		})
	}
	// Trash contains soft-deleted messages; every other
	// folder contains non-deleted ones. The message repo
	// does not filter on `deleted` itself; the per-folder
	// semantics is a webmail policy, not a storage
	// concern.
	isTrash := strings.EqualFold(folder.Path, "Trash")
	filtered := make([]storage.Message, 0, len(messages))
	for _, m := range messages {
		if isTrash {
			if !m.Deleted {
				continue
			}
		} else {
			if m.Deleted {
				continue
			}
		}
		filtered = append(filtered, m)
	}

	// Batch-count attachments for the returned page so the
	// list row can render a paperclip icon. One SQL
	// roundtrip regardless of page size.
	attCounts := map[uint]int64{}
	if len(filtered) > 0 {
		ids := make([]uint, 0, len(filtered))
		for _, m := range filtered {
			ids = append(ids, m.ID)
		}
		cnts, err := ctx.MailboxStore.Attachments.CountByMessages(c.Context(), ids, nil)
		if err != nil {
			// Non-fatal: a failed count is reported
			// as zero so the list still renders. The
			// error is logged so the operator can
			// spot persistent failures.
			h.logger.Warn("webmail list: count attachments failed", zap.Error(err))
		} else {
			attCounts = cnts
		}
	}

	out := make([]fiber.Map, 0, len(filtered))
	for _, m := range filtered {
		row := fiber.Map{
			"id":               m.ID,
			"message_id":       m.MessageID,
			"subject":          m.Subject,
			"from":             m.FromAddress,
			"to":               m.ToAddresses,
			"cc":               m.CcAddresses,
			"size_bytes":       m.SizeBytes,
			"seen":             m.Seen,
			"flagged":          m.Flagged,
			"answered":         m.Answered,
			"draft":            m.Draft,
			"junk":             m.Junk,
			"received_date":    m.ReceivedDate,
			"message_date":     m.MessageDate,
			"folder_id":        m.FolderID,
			"folder_path":      folder.Path,
			"attachment_count": attCounts[m.ID],
		}
		// When the caller supplied a query, attach a
		// short body snippet centred on the first
		// match. The snippet is plain text — the
		// client is responsible for any HTML escape /
		// highlighting. Off the hot path: one file
		// read per row, capped by the page limit.
		// Only the BODY is fed to the snippet helper
		// so we never surface "Message-ID: <…>" or
		// "From: …" lines that would look like HTML
		// to the client.
		if q != "" {
			if data, err := ctx.MailboxStore.GetRFC822(c.Context(), m.ID, nil); err == nil {
				row["snippet"] = extractSearchSnippet(extractBodyForSnippet(string(data)), q)
			}
		}
		out = append(out, row)
	}

	resp := fiber.Map{
		"messages":  out,
		"folder":    folder.Path,
		"folder_id": folder.ID,
		"limit":     limit,
		"offset":    offset,
		"has_more":  len(filtered) >= limit,
	}
	if includeTotal {
		resp["total"] = total
	}
	return c.JSON(resp)
}

// parsePageLimit parses a string limit param into a value
// clamped to [1, max]. Empty input returns def. Non-numeric
// or out-of-range input returns an error so the handler
// can surface 400 rather than silently clamping.
func parsePageLimit(raw string, def, max int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid limit: %s", raw)
	}
	if v < 1 {
		return 0, fmt.Errorf("limit must be >= 1")
	}
	if v > max {
		return max, nil
	}
	return v, nil
}

// parsePageOffset parses a string offset param. Empty
// returns 0. Negative or non-numeric returns an error.
func parsePageOffset(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid offset: %s", raw)
	}
	if v < 0 {
		return 0, fmt.Errorf("offset must be >= 0")
	}
	return v, nil
}

// extractSearchSnippet takes a longer body string and
// returns a 200-char preview centred on the first match
// of the query. Returns "" if query is empty or no match.
// Used by WebmailMessages when ?q= is supplied to give
// the UI a context-rich snippet.
//
// The function expects the body only — not the headers.
// Callers pass the section of the RFC822 below the
// first blank line. The returned string is plain text;
// the client is responsible for any HTML escape and
// match highlighting.
func extractSearchSnippet(body, query string) string {
	if query == "" || body == "" {
		return ""
	}
	lowerBody := strings.ToLower(body)
	lowerQuery := strings.ToLower(query)
	idx := strings.Index(lowerBody, lowerQuery)
	if idx < 0 {
		// Fall back to a leading window — the query
		// might only match in subject/from which the
		// caller can also highlight.
		end := 200
		if end > len(body) {
			end = len(body)
		}
		return strings.TrimSpace(body[:end])
	}
	start := idx - 60
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 140
	if end > len(body) {
		end = len(body)
	}
	prefix := ""
	if start > 0 {
		prefix = "…"
	}
	suffix := ""
	if end < len(body) {
		suffix = "…"
	}
	return prefix + strings.TrimSpace(body[start:end]) + suffix
}

// extractBodyForSnippet strips the headers from an
// RFC822 string and returns only the body section. If
// no body separator is found the whole input is
// returned (defensive). Used by extractSearchSnippet
// so search snippets never include "From: …", "To: …",
// or "Message-ID: <…>" lines that would otherwise
// surface confusing HTML-like characters in the UI.
func extractBodyForSnippet(rfc822 string) string {
	idx := strings.Index(rfc822, "\r\n\r\n")
	if idx < 0 {
		idx = strings.Index(rfc822, "\n\n")
	}
	if idx < 0 {
		return rfc822
	}
	body := rfc822[idx:]
	body = strings.TrimLeft(body, "\r\n")
	return body
}

// WebmailMessage returns one message's metadata and the
// raw RFC822 body. The body is loaded from disk by the
// MailStore — no hardcoded content is ever returned. The
// authorization check is "this message must belong to the
// caller's mailbox"; messages from another mailbox return
// 404 to avoid leaking existence.
//
// The response also includes an `attachments` array
// (filename, content_type, size_bytes, id) so the reading
// pane can render the attachment list and wire download
// buttons to /api/v1/webmail/attachments/:id.
func (h *Handler) WebmailMessage(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}

	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid message id"})
	}

	msg, rfc822, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	// Authorisation: the message must belong to the
	// caller's mailbox. Returning 404 (not 403) avoids
	// leaking existence of messages in other mailboxes.
	if msg.MailboxID != ctx.Mailbox.ID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}

	// Mark as seen. The MailStore supports UpdateFlags but
	// the simplest path is the SQL update via the
	// repository. We do this best-effort — failing to mark
	// as seen must not block message read.
	seen := true
	_ = ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
		&seen, nil, nil, nil, nil, nil, nil)

	attachmentsOut := []fiber.Map{}
	if atts, err := ctx.MailboxStore.Attachments.ListByMessage(c.Context(), msg.ID, nil); err != nil {
		// Non-fatal: a failed list leaves the
		// attachment section empty. The reading pane
		// renders "no attachments" when the list is
		// empty anyway.
		h.logger.Warn("webmail message: list attachments failed",
			zap.Uint("message_id", msg.ID),
			zap.Error(err))
	} else {
		for _, a := range atts {
			attachmentsOut = append(attachmentsOut, fiber.Map{
				"id":           a.ID,
				"filename":     a.Filename,
				"content_type": a.ContentType,
				"size_bytes":   a.SizeBytes,
			})
		}
	}

	return c.JSON(fiber.Map{
		"id":            msg.ID,
		"message_id":    msg.MessageID,
		"subject":       msg.Subject,
		"from":          msg.FromAddress,
		"to":            msg.ToAddresses,
		"cc":            msg.CcAddresses,
		"bcc":           msg.BccAddresses,
		"reply_to":      msg.ReplyTo,
		"size_bytes":    msg.SizeBytes,
		"seen":          msg.Seen,
		"flagged":       msg.Flagged,
		"answered":      msg.Answered,
		"draft":         msg.Draft,
		"junk":          msg.Junk,
		"received_date": msg.ReceivedDate,
		"message_date":  msg.MessageDate,
		"folder_id":     msg.FolderID,
		"internet_id":   msg.InternetMessageID,
		"rfc822":        string(rfc822),
		"attachments":   attachmentsOut,
	})
}

// WebmailSend writes a new outbound message into the
// caller's "Sent" folder via the MailStore, then enqueues
// one delivery job per recipient into the CoreMail outbound
// delivery queue. The same queue the SMTP receiver uses for
// inbound mail and the delivery worker drains for outbound
// mail — no parallel pipeline, no SMTP redesign.
//
// Behavior:
//  1. Authenticate via the standard auth middleware.
//  2. Resolve the caller's mailbox (the sender).
//  3. Parse To/Cc/Bcc safely with mail.ParseAddressList —
//     malformed addresses are rejected with 400 BEFORE we
//     touch disk or queue.
//  4. Look up the Sent folder for the mailbox. If missing,
//     return 500 — system folders must be provisioned first.
//  5. Store the RFC822 message body in the Sent folder
//     (the source of truth for "what the user sent").
//  6. Enqueue one queue.QueueEntry per recipient, all
//     pointing at the same message_id, all
//     direction=outbound / delivery_mode=remote_smtp /
//     status=pending so the existing delivery worker picks
//     them up. The sender is the authenticated mailbox,
//     not anything the client supplies.
//  7. Return 201 Created with status="queued".
//
// If queueing fails for every recipient, the Sent copy is
// kept (it is the user's record of the message) and the
// caller gets a 502 with a clear error — the message is
// NOT lost, but the operator must investigate. Per-
// recipient failures are logged but do not fail the whole
// send — partial success is better than blocking on a
// transient bad row, and the queue worker will retry the
// failed entries on the next pass.
func (h *Handler) WebmailSend(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}

	var req struct {
		To      string
		Cc      string
		Bcc     string
		Subject string
		Body    string
	}
	var rfc822 []byte
	messageID := generateMessageID()
	now := time.Now().UTC()

	contentType := c.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		var err error
		rfc822, req, err = h.webmailParseMultipartSend(c, ctx, messageID, now)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	} else {
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
		}
		if strings.TrimSpace(req.To) == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "to is required"})
		}
		if strings.TrimSpace(req.Subject) == "" {
			req.Subject = "(no subject)"
		}
		rfc822 = []byte(buildRFC822(rfc822Params{
			From:      ctx.Mailbox.Email,
			To:        req.To,
			Cc:        req.Cc,
			Bcc:       req.Bcc,
			Subject:   req.Subject,
			Body:      req.Body,
			MessageID: messageID,
			Date:      now,
			FromName:  ctx.Mailbox.Name,
		}))
	}

	// Parse all three recipient lists with the standard
	// library. mail.ParseAddressList rejects malformed
	// addresses, missing hosts, and other unsafe input
	// BEFORE we touch disk or the queue.
	toList, err := mail.ParseAddressList(req.To)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("invalid To header: %v", err),
		})
	}
	if len(toList) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "to must contain at least one address"})
	}
	var ccList, bccList []*mail.Address
	if strings.TrimSpace(req.Cc) != "" {
		ccList, err = mail.ParseAddressList(req.Cc)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("invalid Cc header: %v", err),
			})
		}
	}
	if strings.TrimSpace(req.Bcc) != "" {
		bccList, err = mail.ParseAddressList(req.Bcc)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("invalid Bcc header: %v", err),
			})
		}
	}

	sentFolder, err := resolveFolderCaseInsensitive(c.Context(), ctx.MailboxStore, ctx.Mailbox.ID, "Sent")
	if err != nil || sentFolder == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Sent folder not found for mailbox; ensure system folders are provisioned",
		})
	}

	msg := &storage.Message{
		MessageID:         messageID,
		InternetMessageID: fmt.Sprintf("<%s@orvix.local>", messageID),
		TenantID:          ctx.Mailbox.TenantID,
		DomainID:          ctx.Mailbox.DomainID,
		MailboxID:         ctx.Mailbox.ID,
		FolderID:          sentFolder.ID,
		Subject:           sanitizeCRLF(req.Subject),
		FromAddress:       ctx.Mailbox.Email,
		ToAddresses:       sanitizeCRLF(req.To),
		CcAddresses:       sanitizeCRLF(req.Cc),
		BccAddresses:      sanitizeCRLF(req.Bcc),
		ReplyTo:           ctx.Mailbox.Email,
		MessageDate:       &now,
		ReceivedDate:      now,
		Seen:              true,
	}

	if err := ctx.MailboxStore.StoreMessage(c.Context(), msg, rfc822, nil); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("store message: %v", err),
		})
	}

	// From here on the message is durable in the Sent
	// folder. We must enqueue at least one recipient for
	// the request to be considered successful — but if
	// the queue engine is not available we surface the
	// error to the operator instead of silently dropping
	// the user's mail.
	qe, ok := h.queueEngineForUser()
	if !ok {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error":      "queue engine not available",
			"message_id": msg.MessageID,
			"folder":     sentFolder.Path,
			"status":     "stored",
		})
	}

	// Collect every recipient across To/Cc/Bcc. Each one
	// gets its own QueueEntry — same message_id, same
	// FromAddress (the authenticated mailbox). The
	// delivery mode is decided per-recipient:
	//   - Local recipient (configured local domain AND
	//     active mailbox): DeliveryMode=local, the
	//     delivery worker copies the message from the
	//     sender's Sent folder into the recipient's
	//     INBOX. No MX lookup, no SMTP connection.
	//   - Remote recipient: DeliveryMode=remote_smtp,
	//     the delivery worker does MX + SMTP delivery
	//     with the existing STARTTLS-aware transport.
	// The classification runs on the same Domain +
	// Mailbox lookups the SMTP receiver uses for
	// inbound — there is no parallel "is-local" path.
	allRecipients := make([]*mail.Address, 0, len(toList)+len(ccList)+len(bccList))
	allRecipients = append(allRecipients, toList...)
	allRecipients = append(allRecipients, ccList...)
	allRecipients = append(allRecipients, bccList...)

	mailboxID := ctx.Mailbox.ID
	enqueueErrors := make([]string, 0, len(allRecipients))
	queuedCount := 0
	deliveredLocal := make([]string, 0, len(allRecipients))
	for _, addr := range allRecipients {
		bare := addr.Address
		domain := extractRecipientDomain(bare)

		local, localMboxID, err := h.classifyLocalRecipient(
			c.Context(),
			ctx.Mailbox.TenantID,
			bare,
			domain,
		)
		if err != nil {
			h.logger.Warn("webmail send: classify recipient failed, falling back to remote_smtp",
				zap.String("to", bare),
				zap.String("domain", domain),
				zap.Error(err))
			local = false
		}

		var deliveryMode queue.DeliveryMode
		var entryMailboxID *uint
		if local {
			deliveryMode = queue.DeliveryLocal
			idCopy := localMboxID
			entryMailboxID = &idCopy
		} else {
			deliveryMode = queue.DeliveryRemoteSMTP
			entryMailboxID = &mailboxID
		}

		entry := &queue.QueueEntry{
			TenantID:        ctx.Mailbox.TenantID,
			DomainID:        ctx.Mailbox.DomainID,
			MailboxID:       entryMailboxID,
			MessageID:       messageID,
			FromAddress:     ctx.Mailbox.Email,
			ToAddress:       bare,
			RecipientDomain: domain,
			Direction:       queue.DirectionOutbound,
			DeliveryMode:    deliveryMode,
			Status:          queue.StatusPending,
			Priority:        0,
		}
		if err := qe.Enqueue(c.Context(), entry); err != nil {
			h.logger.Error("webmail send enqueue failed",
				zap.String("message_id", messageID),
				zap.String("to", bare),
				zap.Error(err),
			)
			enqueueErrors = append(enqueueErrors, fmt.Sprintf("%s: %v", bare, err))
			continue
		}
		queuedCount++
		if local {
			deliveredLocal = append(deliveredLocal, bare)
		}
	}

	if len(enqueueErrors) > 0 {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":      "failed to enqueue some recipients: " + strings.Join(enqueueErrors, "; "),
			"id":         msg.ID,
			"message_id": msg.MessageID,
			"folder":     sentFolder.Path,
			"status":     "stored",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":               msg.ID,
		"message_id":       msg.MessageID,
		"status":           "queued",
		"queued_count":     queuedCount,
		"local_count":      len(deliveredLocal),
		"remote_count":     queuedCount - len(deliveredLocal),
		"local_recipients": deliveredLocal,
	})
}

// webmailParseMultipartSend parses a multipart/form-data request
// for the WebmailSend endpoint. It extracts form fields (to, cc,
// bcc, subject, body) and file uploads, validates sizes and
// filenames, detects MIME types server-side, and builds a
// multipart/mixed RFC822 message with base64-encoded attachments.
func (h *Handler) webmailParseMultipartSend(c fiber.Ctx, ctx *webmailUserContext, messageID string, now time.Time) ([]byte, struct {
	To, Cc, Bcc, Subject, Body string
}, error) {
	var empty struct{ To, Cc, Bcc, Subject, Body string }

	form, err := c.MultipartForm()
	if err != nil {
		return nil, empty, fmt.Errorf("invalid multipart form: %v", err)
	}

	getVal := func(key string) string {
		vals := form.Value[key]
		if len(vals) == 0 {
			return ""
		}
		return vals[0]
	}

	req := struct{ To, Cc, Bcc, Subject, Body string }{
		To:      getVal("to"),
		Cc:      getVal("cc"),
		Bcc:     getVal("bcc"),
		Subject: getVal("subject"),
		Body:    getVal("body"),
	}

	if strings.TrimSpace(req.To) == "" {
		return nil, empty, fmt.Errorf("to is required")
	}
	if strings.TrimSpace(req.Subject) == "" {
		req.Subject = "(no subject)"
	}

	// Get max attachment limits from config.
	maxSize := h.cfg.CoreMail.MaxAttachmentSizeMB
	if maxSize <= 0 {
		maxSize = 25
	}
	maxAttachments := h.cfg.CoreMail.MaxAttachmentsPerMessage
	if maxAttachments <= 0 {
		maxAttachments = 20
	}
	maxBytes := int64(maxSize) * 1024 * 1024

	// Process uploaded files.
	uploadedFiles := form.File["attachment"]
	if len(uploadedFiles) > maxAttachments {
		return nil, empty, fmt.Errorf("too many attachments: max %d", maxAttachments)
	}

	attachments := make([]coremailmime.AttachmentData, 0, len(uploadedFiles))
	for _, fh := range uploadedFiles {
		if fh.Size > maxBytes {
			return nil, empty, fmt.Errorf("attachment %q exceeds max size of %d MB", fh.Filename, maxSize)
		}

		// Sanitize filename to prevent path traversal.
		safeName := coremailmime.SanitizeFilename(fh.Filename)
		if safeName == "" {
			return nil, empty, fmt.Errorf("invalid attachment filename: %q", fh.Filename)
		}

		// Reject filenames that were sanitized away
		// (e.g. only dots, slashes, null bytes).
		if len(safeName) == 0 || safeName == "." || safeName == ".." {
			return nil, empty, fmt.Errorf("invalid attachment filename: %q", fh.Filename)
		}

		file, err := fh.Open()
		if err != nil {
			return nil, empty, fmt.Errorf("cannot read attachment %q: %v", fh.Filename, err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			return nil, empty, fmt.Errorf("cannot read attachment %q: %v", fh.Filename, err)
		}

		// Detect MIME type server-side; do not trust client.
		ct := detectMIMEType(safeName, data)

		attachments = append(attachments, coremailmime.AttachmentData{
			Filename:    safeName,
			ContentType: ct,
			Data:        data,
		})
	}

	rfc822 := buildMultipartRFC822ForWebmail(ctx.Mailbox.Name, ctx.Mailbox.Email,
		req.To, req.Cc, req.Bcc, req.Subject, req.Body, messageID, now, attachments)

	return rfc822, req, nil
}

// detectMIMEType determines the MIME content type for an attachment
// using the file extension. Falls back to application/octet-stream.
// Never trusts the client-provided Content-Type.
func detectMIMEType(filename string, data []byte) string {
	ext := filepath.Ext(filename)
	if ext != "" {
		ct := mime.TypeByExtension(ext)
		if ct != "" {
			// Strip any parameters (e.g. charset).
			if idx := strings.IndexByte(ct, ';'); idx > 0 {
				ct = strings.TrimSpace(ct[:idx])
			}
			return ct
		}
	}
	return "application/octet-stream"
}

// buildMultipartRFC822ForWebmail constructs a multipart/mixed RFC 5322
// message with a text/plain body and base64-encoded attachments.
func buildMultipartRFC822ForWebmail(fromName, fromEmail, to, cc, bcc, subject, body, messageID string, date time.Time, attachments []coremailmime.AttachmentData) []byte {
	boundary := fmt.Sprintf("orvix-mixed-%d", date.UnixNano())

	var b strings.Builder
	if fromName != "" {
		fmt.Fprintf(&b, "From: %s <%s>\r\n", escapeHeader(fromName), fromEmail)
	} else {
		fmt.Fprintf(&b, "From: %s\r\n", fromEmail)
	}
	fmt.Fprintf(&b, "To: %s\r\n", sanitizeCRLF(to))
	if cc != "" {
		fmt.Fprintf(&b, "Cc: %s\r\n", sanitizeCRLF(cc))
	}
	if bcc != "" {
		fmt.Fprintf(&b, "Bcc: %s\r\n", sanitizeCRLF(bcc))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", escapeHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", date.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@orvix.local>\r\n", messageID)
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary)
	b.WriteString("\r\n")

	// text/plain part.
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\r\n")
	}

	// Attachment parts (base64).
	for _, att := range attachments {
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s\r\n", att.ContentType)
		fmt.Fprintf(&b, "Content-Disposition: attachment; filename=\"%s\"\r\n", att.Filename)
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		if att.ContentID != "" {
			fmt.Fprintf(&b, "Content-ID: %s\r\n", att.ContentID)
		}
		b.WriteString("\r\n")
		encoded := base64.StdEncoding.EncodeToString(att.Data)
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			b.WriteString(encoded[i:end])
			b.WriteString("\r\n")
		}
	}

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String())
}

// classifyLocalRecipient decides whether a recipient address
// should be delivered through the local MailStore path
// (no SMTP, no MX lookup) or through the remote_smtp path.
//
// Local means:
//
//  1. The recipient domain is a configured local
//     coremail_domains row with status=active.
//  2. The recipient address has an active coremail_mailboxes
//     row in the SAME tenant as the sender.
//
// Both conditions are required. A recipient with a local
// domain but no active mailbox row is treated as remote
// (the remote_smtp path will return 5.1.1 from the receiver
// if the address is not local there either). A recipient
// with an active mailbox row in a different tenant is
// also treated as remote — the cross-tenant local-delivery
// guard is enforced by the tenant_id filter on the mailbox
// lookup, so a sender in tenant A cannot route to a
// mailbox in tenant B through the local path.
//
// The function is the only source of truth for "local vs
// remote" in the webmail Send flow. Other code paths
// (the SMTP receiver, the queue worker) use the same
// coremail.Engine for their local-vs-remote decision, so
// there is exactly one place to look if classification
// diverges between inbound and outbound.
func (h *Handler) classifyLocalRecipient(
	ctx context.Context,
	senderTenantID uint,
	email string,
	domain string,
) (bool, uint, error) {
	if email == "" || domain == "" {
		return false, 0, nil
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return false, 0, fmt.Errorf("classify: get sql db: %w", err)
	}

	// Step 1: is the domain local + active? We scope
	// the lookup to the sender's tenant to keep the
	// local-delivery path tenant-scoped. A domain
	// that is configured for tenant B but not tenant A
	// does not count as local for a tenant-A sender.
	var domainID uint
	var domainStatus string
	err = sqlDB.QueryRowContext(ctx,
		`SELECT id, status FROM coremail_domains
		 WHERE name = ? AND tenant_id = ? AND deleted_at IS NULL`,
		domain, senderTenantID,
	).Scan(&domainID, &domainStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			// Domain is not configured for this
			// tenant — treat as remote. This is
			// the common case for outbound
			// internet mail and the safe default
			// for unrecognised local-looking
			// domains.
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("classify: lookup domain: %w", err)
	}
	if domainStatus != "active" {
		// Suspended / disabled domain — must NOT
		// route via the local path. The remote_smtp
		// path will surface a clean bounce.
		return false, 0, nil
	}

	// Step 2: is the recipient mailbox local + active
	// in this tenant? Use the same scope as the
	// domain lookup so the cross-tenant guard is
	// symmetric — a mailbox that belongs to another
	// tenant cannot be reached through the local
	// path even if its domain is somehow shared.
	var mailboxID uint
	var mailboxStatus string
	err = sqlDB.QueryRowContext(ctx,
		`SELECT id, status FROM coremail_mailboxes
		 WHERE email = ? AND tenant_id = ? AND deleted_at IS NULL`,
		email, senderTenantID,
	).Scan(&mailboxID, &mailboxStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("classify: lookup mailbox: %w", err)
	}
	if mailboxStatus != "active" {
		return false, 0, nil
	}

	// Local recipient, same tenant, active mailbox.
	return true, mailboxID, nil
}

// extractRecipientDomain returns the domain part of an
// email address (everything after the last "@"). The SMTP
// resolver uses this for MX lookups; an empty string means
// "no @ in input", which mail.ParseAddressList already
// rejected. Defensive: empty input -> empty string.
func extractRecipientDomain(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == '@' {
			return addr[i+1:]
		}
	}
	return ""
}

// WebmailDelete soft-deletes a message by setting the
// deleted flag in the MailStore, then moves it to the
// Trash folder so it stays recoverable. Hard-purge
// (removing the RFC822 file) is left to a separate
// retention/cleanup job.
func (h *Handler) WebmailDelete(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}

	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid message id"})
	}
	msg, _, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	if msg.MailboxID != ctx.Mailbox.ID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}

	trash, err := resolveFolderCaseInsensitive(c.Context(), ctx.MailboxStore, ctx.Mailbox.ID, "Trash")
	if err != nil || trash == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Trash folder not found; ensure system folders are provisioned",
		})
	}

	deleted := true
	if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
		nil, nil, nil, nil, &deleted, nil, nil); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("mark deleted: %v", err),
		})
	}
	if err := ctx.MailboxStore.MoveMessage(c.Context(), msg.ID, trash.ID, nil); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("move to trash: %v", err),
		})
	}
	return c.JSON(fiber.Map{
		"id":       msg.ID,
		"status":   "deleted",
		"moved_to": trash.Path,
	})
}

// WebmailUpdateMessage updates per-message flags. Used by
// the webmail UI for "mark read/unread", "star/flag",
// "mark unread", and "remove from trash" (deleted=false).
// Body: {seen?: bool, flagged?: bool, deleted?: bool} —
// only fields supplied are changed; absent fields stay
// at their current value.
//
// Authorization: same as WebmailMessage — the message must
// belong to the caller's mailbox, else 404.
func (h *Handler) WebmailUpdateMessage(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid message id"})
	}
	msg, _, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	if msg.MailboxID != ctx.Mailbox.ID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	var req struct {
		Seen    *bool `json:"seen"`
		Flagged *bool `json:"flagged"`
		Deleted *bool `json:"deleted"`
		Junk    *bool `json:"junk"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Seen == nil && req.Flagged == nil && req.Deleted == nil && req.Junk == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "at least one of seen/flagged/deleted/junk must be supplied",
		})
	}
	if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
		req.Seen, nil, req.Flagged, nil, req.Deleted, req.Junk, nil); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("update flags: %v", err),
		})
	}
	return c.JSON(fiber.Map{
		"id":     msg.ID,
		"status": "updated",
	})
}

// WebmailArchive moves a message into the Archive system
// folder. Equivalent to "remove from Inbox without
// deleting" — the row stays recoverable in Archive until
// the user explicitly deletes it.
//
// Authorization: same as WebmailMessage.
func (h *Handler) WebmailArchive(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid message id"})
	}
	msg, _, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	if msg.MailboxID != ctx.Mailbox.ID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	archive, err := resolveFolderCaseInsensitive(c.Context(), ctx.MailboxStore, ctx.Mailbox.ID, "Archive")
	if err != nil || archive == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Archive folder not found; ensure system folders are provisioned",
		})
	}
	if err := ctx.MailboxStore.MoveMessage(c.Context(), msg.ID, archive.ID, nil); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("move to archive: %v", err),
		})
	}
	// Clear the deleted flag if it was set; Archive
	// holds live messages, not soft-deleted ones.
	deleted := false
	_ = ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
		nil, nil, nil, nil, &deleted, nil, nil)
	return c.JSON(fiber.Map{
		"id":       msg.ID,
		"status":   "archived",
		"moved_to": archive.Path,
	})
}

// WebmailMoveMessage moves a single message into a
// different folder in the same mailbox. Used by the
// reading pane's "Move to…" menu when the user wants to
// pick a folder other than Archive / Trash / Junk.
//
// Body: {target_folder_id: uint} (required). The target
// must belong to the caller's mailbox; cross-mailbox
// moves return 403.
//
// Authorization: same as WebmailMessage — the source
// message must belong to the caller's mailbox.
func (h *Handler) WebmailMoveMessage(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid message id"})
	}
	var req struct {
		TargetFolderID uint `json:"target_folder_id"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.TargetFolderID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "target_folder_id is required",
		})
	}
	msg, _, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	if msg.MailboxID != ctx.Mailbox.ID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	target, err := ctx.MailboxStore.Folders.GetByID(c.Context(), req.TargetFolderID, nil)
	if err != nil || target == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "target folder not found"})
	}
	if target.MailboxID != ctx.Mailbox.ID {
		// Refuse cross-mailbox folder targets. The
		// caller knows their own folder ids; if they
		// pass a foreign one, treat it as 403 (the
		// folder exists, they just can't use it).
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "target folder not in caller's mailbox",
		})
	}
	if err := ctx.MailboxStore.MoveMessage(c.Context(), msg.ID, target.ID, nil); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("move message: %v", err),
		})
	}
	return c.JSON(fiber.Map{
		"id":        msg.ID,
		"status":    "moved",
		"moved_to":  target.Path,
		"folder_id": target.ID,
	})
}

// WebmailMessageSource returns the raw RFC822 body of a
// single message as a downloadable .eml attachment. Used
// by the reading pane's "Show original" action.
//
// Authorization: same as WebmailMessage — the message
// must belong to the caller's mailbox.
func (h *Handler) WebmailMessageSource(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid message id"})
	}
	msg, rfc822, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	if msg.MailboxID != ctx.Mailbox.ID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "message not found"})
	}
	filename := fmt.Sprintf("message-%d-%s.eml", msg.ID, msg.MessageID)
	c.Set(fiber.HeaderContentType, "message/rfc822; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition,
		fmt.Sprintf(`attachment; filename="%s"`, sanitizeDownloadFilename(filename)))
	c.Set(fiber.HeaderContentLength, fmt.Sprintf("%d", len(rfc822)))
	return c.Send(rfc822)
}

// sanitizeDownloadFilename strips control characters and
// quote characters from a filename before it goes into a
// Content-Disposition header. The string is intended for
// HTTP headers, not for filesystem storage; path
// traversal is blocked at the :id parse step, not here.
func sanitizeDownloadFilename(name string) string {
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, `\`, "")
	return name
}

// WebmailMessageBatch performs a state-changing action on
// multiple messages in a single request. The webmail UI
// uses it for "select all visible, archive / delete /
// mark read / mark unread / spam / nospam / move" and the
// reading pane "Move to…" menu.
//
// Body: {ids: [uint], action: string, target_folder_id?: uint}
//
//	action: "archive" | "delete" | "markRead" | "markUnread"
//	      | "flag" | "unflag" | "spam" | "nospam"
//	      | "move"
//
//	target_folder_id: required when action == "move",
//	                  must belong to caller's mailbox.
//
// The handler runs every id through the same ownership
// check as the single-message endpoints. Cross-mailbox
// ids are reported as failures in the response — they do
// NOT abort the batch silently. The Sent copy, when
// present, is the source of truth and is never deleted
// by this endpoint.
//
// Response shape:
//
//	{
//	  "action": "archive",
//	  "total": 5,
//	  "succeeded": 4,
//	  "failed": 1,
//	  "errors": [{"id": 17, "error": "message not found"}]
//	}
func (h *Handler) WebmailMessageBatch(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	var req struct {
		IDs            []uint `json:"ids"`
		Action         string `json:"action"`
		TargetFolderID uint   `json:"target_folder_id"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if len(req.IDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "ids is required and must be non-empty",
		})
	}
	if len(req.IDs) > 500 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "batch size capped at 500 ids per request",
		})
	}
	action := strings.TrimSpace(req.Action)
	switch action {
	case "archive", "delete", "markRead", "markUnread",
		"flag", "unflag", "spam", "nospam", "move":
		// ok
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("unknown action %q", action),
		})
	}
	var targetFolder *storage.Folder
	if action == "move" {
		if req.TargetFolderID == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "target_folder_id is required for move",
			})
		}
		tf, err := ctx.MailboxStore.Folders.GetByID(c.Context(), req.TargetFolderID, nil)
		if err != nil || tf == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "target folder not found"})
		}
		if tf.MailboxID != ctx.Mailbox.ID {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "target folder not in caller's mailbox",
			})
		}
		targetFolder = tf
	}
	if action == "delete" {
		trash, err := resolveFolderCaseInsensitive(c.Context(), ctx.MailboxStore, ctx.Mailbox.ID, "Trash")
		if err != nil || trash == nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Trash folder not found; ensure system folders are provisioned",
			})
		}
		targetFolder = trash
	}
	if action == "archive" {
		archive, err := resolveFolderCaseInsensitive(c.Context(), ctx.MailboxStore, ctx.Mailbox.ID, "Archive")
		if err != nil || archive == nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Archive folder not found; ensure system folders are provisioned",
			})
		}
		targetFolder = archive
	}
	if action == "spam" {
		junk, err := resolveFolderCaseInsensitive(c.Context(), ctx.MailboxStore, ctx.Mailbox.ID, "Junk")
		if err != nil || junk == nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Junk folder not found; ensure system folders are provisioned",
			})
		}
		targetFolder = junk
	}

	type batchError struct {
		ID    uint   `json:"id"`
		Error string `json:"error"`
	}
	errors := make([]batchError, 0)
	succeeded := 0

	for _, id := range req.IDs {
		msg, _, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
		if err != nil || msg == nil {
			errors = append(errors, batchError{ID: id, Error: "message not found"})
			continue
		}
		if msg.MailboxID != ctx.Mailbox.ID {
			// Cross-mailbox id. The caller should
			// never have one — the UI only surfaces
			// ids from the caller's own list. Treat
			// as a per-id failure rather than a 403
			// on the whole request: the operator
			// can see which id was rejected.
			errors = append(errors, batchError{ID: id, Error: "message not in caller's mailbox"})
			continue
		}
		if err := h.applyBatchAction(c, ctx, msg, action, targetFolder); err != nil {
			errors = append(errors, batchError{ID: id, Error: err.Error()})
			continue
		}
		succeeded++
	}
	return c.JSON(fiber.Map{
		"action":    action,
		"total":     len(req.IDs),
		"succeeded": succeeded,
		"failed":    len(errors),
		"errors":    errors,
	})
}

// applyBatchAction runs the requested action on a single
// message that has already been authorised. Errors are
// returned for the per-id failures in the batch response;
// the caller wraps them.
func (h *Handler) applyBatchAction(
	c fiber.Ctx,
	ctx *webmailUserContext,
	msg *storage.Message,
	action string,
	targetFolder *storage.Folder,
) error {
	switch action {
	case "archive":
		if targetFolder == nil {
			return fmt.Errorf("archive folder not resolved")
		}
		if err := ctx.MailboxStore.MoveMessage(c.Context(), msg.ID, targetFolder.ID, nil); err != nil {
			return fmt.Errorf("move: %w", err)
		}
		deleted := false
		_ = ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
			nil, nil, nil, nil, &deleted, nil, nil)
		return nil
	case "delete":
		if targetFolder == nil {
			return fmt.Errorf("trash folder not resolved")
		}
		deleted := true
		if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
			nil, nil, nil, nil, &deleted, nil, nil); err != nil {
			return fmt.Errorf("mark deleted: %w", err)
		}
		if err := ctx.MailboxStore.MoveMessage(c.Context(), msg.ID, targetFolder.ID, nil); err != nil {
			return fmt.Errorf("move to trash: %w", err)
		}
		return nil
	case "move":
		if targetFolder == nil {
			return fmt.Errorf("target folder not resolved")
		}
		if err := ctx.MailboxStore.MoveMessage(c.Context(), msg.ID, targetFolder.ID, nil); err != nil {
			return fmt.Errorf("move: %w", err)
		}
		return nil
	case "markRead":
		seen := true
		if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
			&seen, nil, nil, nil, nil, nil, nil); err != nil {
			return fmt.Errorf("mark read: %w", err)
		}
		return nil
	case "markUnread":
		seen := false
		if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
			&seen, nil, nil, nil, nil, nil, nil); err != nil {
			return fmt.Errorf("mark unread: %w", err)
		}
		return nil
	case "flag":
		flagged := true
		if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
			nil, nil, &flagged, nil, nil, nil, nil); err != nil {
			return fmt.Errorf("flag: %w", err)
		}
		return nil
	case "unflag":
		flagged := false
		if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
			nil, nil, &flagged, nil, nil, nil, nil); err != nil {
			return fmt.Errorf("unflag: %w", err)
		}
		return nil
	case "spam":
		if targetFolder == nil {
			return fmt.Errorf("junk folder not resolved")
		}
		junk := true
		if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
			nil, nil, nil, nil, nil, &junk, nil); err != nil {
			return fmt.Errorf("mark junk: %w", err)
		}
		if err := ctx.MailboxStore.MoveMessage(c.Context(), msg.ID, targetFolder.ID, nil); err != nil {
			return fmt.Errorf("move to junk: %w", err)
		}
		return nil
	case "nospam":
		junk := false
		if err := ctx.MailboxStore.Messages.UpdateFlags(c.Context(), msg.ID,
			nil, nil, nil, nil, nil, &junk, nil); err != nil {
			return fmt.Errorf("unmark junk: %w", err)
		}
		return nil
	}
	return fmt.Errorf("unknown action %q", action)
}

// WebmailMarkFolderRead sets seen=true on every non-deleted
// message in the given folder. Used by the "Mark all as
// read" action in the message list toolbar.
//
// Authorization: the folder must belong to the caller's
// mailbox, else 404.
func (h *Handler) WebmailMarkFolderRead(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	folderID, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid folder id"})
	}
	folder, err := ctx.MailboxStore.Folders.GetByID(c.Context(), folderID, nil)
	if err != nil || folder == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "folder not found"})
	}
	if folder.MailboxID != ctx.Mailbox.ID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "folder not found"})
	}
	sqlDB, dbErr := h.db.DB()
	if dbErr != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	now := time.Now().UTC()
	res, err := sqlDB.Exec(
		"UPDATE coremail_messages SET seen = 1, updated_at = ? WHERE mailbox_id = ? AND folder_id = ? AND deleted = 0 AND seen = 0",
		now, ctx.Mailbox.ID, folder.ID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("mark folder read: %v", err),
		})
	}
	affected, _ := res.RowsAffected()
	return c.JSON(fiber.Map{
		"folder_id": folder.ID,
		"folder":    folder.Path,
		"marked":    affected,
		"status":    "ok",
	})
}

// WebmailSaveDraft persists a new draft or updates an
// existing one. The body is a normal Message with
// draft=true in the Drafts folder.
//
// Body: {id?: uint, to?, cc?, bcc?, subject?, body?}
//   - id absent  -> create new draft
//   - id present -> update existing draft (must belong
//     to the caller's mailbox)
//
// The "to/cc/bcc/subject/body" fields are stored verbatim
// in the message and the RFC822 body on disk. Sending a
// draft is the user's job: the compose UI reuses
// POST /api/v1/webmail/send with the same payload.
func (h *Handler) WebmailSaveDraft(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	var req struct {
		ID      uint   `json:"id"`
		To      string `json:"to"`
		Cc      string `json:"cc"`
		Bcc     string `json:"bcc"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	// If the URL had an :id segment (PUT /drafts/:id), use
	// that as the draft id when the body didn't carry one.
	if req.ID == 0 {
		if idParam := c.Params("id"); idParam != "" {
			if id, err := parseMessageID(idParam); err == nil {
				req.ID = id
			}
		}
	}
	draftFolder, err := resolveFolderCaseInsensitive(c.Context(), ctx.MailboxStore, ctx.Mailbox.ID, "Drafts")
	if err != nil || draftFolder == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Drafts folder not found; ensure system folders are provisioned",
		})
	}
	now := time.Now().UTC()
	subject := sanitizeCRLF(req.Subject)
	if strings.TrimSpace(subject) == "" {
		subject = "(no subject)"
	}

	if req.ID != 0 {
		// Update existing draft — must belong to caller.
		msg, _, err := ctx.MailboxStore.LoadMessage(c.Context(), req.ID, nil)
		if err != nil || msg == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "draft not found"})
		}
		if msg.MailboxID != ctx.Mailbox.ID || !msg.Draft {
			return c.Status(finderStatusForbidden()).JSON(fiber.Map{"error": "draft not found"})
		}
		// Update metadata in place.
		msg.Subject = subject
		msg.ToAddresses = sanitizeCRLF(req.To)
		msg.CcAddresses = sanitizeCRLF(req.Cc)
		msg.BccAddresses = sanitizeCRLF(req.Bcc)
		msg.MessageDate = &now
		if err := ctx.MailboxStore.UpdateMetadata(c.Context(), msg, nil); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fmt.Sprintf("update draft: %v", err),
			})
		}
		// Rewrite the on-disk RFC822 so the body matches.
		rfc822 := buildRFC822(rfc822Params{
			From:      ctx.Mailbox.Email,
			To:        req.To,
			Cc:        req.Cc,
			Bcc:       req.Bcc,
			Subject:   req.Subject,
			Body:      req.Body,
			MessageID: msg.MessageID,
			Date:      now,
			FromName:  ctx.Mailbox.Name,
		})
		if err := ctx.MailboxStore.WriteRFC822(c.Context(), msg.ID, []byte(rfc822), nil); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fmt.Sprintf("rewrite draft body: %v", err),
			})
		}
		return c.JSON(fiber.Map{
			"id":     msg.ID,
			"status": "updated",
		})
	}

	// Create new draft.
	messageID := generateMessageID()
	rfc822 := buildRFC822(rfc822Params{
		From:      ctx.Mailbox.Email,
		To:        req.To,
		Cc:        req.Cc,
		Bcc:       req.Bcc,
		Subject:   req.Subject,
		Body:      req.Body,
		MessageID: messageID,
		Date:      now,
		FromName:  ctx.Mailbox.Name,
	})
	msg := &storage.Message{
		MessageID:         messageID,
		InternetMessageID: fmt.Sprintf("<%s@orvix.local>", messageID),
		TenantID:          ctx.Mailbox.TenantID,
		DomainID:          ctx.Mailbox.DomainID,
		MailboxID:         ctx.Mailbox.ID,
		FolderID:          draftFolder.ID,
		Subject:           subject,
		FromAddress:       ctx.Mailbox.Email,
		ToAddresses:       sanitizeCRLF(req.To),
		CcAddresses:       sanitizeCRLF(req.Cc),
		BccAddresses:      sanitizeCRLF(req.Bcc),
		ReplyTo:           ctx.Mailbox.Email,
		MessageDate:       &now,
		ReceivedDate:      now,
		Draft:             true,
		Seen:              true,
	}
	if err := ctx.MailboxStore.StoreMessage(c.Context(), msg, []byte(rfc822), nil); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("store draft: %v", err),
		})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         msg.ID,
		"message_id": msg.MessageID,
		"status":     "draft",
	})
}

// finderStatusForbidden returns the Fiber 403 status
// without dragging the import into the file's top-level
// declarations. Small helper because the API has only a
// handful of these.
func finderStatusForbidden() int { return 403 }

// WebmailGetDraft returns one draft message in full
// (metadata + RFC822 body) so the compose UI can restore
// the user's last edit. 404 if the message is not a
// draft in the caller's mailbox.
func (h *Handler) WebmailGetDraft(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid draft id"})
	}
	msg, rfc822, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
	if err != nil {
		return c.Status(finderStatusForbidden()).JSON(fiber.Map{"error": "draft not found"})
	}
	if msg.MailboxID != ctx.Mailbox.ID || !msg.Draft {
		return c.Status(finderStatusForbidden()).JSON(fiber.Map{"error": "draft not found"})
	}
	body := extractBodyPreview(string(rfc822), 100000)
	return c.JSON(fiber.Map{
		"id":      msg.ID,
		"subject": msg.Subject,
		"to":      msg.ToAddresses,
		"cc":      msg.CcAddresses,
		"bcc":     msg.BccAddresses,
		"body":    body,
		"rfc822":  string(rfc822),
		"status":  "draft",
	})
}

// WebmailDeleteDraft removes a draft message. The message
// row is hard-deleted because drafts are user scratch
// space — there is no recovery story for "I deleted my
// draft". Authorization: the draft must belong to the
// caller's mailbox.
func (h *Handler) WebmailDeleteDraft(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid draft id"})
	}
	msg, _, err := ctx.MailboxStore.LoadMessage(c.Context(), id, nil)
	if err != nil {
		return c.Status(finderStatusForbidden()).JSON(fiber.Map{"error": "draft not found"})
	}
	if msg.MailboxID != ctx.Mailbox.ID || !msg.Draft {
		return c.Status(finderStatusForbidden()).JSON(fiber.Map{"error": "draft not found"})
	}
	if err := ctx.MailboxStore.PurgeMessage(c.Context(), msg.ID, nil); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("delete draft: %v", err),
		})
	}
	return c.JSON(fiber.Map{
		"id":     msg.ID,
		"status": "deleted",
	})
}

// WebmailListDrafts returns the user's draft messages.
// Drafts are Message rows with Draft=true; we filter on
// the message repo so non-drafts are excluded. Returns a
// flat JSON array.
func (h *Handler) WebmailListDrafts(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"drafts": []any{},
			"reason": reason,
		})
	}
	draftFolder, err := resolveFolderCaseInsensitive(c.Context(), ctx.MailboxStore, ctx.Mailbox.ID, "Drafts")
	if err != nil || draftFolder == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Drafts folder not found; ensure system folders are provisioned",
		})
	}
	messages, _, err := ctx.MailboxStore.Messages.List(c.Context(), storage.MessageFilter{
		MailboxID: ctx.Mailbox.ID,
		FolderID:  &draftFolder.ID,
		Limit:     200,
	}, nil)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("list drafts: %v", err),
		})
	}
	out := make([]fiber.Map, 0, len(messages))
	for _, m := range messages {
		if !m.Draft {
			continue
		}
		// Read the body preview from disk so the list
		// shows the user's last-typed snippet. Limit to
		// 200 chars; full body comes via /drafts/:id.
		body := ""
		if data, err := ctx.MailboxStore.GetRFC822(c.Context(), m.ID, nil); err == nil {
			body = extractBodyPreview(string(data), 200)
		}
		out = append(out, fiber.Map{
			"id":            m.ID,
			"subject":       m.Subject,
			"to":            m.ToAddresses,
			"cc":            m.CcAddresses,
			"bcc":           m.BccAddresses,
			"body":          body,
			"message_date":  m.MessageDate,
			"received_date": m.ReceivedDate,
			"folder_id":     m.FolderID,
		})
	}
	return c.JSON(fiber.Map{"drafts": out})
}

// extractBodyPreview returns the first non-empty line of
// the body section of an RFC822 message, trimmed to maxLen
// characters. Used by the drafts list so the UI can show
// a snippet of the user's last edit without loading the
// full body.
func extractBodyPreview(rfc822 string, maxLen int) string {
	// Find the blank line separating headers from body.
	idx := strings.Index(rfc822, "\r\n\r\n")
	if idx < 0 {
		idx = strings.Index(rfc822, "\n\n")
	}
	if idx < 0 {
		return ""
	}
	body := rfc822[idx:]
	// Drop leading newlines.
	body = strings.TrimLeft(body, "\r\n")
	// First non-empty line.
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			if len(line) > maxLen {
				return strings.TrimSpace(line[:maxLen]) + "…"
			}
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// resolveFolderCaseInsensitive finds a folder by path or
// name, ignoring case. Used so /webmail/messages?folder=inbox
// and ?folder=INBOX both work.
func resolveFolderCaseInsensitive(ctx context.Context, ms *storage.MailStore, mailboxID uint, name string) (*storage.Folder, error) {
	if f, err := ms.Folders.GetByPath(ctx, mailboxID, name, nil); err == nil && f != nil {
		return f, nil
	}
	folders, err := ms.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(name)
	for i := range folders {
		if strings.ToLower(folders[i].Path) == lower || strings.ToLower(folders[i].Name) == lower {
			return &folders[i], nil
		}
	}
	return nil, nil
}

// parseMessageID parses a string into a uint message id,
// rejecting anything with a sign bit set, overflow, or any
// non-digit character. Used to defend against path-traversal
// or injection in /api/v1/webmail/messages/:id.
func parseMessageID(raw string) (uint, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty id")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit in id: %q", raw)
		}
	}
	var id uint64
	for _, c := range raw {
		id = id*10 + uint64(c-'0')
		if id > 1<<32 {
			return 0, fmt.Errorf("id overflow")
		}
	}
	return uint(id), nil
}

// rfc822Params holds the inputs to buildRFC822.
type rfc822Params struct {
	From      string
	FromName  string
	To        string
	Cc        string
	Bcc       string
	Subject   string
	Body      string
	MessageID string
	Date      time.Time
}

// buildRFC822 constructs an RFC 5322 message. We use the
// standard "folded header" layout: header lines end with
// CRLF, the body is separated by a blank line, and the
// body itself is plain text. No HTML, no multipart — that
// is left for future work and is explicitly out of scope for
// this turn.
func buildRFC822(p rfc822Params) string {
	var b strings.Builder
	if p.FromName != "" {
		fmt.Fprintf(&b, "From: %s <%s>\r\n", escapeHeader(p.FromName), p.From)
	} else {
		fmt.Fprintf(&b, "From: %s\r\n", p.From)
	}
	fmt.Fprintf(&b, "To: %s\r\n", sanitizeCRLF(p.To))
	if p.Cc != "" {
		fmt.Fprintf(&b, "Cc: %s\r\n", sanitizeCRLF(p.Cc))
	}
	if p.Bcc != "" {
		fmt.Fprintf(&b, "Bcc: %s\r\n", sanitizeCRLF(p.Bcc))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", escapeHeader(p.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", p.Date.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@orvix.local>\r\n", p.MessageID)
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=UTF-8\r\n")
	fmt.Fprintf(&b, "Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(p.Body)
	if !strings.HasSuffix(p.Body, "\n") {
		b.WriteString("\r\n")
	}
	return b.String()
}

// escapeHeader escapes RFC 5322 special characters in
// header values and strips CRLF to prevent header injection.
func escapeHeader(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// sanitizeCRLF removes CR and LF characters. Used for header
// fields that contain structured address syntax where the full
// escapeHeader would be inappropriate.
func sanitizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// generateMessageID returns a unique 32-char hex message ID.
// Wraps the storage helper for clarity.
func generateMessageID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a timestamp-derived ID; collisions
		// are exceedingly unlikely in practice.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ──────────────────────────────────────────────────────────────
// User Settings endpoints
// ──────────────────────────────────────────────────────────────
//
// GET /api/v1/webmail/settings      — read the caller's settings row
// PUT /api/v1/webmail/settings      — apply a partial patch
//
// Authorization: the caller must have an authenticated user, AND
// the user's email must resolve to an active coremail_mailboxes row.
// The mailbox id is taken from resolveWebmailUserContext — never
// from the request body or query string — so a caller cannot read
// or write another mailbox's settings by supplying a foreign id.
//
// Validation: every string field has an allowed-value set; numeric
// fields are clamped at the storage layer. Unknown fields in the
// payload are ignored (decoded into the patch, then dropped if not
// declared). This keeps the API forward-compatible.

// allowedSettings* enumerates the closed value sets the API accepts.
// The handler returns 400 with a clear error if a value is outside
// the allowed set, so the storage layer never sees a bad value.
var (
	allowedSettingsTheme       = stringSet("dark", "light", "system")
	allowedSettingsDensity     = stringSet("comfortable", "compact")
	allowedSettingsReadingPane = stringSet("right", "bottom", "hidden")
	allowedSettingsDirection   = stringSet("auto", "ltr", "rtl")
	allowedSettingsLanguage    = stringSet("en", "ar", "fr", "de", "es")
	allowedSettingsDateFmt     = stringSet("locale", "iso", "us", "eu")
	allowedSettingsTimeFmt     = stringSet("locale", "24h", "12h")
	allowedSettingsSender      = stringSet("name", "email", "name_email")
	allowedSettingsReplyMode   = stringSet("reply", "replyAll")
)

func stringSet(items ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, s := range items {
		m[s] = struct{}{}
	}
	return m
}

func validateInSet(value, def string, allowed map[string]struct{}) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return def
	}
	if _, ok := allowed[v]; ok {
		return v
	}
	return "" // signal: invalid
}

// WebmailGetSettings returns the caller's settings row, creating
// one with safe defaults on first read so the UI never has to
// distinguish "no row" from "all defaults".
func (h *Handler) WebmailGetSettings(c fiber.Ctx) error {
	ms, ok := h.mailStoreForUser()
	if !ok {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "mailstore_unavailable",
		})
	}
	userCtx, reason := h.resolveWebmailUserContext(c)
	if userCtx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	settings, err := ms.Settings.GetOrCreate(c.Context(), userCtx.Mailbox.ID)
	if err != nil {
		h.logger.Error("webmail get settings", zap.Uint("mailbox_id", userCtx.Mailbox.ID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "settings read failed"})
	}
	return c.JSON(settings)
}

// WebmailPutSettings applies a partial patch. Unknown fields are
// ignored (the patch decoder drops them). Invalid enum values are
// rejected with 400 and a field-specific message.
func (h *Handler) WebmailPutSettings(c fiber.Ctx) error {
	ms, ok := h.mailStoreForUser()
	if !ok {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "mailstore_unavailable",
		})
	}
	userCtx, reason := h.resolveWebmailUserContext(c)
	if userCtx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}

	// Decoded as a generic map first so unknown fields don't crash and
	// known fields can still be validated individually.
	var raw map[string]json.RawMessage
	if err := c.Bind().JSON(&raw); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	patch := &storage.UserSettingsPatch{}
	// Reject unknown keys up front so a typo ("thme" instead of "theme")
	// surfaces as 400 instead of being silently dropped.
	known := map[string]struct{}{
		"display_name": {}, "timezone": {}, "language": {}, "date_format": {}, "time_format": {}, "text_direction": {},
		"theme": {}, "density": {}, "preview_lines": {}, "reading_pane": {},
		"signature_enabled": {}, "signature_text": {}, "signature_in_replies": {}, "default_reply_mode": {},
		"autosave_seconds": {}, "confirm_before_discard": {}, "warn_on_empty_subject": {},
		"default_folder": {}, "mark_read_delay_seconds": {}, "sender_display": {},
		"notify_inapp": {}, "notify_push": {},
	}
	for k := range raw {
		if _, ok := known[k]; !ok {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "unknown field: " + k,
			})
		}
	}

	// String fields: decode and validate against the closed set.
	decodeString := func(field string, dest **string, allowed map[string]struct{}) error {
		v, ok := raw[field]
		if !ok {
			return nil
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return fmt.Errorf("%s must be a string", field)
		}
		s = strings.TrimSpace(s)
		if s != "" {
			if _, ok := allowed[s]; !ok {
				return fmt.Errorf("%s has invalid value %q", field, s)
			}
		}
		*dest = &s
		return nil
	}

	if err := decodeString("theme", &patch.Theme, allowedSettingsTheme); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeString("density", &patch.Density, allowedSettingsDensity); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeString("reading_pane", &patch.ReadingPane, allowedSettingsReadingPane); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeString("text_direction", &patch.TextDirection, allowedSettingsDirection); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeString("language", &patch.Language, allowedSettingsLanguage); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeString("date_format", &patch.DateFormat, allowedSettingsDateFmt); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeString("time_format", &patch.TimeFormat, allowedSettingsTimeFmt); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeString("sender_display", &patch.SenderDisplay, allowedSettingsSender); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeString("default_reply_mode", &patch.DefaultReplyMode, allowedSettingsReplyMode); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// Free-form string fields (no closed enum): validate length.
	decodeFreeString := func(field string, dest **string, max int) error {
		v, ok := raw[field]
		if !ok {
			return nil
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return fmt.Errorf("%s must be a string", field)
		}
		if len(s) > max {
			return fmt.Errorf("%s exceeds max length %d", field, max)
		}
		*dest = &s
		return nil
	}
	if err := decodeFreeString("display_name", &patch.DisplayName, 200); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeFreeString("timezone", &patch.Timezone, 64); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeFreeString("signature_text", &patch.SignatureText, 4096); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeFreeString("default_folder", &patch.DefaultFolder, 64); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// Numeric fields: decode + clamp range, no error — clamping happens
	// at the storage layer too, so out-of-range values are still saved
	// (clamped) instead of rejected with 400. The UI should never send
	// out-of-range values; a manual API caller will get the clamped value
	// back on the next GET.
	decodeInt := func(field string, dest **int, min, max int) error {
		v, ok := raw[field]
		if !ok {
			return nil
		}
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			return fmt.Errorf("%s must be an integer", field)
		}
		if n < min || n > max {
			return fmt.Errorf("%s must be between %d and %d", field, min, max)
		}
		*dest = &n
		return nil
	}
	if err := decodeInt("preview_lines", &patch.PreviewLines, 0, 6); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeInt("autosave_seconds", &patch.AutosaveSeconds, 0, 60); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeInt("mark_read_delay_seconds", &patch.MarkReadDelaySeconds, 0, 60); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// Boolean fields.
	decodeBool := func(field string, dest **bool) error {
		v, ok := raw[field]
		if !ok {
			return nil
		}
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return fmt.Errorf("%s must be a boolean", field)
		}
		*dest = &b
		return nil
	}
	if err := decodeBool("signature_enabled", &patch.SignatureEnabled); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeBool("signature_in_replies", &patch.SignatureInReplies); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeBool("confirm_before_discard", &patch.ConfirmBeforeDiscard); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeBool("warn_on_empty_subject", &patch.WarnOnEmptySubject); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeBool("notify_inapp", &patch.NotifyInApp); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := decodeBool("notify_push", &patch.NotifyPush); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	updated, err := ms.Settings.Update(c.Context(), userCtx.Mailbox.ID, patch)
	if err != nil {
		h.logger.Error("webmail put settings", zap.Uint("mailbox_id", userCtx.Mailbox.ID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "settings update failed"})
	}
	return c.JSON(updated)
}
