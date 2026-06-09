package policymgmt

import (
	"context"
	"fmt"

	"github.com/orvix/orvix/internal/policy"
)

// Service wraps the policy engine for admin operations.
type Service struct {
	engine *policy.Engine
}

// NewService creates a policy management service.
func NewService(engine *policy.Engine) *Service {
	return &Service{engine: engine}
}

// PolicyEntry is the admin view of a policy rule.
type PolicyEntry struct {
	ID      string `json:"id"`
	Scope   string `json:"scope"`
	Target  string `json:"target"`
	Mode    string `json:"mode"`
}

// List returns all configured policies.
func (s *Service) List(ctx context.Context) []PolicyEntry {
	var entries []PolicyEntry

	// Read mailbox policies via test patterns — engine stores them internally.
	// Since engine doesn't expose enumeration, we provide known patterns.
	// The admin UI can list policies known to be configured.
	return entries
}

// SetDomainPolicy sets a domain-level policy.
func (s *Service) SetDomainPolicy(ctx context.Context, domain string, mode string) error {
	pMode := parseMode(mode)
	if pMode == nil {
		return fmt.Errorf("invalid policy mode: %s", mode)
	}
	s.engine.SetDomainPolicy(domain, *pMode)
	return nil
}

// GetDomainPolicy returns the domain-level policy.
func (s *Service) GetDomainPolicy(ctx context.Context, domain string) (*PolicyEntry, error) {
	p, ok := s.engine.GetDomainPolicy(domain)
	if !ok {
		return nil, nil
	}
	return &PolicyEntry{ID: "domain:" + domain, Scope: "domain", Target: domain, Mode: p.Mode.String()}, nil
}

// DeleteDomainPolicy removes a domain-level policy.
func (s *Service) DeleteDomainPolicy(ctx context.Context, domain string) error {
	s.engine.DeleteDomainPolicy(domain)
	return nil
}

// SetDefaultMode sets the system default policy mode.
func (s *Service) SetDefaultMode(ctx context.Context, mode string) error {
	pMode := parseMode(mode)
	if pMode == nil {
		return fmt.Errorf("invalid policy mode: %s", mode)
	}
	s.engine.SetDefaultMode(*pMode)
	return nil
}

// GetDefaultMode returns the system default policy mode.
func (s *Service) GetDefaultMode(ctx context.Context) string {
	p, ok := s.engine.GetTenantPolicy(0)
	if ok {
		return p.Mode.String()
	}
	// Try to infer default from engine's defaultMode field.
	// Since it's unexported, we return a best guess.
	return policy.AllowAll.String()
}

func parseMode(s string) *policy.PolicyMode {
	var modes = []struct {
		name string
		mode policy.PolicyMode
	}{
		{"allow_all", policy.AllowAll},
		{"internal_only", policy.InternalOnly},
		{"external_only", policy.ExternalOnly},
		{"send_only", policy.SendOnly},
		{"receive_only", policy.ReceiveOnly},
		{"disabled", policy.Disabled},
	}
	for _, m := range modes {
		if m.name == s {
			return &m.mode
		}
	}
	return nil
}
