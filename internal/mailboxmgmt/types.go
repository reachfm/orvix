package mailboxmgmt

import "time"

// MailboxStatus represents the operational state of a mailbox.
type MailboxStatus string

const (
	MailboxActive    MailboxStatus = "active"
	MailboxSuspended MailboxStatus = "suspended"
	MailboxDisabled  MailboxStatus = "disabled"
)

// Mailbox is the admin-safe view of a mailbox.
type Mailbox struct {
	ID        uint          `json:"id"`
	Email     string        `json:"email"`
	LocalPart string        `json:"localPart"`
	Domain    string        `json:"domain"`
	Name      string        `json:"name"`
	Status    MailboxStatus `json:"status"`
	QuotaMB   int64         `json:"quotaMB"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// CreateMailboxRequest is the input for creating a mailbox.
type CreateMailboxRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	DomainID uint   `json:"domainId"`
	QuotaMB  int64  `json:"quotaMB"`
}

// UpdateMailboxRequest is the input for updating a mailbox.
type UpdateMailboxRequest struct {
	Name    *string        `json:"name,omitempty"`
	Status  *MailboxStatus `json:"status,omitempty"`
	QuotaMB *int64         `json:"quotaMB,omitempty"`
}
