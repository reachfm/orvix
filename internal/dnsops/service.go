// Package dnsops top-level Service. The Service wires the Generator,
// the Verifier, and the Provider list together. The handler layer
// uses Service for all admin DNS operations; this is the only type
// the handler imports.
package dnsops

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Service exposes the admin DNS operations as a single dependency.
//
//   - Generate(domain, ...) -> Plan
//   - Verify(plan) -> VerifyReport
//   - PlanProvider(name, plan) -> ChangePlan (dry-run)
//   - ApplyProvider(name, plan, confirm) -> ApplyResult
//   - Providers() -> list of provider names known to the service
//
// The Service is safe for concurrent use.
type Service struct {
	mu        sync.RWMutex
	generator *Generator
	verifier  *Verifier
	providers map[string]Provider
}

// NewService wires a Service with the default Generator, a Verifier
// using r, and the supplied providers. The "manual" provider is
// always added if missing; it never refuses.
func NewService(r Resolver, providers ...Provider) *Service {
	s := &Service{
		generator: NewGenerator(),
		verifier:  NewVerifier(r),
		providers: make(map[string]Provider),
	}
	for _, p := range providers {
		if p == nil {
			continue
		}
		s.providers[p.Name()] = p
	}
	if _, ok := s.providers["manual"]; !ok {
		s.providers["manual"] = newManualProvider(r)
	}
	return s
}

// Generator returns the underlying Generator. Tests use this to
// override the NowFunc.
func (s *Service) Generator() *Generator {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generator
}

// SetGenerator replaces the Generator (tests only).
func (s *Service) SetGenerator(g *Generator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g != nil {
		s.generator = g
	}
}

// Providers returns the sorted list of provider names known to the
// service. The manual provider is always present.
func (s *Service) Providers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.providers))
	for n := range s.providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Provider returns the named provider or nil.
func (s *Service) Provider(name string) Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.providers[name]
}

// Generate produces the desired-state Plan from in. It does not
// perform any DNS lookup.
func (s *Service) Generate(in Inputs) (*Plan, error) {
	s.mu.RLock()
	g := s.generator
	s.mu.RUnlock()
	return g.Generate(in)
}

// Verify runs the Verifier against plan and returns a VerifyReport.
func (s *Service) Verify(ctx context.Context, plan *Plan) (*VerifyReport, error) {
	s.mu.RLock()
	v := s.verifier
	s.mu.RUnlock()
	return v.Verify(ctx, plan)
}

// PlanProvider returns the named provider's dry-run change plan for
// plan.
func (s *Service) PlanProvider(ctx context.Context, name string, plan *Plan) (ChangePlan, error) {
	s.mu.RLock()
	p := s.providers[name]
	s.mu.RUnlock()
	if p == nil {
		return ChangePlan{}, fmt.Errorf("unknown provider %q (known: %s)", name, strings.Join(s.Providers(), ","))
	}
	return p.Plan(ctx, plan)
}

// ApplyProvider calls Apply on the named provider. confirm must be a
// non-empty string the operator typed; providers refuse otherwise.
func (s *Service) ApplyProvider(ctx context.Context, name string, plan ChangePlan, confirm string) (ApplyResult, error) {
	s.mu.RLock()
	p := s.providers[name]
	s.mu.RUnlock()
	if p == nil {
		return ApplyResult{}, fmt.Errorf("unknown provider %q (known: %s)", name, strings.Join(s.Providers(), ","))
	}
	return p.Apply(ctx, plan, confirm)
}
