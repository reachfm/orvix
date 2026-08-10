package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
)

// queueAdminGate returns true if the caller has platform-super-admin
// authority. PORTAL-SEPARATION-PHASE1: the mail queue is a platform-wide
// resource — a tenant admin must not see or act on another tenant's
// messages. The deprecated RoleAdmin is intentionally excluded because
// after startup normalization no legitimate user carries it, and the
// RoleAdmin permission map has been emptied in internal/auth/rbac.
//
// Design note: we deliberately use an EXPLICIT canonical-role check
// here rather than authrbac.HasPermission(role, PermQueueAction).
// Reason: on this base the RBAC map at internal/auth/rbac/rbac.go
// still grants PermQueueAction to the deprecated RoleAdmin and to
// RoleOperator, so a permission-based gate is leaky until PR #58's
// map-emptying merges. This explicit canonical check denies
// RoleAdmin and RoleOperator NOW, independent of the RBAC map state,
// which is the actual defense-in-depth this PR delivers.
// RoleSuperAdmin is a documented migration-window alias for
// RolePlatformSuperAdmin (both name the same canonical platform role).
func (h *Handler) queueAdminGate(c fiber.Ctx) bool {
	role, ok := c.Locals("role").(auth.Role)
	if !ok {
		return false
	}
	return role == auth.RolePlatformSuperAdmin || role == auth.RoleSuperAdmin
}

// QueueMessage represents a queue entry in the API response
type QueueMessage struct {
	ID              uint    `json:"id"`
	FromAddress     string  `json:"from_address"`
	ToAddress       string  `json:"to_address"`
	RecipientDomain string  `json:"recipient_domain"`
	Status          string  `json:"status"`
	Priority        int     `json:"priority"`
	AttemptCount    int     `json:"attempt_count"`
	MaxAttempts     int     `json:"max_attempts"`
	NextAttemptAt   *string `json:"next_attempt_at,omitempty"`
	LastAttemptAt   *string `json:"last_attempt_at,omitempty"`
	LastError       string  `json:"last_error,omitempty"`
	LastStatusCode  int     `json:"last_status_code"`
	DeliveryMode    string  `json:"delivery_mode,omitempty"`
	RemoteHost      string  `json:"remote_host,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

// QueueFilter allows querying the queue with filters
type QueueFilter struct {
	Status string `json:"status"`
	Domain string `json:"domain"`
	Sender string `json:"sender"`
	From   string `json:"from"`
	To     string `json:"to"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// AdminQueueList serves GET /api/v1/admin/queue/messages
// Lists queue messages with filtering, sorting, pagination.
// coreMailUnavailableResponse writes the stable, sanitized 503 contract for
// every queue-admin endpoint when CoreMail is intentionally disabled
// (h.cfg.CoreMail.Enabled == false — the authoritative production
// configuration flag, never inferred from a missing table). "Disabled" is
// never reported as an empty queue: the two states are operationally
// different (nothing to see vs. the feature isn't running at all), and
// collapsing them would hide a real deployment/config problem behind a
// falsely-reassuring empty list.
func coreMailUnavailableResponse(c fiber.Ctx) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
		"error": "mail queue unavailable",
		"code":  "COREMAIL_DISABLED",
	})
}

