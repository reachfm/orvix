// Package mailcontrol is the Platform Super Admin mail-control bounded
// context. It orchestrates the existing production admin services
// (internal/admin/domain, internal/admin/mailbox) and owns the
// platform-side persistence for aliases, groups, and bulk operations
// that no dedicated production service exists for.
//
// Ownership model:
//   - A Platform Super Admin (role platform_super_admin, tenant_id NULL)
//     calls ONLY the /platform/* routes, all platformMW-gated.
//   - Every platform mail-control request MUST name an explicit target
//     tenant_id; the platform service never derives, infers, or defaults
//     a tenant.
//   - Every repository query and mutation is scoped by tenant_id and
//     verifies resource ownership inside the same operation.
//   - Support Access is a separate, temporary, scoped mechanism and is
//     NOT the PSA authorization model.
package mailcontrol

import "time"

// ── Platform domain views ──────────────────────────────────────────

// PlatformDomain is the platform-owned projection of a domain across
// all tenants. It wraps the admin domain detail plus platform-relevant
// counters and policy state. Field names match the admin service wire
// shape so the platform handler never invents a contract.
type PlatformDomain struct {
	ID              uint   `json:"id"`
	TenantID        uint   `json:"tenant_id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Plan            string `json:"plan"`
	Description     string `json:"description,omitempty"`
	MailboxCount    int    `json:"mailbox_count"`
	AliasCount      int    `json:"alias_count"`
	DKIMEnabled     bool   `json:"dkim_enabled"`
	DKIMSelector    string `json:"dkim_selector,omitempty"`
	DMARCEnabled    bool   `json:"dmarc_enabled"`
	MailAccessMode  string `json:"mail_access_mode"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PlatformDomainList is the stable list envelope.
type PlatformDomainList struct {
	Domains []PlatformDomain `json:"domains"`
	Total   int64            `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}

// PlatformDomainFilter controls platform-wide domain listing.
type PlatformDomainFilter struct {
	TenantID uint
	Search   string
	Status   string
	Limit    int
	Offset   int
}

// ── Platform mailbox views ─────────────────────────────────────────

type PlatformMailbox struct {
	ID          uint   `json:"id"`
	TenantID    uint   `json:"tenant_id"`
	DomainID    uint   `json:"domain_id"`
	DomainName  string `json:"domain"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	IsAdmin     bool   `json:"is_admin"`
	QuotaMB     int64  `json:"quota_mb"`
	UsedBytes   int64  `json:"used_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PlatformMailboxList struct {
	Mailboxes []PlatformMailbox `json:"mailboxes"`
	Total     int64             `json:"total"`
	Limit     int               `json:"limit"`
	Offset    int               `json:"offset"`
}

type PlatformMailboxFilter struct {
	TenantID uint
	DomainID uint
	Search   string
	Status   string
	Limit    int
	Offset   int
}

// ── Platform alias views ───────────────────────────────────────────

type PlatformAlias struct {
	ID       uint   `json:"id"`
	TenantID uint   `json:"tenant_id"`
	DomainID uint   `json:"domain_id"`
	FromAddr string `json:"from_addr"`
	ToAddr   string `json:"to_addr"`
	Active   bool   `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PlatformAliasList struct {
	Aliases []PlatformAlias `json:"aliases"`
	Total   int64           `json:"total"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
}

type PlatformAliasFilter struct {
	TenantID    uint
	DomainID    uint
	Search      string
	Destination string
	Limit       int
	Offset      int
}

// ── Platform group views ───────────────────────────────────────────

type PlatformGroup struct {
	ID          uint      `json:"id"`
	TenantID    uint      `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	MemberCount int       `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PlatformGroupList struct {
	Groups []PlatformGroup `json:"groups"`
	Total  int64           `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

type PlatformGroupFilter struct {
	TenantID uint
	Search   string
	Limit    int
	Offset   int
}

// ── Bulk mailbox operations ────────────────────────────────────────

type BulkMailboxAction string

const (
	BulkMailboxSuspend   BulkMailboxAction = "suspend"
	BulkMailboxReactivate BulkMailboxAction = "reactivate"
	BulkMailboxDelete    BulkMailboxAction = "delete"
)

type BulkMailboxRequest struct {
	TenantID uint
	DomainID uint
	IDs      []uint
	Action   BulkMailboxAction
	Reason   string
}

type BulkMailboxResult struct {
	Total     int                 `json:"total"`
	Succeeded int                 `json:"succeeded"`
	Failed    []BulkMailboxFailure `json:"failed,omitempty"`
}

type BulkMailboxFailure struct {
	ID    uint   `json:"id"`
	Error string `json:"error"`
}

// Confirmation phrases for destructive platform actions.
const (
	ConfirmMailboxPurge = "PURGE-MAILBOX-"
)

var ErrInvalidConfirmation = &mailControlError{"invalid or missing confirmation"}
var ErrTenantRequired = &mailControlError{"an explicit target tenant_id is required"}

type mailControlError struct{ msg string }

func (e *mailControlError) Error() string { return e.msg }
