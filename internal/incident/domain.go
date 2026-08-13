// Package incident is a bounded context for incident management and
// public-facing service status, with strict separation between internal
// diagnostics and the public status projection.
package incident

import (
	"time"
)

// Severity orders incidents by impact.
type Severity string

const (
	SevCritical  Severity = "critical"
	SevMajor     Severity = "major"
	SevMinor     Severity = "minor"
	SevDegraded  Severity = "degraded"
	SevScheduled Severity = "scheduled" // planned maintenance
)

// Status is the lifecycle state of an incident.
type Status string

const (
	StatusInvestigating Status = "investigating"
	StatusIdentified    Status = "identified"
	StatusMonitoring    Status = "monitoring"
	StatusResolved      Status = "resolved"
	StatusCancelled     Status = "cancelled"
)

// Incident is the internal incident record.
type Incident struct {
	ID          uint       `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Severity    Severity   `json:"severity"`
	Status      Status     `json:"status"`
	Services    []string   `json:"services,omitempty"`
	Regions     []string   `json:"regions,omitempty"`
	TenantIDs   []uint     `json:"tenant_ids,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`

	version int // optimistic concurrency
}

// TimelineEvent is one update in an incident's timeline.
type TimelineEvent struct {
	ID         uint      `json:"id"`
	IncidentID uint      `json:"incident_id"`
	Status     Status    `json:"status,omitempty"`
	Message    string    `json:"message"`
	Operator   string    `json:"operator"`
	CreatedAt  time.Time `json:"created_at"`
}

// PublicStatus is the safe-for-public projection of all active incidents.
type PublicStatus struct {
	Overall     string           `json:"overall"` // operational, degraded, outage, maintenance
	UpdatedAt   time.Time        `json:"updated_at"`
	Incidents   []PublicIncident `json:"incidents"`
	Maintenance []PublicIncident `json:"maintenance,omitempty"`
}

// PublicIncident is the subset of incident data safe for public display.
type PublicIncident struct {
	ID        uint      `json:"id"`
	Title     string    `json:"title"`
	Severity  Severity  `json:"severity"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// The most recent timeline message, never including diagnostics.
	LastUpdate string `json:"last_update,omitempty"`
}