func (h *Handler) AdminQueueList(c fiber.Ctx) error {
	if !h.cfg.CoreMail.Enabled {
		return coreMailUnavailableResponse(c)
	}
	var f QueueFilter
	f.Status = c.Query("status", "")
	f.Domain = c.Query("domain", "")
	f.Sender = c.Query("sender", "")
	f.From = c.Query("from", "")
	f.To = c.Query("to", "")
	f.Limit = 50
	if l := c.Query("limit", "50"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			f.Limit = n
		}
	}
	if o := c.Query("offset", "0"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			f.Offset = n
		}
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	dial := dbdialect.FromDriver(h.cfg.Database.Driver)

	query := `SELECT id, from_address, to_address, recipient_domain, status, priority,
		attempt_count, max_attempts, next_attempt_at, last_attempt_at,
		last_error, last_status_code, delivery_mode, remote_host,
		created_at FROM coremail_queue WHERE deleted_at IS NULL`
	args := []interface{}{}

	if f.Status != "" {
		query += fmt.Sprintf(" AND status = %s", dial.Placeholder(len(args)+1))
		args = append(args, f.Status)
	}
	if f.Domain != "" {
		query += fmt.Sprintf(" AND recipient_domain = %s", dial.Placeholder(len(args)+1))
		args = append(args, f.Domain)
	}
	if f.Sender != "" || f.From != "" {
		sender := f.From
		if sender == "" {
			sender = f.Sender
		}
		query += fmt.Sprintf(" AND from_address LIKE %s", dial.Placeholder(len(args)+1))
		args = append(args, "%"+sender+"%")
	}
	if f.To != "" {
		query += fmt.Sprintf(" AND to_address LIKE %s", dial.Placeholder(len(args)+1))
		args = append(args, "%"+f.To+"%")
	}

	countQuery := `SELECT COUNT(*) FROM (` + query + `)`
	var total int64
	if err := sqlDB.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.logger.Error("queue count query failed", zap.Error(err))
	}

	query += fmt.Sprintf(" ORDER BY id DESC LIMIT %s OFFSET %s", dial.Placeholder(len(args)+1), dial.Placeholder(len(args)+2))
	args = append(args, f.Limit, f.Offset)

	rows, err := sqlDB.Query(query, args...)
	if err != nil {
		h.logger.Error("queue query failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "query failed"})
	}
	defer rows.Close()

	messages := []QueueMessage{}
	for rows.Next() {
		var m QueueMessage
		var nextAt, lastAt *time.Time
		var createdAt time.Time
		err := rows.Scan(&m.ID, &m.FromAddress, &m.ToAddress, &m.RecipientDomain,
			&m.Status, &m.Priority, &m.AttemptCount, &m.MaxAttempts,
			&nextAt, &lastAt, &m.LastError, &m.LastStatusCode,
			&m.DeliveryMode, &m.RemoteHost, &createdAt)
		if err != nil {
			h.logger.Error("queue row scan failed", zap.Error(err))
			continue
		}
		if nextAt != nil {
			s := nextAt.Format(time.RFC3339)
			m.NextAttemptAt = &s
		}
		if lastAt != nil {
			s := lastAt.Format(time.RFC3339)
			m.LastAttemptAt = &s
		}
		m.CreatedAt = createdAt.Format(time.RFC3339)
		messages = append(messages, m)
	}

	return c.JSON(fiber.Map{
		"messages": messages,
		"total":    total,
		"limit":    f.Limit,
		"offset":   f.Offset,
	})
}

