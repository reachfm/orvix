package incident

import (
	"context"
	"time"
)

// Service is the incident management service.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, title, description string, severity Severity, services, regions []string) (*Incident, error) {
	if title == "" {
		return nil, ErrTitleRequired
	}
	if severity != SevCritical && severity != SevMajor && severity != SevMinor && severity != SevDegraded && severity != SevScheduled {
		severity = SevMinor
	}
	inc := &Incident{
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      StatusInvestigating,
		Services:    services,
		Regions:     regions,
	}
	if err := s.repo.Insert(ctx, inc); err != nil {
		return nil, err
	}
	return inc, nil
}

func (s *Service) Update(ctx context.Context, id uint, status Status, message, operator string) (*Incident, error) {
	inc, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if status != "" {
		inc.Status = status
		if status == StatusResolved || status == StatusCancelled {
			now := time.Now().UTC()
			inc.ResolvedAt = &now
		}
	}
	if err := s.repo.Update(ctx, inc); err != nil {
		return nil, err
	}
	ev := &TimelineEvent{
		IncidentID: inc.ID,
		Status:     inc.Status,
		Message:    message,
		Operator:   operator,
	}
	_ = s.repo.AddTimelineEvent(ctx, ev)
	return inc, nil
}

func (s *Service) Get(ctx context.Context, id uint) (*Incident, error) {
	inc, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	return inc, nil
}

func (s *Service) List(ctx context.Context, status string, limit int) ([]Incident, error) {
	return s.repo.List(ctx, status, limit)
}

func (s *Service) Timeline(ctx context.Context, id uint) ([]TimelineEvent, error) {
	return s.repo.Timeline(ctx, id)
}

func (s *Service) PublicStatus(ctx context.Context) (*PublicStatus, error) {
	return s.repo.PublicStatus(ctx)
}

// EnsureSchema creates the incident tables.
func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repo.EnsureSchema(ctx)
}

var (
	ErrNotFound      = &incidentError{"incident not found"}
	ErrTitleRequired = &incidentError{"title is required"}
)

type incidentError struct{ msg string }

func (e *incidentError) Error() string { return e.msg }
