// Package supportaccess is a strict bounded context for temporary,
// audited support access grants with least-privilege scope, hard
// expiry, and tenant isolation.
package supportaccess

import "time"

// AccessGrant is a temporary, audited support-access grant.
type AccessGrant struct {
	ID                  uint       `json:"id"`
	TicketRef           string     `json:"ticket_ref"`
	Reason              string     `json:"reason"`
	TargetTenantID      uint       `json:"target_tenant_id"`
	TargetTenantName    string     `json:"target_tenant_name,omitempty"`
	GrantedByID         uint       `json:"granted_by_id"`
	PermissionScope     string     `json:"permission_scope"` // read_only, mailbox_view, domain_view, full_tenant_view
	Status              string     `json:"status"`           // requested, approved, active, revoked, expired
	ActivatedAt         *time.Time `json:"activated_at,omitempty"`
	ExpiresAt           time.Time  `json:"expires_at"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
	RevokeReason        string     `json:"revoke_reason,omitempty"`
	EmergencyBreakGlass bool       `json:"emergency_break_glass"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	version             int
}

const (
	StatusRequested = "requested"
	StatusApproved  = "approved"
	StatusActive    = "active"
	StatusRevoked   = "revoked"
	StatusExpired   = "expired"
)

// ValidScopes is the closed list of allowed permission scopes.
var ValidScopes = map[string]bool{
	"read_only":        true,
	"mailbox_view":     true,
	"domain_view":      true,
	"full_tenant_view": true,
}

var ErrAlreadyActive = &saError{"an active support access grant already exists for this tenant"}
var ErrNotFound = &saError{"support access grant not found"}
var ErrExpired = &saError{"support access grant has expired"}
var ErrRevoked = &saError{"support access grant has been revoked"}

type saError struct{ msg string }

func (e *saError) Error() string { return e.msg }
