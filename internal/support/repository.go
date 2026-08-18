// Package support contains the canonical Service + Repository for the
// Support Ticket lifecycle shared by:
//
//   - the tenant-facing Support page (creating a ticket, listing own
//     tickets, reading own ticket detail, replying on own ticket), and
//   - the Platform Super Admin Support Inbox (listing all tickets
//     across tenants, reading any ticket, replying on any ticket,
//     changing status, assigning).
//
// The previous implementation lived entirely in handlers/handlers_account.go
// as a single SubmitSupportRequest function that:
//
//  1. wrote a "received" status row,
//  2. attempted an outbound mail via the transactional mail sender (or
//     fell back to a "queued" status that lied about delivery),
//  3. never gave the tenant a way to read its own ticket back,
//  4. never gave the platform a way to see any ticket at all.
//
// This package replaces that with a real round-trip ticket model: tenant
// creates → row persists → response returns the row → tenant can list /
// read / reply on its own tickets → platform can list / read / reply /
// transition status on every ticket (within platformMW role gates).
//
// Tenant isolation: every read of a ticket by a tenant-side caller is
// scoped by `tenant_id = ?` AND `user_id = ?` for own-only views, or
// `tenant_id = ?` for tenant-wide views (members list, etc.). The
// Platform Support Inbox bypasses the tenant predicate entirely (its
// role gate — platformMW — already rejects tenant actors at the
// middleware layer).
package support

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/models"
	"gorm.io/gorm"
)

// ErrTicketNotFound is returned by every read on a missing ticket ID
// or reference. Stable code: NOT_FOUND.
var ErrTicketNotFound = errors.New("support ticket not found")

// ErrInvalidTransition is returned by Status when the requested
// transition is not part of the canonical lifecycle. Stable code:
// INVALID_STATE_TRANSITION.
var ErrInvalidTransition = errors.New("invalid support ticket status transition")

// ErrMessageEmpty is returned by Create/Reply when the body is empty.
// Stable code: VALIDATION_ERROR.
var ErrMessageEmpty = errors.New("support ticket body is empty")

// ErrPriorityUnknown is returned by Create when priority is not one of
// {low, normal, high, urgent}. Stable code: VALIDATION_ERROR.
var ErrPriorityUnknown = errors.New("support ticket priority is not recognised")

// Repository is the persistence layer for SupportTicket and
// SupportTicketMessage. Every method takes the raw *sql.DB (the GORM
// `*gorm.DB` is held by handlers, but the repository deliberately uses
// raw SQL — same pattern as the platform domain-lifecycle / audit
// repositories — to avoid GORM surprises on either SQLite or
// PostgreSQL.
type Repository struct {
	db   *sql.DB
	gorm *gorm.DB
}

// NewRepository wires the support repository to its underlying DBs.
func NewRepository(sqlDB *sql.DB, gdb *gorm.DB) *Repository {
	return &Repository{db: sqlDB, gorm: gdb}
}

// ListFilter is the canonical filter for ListTickets. A zero value
// returns the latest tickets with default page size.
type ListFilter struct {
	TenantID uint   // 0 = platform-wide
	OwnerID  uint   // 0 = any; >0 = only tickets created by this user
	Status   string // "" = any
	Category string // "" = any
	Search   string // subject / reference contains
	Limit    int    // 0 → default 50, max 200
	Offset   int    // ≥ 0
}

// CreateTicketInput is the canonical input for CreateTicket. The
// reference ID is auto-generated when empty.
type CreateTicketInput struct {
	TenantID    uint
	UserID      uint
	UserEmail   string
	Category    string
	Subject     string
	Description string
	Priority    string // optional; defaults to "normal"
}

// ReplyInput is the canonical input for AddReply.
type ReplyInput struct {
	TicketID     uint
	AuthorUserID uint
	AuthorEmail  string
	AuthorKind   string // "tenant" or "platform"
	Body         string
}

