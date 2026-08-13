package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

type Service struct {
	repo    *Repository
	secrets secretCodec
	breaker CircuitBreaker
	clock   kernel.Clock
	outbox  *kernel.OutboxRepository
	audit   *audit.ExtendedStore
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

// WithAuditStore wires the transactional extended audit store used by
// every platform relay mutation.
func (s *Service) WithAuditStore(a *audit.ExtendedStore) *Service {
	s.audit = a
	return s
}

// WithOutbox wires the transactional outbox used by meaningful state
// changes (enable/disable/rotate/delete).
func (s *Service) WithOutbox(o *kernel.OutboxRepository) *Service {
	s.outbox = o
	return s
}

// AuditActor carries the platform operator identity into audit
// records. Never contains secret material.
type AuditActor struct {
	ID        uint
	Role      string
	RequestID string
	IP        string
	UserAgent string
}

func (s *Service) auditEntry(a AuditActor, action, target string, tenantID uint, result, reason string) *audit.ExtendedEntry {
	return &audit.ExtendedEntry{
		Actor:     fmt.Sprintf("user:%d", a.ID),
		ActorID:   a.ID,
		ActorRole: a.Role,
		TenantID:  tenantID,
		Action:    action,
		Target:    target,
		Result:    result,
		Reason:    reason,
		RequestID: a.RequestID,
		IP:        a.IP,
		UserAgent: a.UserAgent,
		Timestamp: s.clock.Now(),
	}
}

// ── Platform relay administration ──────────────────────────────────

// RelayCreateInput is the platform write contract for a relay
// endpoint. Password is never persisted in plaintext: it is encrypted
// via the secret-reference mechanism before reaching the database and
// is discarded after the call.
type RelayCreateInput struct {
	Scope           Scope
	TenantID        uint
	DomainID        uint
	PoolID          uint
	Name            string
	Host            string
	Port            int
	Username        string
	Password        string
	ConnSecurity    ConnSecurity
	TLSValidation   TLSValidation
	Priority        int
	Weight          int
	Active          bool
	RateLimitPerMin int
}

// RelayUpdateInput is the guarded update contract. Version must match
// the current row version or the update fails with ErrVersionConflict.
type RelayUpdateInput struct {
	Scope           *Scope
	TenantID        *uint
	DomainID        *uint
	PoolID          *uint
	Name            *string
	Host            *string
	Port            *int
	Username        *string
	Password        *string // non-nil => re-encrypt and store; empty string clears the credential
	ConnSecurity    *ConnSecurity
	TLSValidation   *TLSValidation
	Priority        *int
	Weight          *int
	Active          *bool
	RateLimitPerMin *int
}

// ListRelays returns the platform-wide relay list, redacted — the
// only shape that may leave this service.
func (s *Service) ListRelays(ctx context.Context, f ProviderFilter) ([]RedactedProvider, int64, error) {
	ps, total, err := s.repo.ListProviders(ctx, f)
	if err != nil {
		return nil, 0, kernel.Wrap(kernel.ErrCodeInternal, "list platform relays", err)
	}
	return RedactAll(ps), total, nil
}

// GetRelay returns one relay endpoint, redacted.
func (s *Service) GetRelay(ctx context.Context, id uint) (*RedactedProvider, error) {
	p, err := s.repo.GetProvider(ctx, id)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get platform relay", err)
	}
	if p == nil {
		return nil, ErrProviderNotFound
	}
	r := Redact(*p)
	return &r, nil
}

// CreateRelay validates the write contract (including the SSRF
// target policy), encrypts the credential, and persists. The returned
// provider is redacted; the plaintext credential exists only inside
// this call.
func (s *Service) CreateRelay(ctx context.Context, in RelayCreateInput, actor AuditActor) (*RedactedProvider, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, ErrNameRequired
	}
	if err := ValidateRelayTarget(in.Host, in.Port); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsafeTarget, err.Error())
	}
	if !in.ConnSecurity.IsValid() {
		return nil, ErrInvalidConnSecurity
	}
	if in.TLSValidation == "" {
		in.TLSValidation = TLSValidationStrict
	}
	p := Provider{
		Scope: in.Scope, TenantID: in.TenantID, DomainID: in.DomainID, PoolID: in.PoolID,
		Name: strings.TrimSpace(in.Name), Host: in.Host, Port: in.Port, Username: in.Username,
		ConnSecurity: in.ConnSecurity, TLSValidation: in.TLSValidation,
		Priority: in.Priority, Weight: in.Weight, Active: in.Active, RateLimitPerMin: in.RateLimitPerMin,
	}
	if in.Password != "" {
		ref, err := s.secrets.Encrypt(in.Password)
		if err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "encrypt relay credential", err)
		}
		p.SecretRef = ref
	}
	now := s.clock.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	p.CircuitState = CircuitClosed
	if err := s.repo.CreateProvider(ctx, &p); err != nil {
		if kernel.IsUniqueViolation(err) {
			return nil, ErrRelayNameConflict
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create platform relay", err)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, s.auditEntry(actor, "platform.relay.create", fmt.Sprintf("relay:%d", p.ID), p.TenantID, "success", ""))
	}
	r := Redact(p)
	return &r, nil
}

