package handlers

// Per-mailbox rules engine API.
//
// These handlers expose the rules / vacation / forwarding
// configuration to the authenticated webmail user. The
// caller's mailbox is derived from the JWT identity via
// resolveWebmailUserContext — this is the single source of
// truth for "which mailbox does this request operate on".
// There is no mailbox id in the URL: the user can only ever
// see and modify their own rules / vacation / forwarding
// row.
//
// Mailbox ownership isolation is therefore enforced by
// construction: even if the caller passes an id, the
// handler ignores it and uses the mailbox id from the
// authenticated context. The DB-side WHERE mailbox_id=?
// predicate is a second line of defence — the rules
// repository's GetByID / Update / Delete take (mailboxID,
// ruleID) and the SQL has the predicate baked into the
// WHERE clause so a caller cannot read / write a row they
// do not own by guessing the id.

import (
	"net/mail"
	"strconv"

	"github.com/gofiber/fiber/v3"

	"github.com/orvix/orvix/internal/coremail/rules"
	"github.com/orvix/orvix/internal/coremail/storage"
)

// ── Rules CRUD ────────────────────────────────────────────────

// WebmailListRules returns the caller's filter rules in
// canonical sort order. Returns 403 if the caller's
// mailbox cannot be resolved; 503 if the MailStore is
// not wired; 200 with the rule list otherwise.
func (h *Handler) WebmailListRules(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no mailbox", "reason": reason})
	}
	rules, err := ctx.MailboxStore.Rules.ListByMailbox(c.Context(), ctx.Mailbox.ID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"rules": rules})
}

// WebmailCreateRule inserts a new rule on the caller's
// mailbox. The conditions_json + actions_json bodies are
// validated by the rules package BEFORE we touch the DB.
// A malformed JSON body returns 400 — the engine itself
// is the validator.
func (h *Handler) WebmailCreateRule(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no mailbox", "reason": reason})
	}
	var req struct {
		Name           string `json:"name"`
		Enabled        bool   `json:"enabled"`
		SortOrder      int    `json:"sort_order"`
		StopProcessing bool   `json:"stop_processing"`
		ConditionsJSON string `json:"conditions_json"`
		ActionsJSON    string `json:"actions_json"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if err := rules.ValidateConditionsJSON(req.ConditionsJSON); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid conditions_json: " + err.Error()})
	}
	if err := rules.ValidateActionsJSON(req.ActionsJSON); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid actions_json: " + err.Error()})
	}
	rule := &storage.Rule{
		MailboxID:      ctx.Mailbox.ID,
		Name:           req.Name,
		Enabled:        req.Enabled,
		SortOrder:      req.SortOrder,
		StopProcessing: req.StopProcessing,
		ConditionsJSON: req.ConditionsJSON,
		ActionsJSON:    req.ActionsJSON,
	}
	out, err := ctx.MailboxStore.Rules.Create(c.Context(), rule)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

// WebmailUpdateRule patches one rule on the caller's
// mailbox. The (mailboxID, ruleID) predicate in the SQL
// is what stops the caller from updating a rule they
// don't own — even if they guess the id, the storage
// layer's WHERE clause returns ErrNoRows.
func (h *Handler) WebmailUpdateRule(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no mailbox", "reason": reason})
	}
	ruleID, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rule id"})
	}
	var req struct {
		Name           *string `json:"name,omitempty"`
		Enabled        *bool   `json:"enabled,omitempty"`
		SortOrder      *int    `json:"sort_order,omitempty"`
		StopProcessing *bool   `json:"stop_processing,omitempty"`
		ConditionsJSON *string `json:"conditions_json,omitempty"`
		ActionsJSON    *string `json:"actions_json,omitempty"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.ConditionsJSON != nil {
		if err := rules.ValidateConditionsJSON(*req.ConditionsJSON); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid conditions_json: " + err.Error()})
		}
	}
	if req.ActionsJSON != nil {
		if err := rules.ValidateActionsJSON(*req.ActionsJSON); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid actions_json: " + err.Error()})
		}
	}
	patch := &storage.RulePatch{
		Name:           req.Name,
		Enabled:        req.Enabled,
		SortOrder:      req.SortOrder,
		StopProcessing: req.StopProcessing,
		ConditionsJSON: req.ConditionsJSON,
		ActionsJSON:    req.ActionsJSON,
	}
	out, err := ctx.MailboxStore.Rules.Update(c.Context(), ctx.Mailbox.ID, uint(ruleID), patch)
	if err != nil {
		// Update returns sql.ErrNoRows when the id is
		// not on this mailbox — same response shape as
		// "no such rule" so the handler does not leak
		// cross-mailbox existence.
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "rule not found"})
	}
	// The rules repo's Update can return (nil, nil)
	// when the WHERE clause matches 0 rows because the
	// rules SQL repo's GetByID swallows ErrNoRows.
	// Treat that as a not-found too so cross-mailbox
	// PUTs do not silently succeed.
	if out == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "rule not found"})
	}
	return c.JSON(out)
}