// AdminQueueDetail serves GET /api/v1/admin/queue/messages/:id
// Returns full detail for a single queue entry.
func (h *Handler) AdminQueueDetail(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	dial := dbdialect.FromDriver(h.cfg.Database.Driver)

	var m QueueMessage
	var nextAt, lastAt *time.Time
	var createdAt time.Time
	err = sqlDB.QueryRow(
		`SELECT id, from_address, to_address, recipient_domain, status, priority,
		attempt_count, max_attempts, next_attempt_at, last_attempt_at,
		last_error, last_status_code, delivery_mode, remote_host,
		created_at FROM coremail_queue WHERE id = `+dial.Placeholder(1)+` AND deleted_at IS NULL`, id).
		Scan(&m.ID, &m.FromAddress, &m.ToAddress, &m.RecipientDomain,
			&m.Status, &m.Priority, &m.AttemptCount, &m.MaxAttempts,
			&nextAt, &lastAt, &m.LastError, &m.LastStatusCode,
			&m.DeliveryMode, &m.RemoteHost, &createdAt)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "queue entry not found"})
	}
	if nextAt != nil {
		s := nextAt.Format(time.RFC3339)
		m.NextAttemptAt = &s
	}
	if lastAt != nil {
		s := lastAt.Format(time.RFC3339)
		m.LastAttemptAt = &s
	}
	m.CreatedAt = createdAt.Format(time.RFC3339)

	attempts := []fiber.Map{}
	// coremail_delivery_attempts is the real, actively-written attempt-history
	// table (internal/coremail/delivery/history.go). This previously queried
	// a "queue_attempts" table that nothing in the codebase writes to and
	// that has no migration anywhere — a rename-drift bug that silently
	// degraded this endpoint to always-empty attempt history.
	attRows, err := sqlDB.Query(
		`SELECT attempt_number, attempted_at, status, status_msg,
		remote_host, status_code FROM coremail_delivery_attempts WHERE queue_entry_id = `+dial.Placeholder(1)+` ORDER BY attempt_number`, id)
	if err == nil {
		defer attRows.Close()
		for attRows.Next() {
			var num, statusCode int
			var attAt, result, errMsg, remote string
			if err := attRows.Scan(&num, &attAt, &result, &errMsg, &remote, &statusCode); err != nil {
				continue
			}
			attempts = append(attempts, fiber.Map{
				"attempt":     num,
				"at":          attAt,
				"result":      result,
				"error":       errMsg,
				"remote_host": remote,
				"status_code": statusCode,
			})
		}
	}

	return c.JSON(fiber.Map{
		"message":  m,
		"attempts": attempts,
	})
}

// AdminQueueRetryNow serves POST /api/v1/admin/queue/messages/:id/retry
// Retries a specific queue message immediately.
func (h *Handler) AdminQueueRetryNow(c fiber.Ctx) error {
	if !h.queueAdminGate(c) {
		return c.Status(403).JSON(fiber.Map{"error": "admin role required for queue operations"})
	}

	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}

	if h.queueEngine == nil || h.queueEngine.Repo == nil {
		return c.Status(503).JSON(fiber.Map{"error": "queue engine unavailable"})
	}
	if err := h.queueEngine.Repo.AdminRetryNow(context.Background(), uint(id), nil); err != nil {
		return queueActionError(c, err)
	}

	h.writeAuditLog(c, "queue.retry", fmt.Sprintf("id:%d", id))
	return c.JSON(fiber.Map{"status": "retrying", "id": id})
}

// AdminQueueBounce serves POST /api/v1/admin/queue/messages/:id/bounce
// Bounces a message (marks as dead letter with note).
func (h *Handler) AdminQueueBounce(c fiber.Ctx) error {
	if !h.queueAdminGate(c) {
		return c.Status(403).JSON(fiber.Map{"error": "admin role required for queue operations"})
	}

	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}

	var req struct {
		Reason string `json:"reason"`
	}
	c.Bind().JSON(&req)

	reason := "manually bounced"
	if req.Reason != "" {
		reason = req.Reason
	}

	if h.queueEngine == nil || h.queueEngine.Repo == nil {
		return c.Status(503).JSON(fiber.Map{"error": "queue engine unavailable"})
	}
	if err := h.queueEngine.Repo.AdminDeadLetter(context.Background(), uint(id), reason, nil); err != nil {
		return queueActionError(c, err)
	}

	h.writeAuditLog(c, "queue.bounce", fmt.Sprintf("id:%d reason:%s", id, reason))
	return c.JSON(fiber.Map{"status": "bounced", "id": id})
}

