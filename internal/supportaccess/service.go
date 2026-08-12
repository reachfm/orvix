package supportaccess

import (
	"context"
	"time"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

func (s *Service) EnsureSchema(ctx context.Context) error { return s.repo.EnsureSchema(ctx) }

func (s *Service) RequestGrant(ctx context.Context, ticketRef, reason string, targetTenantID, grantedByID uint, scope string, duration time.Duration, emergency bool) (*AccessGrant, error) {
	if ticketRef == "" || reason == "" {
		return nil, &saError{"ticket reference and reason are required"}
	}
	if !ValidScopes[scope] {
		return nil, &saError{"invalid permission scope"}
	}
	if duration <= 0 || duration > 72*time.Hour {
		duration = 4 * time.Hour
	}
	existing, _ := s.repo.FindActiveForTenant(ctx, targetTenantID)
	if existing != nil {
		return nil, ErrAlreadyActive
	}
	g := &AccessGrant{
		TicketRef:           ticketRef,
		Reason:              reason,
		TargetTenantID:      targetTenantID,
		GrantedByID:         grantedByID,
		PermissionScope:     scope,
		Status:              StatusRequested,
		ExpiresAt:           time.Now().UTC().Add(duration),
		EmergencyBreakGlass: emergency,
	}
	if err := s.repo.Insert(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Service) ApproveGrant(ctx context.Context, id uint, operatorID uint) (*AccessGrant, error) {
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if g.Status != StatusRequested {
		return nil, &saError{"grant is not in requested state"}
	}
	g.Status = StatusApproved
	if err := s.repo.Update(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Service) ActivateGrant(ctx context.Context, id uint) (*AccessGrant, error) {
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if g.Status != StatusApproved {
		return nil, &saError{"grant must be approved before activation"}
	}
	if time.Now().UTC().After(g.ExpiresAt) {
		g.Status = StatusExpired
		_ = s.repo.Update(ctx, g)
		return nil, ErrExpired
	}
	now := time.Now().UTC()
	g.Status = StatusActive
	g.ActivatedAt = &now
	if err := s.repo.Update(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Service) RevokeGrant(ctx context.Context, id uint, reason string) (*AccessGrant, error) {
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if g.Status == StatusRevoked || g.Status == StatusExpired {
		return nil, &saError{"grant is already revoked or expired"}
	}
	now := time.Now().UTC()
	g.Status = StatusRevoked
	g.RevokedAt = &now
	g.RevokeReason = reason
	if err := s.repo.Update(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Service) Get(ctx context.Context, id uint) (*AccessGrant, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, tenantID uint, limit int) ([]AccessGrant, error) {
	return s.repo.List(ctx, tenantID, limit)
}

func (s *Service) ValidateAccess(ctx context.Context, tenantID uint) error {
	g, err := s.repo.FindActiveForTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	if g == nil {
		return &saError{"no active support access grant for this tenant"}
	}
	if time.Now().UTC().After(g.ExpiresAt) {
		g.Status = StatusExpired
		_ = s.repo.Update(ctx, g)
		return ErrExpired
	}
	if g.Status != StatusActive {
		return ErrRevoked
	}
	return nil
}

// GrantForOperator returns the active support grant for a given operator
// and target tenant, or nil if none exists.
func (s *Service) GrantForOperator(ctx context.Context, operatorID, tenantID uint) (*AccessGrant, error) {
	return s.repo.FindGrantByOperator(ctx, operatorID, tenantID)
}

// Scopes returns the effective scopes for a grant based on its
// permission_scope. Higher privilege scopes include the permissions
// of lower scopes.
func (g *AccessGrant) Scopes() []string {
	switch g.PermissionScope {
	case "full_tenant_view":
		return []string{"read_only", "mailbox_view", "domain_view", "full_tenant_view"}
	case "domain_view":
		return []string{"read_only", "mailbox_view", "domain_view"}
	case "mailbox_view":
		return []string{"read_only", "mailbox_view"}
	case "read_only":
		return []string{"read_only"}
	default:
		return []string{}
	}
}
