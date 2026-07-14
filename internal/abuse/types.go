package abuse

import "time"

type SendLimitScope string

const (
	ScopeTenant  SendLimitScope = "tenant"
	ScopeUser    SendLimitScope = "user"
	ScopeMailbox SendLimitScope = "mailbox"
)

type RateLimitBucket struct {
	Key       string         `json:"key"`
	Scope     SendLimitScope `json:"scope"`
	Limit     int            `json:"limit"`
	Remaining int            `json:"remaining"`
	ResetAt   time.Time      `json:"reset_at"`
}

type AbuseSignal struct {
	ID             uint           `json:"id"`
	TenantID       uint           `json:"tenant_id"`
	MailboxID      *uint          `json:"mailbox_id,omitempty"`
	SignalType     SignalType     `json:"signal_type"`
	Severity       SignalSeverity `json:"severity"`
	Description    string         `json:"description"`
	Metadata       string         `json:"metadata,omitempty"`
	DetectedAt     time.Time      `json:"detected_at"`
	AcknowledgedAt *time.Time     `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
	ResolvedBy     *uint          `json:"resolved_by,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type SignalType string

const (
	SignalHighBounceRate     SignalType = "high_bounce_rate"
	SignalSpamComplaint      SignalType = "spam_complaint"
	SignalRateLimitBurst     SignalType = "rate_limit_burst"
	SignalSuspiciousLogin    SignalType = "suspicious_login"
	SignalCompromisedAccount SignalType = "compromised_account"
	SignalAbuseReport        SignalType = "abuse_report"
	SignalStorageBurst       SignalType = "storage_burst"
)

type SignalSeverity string

const (
	SeverityInfo     SignalSeverity = "info"
	SeverityWarning  SignalSeverity = "warning"
	SeverityCritical SignalSeverity = "critical"
)
