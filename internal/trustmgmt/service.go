package trustmgmt

import (
	"context"
	"fmt"
	"strings"

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
//
// H-6: this previously keyed on the IP alone and silently discarded the email,
// so a botnet spraying one account never accumulated failures anywhere and the
// lockout engine could not see the attack. It now records against the full key
// set (see AuthKeys).
func (s *Service) RecordAuth(ctx context.Context, email, ip string, success bool) {
	keys := AuthKeys(email, ip)
	if success {
		s.RecordAuthSuccessFor(ctx, keys)
	} else {
		s.RecordAuthFailureFor(ctx, keys)
	}
}

// IsLockedOut returns true if the given key (ip or email) is currently locked out.
func (s *Service) IsLockedOut(ctx context.Context, key string) bool {
	return s.engine.IsLockedOut(key)
}

// LockoutKeys is the canonical key set for one authentication attempt.
type LockoutKeys struct {
	// Account is the normalized account identifier, independent of source.
	Account string
	// IP is the client address, independent of which account was targeted.
	IP string
	// Combo is the (account, ip) pair.
	Combo string
}

// All returns the keys in a stable order.
func (k LockoutKeys) All() []string {
	out := make([]string, 0, 3)
	if k.Account != "" {
		out = append(out, k.Account)
	}
	if k.IP != "" {
		out = append(out, k.IP)
	}
	if k.Combo != "" {
		out = append(out, k.Combo)
	}
	return out
}

// AuthKeys derives the canonical lockout keys for an attempt.
//
// The namespaces ("auth:account:", "auth:ip:", "auth:combo:") keep these
// distinct from the SMTP/submission keys the same engine tracks, so a mail
// lockout can never be confused with a console lockout. The account
// identifier is normalized with the same rule the rate limiter uses, so both
// controls agree on what "the same account" means.
func AuthKeys(email, ip string) LockoutKeys {
	account := normalizeAccount(email)
	keys := LockoutKeys{}
	if account != "" {
		keys.Account = "auth:account:" + account
	}
	if ip != "" {
		keys.IP = "auth:ip:" + ip
	}
	if account != "" && ip != "" {
		keys.Combo = "auth:combo:" + ip + "|" + account
	}
	return keys
}

// RecordAuthFailureFor records a failed attempt against every supplied key.
// Returns true when any key is now locked out.
func (s *Service) RecordAuthFailureFor(ctx context.Context, keys LockoutKeys) bool {
	locked := false
	for _, key := range keys.All() {
		if lockedOut, _ := s.engine.RecordAuthFailure(key); lockedOut {
			locked = true
		}
	}
	return locked
}

// RecordAuthSuccessFor clears failure state after a genuine success.
//
// The account and combo keys are cleared; the IP key is deliberately NOT.
// An attacker holding one valid account on a shared address would otherwise
// authenticate periodically to wipe the IP counter and keep spraying other
// accounts from the same host indefinitely.
func (s *Service) RecordAuthSuccessFor(ctx context.Context, keys LockoutKeys) {
	if keys.Account != "" {
		s.engine.RecordAuthSuccess(keys.Account)
	}
	if keys.Combo != "" {
		s.engine.RecordAuthSuccess(keys.Combo)
	}
}

// IsAnyLockedOut reports whether any key in the set is currently locked out.
func (s *Service) IsAnyLockedOut(ctx context.Context, keys LockoutKeys) bool {
	for _, key := range keys.All() {
		if s.engine.IsLockedOut(key) {
			return true
		}
	}
	return false
}

// normalizeAccount mirrors auth.NormalizeAccount. It is duplicated as an
// unexported helper only to avoid an import cycle (internal/auth imports
// nothing from trustmgmt, but the console handlers import both); the rule must
// stay identical to auth.NormalizeAccount — see its doc comment.
func normalizeAccount(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
