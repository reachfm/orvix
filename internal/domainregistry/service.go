package domainregistry

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// LimitChecker checks license limits before creation.
type LimitChecker interface {
	CanCreateDomain(ctx context.Context) (bool, string)
}

var validDomainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// Service is the central domain registry used by all platform components.
type Service struct {
	repo    *Repository
	limiter LimitChecker
}

// NewService creates a domain registry service.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// SetLimitChecker attaches a license limit checker.
func (s *Service) SetLimitChecker(lc LimitChecker) {
	s.limiter = lc
}

// EnsureSchema creates the backing table.
func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repo.EnsureTable(ctx)
}

// CreateDomain registers a new domain.
func (s *Service) CreateDomain(ctx context.Context, req *CreateDomainRequest) (*Domain, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}
	name := strings.TrimSpace(strings.ToLower(req.Name))
	if err := ValidateDomain(name); err != nil {
		return nil, err
	}
	// Check license limit.
	if s.limiter != nil {
		if ok, msg := s.limiter.CanCreateDomain(ctx); !ok {
			return nil, fmt.Errorf("license limit: %s", msg)
		}
	}
	exists, err := s.repo.Exists(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("check existence: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("domain already exists: %s", name)
	}
	d := &Domain{Name: name, Status: DomainActive}
	if err := s.repo.Create(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// GetDomain returns a domain by ID.
func (s *Service) GetDomain(ctx context.Context, id uint) (*Domain, error) {
	return s.repo.GetByID(ctx, id)
}

// GetByName returns a domain by name.
func (s *Service) GetByName(ctx context.Context, name string) (*Domain, error) {
	return s.repo.GetByName(ctx, strings.ToLower(strings.TrimSpace(name)))
}

// ListDomains returns all domains.
func (s *Service) ListDomains(ctx context.Context) ([]Domain, error) {
	return s.repo.List(ctx)
}

// UpdateDomain updates a domain's fields.
func (s *Service) UpdateDomain(ctx context.Context, id uint, req *UpdateDomainRequest) (*Domain, error) {
	d, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	if d == nil {
		return nil, fmt.Errorf("domain not found")
	}
	if req.Name != nil {
		name := strings.TrimSpace(strings.ToLower(*req.Name))
		if err := ValidateDomain(name); err != nil {
			return nil, err
		}
		exists, err := s.repo.Exists(ctx, name)
		if err != nil {
			return nil, err
		}
		if exists && name != d.Name {
			return nil, fmt.Errorf("domain already exists: %s", name)
		}
		d.Name = name
	}
	if req.Status != nil {
		if !req.Status.IsValid() {
			return nil, fmt.Errorf("invalid status: %s", *req.Status)
		}
		d.Status = *req.Status
	}
	if err := s.repo.Update(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// DeleteDomain removes a domain by ID.
func (s *Service) DeleteDomain(ctx context.Context, id uint) error {
	d, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if d == nil {
		return fmt.Errorf("domain not found")
	}
	return s.repo.Delete(ctx, id)
}

// DomainExists checks if a domain name exists.
func (s *Service) DomainExists(ctx context.Context, name string) (bool, error) {
	return s.repo.Exists(ctx, strings.ToLower(strings.TrimSpace(name)))
}

// ValidateDomain checks whether a domain name is RFC-compliant.
func ValidateDomain(name string) error {
	if name == "" {
		return fmt.Errorf("domain name cannot be empty")
	}
	if len(name) > 255 {
		return fmt.Errorf("domain name too long (max 255 chars)")
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("domain name cannot start or end with a dot")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("domain name cannot contain consecutive dots")
	}
	if strings.ContainsAny(name, " _~!@#$%^&*()+={}[|\\:;\"'<,>?/") {
		return fmt.Errorf("domain name contains invalid characters")
	}
	if !validDomainRE.MatchString(name) {
		return fmt.Errorf("invalid domain name format")
	}
	return nil
}
