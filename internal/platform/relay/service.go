package relay

import (
	"context"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

type Service struct {
	repo    *Repository
	secrets secretCodec
	breaker CircuitBreaker
	clock   kernel.Clock
	outbox  *kernel.OutboxRepository
}

func NewService(repo *Repository, clock kernel.Clock, outbox *kernel.OutboxRepository) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, secrets: configSecretCodec{}, breaker: NewCircuitBreaker(5, 60*time.Second), clock: clock, outbox: outbox}
}

// WithSecretCodec overrides the secret codec — used by tests to avoid
// depending on the real on-disk encryption key.
func (s *Service) WithSecretCodec(c secretCodec) *Service {
	s.secrets = c
	return s
}

// WithCircuitBreaker overrides the breaker's threshold/cooldown —
// used by tests that need a fast cooldown instead of the 60s default.
func (s *Service) WithCircuitBreaker(cb CircuitBreaker) *Service {
	s.breaker = cb
	return s
}

// CreatePool creates a routing pool.
func (s *Service) CreatePool(ctx context.Context, p Pool) (*Pool, error) {
	now := s.clock.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.Strategy == "" {
		p.Strategy = StrategyPriority
	}
	if err := s.repo.CreatePool(ctx, &p); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create relay pool", err)
	}
	return &p, nil
}

// CreateProvider creates a provider, encrypting its credential via
// the secret-reference mechanism before it ever reaches the database
// — the caller-supplied plaintext password never gets assigned to
// p.SecretRef directly; it exists only as the `password` parameter
// and is discarded after Encrypt returns.
func (s *Service) CreateProvider(ctx context.Context, p Provider, password string) (*Provider, error) {
	if !p.ConnSecurity.IsValid() {
		return nil, ErrInvalidConnSecurity
	}
	if password != "" {
		ref, err := s.secrets.Encrypt(password)
		if err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "encrypt relay credential", err)
		}
		p.SecretRef = ref
	}
	now := s.clock.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	p.CircuitState = CircuitClosed
	if err := s.repo.CreateProvider(ctx, &p); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create relay provider", err)
	}
	return &p, nil
}

// ListProvidersRedacted returns a pool's providers with credentials
// stripped — the only form this service ever returns to an API caller
// or audit record.
func (s *Service) ListProvidersRedacted(ctx context.Context, poolID uint) ([]RedactedProvider, error) {
	ps, err := s.repo.ListProvidersByPool(ctx, poolID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list relay providers", err)
	}
	return RedactAll(ps), nil
}

// decryptCredential is the ONLY place in this package a plaintext
// credential exists — called exclusively by the actual outbound dial
// path (dial.go), never by any read/list/get API.
func (s *Service) decryptCredential(p Provider) (string, error) {
	if p.SecretRef == "" {
		return "", nil
	}
	return s.secrets.Decrypt(p.SecretRef)
}

func (s *Service) CreateRoutingRule(ctx context.Context, rule RoutingRule) (*RoutingRule, error) {
	rule.CreatedAt = s.clock.Now()
	if err := s.repo.CreateRoutingRule(ctx, &rule); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create routing rule", err)
	}
	return &rule, nil
}

// SetEmergencyOverride forces all matching traffic through poolID for
// a bounded window. expiresAt must be in the future; the override
// expires automatically (ActiveOverride never returns an
// expired-but-still-flagged row) — no background sweep is required
// for correctness, though ExpireOverrides exists to keep the table
// tidy.
func (s *Service) SetEmergencyOverride(ctx context.Context, tenantID, poolID, actorID uint, reason string, expiresAt time.Time) (*EmergencyOverride, error) {
	if reason == "" {
		return nil, kernel.ValidationError(map[string]string{"reason": "a reason is required for an emergency override"})
	}
	now := s.clock.Now()
	if !expiresAt.After(now) {
		return nil, kernel.ValidationError(map[string]string{"expires_at": "must be in the future"})
	}
	o := EmergencyOverride{TenantID: tenantID, PoolID: poolID, Reason: reason, ActorID: actorID, ExpiresAt: expiresAt, CreatedAt: now}
	if err := s.repo.CreateOverride(ctx, &o); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create emergency override", err)
	}
	return &o, nil
}

// RouteRequest is everything SelectRoute needs to resolve a route,
// carried explicitly rather than reached for via package-global
// state, so the selection is a pure function of its inputs (modulo
// the DB reads for pool/provider/circuit state).
type RouteRequest struct {
	TenantID        uint
	DomainID        uint
	SenderAddress   string
	RecipientDomain string
	Classification  string
	// SenderMailAccessMode, if MailAccessInternalOnly, means the
	// sending domain is restricted to local delivery only — SelectRoute
	// must NEVER return a relay/direct route in that case; the caller
	// (delivery worker) is expected to have already rejected this at
	// SMTP accept time, but this is enforced here too as defense in
	// depth, matching the requirement that internal-only identities
	// must never bypass their restriction through a relay.
	SenderMailAccessMode string
	// Seed drives deterministic weighted selection (e.g. the queue
	// entry ID) — same message resolves to the same primary provider
	// across retries.
	Seed int64
}