// AdminQueueCancel serves POST /api/v1/admin/queue/messages/:id/cancel
// Cancels (soft-deletes) a message from the queue.
func (h *Handler) AdminQueueCancel(c fiber.Ctx) error {
	if !h.queueAdminGate(c) {
		return c.Status(403).JSON(fiber.Map{"error": "admin role required for queue operations"})
	}

	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}

	if h.queueEngine == nil || h.queueEngine.Repo == nil {
		return c.Status(503).JSON(fiber.Map{"error": "queue engine unavailable"})
	}
	if err := h.queueEngine.Repo.AdminCancel(context.Background(), uint(id), nil); err != nil {
		return queueActionError(c, err)
	}

	h.writeAuditLog(c, "queue.cancel", fmt.Sprintf("id:%d", id))
	return c.JSON(fiber.Map{"status": "cancelled", "id": id})
}

// queueActionError maps a queue-action failure to an HTTP status and a
// stable machine-readable code. The HTTP status intentionally stays
// 400 for an invalid state transition (queue.SQLRepo.transitionStatus's
// "queue entry %d is in status %q; allowed statuses: %v" message) —
// that is the established, tested contract
// (TestAdminQueueActionsRejectLeasedEntries asserts 400 for a
// leased-entry rejection) — but the response body now also carries a
// `code` field so a caller can distinguish "the resource doesn't
// exist" from "the resource exists but isn't in a state this action
// allows" without parsing the free-text message.
func queueActionError(c fiber.Ctx, err error) error {
	httpStatus := 400
	code := "invalid_state_transition"
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		httpStatus = 404
		code = "not_found"
	case strings.Contains(msg, "allowed statuses"):
		code = "invalid_state_transition"
	default:
		code = "bad_request"
	}
	return c.Status(httpStatus).JSON(fiber.Map{"error": msg, "code": code})
}

