package handlers

// Webmail attachment download and preview handlers.
//
// All endpoints resolve the caller's mailbox through
// webmailUserContext and verify that the attachment's
// parent message belongs to that mailbox before any
// filesystem or content read happens. A non-owner gets a
// 404 (not a 403) so the response shape does not leak
// whether the attachment id exists in another mailbox.
//
// Endpoints:
//
//	GET  /api/v1/webmail/attachments/:id          — force-download the file
//	GET  /api/v1/webmail/attachments/:id/preview  — inline preview for the UI
//
// The preview endpoint refuses SVG (the only widely-used
// image type that can carry script via inline XML) and
// image types that the UI does not render. It also caps
// the response size so a single huge image cannot exhaust
// the worker's memory.

import (
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// inlinePreviewableContentTypes is the set of content
// types the preview endpoint will serve inline. SVG is
// intentionally excluded — it can carry executable script
// via inline XML and the renderer cannot sanitize it
// safely. Plain text and the common raster image types
// are allowed; everything else is rejected.
var inlinePreviewableContentTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"text/plain": true,
}

// inlinePreviewMaxBytes caps how much of an attachment
// the preview endpoint will buffer in memory. Attachments
// larger than this are rejected with 413. 1 MB is enough
// for typical signature / invoice images; anything larger
// should be downloaded, not previewed.
const inlinePreviewMaxBytes int64 = 1 * 1024 * 1024

// WebmailAttachmentDownload serves the raw attachment
// file as `Content-Disposition: attachment` so the
// browser saves it instead of trying to render it. The
// file path on disk is the value the MailStore wrote at
// ingest — it is local to the server and was sanitized
// by sanitizeFilenameForStorage before reaching the
// filesystem, so path traversal is not a concern at the
// serving step. Defense in depth: the :id is parsed via
// parseMessageID (digits only) and the attachment is
// confirmed to belong to the caller's mailbox before any
// file is opened.
func (h *Handler) WebmailAttachmentDownload(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid attachment id"})
	}
	att, ok := h.loadOwnedAttachment(c, ctx, id)
	if !ok {
		// The helper already wrote the error response.
		return nil
	}
	if att.StoragePath == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "attachment file not found"})
	}
	if info, err := os.Stat(att.StoragePath); err != nil || info.IsDir() {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "attachment file not found"})
	}
	filename := attachmentDownloadName(att)
	c.Set(fiber.HeaderContentType, attachmentResponseContentType(att))
	c.Set(fiber.HeaderContentDisposition,
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
			sanitizeDownloadFilename(filename),
			sanitizeDownloadFilename(filename)))
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	// Read into memory and Send rather than SendFile so
	// the file handle is closed before the response is
	// flushed. This avoids Windows file-locking issues
	// during test cleanup and is bounded by the
	// per-attachment 25 MB cap enforced at ingest.
	data, err := os.ReadFile(att.StoragePath)
	if err != nil {
		h.logger.Warn("webmail attachment download: read failed",
			zap.Uint("attachment_id", att.ID),
			zap.String("path", att.StoragePath),
			zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "read attachment failed",
		})
	}
	return c.Send(data)
}

