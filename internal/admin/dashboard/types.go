package dashboard

type CustomerDashboard struct {
	TotalDomains        int64 `json:"total_domains"`
	HealthyDomains      int64 `json:"healthy_domains"`
	DomainsNeedingAttention int64 `json:"domains_needing_attention"`
	TotalMailboxes      int64 `json:"total_mailboxes"`
	ActiveMailboxes     int64 `json:"active_mailboxes"`
	SuspendedMailboxes  int64 `json:"suspended_mailboxes"`
	DisabledMailboxes   int64 `json:"disabled_mailboxes"`
	QuotaUsedBytes      int64 `json:"quota_used_bytes"`
	RecentActions       []RecentAction `json:"recent_actions"`
}

type PlatformDashboard struct {
	TotalOrganizations  int64 `json:"total_organizations"`
	ActiveOrganizations int64 `json:"active_organizations"`
	TotalDomains        int64 `json:"total_domains"`
	TotalMailboxes      int64 `json:"total_mailboxes"`
	QuotaUsedBytes      int64 `json:"quota_used_bytes"`
	RecentAuditEntries  []RecentAction `json:"recent_audit_entries"`
}

type RecentAction struct {
	Action    string `json:"action"`
	Target    string `json:"target"`
	Timestamp string `json:"timestamp"`
}

type DomainHealthSummary struct {
	DomainID   uint   `json:"domain_id"`
	DomainName string `json:"domain_name"`
	Status     string `json:"status"`
	MXStatus   string `json:"mx_status"`
	SPFStatus  string `json:"spf_status"`
	DKIMStatus string `json:"dkim_status"`
	DMARCStatus string `json:"dmarc_status"`
}
