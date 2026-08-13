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
	// SignalSuppressed is the canonical category for the delivery
	// worker's real suppression rejection (StatusMsg
	// "recipient is suppressed" from deliverRemote). It maps to real
	// delivery-path evidence — never fabricated.
	SignalSuppressed SignalType = "suppressed"
)

func (s SignalType) IsValid() bool {
	switch s {
	case SignalDelivered, SignalTempFail, SignalPermFail, SignalBounce, SignalComplaint,
		SignalSpamReject, SignalPolicyReject, SignalThrottled, SignalTLSFailure, SignalAuthFailure,
		SignalSuppressed:
		return true
	default:
		return false
	}
}

// Category is the platform-facing event taxonomy. Each category maps
// to real recorded SignalType evidence — no invented categories.
type Category string

const (
	CategoryDelivered    Category = "delivered"
	CategoryFailed       Category = "failed"
	CategoryDeferred     Category = "deferred"
	CategoryBounced      Category = "bounced"
	CategoryPolicyDenied Category = "policy_denied"
	CategorySuppressed   Category = "suppressed"
	CategoryRelayFailure Category = "relay_failure"
	CategoryOther        Category = "other"
)

// categoryOf maps a real SignalType to its platform category.
func categoryOf(t SignalType) Category {
	switch t {
	case SignalDelivered:
		return CategoryDelivered
	case SignalTempFail, SignalThrottled:
		return CategoryDeferred
	case SignalBounce, SignalSpamReject:
		return CategoryBounced
	case SignalPolicyReject:
		return CategoryPolicyDenied
	case SignalSuppressed:
		return CategorySuppressed
	case SignalTLSFailure, SignalAuthFailure:
		return CategoryRelayFailure
	case SignalPermFail, SignalComplaint:
		return CategoryFailed
	default:
		return CategoryOther
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

// SuppressionState is the history-preserving lifecycle state. Release
// semantics: a suppression is never "deleted" — it is released
// (operator action), expires (scheduler reconciliation), or is active.
type SuppressionState string

const (
	SuppressionActive   SuppressionState = "active"
	SuppressionReleased SuppressionState = "released"
	SuppressionExpired  SuppressionState = "expired"
)

// Suppression blocks future outbound delivery to Address for TenantID
// until ExpiresAt (nil = indefinite). Enforced transactionally in the
// real outbound delivery path, not just surfaced as an admin list.
type Suppression struct {
	ID             uint              `json:"id"`
	TenantID       uint              `json:"tenant_id"`
	Address        string            `json:"address"` // normalized lowercase
	Reason         SuppressionReason `json:"reason"`
	Source         string            `json:"source"` // e.g. "smtp_5xx", "fbl_provider_x", "operator"
	ActorID        uint              `json:"actor_id,omitempty"`
	Notes          string            `json:"notes,omitempty"`
	ExpiresAt      *time.Time        `json:"expires_at,omitempty"`
	State          SuppressionState  `json:"state"`
	ReleasedAt     *time.Time        `json:"released_at,omitempty"`
	ReleasedBy     uint              `json:"released_by,omitempty"`
	ReleasedReason string            `json:"released_reason,omitempty"`
	Version        int               `json:"version"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// SuppressionEvent is one append-only lifecycle transition record —
// the safe evidence trail for a suppression (created / released /
// reactivated / expired). It carries only the projection fields, never
// message content.
type SuppressionEvent struct {
	ID            uint      `json:"id"`
	SuppressionID uint      `json:"suppression_id"`
	TenantID      uint      `json:"tenant_id"`
	Event         string    `json:"event"`
	ActorID       uint      `json:"actor_id,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	At            time.Time `json:"at"`
}

// SuppressionFilter bounds and filters the suppression list.
type SuppressionFilter struct {
	TenantID    uint
	Domain      string // address-domain filter (LIKE %@domain)
	Search      string // address substring search
	Reason      string
	Source      string
	State       SuppressionState
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	ExpiryFrom  *time.Time
	ExpiryTo    *time.Time
	Limit       int
	Offset      int
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