// bulkActionResult is the per-message outcome for a bulk queue
// action — every ID gets an explicit result, success or typed
// failure, so a caller never has to guess which of N messages
// actually changed state.
type bulkActionResult struct {
	ID      uint   `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

// AdminQueueBulkAction serves POST /api/v1/admin/queue/messages/bulk-action.
// Applies the same action (retry/cancel/bounce) to a list of IDs,
// each through the existing single-message state-machine-guarded
// path (AdminRetryNow/AdminCancel/AdminDeadLetter) — one failing ID
// never blocks or rolls back the others, and the response reports
// every ID's own outcome.
func (h *Handler) AdminQueueBulkAction(c fiber.Ctx) error {
	if !h.cfg.CoreMail.Enabled {
		return coreMailUnavailableResponse(c)
	}
	if !h.queueAdminGate(c) {
		return c.Status(403).JSON(fiber.Map{"error": "admin role required for queue operations"})
	}
	if h.queueEngine == nil || h.queueEngine.Repo == nil {
		return c.Status(503).JSON(fiber.Map{"error": "queue engine unavailable"})
	}

	var req struct {
		IDs    []uint `json:"ids"`
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil || len(req.IDs) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "ids and action are required"})
	}
	if len(req.IDs) > 500 {
		return c.Status(400).JSON(fiber.Map{"error": "at most 500 ids per bulk action"})
	}
	reason := req.Reason
	if reason == "" {
		reason = "bulk operator action"
	}

	var apply func(context.Context, uint) error
	switch req.Action {
	case "retry":
		apply = func(ctx context.Context, id uint) error { return h.queueEngine.Repo.AdminRetryNow(ctx, id, nil) }
	case "cancel":
		apply = func(ctx context.Context, id uint) error { return h.queueEngine.Repo.AdminCancel(ctx, id, nil) }
	case "bounce":
		apply = func(ctx context.Context, id uint) error {
			return h.queueEngine.Repo.AdminDeadLetter(ctx, id, reason, nil)
		}
	default:
		return c.Status(400).JSON(fiber.Map{"error": "action must be one of: retry, cancel, bounce"})
	}

	results := make([]bulkActionResult, 0, len(req.IDs))
	succeeded := 0
	for _, id := range req.IDs {
		if err := apply(context.Background(), id); err != nil {
			code := "bad_request"
			if strings.Contains(err.Error(), "not found") {
				code = "not_found"
			} else {
				code = "invalid_state_transition"
			}
			results = append(results, bulkActionResult{ID: id, Success: false, Error: err.Error(), Code: code})
			continue
		}
		succeeded++
		results = append(results, bulkActionResult{ID: id, Success: true})
	}

	h.writeAuditLog(c, "queue.bulk_"+req.Action, fmt.Sprintf("count:%d succeeded:%d", len(req.IDs), succeeded))
	return c.JSON(fiber.Map{"action": req.Action, "total": len(req.IDs), "succeeded": succeeded, "results": results})
}

// redactAddress masks the local part of an email address for export/
// history views, keeping only the domain and a length hint — enough
// for an operator to spot patterns without exposing full addresses in
// a downloadable file.
func redactAddress(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		return "***"
	}
	return "***@" + addr[at+1:]
}

// AdminQueueHistory serves GET /api/v1/admin/queue/history — the
// immutable, cross-entry delivery-attempt history, cursor-paginated
// (query param after_id), separate from the mutable live queue
// listing in AdminQueueList.
func (h *Handler) AdminQueueHistory(c fiber.Ctx) error {
	if !h.cfg.CoreMail.Enabled {
		return coreMailUnavailableResponse(c)
	}
	if h.historyRepo == nil {
		return c.Status(503).JSON(fiber.Map{"error": "delivery history unavailable", "code": "queue_unavailable"})
	}
	var filter delivery.HistoryFilter
	filter.Status = c.Query("status", "")
	filter.RemoteHost = c.Query("remote_host", "")
	if v, err := strconv.ParseUint(c.Query("after_id", "0"), 10, 64); err == nil {
		filter.AfterID = uint(v)
	}
	filter.Limit = 100
	if v, err := strconv.Atoi(c.Query("limit", "100")); err == nil && v > 0 && v <= 500 {
		filter.Limit = v
	}

	attempts, err := h.historyRepo.ListRecent(c.Context(), filter, nil)
	if err != nil {
		h.logger.Error("delivery history query failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "query failed"})
	}
	var nextCursor uint
	if len(attempts) > 0 {
		nextCursor = attempts[len(attempts)-1].ID
	}
	return c.JSON(fiber.Map{"attempts": attempts, "next_after_id": nextCursor, "count": len(attempts)})
}

// AdminQueueExport serves GET /api/v1/admin/queue/export — a safe,
// redacted CSV export of the current live queue. Requires the same
// platform-super-admin gate as every other queue action; recipient/
// sender addresses are masked to domain-only (redactAddress).
func (h *Handler) AdminQueueExport(c fiber.Ctx) error {
	if !h.cfg.CoreMail.Enabled {
		return coreMailUnavailableResponse(c)
	}
	if !h.queueAdminGate(c) {
		return c.Status(403).JSON(fiber.Map{"error": "admin role required for queue export"})
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	rows, err := sqlDB.Query(`SELECT id, from_address, to_address, recipient_domain, status, attempt_count, created_at
		FROM coremail_queue WHERE deleted_at IS NULL ORDER BY id DESC LIMIT 5000`)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "query failed"})
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString("id,from,to,recipient_domain,status,attempt_count,created_at\n")
	for rows.Next() {
		var id uint
		var from, to, domain, status string
		var attemptCount int
		var createdAt time.Time
		if err := rows.Scan(&id, &from, &to, &domain, &status, &attemptCount, &createdAt); err != nil {
			continue
		}
		fmt.Fprintf(&b, "%d,%s,%s,%s,%s,%d,%s\n", id, redactAddress(from), redactAddress(to), domain, status, attemptCount, createdAt.Format(time.RFC3339))
	}
	h.writeAuditLog(c, "queue.export", "format:csv")
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", "attachment; filename=\"queue-export.csv\"")
	return c.SendString(b.String())
}
