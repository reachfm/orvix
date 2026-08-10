package publicv1

import "time"

const BasePath = "/api/v1/public"

const (
	ScopeOrganizationRead = "public:organization:read"
	ScopeDomainsRead      = "public:domains:read"
	ScopeDomainsWrite     = "public:domains:write"
	ScopeMailboxesRead    = "public:mailboxes:read"
	ScopeMailboxesWrite   = "public:mailboxes:write"
	ScopeAliasesRead      = "public:aliases:read"
	ScopeAliasesWrite     = "public:aliases:write"
	ScopeGroupsRead       = "public:groups:read"
	ScopeGroupsWrite      = "public:groups:write"
	ScopeUsageRead        = "public:usage:read"
)

type Metadata struct {
	RequestID string `json:"request_id"`
}

type ErrorDetail struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type ErrorBody struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Details   []ErrorDetail `json:"details,omitempty"`
	RequestID string        `json:"request_id"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type PageMeta struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

type Organization struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Domain       string    `json:"domain"`
	Plan         string    `json:"plan"`
	MaxDomains   int       `json:"max_domains"`
	MaxMailboxes int       `json:"max_mailboxes"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Domain struct {
	ID                    uint       `json:"id"`
	Name                  string     `json:"name"`
	Status                string     `json:"status"`
	Plan                  string     `json:"plan"`
	Description           string     `json:"description,omitempty"`
	MaxMailboxes          int        `json:"max_mailboxes"`
	MaxAliases            int        `json:"max_aliases"`
	MaxQuotaMB            int64      `json:"max_quota_mb"`
	DefaultMailboxQuotaMB int64      `json:"default_mailbox_quota_mb"`
	MailboxCount          int        `json:"mailbox_count"`
	AliasCount            int        `json:"alias_count"`
	StorageUsedBytes      int64      `json:"storage_used_bytes"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	DNSLastCheckedAt      *time.Time `json:"dns_last_checked_at,omitempty"`
}

type DomainList struct {
	Data []Domain `json:"data"`
	Page PageMeta `json:"page"`
	Meta Metadata `json:"meta"`
}
type DomainResponse struct {
	Data Domain   `json:"data"`
	Meta Metadata `json:"meta"`
}

type CreateDomainRequest struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Status       string `json:"status,omitempty"`
	MaxMailboxes int    `json:"max_mailboxes,omitempty"`
	MaxAliases   int    `json:"max_aliases,omitempty"`
	MaxQuotaMB   int64  `json:"max_quota_mb,omitempty"`
}
type UpdateDomainRequest struct {
	Description  *string `json:"description,omitempty"`
	MaxMailboxes *int    `json:"max_mailboxes,omitempty"`
	MaxAliases   *int    `json:"max_aliases,omitempty"`
	MaxQuotaMB   *int64  `json:"max_quota_mb,omitempty"`
}
type StatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type Mailbox struct {
	ID               uint      `json:"id"`
	DomainID         uint      `json:"domain_id"`
	Email            string    `json:"email"`
	LocalPart        string    `json:"local_part"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	QuotaMB          int64     `json:"quota_mb"`
	UsedBytes        int64     `json:"used_bytes"`
	MessageCount     int       `json:"message_count"`
	SendLimitPerHour int       `json:"send_limit_per_hour"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type MailboxList struct {
	Data []Mailbox `json:"data"`
	Page PageMeta  `json:"page"`
	Meta Metadata  `json:"meta"`
}
type MailboxResponse struct {
	Data Mailbox  `json:"data"`
	Meta Metadata `json:"meta"`
}
type CreateMailboxRequest struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	Name             string `json:"name,omitempty"`
	QuotaMB          int64  `json:"quota_mb,omitempty"`
	SendLimitPerHour int    `json:"send_limit_per_hour,omitempty"`
}
type UpdateMailboxRequest struct {
	Name             *string `json:"name,omitempty"`
	QuotaMB          *int64  `json:"quota_mb,omitempty"`
	SendLimitPerHour *int    `json:"send_limit_per_hour,omitempty"`
	AllowSMTP        *bool   `json:"allow_smtp,omitempty"`
	AllowIMAP        *bool   `json:"allow_imap,omitempty"`
	AllowPOP3        *bool   `json:"allow_pop3,omitempty"`
	AllowJMAP        *bool   `json:"allow_jmap,omitempty"`
}

type Alias struct {
	ID          uint      `json:"id"`
	DomainID    uint      `json:"domain_id"`
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type AliasList struct {
	Data []Alias  `json:"data"`
	Page PageMeta `json:"page"`
	Meta Metadata `json:"meta"`
}
type AliasResponse struct {
	Data Alias    `json:"data"`
	Meta Metadata `json:"meta"`
}
type AliasRequest struct {
	DomainID    uint   `json:"domain_id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Active      *bool  `json:"active,omitempty"`
}

type Group struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type GroupMember struct {
	ID      uint      `json:"id"`
	Email   string    `json:"email"`
	AddedAt time.Time `json:"added_at"`
}
type GroupList struct {
	Data []Group  `json:"data"`
	Page PageMeta `json:"page"`
	Meta Metadata `json:"meta"`
}
type GroupResponse struct {
	Data Group    `json:"data"`
	Meta Metadata `json:"meta"`
}
type GroupMemberList struct {
	Data []GroupMember `json:"data"`
	Page PageMeta      `json:"page"`
	Meta Metadata      `json:"meta"`
}
type GroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type GroupMemberRequest struct {
	Email string `json:"email"`
}

type Usage struct {
	PeriodStart    time.Time `json:"period_start"`
	PeriodEnd      time.Time `json:"period_end"`
	MailboxesUsed  int       `json:"mailboxes_used"`
	DomainsUsed    int       `json:"domains_used"`
	StorageUsedMB  int64     `json:"storage_used_mb"`
	EmailsSent     int64     `json:"emails_sent"`
	EmailsReceived int64     `json:"emails_received"`
	APICalls       int64     `json:"api_calls"`
}
type UsageResponse struct {
	Data Usage    `json:"data"`
	Meta Metadata `json:"meta"`
}
type DeleteResponse struct {
	Deleted bool     `json:"deleted"`
	Meta    Metadata `json:"meta"`
}
