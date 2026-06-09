package licensingauthority

import "time"

// LicenseState represents the current state of a license.
type LicenseState string

const (
	LicenseValid       LicenseState = "valid"
	LicenseWarning     LicenseState = "warning"
	LicenseGrace       LicenseState = "grace"
	LicenseExpired     LicenseState = "expired"
	LicenseRevoked     LicenseState = "revoked"
	LicenseSuspended   LicenseState = "suspended"
	LicenseOfflineGrace LicenseState = "offline_grace"
)

// AuthorityState represents the reachability of the license authority.
type AuthorityState string

const (
	AuthorityOnline  AuthorityState = "online"
	AuthorityOffline AuthorityState = "offline"
	AuthorityUnknown AuthorityState = "unknown"
)

// ValidationRequest is sent to validate a license with the authority.
type ValidationRequest struct {
	LicenseID  string `json:"licenseId"`
	Edition    string `json:"edition"`
	MachineID  string `json:"machineId"`
}

// ValidationResponse from the authority.
type ValidationResponse struct {
	Valid        bool       `json:"valid"`
	LicenseState LicenseState `json:"licenseState"`
	Reason       string       `json:"reason,omitempty"`
	ValidatedAt  time.Time    `json:"validatedAt"`
}

// ActivationRequest to activate a license.
type ActivationRequest struct {
	LicenseID string `json:"licenseId"`
	MachineID string `json:"machineId"`
	Hostname  string `json:"hostname"`
}

// ActivationResponse from the authority.
type ActivationResponse struct {
	Activated bool   `json:"activated"`
	Reason    string `json:"reason,omitempty"`
}

// HeartbeatRequest sent to the authority periodically.
type HeartbeatRequest struct {
	LicenseID string `json:"licenseId"`
	MachineID string `json:"machineId"`
	UptimeSec int64  `json:"uptimeSec"`
}

// HeartbeatResponse from the authority.
type HeartbeatResponse struct {
	Acknowledged bool   `json:"acknowledged"`
	Message      string `json:"message,omitempty"`
}

// EntitlementRequest to fetch entitlements.
type EntitlementRequest struct {
	LicenseID string `json:"licenseId"`
	MachineID string `json:"machineId"`
}

// EntitlementResponse from the authority.
type EntitlementResponse struct {
	LicenseID string `json:"licenseId"`
	Edition   string `json:"edition"`
	Features  []string `json:"features"`
	Limits    EntitlementLimits `json:"limits"`
	ExpiresAt time.Time `json:"expiresAt"`
	IssuedAt  time.Time `json:"issuedAt"`
}

// EntitlementLimits defines resource limits.
type EntitlementLimits struct {
	MaxDomains   int64 `json:"maxDomains"`
	MaxMailboxes int64 `json:"maxMailboxes"`
	MaxStorageGB int64 `json:"maxStorageGB"`
	MaxNodes     int   `json:"maxNodes"`
	MaxTenants   int   `json:"maxTenants"`
	MaxChildren  int   `json:"maxChildren"`
}

// AuthorityStatus is the runtime status for admin visibility.
type AuthorityStatus struct {
	LicenseID           string         `json:"licenseId"`
	Edition             string         `json:"edition"`
	LicenseState        LicenseState   `json:"licenseState"`
	AuthorityState      AuthorityState `json:"authorityState"`
	LastValidation      time.Time      `json:"lastValidation"`
	NextValidation      time.Time      `json:"nextValidation"`
	GraceExpiresAt      time.Time      `json:"graceExpiresAt"`
	CacheValid          bool           `json:"cacheValid"`
	OfflineAllowed      bool           `json:"offlineAllowed"`
	OfflineSeconds      int64          `json:"offlineSeconds"`
	ErrorMessage        string         `json:"errorMessage,omitempty"`
}
