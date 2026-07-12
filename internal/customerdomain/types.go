package customerdomain

import (
	"time"
)

// DomainOverview is the customer-facing domain summary.
type DomainOverview struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	MailboxCount int       `json:"mailbox_count"`
	DNSHealth    string    `json:"dns_health"`
	HealthScore  int       `json:"health_score"`
	LastChecked  *string   `json:"last_checked,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DomainDetail is the customer-facing domain detail view.
type DomainDetail struct {
	ID              uint       `json:"id"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	Plan            string     `json:"plan"`
	Description     string     `json:"description,omitempty"`
	MaxMailboxes    int        `json:"max_mailboxes"`
	MaxAliases      int        `json:"max_aliases"`
	MaxQuotaMB      int64      `json:"max_quota_mb"`
	MailboxCount    int        `json:"mailbox_count"`
	DKIMEnabled     bool       `json:"dkim_enabled"`
	DKIMSelector    string     `json:"dkim_selector,omitempty"`
	DMARCEnabled    bool       `json:"dmarc_enabled"`
	MTASTSEnabled   bool       `json:"mtasts_enabled"`
	HealthScore     int        `json:"health_score"`
	DNSHealth       string     `json:"dns_health"`
	LatestDNSResult *DNSResult `json:"latest_dns_result,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// DNSResult is a structured DNS inspection outcome.
type DNSResult struct {
	MX    *MXCheck    `json:"mx"`
	SPF   *SPFCheck   `json:"spf"`
	DKIM  *DKIMCheck  `json:"dkim"`
	DMARC *DMARCCheck `json:"dmarc"`
}

// MXCheck is the MX record inspection result.
type MXCheck struct {
	Status    string   `json:"status"`
	Observed  []string `json:"observed,omitempty"`
	Expected  string   `json:"expected,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	CheckedAt string   `json:"checked_at"`
}

// SPFCheck is the SPF record inspection result.
type SPFCheck struct {
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
}

// DKIMCheck is the DKIM record inspection result.
type DKIMCheck struct {
	Selector  string `json:"selector,omitempty"`
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
	PublicKey string `json:"public_key,omitempty"`
}

// DMARCCheck is the DMARC record inspection result.
type DMARCCheck struct {
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
}

// DNSStatus represents the check outcome.
type DNSStatus string

const (
	DNSStatusPass    DNSStatus = "pass"
	DNSStatusWarning DNSStatus = "warning"
	DNSStatusFail    DNSStatus = "fail"
	DNSStatusUnknown DNSStatus = "unknown"
)

// DomainListRequest is the paginated list input.
type DomainListRequest struct {
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Search string `json:"search,omitempty"`
	Status string `json:"status,omitempty"`
}

// DomainListResponse is the paginated list output.
type DomainListResponse struct {
	Domains []DomainOverview `json:"domains"`
	Total   int64            `json:"total"`
	Offset  int              `json:"offset"`
	Limit   int              `json:"limit"`
}

// VerificationSnapshot holds a persisted verification result.
type VerificationSnapshot struct {
	ID          uint      `json:"id"`
	DomainID    uint      `json:"domain_id"`
	Score       int       `json:"score"`
	Status      string    `json:"status"`
	MXStatus    string    `json:"mx_status,omitempty"`
	SPFStatus   string    `json:"spf_status,omitempty"`
	DKIMStatus  string    `json:"dkim_status,omitempty"`
	DMARCStatus string    `json:"dmarc_status,omitempty"`
	Evidence    string    `json:"evidence,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	CreatedAt   time.Time `json:"created_at"`
}
