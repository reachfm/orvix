package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
)

// queueAdminGate returns true if the caller has admin or super_admin role.
// The product uses role-based access control: admin and super_admin roles
// have full queue read and action access. No granular queue.read/queue.action
// permission system exists yet — the admin role gates all queue endpoints.
func (h *Handler) queueAdminGate(c fiber.Ctx) bool {
	role, ok := c.Locals("role").(auth.Role)
	if !ok {
		return false
	}
	return role == auth.RoleAdmin || role == auth.RoleSuperAdmin
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
func (h *Handler) AdminQueueList(c fiber.Ctx) error {
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

func queueActionError(c fiber.Ctx, err error) error {
	code := 400
	if strings.Contains(err.Error(), "not found") {
		code = 404
	}
	return c.Status(code).JSON(fiber.Map{"error": err.Error()})
}