// ListTickets returns tickets matching the filter. TenantID > 0 scopes
// the query; OwnerID > 0 further scopes to a single creator. The
// result is ordered by created_at DESC for stable pagination.
func (r *Repository) ListTickets(ctx context.Context, f ListFilter) ([]models.SupportTicket, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	var (
		conds []string
		args  []any
	)
	if f.TenantID > 0 {
		conds = append(conds, "tenant_id = ?")
		args = append(args, f.TenantID)
	}
	if f.OwnerID > 0 {
		conds = append(conds, "user_id = ?")
		args = append(args, f.OwnerID)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if f.Category != "" {
		conds = append(conds, "category = ?")
		args = append(args, f.Category)
	}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		conds = append(conds, "(subject LIKE ? OR reference_id LIKE ?)")
		args = append(args, like, like)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	var total int64
	countSQL := "SELECT COUNT(*) FROM support_requests " + where
	if err := r.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tickets: %w", err)
	}

	listSQL := "SELECT id, created_at, updated_at, reference_id, tenant_id, user_id, user_email, category, subject, message, status, priority, assigned_to_id, last_reply_at, last_reply_by, resolved_at, closed_at, delivery_status, delivery_error FROM support_requests " + where + " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"
	listArgs := append(append([]any{}, args...), limit, offset)

	rows, err := r.db.QueryContext(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()

	out := make([]models.SupportTicket, 0)
	for rows.Next() {
		var t models.SupportTicket
		if err := rows.Scan(
			&t.ID, &t.CreatedAt, &t.UpdatedAt, &t.ReferenceID, &t.TenantID, &t.UserID, &t.UserEmail,
			&t.Category, &t.Subject, &t.Description, &t.Status, &t.Priority, &t.AssignedToID,
			&t.LastReplyAt, &t.LastReplyBy, &t.ResolvedAt, &t.ClosedAt,
			new(string), new(string), // delivery_status / delivery_error are vestigial; keep columns but don't surface
		); err != nil {
			return nil, 0, fmt.Errorf("scan ticket: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iter tickets: %w", err)
	}
	return out, total, nil
}

// GetTicketByReference fetches one ticket by its human-friendly ref id
// (the SR-... string). tenantID is the scope guard: when > 0, the query
// is restricted to that tenant. When 0 (platform inbox), no scope is
// applied — the caller must already have passed the platformMW gate.
func (r *Repository) GetTicketByReference(ctx context.Context, ref string, tenantID uint) (*models.SupportTicket, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrTicketNotFound
	}
	var (
		query string
		args  []any
	)
	if tenantID > 0 {
		query = "SELECT id, created_at, updated_at, reference_id, tenant_id, user_id, user_email, category, subject, message, status, priority, assigned_to_id, last_reply_at, last_reply_by, resolved_at, closed_at FROM support_requests WHERE reference_id = ? AND tenant_id = ?"
		args = []any{ref, tenantID}
	} else {
		query = "SELECT id, created_at, updated_at, reference_id, tenant_id, user_id, user_email, category, subject, message, status, priority, assigned_to_id, last_reply_at, last_reply_by, resolved_at, closed_at FROM support_requests WHERE reference_id = ?"
		args = []any{ref}
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	var t models.SupportTicket
	if err := row.Scan(
		&t.ID, &t.CreatedAt, &t.UpdatedAt, &t.ReferenceID, &t.TenantID, &t.UserID, &t.UserEmail,
		&t.Category, &t.Subject, &t.Description, &t.Status, &t.Priority, &t.AssignedToID,
		&t.LastReplyAt, &t.LastReplyBy, &t.ResolvedAt, &t.ClosedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("get ticket by ref: %w", err)
	}
	return &t, nil
}

// GetTicketByID fetches one ticket by its numeric id. tenantID scope
// is enforced when > 0.
func (r *Repository) GetTicketByID(ctx context.Context, id uint, tenantID uint) (*models.SupportTicket, error) {
	if id == 0 {
		return nil, ErrTicketNotFound
	}
	var (
		query string
		args  []any
	)
	if tenantID > 0 {
		query = "SELECT id, created_at, updated_at, reference_id, tenant_id, user_id, user_email, category, subject, message, status, priority, assigned_to_id, last_reply_at, last_reply_by, resolved_at, closed_at FROM support_requests WHERE id = ? AND tenant_id = ?"
		args = []any{id, tenantID}
	} else {
		query = "SELECT id, created_at, updated_at, reference_id, tenant_id, user_id, user_email, category, subject, message, status, priority, assigned_to_id, last_reply_at, last_reply_by, resolved_at, closed_at FROM support_requests WHERE id = ?"
		args = []any{id}
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	var t models.SupportTicket
	if err := row.Scan(
		&t.ID, &t.CreatedAt, &t.UpdatedAt, &t.ReferenceID, &t.TenantID, &t.UserID, &t.UserEmail,
		&t.Category, &t.Subject, &t.Description, &t.Status, &t.Priority, &t.AssignedToID,
		&t.LastReplyAt, &t.LastReplyBy, &t.ResolvedAt, &t.ClosedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("get ticket by id: %w", err)
	}
	return &t, nil
}

// ListMessages returns the messages thread for a ticket, ordered oldest
// first. tenantID, when > 0, scopes the parent ticket first; the
// messages themselves inherit tenant scope from their parent.
func (r *Repository) ListMessages(ctx context.Context, ticketID uint, tenantID uint) ([]models.SupportTicketMessage, error) {
	if ticketID == 0 {
		return nil, ErrTicketNotFound
	}
	// tenantID scope guard via the parent ticket. We do this in a
	// single query to keep the contract honest: a tenant caller cannot
	// enumerate message rows for a ticket that does not belong to it.
	if tenantID > 0 {
		var ok int
		err := r.db.QueryRowContext(ctx, "SELECT 1 FROM support_requests WHERE id = ? AND tenant_id = ?", ticketID, tenantID).Scan(&ok)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrTicketNotFound
			}
			return nil, fmt.Errorf("scope guard: %w", err)
		}
	}
	rows, err := r.db.QueryContext(ctx, "SELECT id, created_at, updated_at, ticket_id, author_user_id, author_email, author_kind, body FROM support_ticket_messages WHERE ticket_id = ? ORDER BY created_at ASC, id ASC", ticketID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	out := make([]models.SupportTicketMessage, 0)
	for rows.Next() {
		var m models.SupportTicketMessage
		if err := rows.Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt, &m.TicketID, &m.AuthorUserID, &m.AuthorEmail, &m.AuthorKind, &m.Body); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter messages: %w", err)
	}
	return out, nil
}

// CreateTicket persists a new ticket and returns the populated row
// (including the auto-generated reference_id). The description is the
// full body; the legacy `message` column is kept in sync because the
// schema still carries it. The status starts at "open".
func (r *Repository) CreateTicket(ctx context.Context, in CreateTicketInput) (*models.SupportTicket, error) {
	if strings.TrimSpace(in.Subject) == "" || strings.TrimSpace(in.Description) == "" || strings.TrimSpace(in.Category) == "" {
		return nil, ErrMessageEmpty
	}
	priority := strings.ToLower(strings.TrimSpace(in.Priority))
	if priority == "" {
		priority = "normal"
	}
	switch priority {
	case "low", "normal", "high", "urgent":
	default:
		return nil, ErrPriorityUnknown
	}
	now := time.Now().UTC()
	ref := fmt.Sprintf("SR-%d-%d", in.UserID, now.UnixNano())
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO support_requests (created_at, updated_at, reference_id, tenant_id, user_id, user_email, category, subject, message, status, priority, last_reply_by, delivery_status, delivery_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, '', 'pending', '')`,
		now, now, ref, in.TenantID, in.UserID, in.UserEmail, in.Category, in.Subject, in.Description, priority)
	if err != nil {
		return nil, fmt.Errorf("insert ticket: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("ticket last insert id: %w", err)
	}
	return r.GetTicketByID(ctx, uint(id), 0)
}

// AddReply persists a new reply message and updates the parent
// ticket's last_reply_at / last_reply_by / status (the reply drives a
// status transition: a tenant reply on an in_progress ticket moves it
// back to customer_replied, etc.).
func (r *Repository) AddReply(ctx context.Context, in ReplyInput) (*models.SupportTicketMessage, error) {
	if strings.TrimSpace(in.Body) == "" {
		return nil, ErrMessageEmpty
	}
	switch in.AuthorKind {
	case "tenant", "platform":
	default:
		return nil, ErrInvalidTransition
	}
	t, err := r.GetTicketByID(ctx, in.TicketID, 0)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO support_ticket_messages (created_at, updated_at, ticket_id, author_user_id, author_email, author_kind, body)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		now, now, in.TicketID, in.AuthorUserID, in.AuthorEmail, in.AuthorKind, in.Body)
	if err != nil {
		return nil, fmt.Errorf("insert reply: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("reply last insert id: %w", err)
	}
	// Update parent ticket's last_reply_* and drive the canonical
	// status transition. Closed tickets do NOT accept further
	// messages — that returns ErrInvalidTransition from the handler.
	newStatus := t.Status
	switch in.AuthorKind {
	case "platform":
		// A platform reply on an open / waiting ticket moves it to
		// in_progress; on in_progress it stays; on customer_replied
		// it moves back to in_progress.
		switch t.Status {
		case models.SupportTicketStatusOpen,
			models.SupportTicketStatusWaitingForCustomer,
			models.SupportTicketStatusCustomerReplied:
			newStatus = models.SupportTicketStatusInProgress
		}
	case "tenant":
		// A tenant reply on a waiting ticket resolves the wait
		// (customer_replied); on in_progress it moves to
		// customer_replied (waiting for platform).
		switch t.Status {
		case models.SupportTicketStatusWaitingForCustomer:
			newStatus = models.SupportTicketStatusCustomerReplied
		case models.SupportTicketStatusInProgress,
			models.SupportTicketStatusOpen:
			newStatus = models.SupportTicketStatusCustomerReplied
		}
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE support_requests SET updated_at = ?, last_reply_at = ?, last_reply_by = ?, status = ? WHERE id = ?`,
		now, now, in.AuthorKind, newStatus, t.ID); err != nil {
		return nil, fmt.Errorf("update ticket after reply: %w", err)
	}
	return &models.SupportTicketMessage{
		ID: uint(id), CreatedAt: now, UpdatedAt: now,
		TicketID: in.TicketID, AuthorUserID: in.AuthorUserID,
		AuthorEmail: in.AuthorEmail, AuthorKind: in.AuthorKind, Body: in.Body,
	}, nil
}