// WebmailAttachmentPreview serves the attachment inline
// so the reading pane can render it. Restricted to a
// fixed allowlist of safe content types and a fixed
// maximum size; anything else is rejected with a clear
// error code so the client can fall back to download.
//
// The response is `Content-Disposition: inline` so the
// browser tries to render. We still set
// X-Content-Type-Options: nosniff and the CSP sandbox
// should treat preview content as untrusted.
func (h *Handler) WebmailAttachmentPreview(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	id, err := parseMessageID(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid attachment id"})
	}
	att, ok := h.loadOwnedAttachment(c, ctx, id)
	if !ok {
		return nil
	}
	ct := strings.ToLower(strings.TrimSpace(att.ContentType))
	if !inlinePreviewableContentTypes[ct] {
		return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{
			"error": fmt.Sprintf("content type %q is not previewable", att.ContentType),
		})
	}
	if att.SizeBytes > inlinePreviewMaxBytes {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error":     "attachment too large for inline preview",
			"size":      att.SizeBytes,
			"max_bytes": inlinePreviewMaxBytes,
		})
	}
	if att.StoragePath == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "attachment file not found"})
	}
	data, err := os.ReadFile(filepath.Clean(att.StoragePath))
	if err != nil {
		h.logger.Warn("webmail attachment preview: read failed",
			zap.Uint("attachment_id", att.ID),
			zap.String("path", att.StoragePath),
			zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "read attachment failed",
		})
	}
	filename := attachmentDownloadName(att)
	c.Set(fiber.HeaderContentType, ct)
	c.Set(fiber.HeaderContentDisposition,
		fmt.Sprintf(`inline; filename="%s"`, sanitizeDownloadFilename(filename)))
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Set(fiber.HeaderContentLength, fmt.Sprintf("%d", len(data)))
	return c.Send(data)
}

// loadOwnedAttachment is the shared authorisation path
// for both download and preview. It looks up the
// attachment, walks message→mailbox ownership, and
// returns a 404-shaped error to the caller if any of
// those checks fail. Returning a 404 (not a 403) avoids
// leaking the existence of attachments in other
// mailboxes — the same policy the message endpoints use.
//
// The helper writes the error response itself and
// returns ok=false so the caller can short-circuit. The
// alternative — returning a Fiber error and unwrapping
// the status code — is fragile and invites drift
// between the helper and the handler.
func (h *Handler) loadOwnedAttachment(c fiber.Ctx, ctx *webmailUserContext, attachmentID uint) (att *fiberAttachment, ok bool) {
	sqlDB, err := h.db.DB()
	if err != nil {
		_ = c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "database unavailable",
		})
		return nil, false
	}
	// One JOIN-driven read so the mailbox check rides on
	// the same query: we only return the attachment row
	// if its parent message is in the caller's mailbox.
	// Cross-mailbox attachment ids return no rows.
	row := sqlDB.QueryRowContext(c.Context(), `
		SELECT a.id, a.message_id, a.filename, a.content_type, a.size_bytes, a.storage_path
		FROM coremail_attachments a
		JOIN coremail_messages m ON m.id = a.message_id
		WHERE a.id = ? AND m.mailbox_id = ? AND m.purged_at IS NULL`,
		attachmentID, ctx.Mailbox.ID)
	var a fiberAttachment
	if err := row.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.StoragePath); err != nil {
		_ = c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "attachment not found",
		})
		return nil, false
	}
	return &a, true
}

// fiberAttachment is the minimal shape of an attachment
// row used by the download / preview handlers. We use a
// local struct instead of storage.Attachment to keep
// these handlers from coupling to the storage package's
// internal column list.
type fiberAttachment struct {
	ID          uint
	MessageID   uint
	Filename    string
	ContentType string
	SizeBytes   int64
	StoragePath string
}

// attachmentDownloadName picks a safe filename for the
// Content-Disposition header. If the original filename
// parses as a mail.Address (some clients do that), the
// local part is used instead — the value still goes
// through sanitizeDownloadFilename before it is set in
// the header, so this is belt-and-suspenders.
func attachmentDownloadName(att *fiberAttachment) string {
	name := strings.TrimSpace(att.Filename)
	if name == "" {
		return fmt.Sprintf("attachment-%d", att.ID)
	}
	if addr, err := mail.ParseAddress(name); err == nil && addr.Name != "" {
		name = addr.Name
	}
	return name
}

// attachmentResponseContentType returns the content type
// to put on the download response. Empty attachment
// content type falls back to application/octet-stream
// so the browser does not try to render it.
func attachmentResponseContentType(att *fiberAttachment) string {
	ct := strings.TrimSpace(att.ContentType)
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}
