package coremail

import (
	"context"
	"time"
)

// DomainStatus represents the operational status of a hosted domain.
type DomainStatus string

const (
	DomainActive    DomainStatus = "active"
	DomainSuspended DomainStatus = "suspended"
	DomainLocked    DomainStatus = "locked"
	DomainDeleted   DomainStatus = "deleted"
)

// MailAccessMode governs whether a domain may exchange mail with the
// outside world. It is enforced in the real SMTP inbound/outbound
// delivery paths (internal/coremail/smtp/commands.go handleRCPT), not
// merely surfaced in CRUD/API responses.
type MailAccessMode string

const (
	// MailAccessInternalOnly permits local delivery between mailboxes
	// on domains hosted by this platform only. Inbound mail from an
	// external (non-local) sender and outbound relay to an external
	// recipient are both rejected.
	MailAccessInternalOnly MailAccessMode = "internal_only"
	// MailAccessInternalExternal permits local delivery plus normal
	// inbound receipt from and outbound relay to external domains —
	// the default, unrestricted mode.
	MailAccessInternalExternal MailAccessMode = "internal_external"
)

func (m MailAccessMode) IsValid() bool {
	switch m {
	case MailAccessInternalOnly, MailAccessInternalExternal:
		return true
	default:
		return false
	}
}

// Domain represents a mail domain hosted by Orvix.
// Enterprise fields support multi-tenant, reseller, licensing, and abuse tracking.
type Domain struct {
	ID          uint         `json:"id"`
	Name        string       `json:"name"`
	TenantID    uint         `json:"tenant_id"`
	ResellerID  uint         `json:"reseller_id,omitempty"`
	Status      DomainStatus `json:"status"`
	Plan        string       `json:"plan"`
	Description string       `json:"description,omitempty"`

	// Quota limits (0 = unlimited at this level, inherits from tenant/plan).
	MaxMailboxes int   `json:"max_mailboxes"`
	MaxAliases   int   `json:"max_aliases"`
	MaxQuotaMB   int64 `json:"max_quota_mb"`

	// Enterprise features.
	DKIMEnabled     bool   `json:"dkim_enabled"`
	DKIMSelector    string `json:"dkim_selector,omitempty"`
	DMARCEnabled    bool   `json:"dmarc_enabled"`
	MTASTSEnabled   bool   `json:"mtasts_enabled"`
	CatchallAddress string `json:"catchall_address,omitempty"`

	// Abuse tracking.
	AbuseContact string `json:"abuse_contact,omitempty"`

	// Metadata.
	Labels       string     `json:"labels,omitempty"`        // comma-separated for reseller categorization
	MailboxCount int        `json:"mailbox_count,omitempty"` // cached count
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

// DomainFilter represents search/filter criteria for domain queries.
type DomainFilter struct {
	TenantID   *uint
	ResellerID *uint
	Status     *DomainStatus
	Search     string // name contains
	Pagination Pagination
}

// DomainRepository defines the contract for domain persistence.
type DomainRepository interface {
	Create(ctx context.Context, d *Domain, tx interface{}) error
	GetByID(ctx context.Context, id uint, tx interface{}) (*Domain, error)
	GetByName(ctx context.Context, name string, tx interface{}) (*Domain, error)
	List(ctx context.Context, filter DomainFilter, tx interface{}) ([]Domain, int64, error)
	Update(ctx context.Context, d *Domain, tx interface{}) error
	Delete(ctx context.Context, id uint, tx interface{}) error
	CountByTenant(ctx context.Context, tenantID uint, tx interface{}) (int64, error)
	CountByReseller(ctx context.Context, resellerID uint, tx interface{}) (int64, error)
	Exists(ctx context.Context, name string, tx interface{}) (bool, error)
	// GetMailAccessMode is a narrow, dedicated read used on the SMTP
	// hot path (handleRCPT, once per RCPT TO) — deliberately separate
	// from the full GetByName/scanDomain path so the policy check
	// stays a single indexed column read. Returns
	// MailAccessInternalExternal (the safe default) when the domain
	// has no explicit mode set, so pre-migration rows never fail
	// closed against inbound/outbound mail unexpectedly.
	GetMailAccessMode(ctx context.Context, name string, tx interface{}) (MailAccessMode, error)
	// SetMailAccessMode updates the domain's access mode by ID,
	// tenant-scoped so a caller cannot alter a domain it does not own.
	SetMailAccessMode(ctx context.Context, id, tenantID uint, mode MailAccessMode, tx interface{}) error
}
