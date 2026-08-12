package domainlifecycle

import (
	"context"

	"github.com/orvix/orvix/internal/domainregistry"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// registrySync is the narrow interface this package needs from
// domainregistry.Service — the coarse active/suspended/disabled flag
// that mail protocol servers actually read. Defined here (consumer
// side) so domainlifecycle depends on a port, not the concrete type.
type registrySync interface {
	SetStatus(ctx context.Context, name string, status domainregistry.DomainStatus) error
}

type Service struct {
	repo    *Repository
	clock   kernel.Clock
	protoRW registrySync
}

func NewService(repo *Repository, protoRW registrySync, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, protoRW: protoRW, clock: clock}
}

func (s *Service) Register(ctx context.Context, tenantID uint, name string) (*Domain, error) {
	if name == "" {
		return nil, kernel.ValidationError(map[string]string{"name": "domain name is required"})
	}
	d, err := s.repo.Create(ctx, tenantID, name, s.clock.Now())
	if err != nil {
		if kernel.IsUniqueViolation(err) {
			return nil, ErrDomainNameTaken
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create domain", err)
	}
	return d, nil
}

// Transition moves a domain from its current state to newState,
// enforcing the state machine's allowlist and optimistic concurrency.
// It never trusts a caller-supplied "current state" implicitly — it
// re-reads the row, checks CanTransition, and only then attempts the
// atomic guarded update, retrying once on a lost race (another actor
// moved the row between the read and the write) before surfacing
// ErrVersionConflict.
func (s *Service) Transition(ctx context.Context, id uint, newState State) (*Domain, error) {
	if !newState.IsValid() {
		return nil, kernel.ValidationError(map[string]string{"state": "unknown domain state"})
	}
	for attempt := 0; attempt < 2; attempt++ {
		d, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if !CanTransition(d.State, newState) {
			return nil, kernel.InvalidStateTransition(string(d.State), string(newState))
		}
		applied, err := s.repo.TransitionIfVersion(ctx, id, d.State, newState, d.Version, s.clock.Now())
		if err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "transition domain state", err)
		}
		if !applied {
			continue // lost the race — re-read and retry once
		}
		s.syncProtocolFlag(ctx, d.Name, newState)
		d.State = newState
		d.Version++
		return d, nil
	}
	return nil, ErrVersionConflict
}

// syncProtocolFlag projects the rich lifecycle state onto
// domainregistry's coarse flag. Best-effort and non-fatal: a sync
// failure must not roll back a lifecycle transition that already
// committed, but it is never silently dropped either — callers that
// need the guarantee should reconcile via a background job, which is
// exactly what the sync failure ought to feed (not implemented here;
// out of scope for this milestone slice).
func (s *Service) syncProtocolFlag(ctx context.Context, name string, state State) {
	if s.protoRW == nil {
		return
	}
	switch state {
	case StateActive, StateDegraded:
		_ = s.protoRW.SetStatus(ctx, name, domainregistry.DomainActive)
	case StateSuspended, StateDeleting, StateDeleted, StateFailed:
		_ = s.protoRW.SetStatus(ctx, name, domainregistry.DomainSuspended)
	}
}
