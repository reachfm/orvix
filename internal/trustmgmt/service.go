package trustmgmt

import (
	"context"
	"fmt"

	"github.com/orvix/orvix/internal/trust"
)

// Service wraps the trust engine for admin operations.
type Service struct {
	engine *trust.Engine
}

// NewService creates a trust management service.
func NewService(engine *trust.Engine) *Service {
	return &Service{engine: engine}
}

// Summary returns aggregate trust metrics.
func (s *Service) Summary(ctx context.Context) *trust.Snapshot {
	snap := s.engine.Snapshot()
	return &snap
}

// ListLockouts returns all current lockouts.
func (s *Service) ListLockouts(ctx context.Context) []trust.LockoutEntry {
	return s.engine.LockoutList()
}

// ClearLockout removes a specific lockout.
func (s *Service) ClearLockout(ctx context.Context, key string) error {
	if s.engine.ClearLockout(key) {
		return nil
	}
	return fmt.Errorf("lockout not found: %s", key)
}

// RecordAuth records an authentication event (success or failure).
func (s *Service) RecordAuth(ctx context.Context, email, ip string, success bool) {
	if success {
		s.engine.RecordAuthSuccess(ip)
	} else {
		s.engine.RecordAuthFailure(ip)
	}
}

// IsLockedOut returns true if the given key (ip or email) is currently locked out.
func (s *Service) IsLockedOut(ctx context.Context, key string) bool {
	return s.engine.IsLockedOut(key)
}
