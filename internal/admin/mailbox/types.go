package mailbox

import "time"

type AdminMailboxStatus string

const (
	AdminMailboxActive    AdminMailboxStatus = "active"
	AdminMailboxDisabled  AdminMailboxStatus = "disabled"
	AdminMailboxSuspended AdminMailboxStatus = "suspended"
	AdminMailboxDeleted   AdminMailboxStatus = "deleted"
)

type AdminMailbox struct {
	ID        uint              `json:"id"`
	DomainID  uint              `json:"domain_id"`
	TenantID  uint              `json:"tenant_id"`
	Email     string            `json:"email"`
	LocalPart string            `json:"local_part"`
	Name      string            `json:"name"`
	Status    AdminMailboxStatus `json:"status"`
	QuotaMB   int64             `json:"quota_mb"`
	UsedBytes int64             `json:"used_bytes"`
	MsgCount  int               `json:"msg_count"`
	IsAdmin   bool              `json:"is_admin"`
	AllowSMTP bool              `json:"allow_smtp"`
	AllowIMAP bool              `json:"allow_imap"`
	AllowPOP3 bool              `json:"allow_pop3"`
	AllowJMAP bool              `json:"allow_jmap"`
	MFAEnabled  bool             `json:"mfa_enabled"`
	SendLimit   int              `json:"send_limit_per_hour"`
	LastLogin   *time.Time       `json:"last_login"`
	LastIP      string           `json:"last_ip"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

type AdminMailboxResponse struct {
	Mailbox   AdminMailbox `json:"mailbox"`
	AliasCount int          `json:"alias_count,omitempty"`
}

type CreateMailboxRequest struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	Name          string `json:"name,omitempty"`
	QuotaMB       int64  `json:"quota_mb,omitempty"`
	SendLimit     int    `json:"send_limit_per_hour,omitempty"`
	ForcePasswordChange bool `json:"force_password_change,omitempty"`
}

type CreateMailboxResponse struct {
	Mailbox  AdminMailbox `json:"mailbox"`
	Password string       `json:"password,omitempty"`
}

type UpdateMailboxRequest struct {
	Name      *string            `json:"name,omitempty"`
	QuotaMB   *int64             `json:"quota_mb,omitempty"`
	SendLimit *int               `json:"send_limit_per_hour,omitempty"`
	IsAdmin   *bool              `json:"is_admin,omitempty"`
	AllowSMTP *bool              `json:"allow_smtp,omitempty"`
	AllowIMAP *bool              `json:"allow_imap,omitempty"`
	AllowPOP3 *bool              `json:"allow_pop3,omitempty"`
	AllowJMAP *bool              `json:"allow_jmap,omitempty"`
}

type MailboxFilter struct {
	DomainID *uint
	TenantID *uint
	Status   *AdminMailboxStatus
	Search   string
	Limit    int
	Offset   int
}

type BulkActionRequest struct {
	MailboxIDs []uint `json:"mailbox_ids"`
	Reason     string `json:"reason,omitempty"`
}
