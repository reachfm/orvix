package incident

import (
	"context"
	"fmt"
	"time"
)

// Valid incident status transitions. The map key is the current status;
// the value is the set of allowed next statuses.
var validTransitions = map[Status]map[Status]bool{
	StatusInvestigating: {StatusIdentified: true, StatusMonitoring: true, StatusResolved: true, StatusCancelled: true},
	StatusIdentified:    {StatusMonitoring: true, StatusResolved: true, StatusCancelled: true},
	StatusMonitoring:    {StatusResolved: true, StatusCancelled: true},
	StatusResolved:      {}, // terminal
	StatusCancelled:     {}, // terminal
}

// CanTransition reports whether transitioning from `from` to `to` is valid.
func CanTransition(from, to Status) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}

// Service is the incident management service.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// EnsureSchema creates the incident tables.
func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repo.EnsureSchema(ctx)
}

// Create adds a new incident.
func (s *Service) Create(ctx context.Context, title, description string, severity Severity, services, regions []string, tenantIDs []uint) (*Incident, error) {
	if title == "" {
		return nil, ErrTitleRequired
	}
	severity = normalizeSeverity(severity)
	inc := &Incident{
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      StatusInvestigating,
		Services:    services,
		Regions:     regions,
		TenantIDs:   tenantIDs,
	}
	if err := s.repo.Insert(ctx, inc); err != nil {
		return nil, err
	}
	return inc, nil
}

// Update transitions an incident and appends a timeline event.
func (s *Service) Update(ctx context.Context, id uint, to Status, message, operator string) (*Incident, error) {
	inc, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	to = normalizeStatus(to)
	if to == "" || to == inc.Status {
		// No-op or unchanged — still record the message.
		if message != "" {
			_ = s.repo.AddTimelineEvent(ctx, &TimelineEvent{IncidentID: inc.ID, Message: message, Operator: operator})
		}
		return inc, nil
	}
	if !CanTransition(inc.Status, to) {
		return nil, fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, inc.Status, to)
	}
	inc.Status = to
	if message != "" {
		inc.Description = message
	}
	if to == StatusResolved || to == StatusCancelled {
		now := time.Now().UTC()
		inc.ResolvedAt = &now
	}
	if err := s.repo.Update(ctx, inc); err != nil {
		return nil, err
	}
	ev := &TimelineEvent{IncidentID: inc.ID, Status: inc.Status, Message: message, Operator: operator}
	_ = s.repo.AddTimelineEvent(ctx, ev)
	return inc, nil
}

// Get returns an incident by ID.
func (s *Service) Get(ctx context.Context, id uint) (*Incident, error) {
	inc, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	return inc, nil
}

// List returns incidents with optional status filter and cursor pagination.
func (s *Service) List(ctx context.Context, status string, limit int) ([]Incident, error) {
	return s.repo.List(ctx, status, limit)
}

// Timeline returns the timeline for an incident.
func (s *Service) Timeline(ctx context.Context, id uint) ([]TimelineEvent, error) {
	return s.repo.Timeline(ctx, id)
}

// PublicStatus returns the safe-for-public status projection.
func (s *Service) PublicStatus(ctx context.Context) (*PublicStatus, error) {
	return s.repo.PublicStatus(ctx)
}

var (
	ErrNotFound          = &incidentError{"incident not found"}
	ErrTitleRequired     = &incidentError{"incident title is required"}
	ErrInvalidTransition = &incidentError{"invalid incident status transition"}
)

func normalizeSeverity(s Severity) Severity {
	switch s {
	case SevCritical, SevMajor, SevMinor, SevDegraded, SevScheduled:
		return s
	}
	return SevMinor
}

func normalizeStatus(s Status) Status {
	switch s {
	case StatusInvestigating, StatusIdentified, StatusMonitoring, StatusResolved, StatusCancelled:
		return s
	}
	return ""
}