// WebmailDeleteRule removes a rule on the caller's
// mailbox. The (mailboxID, ruleID) predicate in the SQL
// is the only ownership check.
func (h *Handler) WebmailDeleteRule(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no mailbox", "reason": reason})
	}
	ruleID, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rule id"})
	}
	if err := ctx.MailboxStore.Rules.Delete(c.Context(), ctx.Mailbox.ID, uint(ruleID)); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "rule not found"})
	}
	return c.JSON(fiber.Map{"deleted": true, "id": ruleID})
}

// ── Vacation ──────────────────────────────────────────────────

// WebmailGetVacation returns the caller's vacation
// configuration. The handler always returns 200 — the row
// may not exist yet (vacation is opt-in), in which case
// the response carries enabled=false with empty subject
// and body. The repository's GetOrCreate materialises the
// row on the first read so subsequent writes have a
// stable id.
func (h *Handler) WebmailGetVacation(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no mailbox", "reason": reason})
	}
	vac, err := ctx.MailboxStore.Vacation.GetOrCreate(c.Context(), ctx.Mailbox.ID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(vac)
}

// WebmailPutVacation applies a partial-update patch to
// the caller's vacation row. Subject + body are clamped
// to 256 / 4 KB by the storage layer; the rate-limit
// interval is clamped to [60, 30*86400] seconds (1 min
// to 30 days) so a misconfigured client cannot spam
// vacation replies.
func (h *Handler) WebmailPutVacation(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no mailbox", "reason": reason})
	}
	var req struct {
		Enabled              *bool   `json:"enabled,omitempty"`
		Subject              *string `json:"subject,omitempty"`
		Body                 *string `json:"body,omitempty"`
		StartAt              *string `json:"start_at,omitempty"`
		EndAt                *string `json:"end_at,omitempty"`
		ReplyIntervalSeconds *int    `json:"reply_interval_seconds,omitempty"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	patch := &storage.VacationPatch{
		Enabled:              req.Enabled,
		Subject:              req.Subject,
		Body:                 req.Body,
		StartAt:              req.StartAt,
		EndAt:                req.EndAt,
		ReplyIntervalSeconds: req.ReplyIntervalSeconds,
	}
	out, err := ctx.MailboxStore.Vacation.Update(c.Context(), ctx.Mailbox.ID, patch)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(out)
}

// ── Forwarding ────────────────────────────────────────────────

// WebmailGetForwarding returns the caller's forwarding
// configuration. Same "may not exist yet" handling as
// vacation — GetOrCreate materialises the row on first
// read.
func (h *Handler) WebmailGetForwarding(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no mailbox", "reason": reason})
	}
	fwd, err := ctx.MailboxStore.Forwarding.GetOrCreate(c.Context(), ctx.Mailbox.ID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fwd)
}

// WebmailPutForwarding applies a partial-update patch to
// the caller's forwarding row. The forward_to address is
// validated with mail.ParseAddress BEFORE the storage
// layer sees it; an invalid address returns 400 without
// touching the DB.
func (h *Handler) WebmailPutForwarding(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no mailbox", "reason": reason})
	}
	var req struct {
		Enabled   *bool   `json:"enabled,omitempty"`
		ForwardTo *string `json:"forward_to,omitempty"`
		KeepCopy  *bool   `json:"keep_copy,omitempty"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.ForwardTo != nil && *req.ForwardTo != "" {
		if _, err := mail.ParseAddress(*req.ForwardTo); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid forward_to: " + err.Error()})
		}
	}
	patch := &storage.ForwardingPatch{
		Enabled:   req.Enabled,
		ForwardTo: req.ForwardTo,
		KeepCopy:  req.KeepCopy,
	}
	out, err := ctx.MailboxStore.Forwarding.Update(c.Context(), ctx.Mailbox.ID, patch)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(out)
}
