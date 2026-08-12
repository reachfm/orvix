// Package deliverability implements Feature 12 (Milestone 9): a
// deliverability and reputation control plane integrated with the
// existing SMTP, queue, delivery-history, and relay systems. It
// reuses internal/coremail/delivery.BounceType/ClassifyBounce for
// bounce classification and internal/customerdomain's DNS inspector
// for SPF/DKIM/DMARC posture — this package does not duplicate either.
package deliverability

import "time"

// SignalType is the normalized outcome of one outbound delivery
// attempt or inbound feedback event.
type SignalType string

const (
	SignalDelivered    SignalType = "delivered"
	SignalTempFail     SignalType = "temp_fail"
	SignalPermFail     SignalType = "perm_fail"
	SignalBounce       SignalType = "bounce"
	SignalComplaint    SignalType = "complaint"
	SignalSpamReject   SignalType = "spam_reject"
	SignalPolicyReject SignalType = "policy_reject"
	SignalThrottled    SignalType = "throttled"
	SignalTLSFailure   SignalType = "tls_failure"
	SignalAuthFailure  SignalType = "auth_failure"
)

func (s SignalType) IsValid() bool {
	switch s {
	case SignalDelivered, SignalTempFail, SignalPermFail, SignalBounce, SignalComplaint,
		SignalSpamReject, SignalPolicyReject, SignalThrottled, SignalTLSFailure, SignalAuthFailure:
		return true
	default:
		return false
	}
}

// Dimension is which reputation axis a Signal is recorded against.
// A single delivery attempt is recorded once per applicable
// dimension (tenant, sending domain, recipient domain, relay
// provider) so aggregation never has to fan a raw event out at query
// time — bounded cardinality by construction, not by a query-time
// GROUP BY over unbounded label values.
type Dimension string

const (
	DimensionTenant          Dimension = "tenant"
	DimensionSendingDomain   Dimension = "sending_domain"
	DimensionRecipientDomain Dimension = "recipient_domain"
	DimensionRelayProvider   Dimension = "relay_provider"
)

// Signal is one recorded delivery outcome for one dimension.
type Signal struct {
	ID             uint       `json:"id"`
	EventKey       string     `json:"event_key"` // idempotency key: same event recorded twice is a no-op
	TenantID       uint       `json:"tenant_id"`
	Dimension      Dimension  `json:"dimension"`
	DimensionValue string     `json:"dimension_value"` // e.g. the domain name, or the provider name
	Type           SignalType `json:"type"`
	LatencyMS      int64      `json:"latency_ms,omitempty"`
	RecordedAt     time.Time  `json:"recorded_at"`
}

// WindowMetrics is the aggregated result for one dimension value over
// one time window.
type WindowMetrics struct {
	Dimension      Dimension `json:"dimension"`
	DimensionValue string    `json:"dimension_value"`
	WindowStart    time.Time `json:"window_start"`
	WindowEnd      time.Time `json:"window_end"`
	Volume         int64     `json:"volume"`
	Delivered      int64     `json:"delivered"`
	TempFail       int64     `json:"temp_fail"`
	PermFail       int64     `json:"perm_fail"`
	Bounced        int64     `json:"bounced"`
	Complaints     int64     `json:"complaints"`
	AvgLatencyMS   float64   `json:"avg_latency_ms"`
	DeliveryRate   float64   `json:"delivery_rate"`  // Delivered / Volume
	BounceRate     float64   `json:"bounce_rate"`    // Bounced / Volume
	ComplaintRate  float64   `json:"complaint_rate"` // Complaints / Volume
	TempFailRate   float64   `json:"temp_fail_rate"`
	PermFailRate   float64   `json:"perm_fail_rate"`
}

// SuppressionReason is why an address is suppressed from future sends.
type SuppressionReason string

const (
	SuppressionHardBounce SuppressionReason = "hard_bounce"
	SuppressionComplaint  SuppressionReason = "complaint"
	SuppressionManual     SuppressionReason = "manual"
)

// Suppression blocks future outbound delivery to Address for TenantID
// until ExpiresAt (nil = indefinite). Enforced transactionally in the
// real outbound delivery path, not just surfaced as an admin list.
type Suppression struct {
	ID        uint              `json:"id"`
	TenantID  uint              `json:"tenant_id"`
	Address   string            `json:"address"`
	Reason    SuppressionReason `json:"reason"`
	Source    string            `json:"source"` // e.g. "smtp_5xx", "fbl_provider_x", "operator"
	ActorID   uint              `json:"actor_id,omitempty"`
	Notes     string            `json:"notes,omitempty"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// FeedbackEvent is a normalized provider feedback item (FBL complaint,
// bounce webhook, etc.) — the port every provider adapter must
// produce, so ingestion logic is provider-agnostic.
type FeedbackEvent struct {
	ProviderEventID string // used as the idempotency key
	TenantID        uint
	Address         string
	Type            SignalType // typically SignalComplaint or SignalBounce
	OccurredAt      time.Time
	RawSource       string // provider name, for audit only — never the raw payload body
}