// SelectRoute resolves, in order: mail_access_mode policy (fails
// closed) -> active emergency override -> most-specific matching
// routing rule (domain > tenant > global) -> the resolved pool's
// direct-vs-relay policy -> (if relay) provider selection within the
// pool, filtering circuit-open and rate-limited providers, returning
// a primary plus fallback chain. Never returns a credential.
func (s *Service) SelectRoute(ctx context.Context, req RouteRequest) (*SelectedRoute, error) {
	if req.SenderMailAccessMode == "internal_only" {
		return nil, ErrPolicyBlocked
	}

	now := s.clock.Now()
	var poolID uint
	if override, err := s.repo.ActiveOverride(ctx, req.TenantID, now); err == nil && override != nil {
		poolID = override.PoolID
	} else {
		rules, err := s.repo.ListRoutingRules(ctx, req.TenantID, req.DomainID)
		if err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "list routing rules", err)
		}
		for _, rule := range rules {
			if ruleMatches(rule, req) {
				poolID = rule.PoolID
				break
			}
		}
	}
	if poolID == 0 {
		// No rule and no override matched — default is direct
		// delivery (no relay), preserving the platform's existing
		// direct-to-MX behavior for any tenant that hasn't configured
		// relay routing at all.
		return &SelectedRoute{Direct: true}, nil
	}

	pool, err := s.repo.GetPool(ctx, poolID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get relay pool", err)
	}
	if pool == nil {
		return nil, ErrPoolNotFound
	}
	if pool.DirectOnly {
		return &SelectedRoute{PoolID: pool.ID, Direct: true}, nil
	}

	providers, err := s.repo.ListProvidersByPool(ctx, pool.ID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list relay providers", err)
	}
	var candidates []availableProvider
	for _, p := range providers {
		resolvedState, avail := s.breaker.IsAvailable(p.CircuitState, p.CircuitOpenedAt, now)
		if resolvedState != p.CircuitState {
			// Lazily transition open->half_open on read so a stale
			// "open" row doesn't block selection forever without a
			// background sweep.
			_ = s.repo.UpdateProviderCircuit(ctx, p.ID, resolvedState, p.CircuitFailures, p.CircuitOpenedAt, now)
			p.CircuitState = resolvedState
		}
		if avail && p.RateLimitPerMin > 0 {
			window := now.Truncate(time.Minute)
			ok, rerr := s.repo.IncrementAndCheck(ctx, p.ID, window, p.RateLimitPerMin)
			if rerr == nil && !ok {
				avail = false
			}
		}
		candidates = append(candidates, availableProvider{Provider: p, Available: avail})
	}

	ordered, ok := selectFromPool(candidates, pool.Strategy, req.Seed)
	if !ok {
		return nil, ErrNoRouteAvailable
	}

	routes := make([]SelectedRoute, 0, len(ordered))
	for _, p := range ordered {
		routes = append(routes, SelectedRoute{
			PoolID: pool.ID, ProviderID: p.ID, ProviderName: p.Name,
			Host: p.Host, Port: p.Port, ConnSecurity: p.ConnSecurity,
			TLSStrict: p.TLSValidation == TLSValidationStrict,
		})
	}
	primary := routes[0]
	primary.Fallbacks = routes[1:]
	return &primary, nil
}

func ruleMatches(rule RoutingRule, req RouteRequest) bool {
	if rule.DomainID != 0 && rule.DomainID != req.DomainID {
		return false
	}
	if rule.DomainID == 0 && rule.TenantID != 0 && rule.TenantID != req.TenantID {
		return false
	}
	if rule.RecipientDomain != "" && rule.RecipientDomain != req.RecipientDomain {
		return false
	}
	if rule.Classification != "" && rule.Classification != req.Classification {
		return false
	}
	return true
}

// RecordAttemptResult updates a provider's circuit breaker state
// after a real delivery attempt through it and, when the outcome
// changes the state, enqueues an audit-visible outbox event — the
// circuit tripping is an operationally significant event, not silent.
func (s *Service) RecordAttemptResult(ctx context.Context, providerID uint, success bool) error {
	p, err := s.repo.GetProvider(ctx, providerID)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "get relay provider", err)
	}
	if p == nil {
		return ErrProviderNotFound
	}
	now := s.clock.Now()
	var nextState CircuitState
	var failures int
	var openedAt *time.Time
	if success {
		nextState, failures = s.breaker.OnSuccess()
	} else {
		nextState, failures, openedAt = s.breaker.OnFailure(p.CircuitState, p.CircuitFailures, now)
	}
	if err := s.repo.UpdateProviderCircuit(ctx, providerID, nextState, failures, openedAt, now); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "update relay circuit state", err)
	}
	if nextState != p.CircuitState && s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, s.repo.db, "relay.circuit.transition", fmt.Sprintf("%d", providerID), map[string]any{
			"from": p.CircuitState, "to": nextState, "provider": p.Name,
		}, now)
	}
	return nil
}