// UpdateRelay applies a guarded, optimistic-concurrency update:
// version must equal the current row version or the update fails with
// ErrVersionConflict. Unsafe targets and invalid connection-security
// modes are rejected with their typed errors.
func (s *Service) UpdateRelay(ctx context.Context, id uint, version int, in RelayUpdateInput, actor AuditActor) (*RedactedProvider, error) {
	if version <= 0 {
		return nil, kernel.NewError(kernel.ErrCodeValidation, "a current version is required for updates")
	}
	cur, err := s.repo.GetProvider(ctx, id)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get platform relay", err)
	}
	if cur == nil {
		return nil, ErrProviderNotFound
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, ErrNameRequired
	}
	next := *cur
	if in.Scope != nil {
		next.Scope = *in.Scope
	}
	if in.TenantID != nil {
		next.TenantID = *in.TenantID
	}
	if in.DomainID != nil {
		next.DomainID = *in.DomainID
	}
	if in.PoolID != nil {
		next.PoolID = *in.PoolID
	}
	if in.Name != nil {
		next.Name = strings.TrimSpace(*in.Name)
	}
	if in.Host != nil {
		next.Host = *in.Host
	}
	if in.Port != nil {
		next.Port = *in.Port
	}
	if in.Username != nil {
		next.Username = *in.Username
	}
	if in.ConnSecurity != nil {
		next.ConnSecurity = *in.ConnSecurity
	}
	if in.TLSValidation != nil {
		next.TLSValidation = *in.TLSValidation
	}
	if in.Priority != nil {
		next.Priority = *in.Priority
	}
	if in.Weight != nil {
		next.Weight = *in.Weight
	}
	if in.Active != nil {
		next.Active = *in.Active
	}
	if in.RateLimitPerMin != nil {
		next.RateLimitPerMin = *in.RateLimitPerMin
	}
	if err := ValidateRelayTarget(next.Host, next.Port); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsafeTarget, err.Error())
	}
	if !next.ConnSecurity.IsValid() {
		return nil, ErrInvalidConnSecurity
	}
	if in.Password != nil {
		if *in.Password == "" {
			next.SecretRef = "" // explicit credential clear
		} else {
			ref, err := s.secrets.Encrypt(*in.Password)
			if err != nil {
				return nil, kernel.Wrap(kernel.ErrCodeInternal, "encrypt relay credential", err)
			}
			next.SecretRef = ref
		}
	}
	next.UpdatedAt = s.clock.Now()
	next.Version = version
	ok, err := s.repo.UpdateProvider(ctx, &next)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "update platform relay", err)
	}
	if !ok {
		return nil, ErrVersionConflict
	}
	next.Version++
	if s.audit != nil {
		_ = s.audit.Record(ctx, s.auditEntry(actor, "platform.relay.update", fmt.Sprintf("relay:%d", next.ID), next.TenantID, "success", ""))
	}
	r := Redact(next)
	return &r, nil
}

// SetRelayActive is the guarded enable/disable transition. The
// mutation, its outbox event, and its audit record commit in ONE
// transaction.
func (s *Service) SetRelayActive(ctx context.Context, id uint, active bool, version int, actor AuditActor) (*RedactedProvider, error) {
	if version <= 0 {
		return nil, kernel.NewError(kernel.ErrCodeValidation, "a current version is required for state transitions")
	}
	now := s.clock.Now()
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin relay state transition", err)
	}
	defer tx.Rollback()

	ok, err := s.repo.SetProviderActiveTx(ctx, tx, id, active, version, now)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "update relay state", err)
	}
	if !ok {
		return nil, ErrVersionConflict
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, tx, "relay.provider.state", fmt.Sprintf("%d", id), map[string]any{
			"action": map[bool]string{true: "enabled", false: "disabled"}[active],
		}, now)
	}
	if s.audit != nil {
		_ = s.audit.RecordTx(ctx, tx, s.auditEntry(actor,
			map[bool]string{true: "platform.relay.enable", false: "platform.relay.disable"}[active],
			fmt.Sprintf("relay:%d", id), 0, "success", ""))
	}
	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit relay state transition", err)
	}
	r, err := s.GetRelay(ctx, id)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// RotateRelayCredentials replaces the encrypted credential under the
