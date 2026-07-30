package domain

import (
	"fmt"
	"strings"
	"time"
)

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

// Domain errors
var (
	ErrDomainNotFound          = fmt.Errorf("domain not found")
	ErrDomainForbidden         = fmt.Errorf("domain access denied")
	ErrDomainHasMailboxes      = fmt.Errorf("domain has mailboxes")
	ErrDomainHasDependencies   = fmt.Errorf("domain has dependencies (aliases, DKIM, routing)")
	ErrPrimaryDomainDelete     = fmt.Errorf("cannot delete primary domain")
	ErrDomainDeleteFailed      = fmt.Errorf("domain deletion failed")
	ErrDomainAlreadyExists     = fmt.Errorf("domain already exists")
	ErrInvalidDomainName       = fmt.Errorf("invalid domain name")
	ErrDomainLimitReached      = fmt.Errorf("domain limit reached")
	ErrDomainDisabled          = fmt.Errorf("domain is disabled")
	ErrDomainNotMailEnabled    = fmt.Errorf("domain is not mail-enabled")
	ErrDomainNotVerified       = fmt.Errorf("domain DNS not verified")
	ErrDomainNotActive         = fmt.Errorf("domain is not active")
	ErrDomainDeleted           = fmt.Errorf("domain is deleted")
)

// ValidateDomainName validates and normalizes a domain name.
// Returns the normalized lowercase domain or an error.
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
		// Must contain at least one letter (not purely numeric)
		hasLetter := false
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
				hasLetter = true
				break
			}
		}
		if !hasLetter && len(label) > 0 {
			// Allow purely numeric only for special cases like IP addresses
			// Domain labels should have at least one letter
			// Actually pure numeric labels are valid DNS (e.g., 123.com)
			// So skip this check
		}
		// Check valid characters
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-') {
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
