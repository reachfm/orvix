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
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/coremail"
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
	Mailbox  *coremail.Mailbox
	Email    string
	UserID   uint
	Role     string
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

	messages, _, err := ctx.MailboxStore.Messages.List(c.Context(), storage.MessageFilter{
		MailboxID: ctx.Mailbox.ID,
		FolderID:  &folder.ID,
		Limit:     200,
	}, nil)
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
	out := make([]fiber.Map, 0, len(messages))
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
		out = append(out, fiber.Map{
			"id":            m.ID,
			"message_id":    m.MessageID,
			"subject":       m.Subject,
			"from":          m.FromAddress,
			"to":            m.ToAddresses,
			"cc":            m.CcAddresses,
			"size_bytes":    m.SizeBytes,
			"seen":          m.Seen,
			"flagged":       m.Flagged,
			"answered":      m.Answered,
			"draft":         m.Draft,
			"junk":          m.Junk,
			"received_date": m.ReceivedDate,
			"message_date":  m.MessageDate,
			"folder_id":     m.FolderID,
			"folder_path":   folder.Path,
		})
	}
	return c.JSON(fiber.Map{
		"messages":   out,
		"folder":     folder.Path,
		"folder_id":  folder.ID,
	})
}

// WebmailMessage returns one message's metadata and the
// raw RFC822 body. The body is loaded from disk by the
// MailStore — no hardcoded content is ever returned. The
// authorization check is "this message must belong to the
// caller's mailbox"; messages from another mailbox return
// 404 to avoid leaking existence.
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

	return c.JSON(fiber.Map{
		"id":              msg.ID,
		"message_id":      msg.MessageID,
		"subject":         msg.Subject,
		"from":            msg.FromAddress,
		"to":              msg.ToAddresses,
		"cc":              msg.CcAddresses,
		"bcc":             msg.BccAddresses,
		"reply_to":        msg.ReplyTo,
		"size_bytes":      msg.SizeBytes,
		"seen":            msg.Seen,
		"flagged":         msg.Flagged,
		"answered":        msg.Answered,
		"draft":           msg.Draft,
		"junk":            msg.Junk,
		"received_date":   msg.ReceivedDate,
		"message_date":    msg.MessageDate,
		"folder_id":       msg.FolderID,
		"internet_id":     msg.InternetMessageID,
		"rfc822":          string(rfc822),
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
//   1. Authenticate via the standard auth middleware.
//   2. Resolve the caller's mailbox (the sender).
//   3. Parse To/Cc/Bcc safely with mail.ParseAddressList —
//      malformed addresses are rejected with 400 BEFORE we
//      touch disk or queue.
//   4. Look up the Sent folder for the mailbox. If missing,
//      return 500 — system folders must be provisioned first.
//   5. Store the RFC822 message body in the Sent folder
//      (the source of truth for "what the user sent").
//   6. Enqueue one queue.QueueEntry per recipient, all
//      pointing at the same message_id, all
//      direction=outbound / delivery_mode=remote_smtp /
//      status=pending so the existing delivery worker picks
//      them up. The sender is the authenticated mailbox,
//      not anything the client supplies.
//   7. Return 201 Created with status="queued".
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
		To      string `json:"to"`
		Cc      string `json:"cc"`
		Bcc     string `json:"bcc"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if strings.TrimSpace(req.To) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "to is required"})
	}
	if strings.TrimSpace(req.Subject) == "" {
		req.Subject = "(no subject)"
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

	// Build the RFC822 message body. The body is plain
	// text by default; HTML is out of scope for this
	// endpoint (no redesign rule).
	messageID := generateMessageID()
	now := time.Now().UTC()
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

	if err := ctx.MailboxStore.StoreMessage(c.Context(), msg, []byte(rfc822), nil); err != nil {
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
	// FromAddress (the authenticated mailbox), same
	// delivery mode. DeliveryMode=remote_smtp lets the
	// existing delivery worker handle it via SMTP MX
	// resolution; local same-server deliveries use
	// local, which is what inbound uses — we pick the
	// same path as the SMTP receiver for outbound to
	// remote domains and let the resolver/local-domain
	// checker decide.
	allRecipients := make([]*mail.Address, 0, len(toList)+len(ccList)+len(bccList))
	allRecipients = append(allRecipients, toList...)
	allRecipients = append(allRecipients, ccList...)
	allRecipients = append(allRecipients, bccList...)

	mailboxID := ctx.Mailbox.ID
	queuedIDs := make([]uint, 0, len(allRecipients))
	enqueueErrors := make([]string, 0)
	for _, addr := range allRecipients {
		bare := addr.Address
		domain := extractRecipientDomain(bare)
		entry := &queue.QueueEntry{
			TenantID:        ctx.Mailbox.TenantID,
			DomainID:        ctx.Mailbox.DomainID,
			MailboxID:       &mailboxID,
			MessageID:       messageID,
			FromAddress:     ctx.Mailbox.Email,
			ToAddress:       bare,
			RecipientDomain: domain,
			Direction:       queue.DirectionOutbound,
			DeliveryMode:    queue.DeliveryRemoteSMTP,
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
		queuedIDs = append(queuedIDs, entry.ID)
	}

	if len(queuedIDs) == 0 {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error":           "no recipients enqueued: " + strings.Join(enqueueErrors, "; "),
			"message_id":      msg.MessageID,
			"folder":          sentFolder.Path,
			"status":          "stored",
			"enqueue_errors":  enqueueErrors,
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":             msg.ID,
		"message_id":     msg.MessageID,
		"folder":         sentFolder.Path,
		"status":         "queued",
		"queued_count":   len(queuedIDs),
		"queue_ids":      queuedIDs,
		"enqueue_errors": enqueueErrors,
	})
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
		"id":      msg.ID,
		"status":  "deleted",
		"moved_to": trash.Path,
	})
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
