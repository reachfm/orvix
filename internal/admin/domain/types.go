package domain

import (
	"fmt"
	"strings"
	"time"
)

type AdminDomain struct {
	ID           uint   `json:"id"`
	TenantID     uint   `json:"tenant_id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Plan         string `json:"plan"`
	Description  string `json:"description,omitempty"`
	MaxMailboxes int    `json:"max_mailboxes"`
	MaxAliases   int    `json:"max_aliases"`
	MaxQuotaMB   int64  `json:"max_quota_mb"`
	DKIMEnabled  bool   `json:"dkim_enabled"`
	DKIMSelector string `json:"dkim_selector"`
	DMARCEnabled bool   `json:"dmarc_enabled"`
	MailboxCount int    `json:"mailbox_count"`
	AliasCount   int    `json:"alias_count"`
	// StorageUsedBytes/MessageCount are real aggregates over
	// coremail_mailboxes.used_bytes/msg_count, computed in the same
	// batched list query as MailboxCount/AliasCount above (correlated
	// subqueries evaluated per-row by the database in one round trip,
	// not one query per domain). DNSHealth/DNSScore/DNSLastCheckedAt
	// come from the domain's latest customer_domain_verifications row,
	// also joined in the same query — never a separate per-domain call.
	StorageUsedBytes  int64      `json:"storage_used_bytes"`
	StorageLimitBytes int64      `json:"storage_limit_bytes"`
	MessageCount      int        `json:"message_count"`
	DNSHealth         string     `json:"dns_health,omitempty"`
	DNSScore          int        `json:"dns_score"`
	DNSLastCheckedAt  *time.Time `json:"dns_last_checked_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
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

// DomainStatus is the explicit, typed set of persisted domain states on
// coremail_domains.status. Only these values may be written; every other value
// is rejected at the service boundary. "deleted" is intentionally absent: domain
// deletion is represented by the deleted_at soft-delete column, not the status
// column. "pending"/"verified" are absent because no DNS-verification workflow
// persists a verification state on coremail_domains (see D2 investigation).
type DomainStatus string

const (
	DomainStatusActive    DomainStatus = "active"
	DomainStatusDisabled  DomainStatus = "disabled"
	DomainStatusSuspended DomainStatus = "suspended"
	// DomainStatusLocked is a defensive READ-side value only: the mailbox
	// eligibility switch (internal/admin/mailbox/service.go) maps it to
	// ErrDomainLocked in case a pre-existing or externally-written row ever
	// carries it. It is deliberately NOT accepted by ParseDomainStatus below
	// — no production write path (SetDomainStatus, any frontend control, or
	// git history) ever set a domain to "locked" before this fix, so
	// SetDomainStatus must not be able to introduce it as a new writable
	// product state. If a genuine account-lock feature is built, add it
	// back here alongside real product support for setting it.
	DomainStatusLocked DomainStatus = "locked"
)

// ParseDomainStatus normalizes (trim + lowercase) and validates a status
// string against the supported, WRITABLE set. It returns the canonical form
// and whether it is supported. Unsupported/unknown values — including
// "locked", which has no evidenced writer — return ok=false so callers fail
// closed instead of persisting free-text or invented status.
func ParseDomainStatus(s string) (DomainStatus, bool) {
	n := strings.ToLower(strings.TrimSpace(s))
	switch DomainStatus(n) {
	case DomainStatusActive, DomainStatusDisabled, DomainStatusSuspended:
		return DomainStatus(n), true
	default:
		return "", false
	}
}

// Domain errors
var (
	ErrDomainNotFound        = fmt.Errorf("domain not found")
	ErrDomainForbidden       = fmt.Errorf("domain access denied")
	ErrDomainHasMailboxes    = fmt.Errorf("domain has mailboxes")
	ErrDomainHasDependencies = fmt.Errorf("domain has dependencies (aliases, DKIM, routing)")
	ErrPrimaryDomainDelete   = fmt.Errorf("cannot delete primary domain")
	ErrDomainDeleteFailed    = fmt.Errorf("domain deletion failed")
	ErrDomainAlreadyExists   = fmt.Errorf("domain already exists")
	ErrInvalidDomainName     = fmt.Errorf("invalid domain name")
	ErrDomainLimitReached    = fmt.Errorf("domain limit reached")
	ErrDomainDisabled        = fmt.Errorf("domain is disabled")
	ErrDomainNotMailEnabled  = fmt.Errorf("domain is not mail-enabled")
	ErrDomainNotVerified     = fmt.Errorf("domain DNS not verified")
	ErrDomainNotActive       = fmt.Errorf("domain is not active")
	ErrDomainDeleted         = fmt.Errorf("domain is deleted")
	ErrDomainSuspended       = fmt.Errorf("domain is suspended")
	ErrDomainLocked          = fmt.Errorf("domain is locked")
	ErrDomainUnavailable     = fmt.Errorf("domain is unavailable")
	ErrInvalidDomainStatus   = fmt.Errorf("unsupported domain status")
	ErrDKIMAlreadyConfigured = fmt.Errorf("dkim already configured for domain")
	ErrDKIMNotConfigured     = fmt.Errorf("dkim not configured for domain")
)

// Machine-readable error codes for the domain API contract. These codes are
// the stable, versioned contract the frontend maps to user-facing messages;
// the "error" string field is never parsed by clients.
//
// DOMAIN_NOT_VERIFIED is RESERVED: the coremail_domains schema has no
// verification column and no DNS-verification workflow persists one, so no
// production code path emits it today. It is kept in the contract for a future
// genuine verification feature, but unknown or administratively restricted
// domain states return DOMAIN_UNAVAILABLE instead of being mislabeled as
// DNS-unverified.
const (
	CodeDomainNotFound        = "DOMAIN_NOT_FOUND"
	CodeDomainDisabled        = "DOMAIN_DISABLED"
	CodeDomainSuspended       = "DOMAIN_SUSPENDED"
	CodeDomainLocked          = "DOMAIN_LOCKED"
	CodeDomainUnavailable     = "DOMAIN_UNAVAILABLE"
	CodeDomainStatusInvalid   = "DOMAIN_STATUS_INVALID"
	CodeDomainNotVerified     = "DOMAIN_NOT_VERIFIED"
	CodeDomainAlreadyExists   = "DOMAIN_ALREADY_EXISTS"
	CodeInvalidDomainName     = "INVALID_DOMAIN_NAME"
	CodeDomainHasMailboxes    = "DOMAIN_HAS_MAILBOXES"
	CodeDomainHasDependencies = "DOMAIN_HAS_DEPENDENCIES"
	CodeDomainLimitReached    = "DOMAIN_LIMIT_REACHED"
	CodeDKIMAlreadyConfigured = "DKIM_ALREADY_CONFIGURED"
	CodeDKIMNotConfigured     = "DKIM_NOT_CONFIGURED"
)

// ValidateDomainName validates and normalizes a domain name.
//
// Returns the normalized lowercase domain or ErrInvalidDomainName.
//
// Behavior notes:
//   - IDNA: the project does not deliberately support internationalized
//     domain names (no punycode conversion). Any label containing a
//     non-ASCII rune is rejected consistently here and everywhere this
//     canonical path is used.
//   - Trailing dots (FQDN form) are tolerated and stripped; the normalized
//     form has no trailing dot.
//   - URL schemes, paths, ports, fragments, query strings, wildcards,
//     whitespace, email addresses, and out-of-range labels are rejected.
func ValidateDomainName(name string) (string, error) {
	d := strings.TrimSpace(name)
	if d == "" {
		return "", ErrInvalidDomainName
	}
	d = strings.ToLower(d)

	// Reject URLs, schemes, paths
	if strings.Contains(d, "://") || strings.Contains(d, "/") || strings.Contains(d, "\\") {
		return "", ErrInvalidDomainName
	}
	// Reject spaces, wildcards
	if strings.Contains(d, " ") || strings.Contains(d, "*") || strings.Contains(d, "?") {
		return "", ErrInvalidDomainName
	}
	// Reject email addresses (contains @)
	if strings.Contains(d, "@") {
		return "", ErrInvalidDomainName
	}
	// Reject port syntax (host:port), fragments (#), and query strings (?)
	if strings.Contains(d, ":") || strings.Contains(d, "#") || strings.Contains(d, "?") {
		return "", ErrInvalidDomainName
	}
	// Remove trailing dot (FQDN policy)
	d = strings.TrimSuffix(d, ".")

	// Split into labels
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return "", ErrInvalidDomainName
	}
	for _, label := range labels {
		if label == "" {
			return "", ErrInvalidDomainName
		}
		if len(label) > 63 {
			return "", ErrInvalidDomainName
		}
		// Leading/trailing hyphen not allowed
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", ErrInvalidDomainName
		}
		// Check valid characters. Non-ASCII (IDN) labels are rejected
		// consistently because the project does not implement IDNA.
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return "", ErrInvalidDomainName
			}
		}
	}

	// Max FQDN length
	if len(d) > 253 {
		return "", ErrInvalidDomainName
	}

	return d, nil
}
