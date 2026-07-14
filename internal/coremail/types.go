package coremail

// AccountStatus represents the operational status of a mailbox or domain.
type AccountStatus string

const (
	StatusActive    AccountStatus = "active"
	StatusSuspended AccountStatus = "suspended"
	StatusDisabled  AccountStatus = "disabled"
	StatusDeleted   AccountStatus = "deleted"
)

// QuotaScope defines how quota is calculated.
type QuotaScope string

const (
	QuotaScopeMailbox QuotaScope = "mailbox"
	QuotaScopeDomain  QuotaScope = "domain"
	QuotaScopeTenant  QuotaScope = "tenant"
)

// FolderType represents the well-known IMAP folder types.
type FolderType string

const (
	FolderInbox   FolderType = "inbox"
	FolderSent    FolderType = "sent"
	FolderDrafts  FolderType = "drafts"
	FolderTrash   FolderType = "trash"
	FolderJunk    FolderType = "junk"
	FolderArchive FolderType = "archive"
)

// AuthScheme defines the password hashing algorithm.
type AuthScheme string

const (
	AuthSchemeArgon2ID AuthScheme = "argon2id"
	AuthSchemeSHA256   AuthScheme = "sha256"
	AuthSchemeSCRAM    AuthScheme = "scram"
)

// DeliveryProtocol indicates which protocol delivered a message.
type DeliveryProtocol string

const (
	DeliverySMTP    DeliveryProtocol = "smtp"
	DeliveryIMAP    DeliveryProtocol = "imap"
	DeliveryPOP3    DeliveryProtocol = "pop3"
	DeliveryAPISMTP DeliveryProtocol = "api"
)

// MessageFlag represents standard IMAP message flags.
type MessageFlag string

const (
	FlagSeen     MessageFlag = "seen"
	FlagAnswered MessageFlag = "answered"
	FlagFlagged  MessageFlag = "flagged"
	FlagDeleted  MessageFlag = "deleted"
	FlagDraft    MessageFlag = "draft"
)

// Pagination is a generic pagination request.
type Pagination struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

func (p Pagination) Normalize() Pagination {
	if p.Limit < 1 || p.Limit > 1000 {
		p.Limit = 100
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}
