package models

import "time"

// SupportTicket lifecycle statuses (canonical order):
//
//	open                 — created by a tenant user, awaiting platform triage.
//	in_progress          — a platform operator has started work on it.
//	waiting_for_customer — platform needs more info / reply from the tenant.
//	customer_replied     — tenant has added a reply; waiting for platform.
//	resolved             — root cause addressed; closure pending confirmation.
//	closed               — final state; no further mutations accepted.
//
// These names are deliberately stable across the API contract and the
// frontend status badge. Anything outside this set is rejected at the
// handler boundary (400 VALIDATION_ERROR).
type SupportTicketStatus = string

const (
	SupportTicketStatusOpen               SupportTicketStatus = "open"
	SupportTicketStatusInProgress         SupportTicketStatus = "in_progress"
	SupportTicketStatusWaitingForCustomer SupportTicketStatus = "waiting_for_customer"
	SupportTicketStatusCustomerReplied    SupportTicketStatus = "customer_replied"
	SupportTicketStatusResolved           SupportTicketStatus = "resolved"
	SupportTicketStatusClosed             SupportTicketStatus = "closed"
)

// IsValidSupportTicketStatus reports whether the given status is one
// of the canonical lifecycle values.
func IsValidSupportTicketStatus(s string) bool {
	switch s {
	case SupportTicketStatusOpen,
		SupportTicketStatusInProgress,
		SupportTicketStatusWaitingForCustomer,
		SupportTicketStatusCustomerReplied,
		SupportTicketStatusResolved,
		SupportTicketStatusClosed:
		return true
	}
	return false
}

// SupportTicket is the canonical Support Inbox ticket model. It is
// persisted in the `support_requests` table (legacy name retained for
// backward compat with the existing schema migration) and is the
// source of truth for both the tenant-facing Support page (own tickets)
// and the Platform Super Admin's Support Inbox (all tickets across
// tenants).
//
// One ticket row is created on POST /account/support-requests. Replies
// (tenant OR platform) live in `support_ticket_messages`. The original
// message body is stored in the DB column `message` (legacy) but is
// exposed over JSON as `description` for the canonical contract.
//
// DeliveryStatus / DeliveryError reflect the best-effort transactional
// mail attempt that happens after a ticket is persisted. They are
// honest: "sent" only when the mail sender succeeded; "failed" with
// the sender error otherwise; "disabled" when no sender is wired
// (rather than the previous lying "queued" sentinel).
type SupportTicket struct {
	ID             uint                `gorm:"primaryKey" json:"id"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	ReferenceID    string              `gorm:"uniqueIndex;not null;size:64" json:"reference_id"`
	TenantID       uint                `gorm:"index;not null" json:"tenant_id"`
	UserID         uint                `gorm:"index;not null" json:"user_id"`
	UserEmail      string              `gorm:"size:255;not null" json:"user_email"`
	Category       string              `gorm:"size:64;not null" json:"category"`
	Subject        string              `gorm:"size:255;not null" json:"subject"`
	Description    string              `gorm:"column:message;type:text;not null" json:"description"`
	Status         SupportTicketStatus `gorm:"size:32;not null;default:'open'" json:"status"`
	Priority       string              `gorm:"size:16;not null;default:'normal'" json:"priority"`
	AssignedToID   *uint               `gorm:"index" json:"assigned_to_id,omitempty"`
	LastReplyAt    *time.Time          `json:"last_reply_at,omitempty"`
	LastReplyBy    string              `gorm:"size:32" json:"last_reply_by,omitempty"`
	ClosedAt       *time.Time          `json:"closed_at,omitempty"`
	ResolvedAt     *time.Time          `json:"resolved_at,omitempty"`
	DeliveryStatus string              `gorm:"size:16;not null;default:'pending'" json:"delivery_status"`
	DeliveryError  string              `gorm:"type:text" json:"delivery_error,omitempty"`
}

// TableName pins the table name (GORM would otherwise derive it from
// the type name → support_tickets, which matches the migration).
func (SupportTicket) TableName() string { return "support_tickets" }

// SupportTicketMessage is a reply on a SupportTicket — either a tenant
// user replying to the platform, or a platform operator replying back.
// `author_kind` is one of "tenant" or "platform" so a single table can
// represent both sides of the thread without ambiguity.
type SupportTicketMessage struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	TicketID     uint      `gorm:"index;not null" json:"ticket_id"`
	AuthorUserID uint      `gorm:"index;not null" json:"author_user_id"`
	AuthorEmail  string    `gorm:"size:255;not null" json:"author_email"`
	AuthorKind   string    `gorm:"size:16;not null" json:"author_kind"` // "tenant" | "platform"
	Body         string    `gorm:"type:text;not null" json:"body"`
}

func (SupportTicketMessage) TableName() string { return "support_ticket_messages" }
