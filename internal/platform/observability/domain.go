// Package observability implements Feature 14 (Milestone 11): a
// bounded alerting and SLO control plane built ON TOP OF the existing
// internal/observability metrics/event/health primitives (reused, not
// duplicated) — this package owns rule evaluation, alert lifecycle,
// deduplication/cooldown, notification dispatch via provider ports,
// and SLO burn-rate math. It does not collect metrics itself.
package observability

import "time"

// AlertSeverity mirrors common ops severity levels.
type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "info"
	SeverityWarning  AlertSeverity = "warning"
	SeverityCritical AlertSeverity = "critical"
)

// Comparator is how a rule's threshold is evaluated against a metric value.
type Comparator string

const (
	ComparatorGT Comparator = "gt"
	ComparatorGE Comparator = "ge"
	ComparatorLT Comparator = "lt"
	ComparatorLE Comparator = "le"
)

func (c Comparator) Evaluate(value, threshold float64) bool {
	switch c {
	case ComparatorGT:
		return value > threshold
	case ComparatorGE:
		return value >= threshold
	case ComparatorLT:
		return value < threshold
	case ComparatorLE:
		return value <= threshold
	default:
		return false
	}
}

// Rule defines when an alert should fire: metric name (a bounded,
// known label — see kernel guidance, never a tenant email or message
// ID), comparator, threshold, and how long the condition must hold
// (Duration) before actually firing — this is what prevents a single
// noisy sample from paging anyone.
type Rule struct {
	ID         uint          `json:"id"`
	Name       string        `json:"name"`
	MetricName string        `json:"metric_name"`
	Comparator Comparator    `json:"comparator"`
	Threshold  float64       `json:"threshold"`
	Duration   time.Duration `json:"duration"`
	Severity   AlertSeverity `json:"severity"`
	Scope      string        `json:"scope,omitempty"` // e.g. "global", "tenant:<id>", "node:<id>"
	Enabled    bool          `json:"enabled"`
	// CooldownSeconds is the minimum time between two separate firings
	// of the same rule+scope after a resolution — prevents alert
	// flapping from paging repeatedly for the same underlying issue.
	CooldownSeconds int       `json:"cooldown_seconds"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// AlertState is the lifecycle of one alert instance (one Rule
// evaluated against one Scope over time).
type AlertState string

const (
	AlertPending      AlertState = "pending"      // condition observed, Duration not yet elapsed
	AlertFiring       AlertState = "firing"       // condition held for >= Duration
	AlertAcknowledged AlertState = "acknowledged" // operator has seen it, still firing
	AlertResolved     AlertState = "resolved"     // condition no longer holds
	AlertSilenced     AlertState = "silenced"     // operator suppressed notifications, condition may still hold
)

// Alert is one instance of a Rule's evaluation against a specific
// scope value over time.
type Alert struct {
	ID              uint       `json:"id"`
	RuleID          uint       `json:"rule_id"`
	Scope           string     `json:"scope"`
	State           AlertState `json:"state"`
	Value           float64    `json:"value"`
	FirstObservedAt time.Time  `json:"first_observed_at"`
	FiredAt         *time.Time `json:"fired_at,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	AcknowledgedAt  *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy  uint       `json:"acknowledged_by,omitempty"`
	SilencedUntil   *time.Time `json:"silenced_until,omitempty"`
	LastNotifiedAt  *time.Time `json:"last_notified_at,omitempty"`
	Version         int        `json:"version"`
}

// SLO is a service-level objective over a rolling window, expressed
// as a target success ratio (e.g. 0.999 = "99.9%").
type SLO struct {
	ID         uint          `json:"id"`
	Name       string        `json:"name"`
	MetricName string        `json:"metric_name"` // e.g. "delivery_success_ratio"
	Target     float64       `json:"target"`      // 0..1
	Window     time.Duration `json:"window"`
	CreatedAt  time.Time     `json:"created_at"`
}

// BurnRate is the SLO burn-rate calculation result: how fast the
// error budget is being consumed relative to the window. A burn rate
// of 1.0 means the error budget will be exactly exhausted at the end
// of Window if the current failure ratio continues; >1.0 means it
// will be exhausted early.
type BurnRate struct {
	SLOName         string    `json:"slo_name"`
	WindowStart     time.Time `json:"window_start"`
	WindowEnd       time.Time `json:"window_end"`
	Total           int64     `json:"total"`
	Good            int64     `json:"good"`
	SuccessRatio    float64   `json:"success_ratio"`
	ErrorBudget     float64   `json:"error_budget"`      // 1 - Target
	ErrorBudgetUsed float64   `json:"error_budget_used"` // (1 - SuccessRatio) / ErrorBudget
	BurnRate        float64   `json:"burn_rate"`
}