// UpdateTicketStatus performs a guarded status transition. Closed is
// terminal (cannot transition out); Resolved is one-way to Closed or
// back to InProgress via the platform.
func (r *Repository) UpdateTicketStatus(ctx context.Context, ticketID uint, tenantID uint, target string) (*models.SupportTicket, error) {
	if !models.IsValidSupportTicketStatus(target) {
		return nil, ErrInvalidTransition
	}
	t, err := r.GetTicketByID(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}
	if t.Status == models.SupportTicketStatusClosed {
		return nil, ErrInvalidTransition
	}
	// Canonical transition rules. Everything else is invalid.
	if !isAllowedStatusTransition(t.Status, target) {
		return nil, ErrInvalidTransition
	}
	now := time.Now().UTC()
	var resolvedAt, closedAt *time.Time
	switch target {
	case models.SupportTicketStatusResolved:
		resolvedAt = &now
	case models.SupportTicketStatusClosed:
		closedAt = &now
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE support_requests SET updated_at = ?, status = ?, resolved_at = COALESCE(?, resolved_at), closed_at = COALESCE(?, closed_at) WHERE id = ?`,
		now, target, resolvedAt, closedAt, t.ID); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}
	return r.GetTicketByID(ctx, t.ID, tenantID)
}

// isAllowedStatusTransition encodes the canonical status graph.
//
//	open                → in_progress, waiting_for_customer, resolved
//	in_progress         → waiting_for_customer, customer_replied, resolved
//	waiting_for_customer → customer_replied, resolved, in_progress
//	customer_replied    → in_progress, resolved, waiting_for_customer
//	resolved            → closed, in_progress (re-open)
//	closed              → (terminal)
func isAllowedStatusTransition(from, to string) bool {
	if from == to {
		// Same-state transitions are allowed as no-ops so retry
		// safety on the client is straightforward.
		return true
	}
	switch from {
	case models.SupportTicketStatusOpen:
		switch to {
		case models.SupportTicketStatusInProgress,
			models.SupportTicketStatusWaitingForCustomer,
			models.SupportTicketStatusResolved:
			return true
		}
	case models.SupportTicketStatusInProgress:
		switch to {
		case models.SupportTicketStatusWaitingForCustomer,
			models.SupportTicketStatusCustomerReplied,
			models.SupportTicketStatusResolved:
			return true
		}
	case models.SupportTicketStatusWaitingForCustomer:
		switch to {
		case models.SupportTicketStatusCustomerReplied,
			models.SupportTicketStatusResolved,
			models.SupportTicketStatusInProgress:
			return true
		}
	case models.SupportTicketStatusCustomerReplied:
		switch to {
		case models.SupportTicketStatusInProgress,
			models.SupportTicketStatusResolved,
			models.SupportTicketStatusWaitingForCustomer:
			return true
		}
	case models.SupportTicketStatusResolved:
		switch to {
		case models.SupportTicketStatusClosed,
			models.SupportTicketStatusInProgress:
			return true
		}
	}
	return false
}

// MarkDeliveryStatus updates the (vestigial) delivery_status /
// delivery_error columns on the ticket. The legacy handler used these
// to reflect whether transactional mail submission succeeded; with
// the in-app Support Inbox the columns are still useful as an
// audit-of-attempt signal (the platform inbox shows them on detail).
// The function never claims success — only the mail sender's own
// Send() return value decides the verdict.
func (r *Repository) MarkDeliveryStatus(ctx context.Context, ticketID uint, status, errMsg string) error {
	if status == "" {
		return nil
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE support_requests SET delivery_status = ?, delivery_error = ?, updated_at = ? WHERE id = ?`,
		status, errMsg, time.Now().UTC(), ticketID); err != nil {
		return fmt.Errorf("mark delivery status: %w", err)
	}
	return nil
}
