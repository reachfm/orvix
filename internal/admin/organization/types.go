package organization

import "time"

type Organization struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Domain       string    `json:"domain"`
	Plan         string    `json:"plan"`
	MaxDomains   int       `json:"max_domains"`
	MaxMailboxes int       `json:"max_mailboxes"`
	LogoURL      string    `json:"logo_url,omitempty"`
	PrimaryColor string    `json:"primary_color"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type OrganizationMember struct {
	ID        uint      `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type OrganizationDetail struct {
	Organization
	DomainCount    int    `json:"domain_count"`
	MailboxCount   int    `json:"mailbox_count"`
	AdminCount     int    `json:"admin_count"`
	QuotaUsedBytes int64  `json:"quota_used_bytes"`
	StatusLabel    string `json:"status_label"`
}

type OrganizationFilter struct {
	Search string
	Active *bool
	Limit  int
	Offset int
}

type CreateOrganizationRequest struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Domain        string `json:"domain"`
	AdminEmail    string `json:"admin_email"`
	AdminPassword string `json:"admin_password"`
	Plan          string `json:"plan,omitempty"`
	MaxDomains    int    `json:"max_domains,omitempty"`
	MaxMailboxes  int    `json:"max_mailboxes,omitempty"`
}

type UpdateOrganizationRequest struct {
	Name         *string `json:"name,omitempty"`
	Domain       *string `json:"domain,omitempty"`
	Plan         *string `json:"plan,omitempty"`
	MaxDomains   *int    `json:"max_domains,omitempty"`
	MaxMailboxes *int    `json:"max_mailboxes,omitempty"`
	LogoURL      *string `json:"logo_url,omitempty"`
	PrimaryColor *string `json:"primary_color,omitempty"`
}
