package handlers

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/coremail/push"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/webmailmgmt"
)

func (h *Handler) SetWebmailService(ws *webmailmgmt.Service) {
	h.webmailSvc = ws
}

func (h *Handler) SetMailStore(ms *storage.MailStore) {
	h.mailStore = ms
}

// SetQueueEngine wires the outbound queue engine into the
// handler. Called by the router constructor when the
// coremail runtime module exposes QueueEngine(). The
// webmail Send endpoint enqueues outbound messages through
// this engine so they are picked up by the existing
// delivery worker — the queue is shared with the SMTP
// receiver and the delivery pipeline, not a separate one.
func (h *Handler) SetQueueEngine(qe *queue.QueueEngine) {
	h.queueEngine = qe
}

func (h *Handler) SetPushNotifier(pn *push.PushNotifier) {
	h.pushNotifier = pn
}

func (h *Handler) webmailService() *webmailmgmt.Service {
	if h.webmailSvc == nil {
		return nil
	}
	return h.webmailSvc
}

// mailStoreForUser returns the MailStore and true if it is
// available, or nil and false otherwise. Used by the user-
// facing webmail endpoints (me, folders, messages, send,
// delete) which read from the same MailStore the runtime
// coremail module uses.
func (h *Handler) mailStoreForUser() (*storage.MailStore, bool) {
	if h.mailStore == nil {
		return nil, false
	}
	return h.mailStore, true
}

// queueEngineForUser returns the QueueEngine and true if
// it is wired. The webmail Send endpoint uses this to
// enqueue outbound messages into the same delivery queue
// the SMTP receiver uses for inbound mail. Returns nil,
// false if the runtime did not expose a queue (e.g.,
// CoreMail runtime is disabled or not booted).
func (h *Handler) queueEngineForUser() (*queue.QueueEngine, bool) {
	if h.queueEngine == nil {
		return nil, false
	}
	return h.queueEngine, true
}

func (h *Handler) ListWebmailAccounts(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	search := strings.TrimSpace(c.Query("search"))
	domainFilter := strings.TrimSpace(c.Query("domain"))
	statusFilter := strings.TrimSpace(c.Query("status"))
	var adminFilter *bool
	if v := c.Query("admin"); v != "" {
		b := v == "1" || v == "true"
		adminFilter = &b
	}
	accounts, err := svc.ListAccounts(c.Context(), search, domainFilter, statusFilter, adminFilter)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(accounts)
}

func (h *Handler) ListWebmailSessions(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	var mailboxID *uint
	if v := c.Query("mailboxId"); v != "" {
		if id, err := strconv.ParseUint(v, 10, 64); err == nil {
			uid := uint(id)
			mailboxID = &uid
		}
	}
	sessions, err := svc.ListSessions(c.Context(), mailboxID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(sessions)
}

func (h *Handler) RevokeWebmailSession(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	idParam := c.Params("id")
	id, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid session id"})
	}
	if err := svc.RevokeSession(c.Context(), uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "revoked"})
}

func (h *Handler) RevokeAllWebmailSessions(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	var req struct {
		MailboxID uint `json:"mailbox_id"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.MailboxID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mailbox_id required"})
	}
	if err := svc.RevokeAllSessions(c.Context(), req.MailboxID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "revoked"})
}

func (h *Handler) GetWebmailLoginActivity(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	mailboxIDStr := c.Params("mailboxId")
	mailboxID, err := strconv.ParseUint(mailboxIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	activity, err := svc.GetLoginActivity(c.Context(), uint(mailboxID))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(activity)
}

func (h *Handler) GetWebmailStorageMetrics(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	mailboxIDStr := c.Params("mailboxId")
	mailboxID, err := strconv.ParseUint(mailboxIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	metrics, err := svc.GetStorageMetrics(c.Context(), uint(mailboxID))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(metrics)
}

func (h *Handler) ForceLogoutWebmail(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	mailboxIDStr := c.Params("mailboxId")
	mailboxID, err := strconv.ParseUint(mailboxIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	if err := svc.ForceLogoutAll(c.Context(), uint(mailboxID)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "logged_out"})
}

func (h *Handler) UnlockWebmailMailbox(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	mailboxIDStr := c.Params("mailboxId")
	mailboxID, err := strconv.ParseUint(mailboxIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	if err := svc.UnlockMailbox(c.Context(), uint(mailboxID)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "unlocked"})
}

func (h *Handler) ResetWebmailPreferences(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	mailboxIDStr := c.Params("mailboxId")
	mailboxID, err := strconv.ParseUint(mailboxIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	if err := svc.ResetWebmailPreferences(c.Context(), uint(mailboxID)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "reset"})
}

func (h *Handler) ClearFailedLoginCounters(c fiber.Ctx) error {
	svc := h.webmailService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "webmail service not available"})
	}
	mailboxIDStr := c.Params("mailboxId")
	mailboxID, err := strconv.ParseUint(mailboxIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}
	if err := svc.ClearFailedLoginCounters(c.Context(), uint(mailboxID)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "cleared"})
}
