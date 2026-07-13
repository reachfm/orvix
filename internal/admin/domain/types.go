package domain

import "time"

type AdminDomain struct {
	ID           uint      `json:"id"`
	TenantID     uint      `json:"tenant_id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	Plan         string    `json:"plan"`
	Description  string    `json:"description,omitempty"`
	MaxMailboxes int       `json:"max_mailboxes"`
	MaxAliases   int       `json:"max_aliases"`
	MaxQuotaMB   int64     `json:"max_quota_mb"`
	DKIMEnabled  bool      `json:"dkim_enabled"`
	DKIMSelector string    `json:"dkim_selector"`
	DMARCEnabled bool      `json:"dmarc_enabled"`
	MailboxCount int       `json:"mailbox_count"`
	AliasCount   int       `json:"alias_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type DomainAdminAssignment struct {
	ID        uint      `json:"id"`
	DomainID  uint      `json:"domain_id"`
	UserID    uint      `json:"user_id"`
	TenantID  uint      `json:"tenant_id"`
	CreatedAt time.Time `json:"created_at"`
}

type DomainFilter struct {
	TenantID *uint
	Status   *string
	Search   string
	Limit    int
	Offset   int
}

type CreateDomainRequest struct {
	Name         string `json:"name"`
	MaxMailboxes int    `json:"max_mailboxes,omitempty"`
	MaxAliases   int    `json:"max_aliases,omitempty"`
	MaxQuotaMB   int64  `json:"max_quota_mb,omitempty"`
}

type UpdateDomainRequest struct {
	Description  *string `json:"description,omitempty"`
	MaxMailboxes *int    `json:"max_mailboxes,omitempty"`
	MaxAliases   *int    `json:"max_aliases,omitempty"`
	MaxQuotaMB   *int64  `json:"max_quota_mb,omitempty"`
	DKIMEnabled  *bool   `json:"dkim_enabled,omitempty"`
	DMARCEnabled *bool   `json:"dmarc_enabled,omitempty"`
}