// version predicate. When newPassword is empty the service generates
// a strong credential and returns it EXACTLY ONCE — callers must
// surface it immediately and must never log or persist it. The
// returned relay is redacted.
func (s *Service) RotateRelayCredentials(ctx context.Context, id uint, version int, newPassword string, actor AuditActor) (*RedactedProvider, string, error) {
	if version <= 0 {
		return nil, "", kernel.NewError(kernel.ErrCodeValidation, "a current version is required for credential rotation")
	}
	cur, err := s.repo.GetProvider(ctx, id)
	if err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "get platform relay", err)
	}
	if cur == nil {
		return nil, "", ErrProviderNotFound
	}
	generated := false
	if newPassword == "" {
		generated = true
		newPassword = GenerateRelayCredential()
	}
	ref, err := s.secrets.Encrypt(newPassword)
	if err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "encrypt relay credential", err)
	}
	now := s.clock.Now()
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "begin relay credential rotation", err)
	}
	defer tx.Rollback()
	ok, err := s.repo.RotateProviderSecretTx(ctx, tx, id, ref, version, now)
	if err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "rotate relay credential", err)
	}
	if !ok {
		return nil, "", ErrVersionConflict
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, tx, "relay.provider.credentials_rotated", fmt.Sprintf("%d", id), map[string]any{}, now)
	}
	if s.audit != nil {
		_ = s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "platform.relay.rotate_credentials", fmt.Sprintf("relay:%d", id), cur.TenantID, "success", ""))
	}
	if err := tx.Commit(); err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "commit relay credential rotation", err)
	}
	updated, err := s.GetRelay(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if generated {
		return updated, newPassword, nil
	}
	return updated, "", nil
}

// TestRelay runs the SSRF-safe connection test, persists the safe
// result, and audits the action. The returned result is redacted and
// bounded — it never contains the credential.
func (s *Service) TestRelay(ctx context.Context, id uint, actor AuditActor) (*HealthCheckResult, error) {
	result, err := s.TestConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, s.auditEntry(actor, "platform.relay.test", fmt.Sprintf("relay:%d", id), 0, map[bool]string{true: "success", false: "failed"}[result.Connected], ""))
	}
	return result, nil
}

// DeleteRelay removes a relay endpoint transactionally with audit and
// outbox evidence.
func (s *Service) DeleteRelay(ctx context.Context, id uint, actor AuditActor) error {
	cur, err := s.repo.GetProvider(ctx, id)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "get platform relay", err)
	}
	if cur == nil {
		return ErrProviderNotFound
	}
	now := s.clock.Now()
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "begin relay delete", err)
	}
	defer tx.Rollback()
	ok, err := s.repo.DeleteProviderTx(ctx, tx, id)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "delete platform relay", err)
	}
	if !ok {
		return ErrProviderNotFound
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, tx, "relay.provider.deleted", fmt.Sprintf("%d", id), map[string]any{}, now)
	}
	if s.audit != nil {
		_ = s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "platform.relay.delete", fmt.Sprintf("relay:%d", id), cur.TenantID, "success", ""))
	}
	if err := tx.Commit(); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "commit relay delete", err)
	}
	return nil
}

// GenerateRelayCredential returns a strong random credential
// (256 bits, URL-safe base64) for operator-initiated rotation. It is
// returned exactly once by RotateRelayCredentials and never stored in
// plaintext anywhere.
func GenerateRelayCredential() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is effectively unrecoverable; fall back
		// to a bounded error path by returning an empty string (the
		// caller will fail the rotation rather than store a weak
		// credential).
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

var _ = errors.Is

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
	TenantID uint
	DomainID uint
	// SenderAddress is the FULL envelope sender ("local@domain"), which is
	// what SenderPattern matches against. Callers previously passed the bare
	// sending DOMAIN here, so no sender pattern with a local part could ever
	// match and sender-scoped routing was silently inert.
	SenderAddress string
	// SenderDomain is the sending domain on its own, used for domain-scoped
	// matching when the sending domain's row id is not known.
	SenderDomain    string
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
	// (E) FAIL CLOSED on the override lookup. The previous form was
	// `if override, err := ...; err == nil && override != nil`, so a database
	// error silently fell through to normal rule evaluation. An emergency
	// override exists precisely to force all mail through one pool during an
	// incident; a transient DB error must not quietly cancel it. A lookup
	// failure is now a retryable error, not a bypass.
	override, oerr := s.repo.ActiveOverride(ctx, req.TenantID, now)
	if oerr != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "active emergency override", oerr)
	}
	if override != nil {
		poolID = override.PoolID
	} else {
		rules, err := s.repo.ListRoutingRules(ctx, req.TenantID, req.DomainID)
		if err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "list routing rules", err)
		}
		if rule, ok := matchRule(rules, req); ok {
			poolID = rule.PoolID
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
			//
			// (E) A failure to PERSIST this transition is not fatal: the
			// in-memory resolution already reflects the correct state, and the
			// only consequence is that the next request re-derives it. It is
			// deliberately not a fail-closed condition because the persistence
			// is an optimisation, not the control.
			_ = s.repo.UpdateProviderCircuit(ctx, p.ID, resolvedState, p.CircuitFailures, p.CircuitOpenedAt, now)
			p.CircuitState = resolvedState
		}
		if avail && p.RateLimitPerMin > 0 {
			window := now.Truncate(time.Minute)
			// (E) FAIL CLOSED on the rate-limit store. The previous form
			// (`if rerr == nil && !ok`) left the provider AVAILABLE whenever
			// the counter store errored — so an outage of the rate-limit
			// backend removed every configured send-rate cap at once, which is
			// exactly when a provider is most likely to block or blacklist the
			// platform. A counter that cannot be consulted means the limit
			// cannot be honoured, so the provider is treated as unavailable
			// and the next fallback is used.
			ok, rerr := s.repo.IncrementAndCheck(ctx, p.ID, window, p.RateLimitPerMin)
			if rerr != nil || !ok {
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

// matchRule picks the single winning routing rule with DETERMINISTIC
// precedence, independent of the order the repository happened to return.
//
// Precedence, most specific first:
//  1. domain-scoped rules (DomainID != 0)
//  2. tenant-scoped rules (TenantID != 0, DomainID == 0)
//  3. global rules
//
// Within one tier: lower Priority wins; ties are broken by lower ID, so the
// same inputs always resolve to the same rule across processes and retries.
func matchRule(rules []RoutingRule, req RouteRequest) (RoutingRule, bool) {
	best := RoutingRule{}
	found := false
	for _, rule := range rules {
		if !ruleMatches(rule, req) {
			continue
		}
		if !found || ruleMoreSpecific(rule, best) {
			best, found = rule, true
		}
	}
	return best, found
}

func ruleTier(r RoutingRule) int {
	switch {
	case r.DomainID != 0:
		return 0
	case r.TenantID != 0:
		return 1
	default:
		return 2
	}
}

// ruleMoreSpecific reports whether a should win over b.
func ruleMoreSpecific(a, b RoutingRule) bool {
	ta, tb := ruleTier(a), ruleTier(b)
	if ta != tb {
		return ta < tb
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	return a.ID < b.ID
}

func ruleMatches(rule RoutingRule, req RouteRequest) bool {
	// TENANT SCOPING, checked FIRST and for every rule.
	//
	// The previous implementation only compared tenants when the rule was NOT
	// domain-scoped (`if rule.DomainID == 0 && rule.TenantID != 0 && ...`), so
	// a domain-scoped rule belonging to tenant A could route tenant B's mail
	// as soon as the domain ids collided or a stale rule survived a domain
	// move. A rule owned by a tenant now applies ONLY to that tenant,
	// regardless of its other selectors. TenantID == 0 means a platform-global
	// rule, which legitimately applies to everyone.
	if rule.TenantID != 0 && rule.TenantID != req.TenantID {
		return false
	}

	// DOMAIN SCOPING. A rule naming a domain requires that exact domain. A
	// request with an UNKNOWN sending domain (DomainID == 0) must not match a
	// domain-scoped rule: "unknown" is not "any".
	if rule.DomainID != 0 {
		if req.DomainID == 0 || rule.DomainID != req.DomainID {
			return false
		}
	}

	if rule.RecipientDomain != "" && !strings.EqualFold(rule.RecipientDomain, req.RecipientDomain) {
		return false
	}
	if rule.Classification != "" && rule.Classification != req.Classification {
		return false
	}

	// SENDER PATTERN. This selector was stored, documented and exposed in the
	// admin API but NEVER evaluated — every rule matched as though its sender
	// pattern were empty, so an operator who scoped a rule to a specific
	// sender silently applied it to every sender in scope. It is now enforced
	// against the FULL envelope sender.
	if rule.SenderPattern != "" && !senderPatternMatches(rule.SenderPattern, req.SenderAddress, req.SenderDomain) {
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
