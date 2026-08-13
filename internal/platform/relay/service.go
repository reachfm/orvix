package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	// The row, its audit entry, and its outbox event commit together. This
	// was previously an unguarded insert followed by `_ = s.audit.Record(...)`
	// outside any transaction, so an audit failure left a relay endpoint
	// configured with NO record of who created it or when - the exact evidence
	// an incident review depends on.
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin relay create", err)
	}
	defer tx.Rollback()

	if err := s.repo.CreateProviderTx(ctx, tx, &p); err != nil {
		if kernel.IsUniqueViolation(err) {
			return nil, ErrRelayNameConflict
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create platform relay", err)
	}
	if s.outbox != nil {
		if err := s.outbox.Enqueue(ctx, tx, "relay.provider.created", fmt.Sprintf("%d", p.ID), map[string]any{
			// Non-secret identifying fields only: never the password, the
			// secret reference, or the ciphertext.
			"scope": string(p.Scope), "tenant_id": p.TenantID, "pool_id": p.PoolID,
		}, now); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue relay create event", err)
		}
	}
	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "platform.relay.create", fmt.Sprintf("relay:%d", p.ID), p.TenantID, "success", "")); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record relay create audit", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit relay create", err)
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
	now := s.clock.Now()
	next.UpdatedAt = now
	next.Version = version

	// Same transactional guarantee as create: an update that changes a relay's
	// host, credential, or TLS posture must not survive without its audit
	// entry.
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin relay update", err)
	}
	defer tx.Rollback()

	ok, err := s.repo.UpdateProviderTx(ctx, tx, &next)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "update platform relay", err)
	}
	if !ok {
		return nil, ErrVersionConflict
	}
	next.Version++
	if s.outbox != nil {
		if err := s.outbox.Enqueue(ctx, tx, "relay.provider.updated", fmt.Sprintf("%d", next.ID), map[string]any{
			"tenant_id": next.TenantID,
		}, now); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue relay update event", err)
		}
	}
	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "platform.relay.update", fmt.Sprintf("relay:%d", next.ID), next.TenantID, "success", "")); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record relay update audit", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit relay update", err)
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
	// Load the current row first: the audit entry must name the relay's real
	// owning tenant, and a transition against a missing relay must be a typed
	// not-found rather than an indistinguishable version conflict.
	cur, err := s.repo.GetProvider(ctx, id)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get platform relay", err)
	}
	if cur == nil {
		return nil, ErrProviderNotFound
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
		// A dropped outbox event means downstream consumers never learn the
		// relay was disabled. Failing the transaction is the only outcome
		// that keeps business state and evidence consistent.
		if err := s.outbox.Enqueue(ctx, tx, "relay.provider.state", fmt.Sprintf("%d", id), map[string]any{
			"action": map[bool]string{true: "enabled", false: "disabled"}[active],
		}, now); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue relay state event", err)
		}
	}
	if s.audit != nil {
		// The tenant is read from the CURRENT row, not hardcoded. This call
		// previously passed 0, so every enable/disable of a tenant-owned
		// relay was recorded against tenant 0 - invisible in that tenant's
		// audit trail and wrong in the platform's.
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(actor,
			map[bool]string{true: "platform.relay.enable", false: "platform.relay.disable"}[active],
			fmt.Sprintf("relay:%d", id), cur.TenantID, "success", "")); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record relay state audit", err)
		}
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
		pw, gerr := GenerateRelayCredential()
		if gerr != nil {
			// Abort BEFORE encrypting, mutating, auditing, or enqueuing
			// anything: a rotation that cannot produce a strong credential
			// must leave the existing credential untouched.
			return nil, "", gerr
		}
		newPassword = pw
	}
	if strings.TrimSpace(newPassword) == "" {
		// Defence in depth: an empty credential must never reach the secret
		// codec or the database, whatever produced it.
		return nil, "", kernel.NewError(kernel.ErrCodeValidation, "a relay credential must not be empty")
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
		// The payload is intentionally empty: a credential-rotation event
		// must carry no credential material, no secret reference, and no
		// ciphertext.
		if err := s.outbox.Enqueue(ctx, tx, "relay.provider.credentials_rotated", fmt.Sprintf("%d", id), map[string]any{}, now); err != nil {
			return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "enqueue relay rotation event", err)
		}
	}
	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "platform.relay.rotate_credentials", fmt.Sprintf("relay:%d", id), cur.TenantID, "success", "")); err != nil {
			return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "record relay rotation audit", err)
		}
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
		// The audited tenant is the relay's OWNER, read from the row. This
		// previously passed 0, so an operator-triggered connection test
		// against a tenant-owned relay left no trace in that tenant's audit
		// trail. The audit failure is reported rather than dropped: a
		// connection test is a security-relevant administrative action.
		var tenantID uint
		if cur, gerr := s.repo.GetProvider(ctx, id); gerr == nil && cur != nil {
			tenantID = cur.TenantID
		}
		if aerr := s.audit.Record(ctx, s.auditEntry(actor, "platform.relay.test", fmt.Sprintf("relay:%d", id), tenantID,
			map[bool]string{true: "success", false: "failed"}[result.Connected], "")); aerr != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record relay test audit", aerr)
		}
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
		if err := s.outbox.Enqueue(ctx, tx, "relay.provider.deleted", fmt.Sprintf("%d", id), map[string]any{}, now); err != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "enqueue relay delete event", err)
		}
	}
	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "platform.relay.delete", fmt.Sprintf("relay:%d", id), cur.TenantID, "success", "")); err != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "record relay delete audit", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "commit relay delete", err)
	}
	return nil
}

// GenerateRelayCredential returns a strong random credential (256 bits,
// URL-safe base64) for operator-initiated rotation. It is returned exactly
// once by RotateRelayCredentials and never stored in plaintext anywhere.
//
// It previously returned a bare string and, on a crypto/rand failure,
// returned "" with a comment asserting "the caller will fail the rotation".
// The caller did no such thing: RotateRelayCredentials encrypted the empty
// string, persisted it as the provider's credential, audited the rotation as
// a success, and handed "" to the operator as the new one-time secret. A
// crypto/rand failure therefore silently replaced a working credential with
// an empty one. The failure is now typed and unmissable.
func GenerateRelayCredential() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", kernel.Wrap(kernel.ErrCodeInternal, "generate relay credential", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CreatePool creates a routing pool. A tenant-owned pool carries the
// authenticated tenant ID (the tenant handler stamps it from auth
// context); a platform-created pool (TenantID 0) is platform-managed.
// The row, its audit entry, and its outbox event commit together.
func (s *Service) CreatePool(ctx context.Context, p Pool, actor AuditActor) (*Pool, error) {
	if strings.TrimSpace(p.Name) == "" {
		return nil, ErrNameRequired
	}
	now := s.clock.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.Strategy == "" {
		p.Strategy = StrategyPriority
	}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin relay pool create", err)
	}
	defer tx.Rollback()
	if err := s.repo.CreatePoolTx(ctx, tx, &p); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create relay pool", err)
	}
	if s.outbox != nil {
		if err := s.outbox.Enqueue(ctx, tx, "relay.pool.created", fmt.Sprintf("%d", p.ID), map[string]any{
			"tenant_id": p.TenantID, "scope": string(p.Scope), "name": p.Name,
		}, now); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue relay pool create event", err)
		}
	}
	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "relay.pool.create", fmt.Sprintf("relay_pool:%d", p.ID), p.TenantID, "success", "")); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record relay pool create audit", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit relay pool create", err)
	}
	return &p, nil
}

// CreateProvider creates a provider, encrypting its credential via
// the secret-reference mechanism before it ever reaches the database
// — the caller-supplied plaintext password never gets assigned to
// p.SecretRef directly; it exists only as the `password` parameter
// and is discarded after Encrypt returns.
//
// OWNERSHIP (F2): the provider may join ONLY a pool owned by the same
// tenant (p.TenantID == pool.TenantID). A tenant-owned provider cannot
// attach to another tenant's pool or to a platform-global pool
// (default deny); a platform-global provider (TenantID 0) cannot
// attach to a tenant-owned pool. This closes the cross-tenant
// provider-injection path: an injected tenant-0 provider inside a
// tenant's pool would otherwise be treated as platform-shared and
// route that tenant's mail through an attacker-controlled SMTP server.
//
// The row, its audit entry, and its outbox event commit together.
func (s *Service) CreateProvider(ctx context.Context, p Provider, password string, actor AuditActor) (*Provider, error) {
	if !p.ConnSecurity.IsValid() {
		return nil, ErrInvalidConnSecurity
	}
	if p.PoolID == 0 {
		return nil, kernel.ValidationError(map[string]string{"pool_id": "a target pool is required"})
	}
	pool, err := s.repo.GetPool(ctx, p.PoolID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get relay pool", err)
	}
	if pool == nil {
		return nil, ErrPoolNotFound
	}
	if pool.TenantID != p.TenantID {
		return nil, ErrCrossTenantPool
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
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin relay provider create", err)
	}
	defer tx.Rollback()
	if err := s.repo.CreateProviderTx(ctx, tx, &p); err != nil {
		if kernel.IsUniqueViolation(err) {
			return nil, ErrRelayNameConflict
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create relay provider", err)
	}
	if s.outbox != nil {
		if err := s.outbox.Enqueue(ctx, tx, "relay.provider.created", fmt.Sprintf("%d", p.ID), map[string]any{
			"tenant_id": p.TenantID, "pool_id": p.PoolID, "scope": string(p.Scope),
		}, now); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue relay provider create event", err)
		}
	}
	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "relay.provider.create", fmt.Sprintf("relay_provider:%d", p.ID), p.TenantID, "success", "")); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record relay provider create audit", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit relay provider create", err)
	}
	return &p, nil
}

// ListProvidersRedacted returns a pool's providers with credentials
// stripped — the only form this service ever returns to an API caller
// or audit record. Platform surface: no ownership restriction.
func (s *Service) ListProvidersRedacted(ctx context.Context, poolID uint) ([]RedactedProvider, error) {
	ps, err := s.repo.ListProvidersByPool(ctx, poolID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list relay providers", err)
	}
	return RedactAll(ps), nil
}

// ListPoolProvidersScoped returns a pool's providers (redacted) for a
// TENANT caller, verifying the pool belongs to that tenant first. A
// tenant must never enumerate another tenant's relay configuration.
func (s *Service) ListPoolProvidersScoped(ctx context.Context, poolID, tenantID uint) ([]RedactedProvider, error) {
	pool, err := s.repo.GetPool(ctx, poolID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get relay pool", err)
	}
	if pool == nil {
		return nil, ErrPoolNotFound
	}
	if pool.TenantID != tenantID {
		return nil, ErrCrossTenantPool
	}
	return s.ListProvidersRedacted(ctx, poolID)
}

// TestConnectionScoped is the TENANT-surface connection test. It
// verifies the provider belongs to the authenticated tenant before
// dialing anything, and records an audited outcome. A tenant must
// never probe another tenant's relay endpoint.
func (s *Service) TestConnectionScoped(ctx context.Context, providerID, tenantID uint, actor AuditActor) (*HealthCheckResult, error) {
	p, err := s.repo.GetProvider(ctx, providerID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get relay provider", err)
	}
	if p == nil {
		return nil, ErrProviderNotFound
	}
	if p.TenantID != tenantID {
		return nil, ErrCrossTenantProvider
	}
	result, err := s.TestConnection(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if s.audit != nil {
		if aerr := s.audit.Record(ctx, s.auditEntry(actor, "relay.provider.test", fmt.Sprintf("relay_provider:%d", providerID), tenantID,
			map[bool]string{true: "success", false: "failed"}[result.Connected], "")); aerr != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record relay provider test audit", aerr)
		}
	}
	return result, nil
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

// CreateRoutingRule persists a routing rule together with its audit evidence
// in ONE transaction. A routing rule decides which relay every matching
// message egresses through, so creating one with no record of who did it is a
// gap in exactly the evidence an incident review needs; the previous
// implementation wrote the rule with no audit entry at all.
//
// The referenced pool is validated against the rule's tenant BEFORE insert: a
// tenant-scoped rule pointing at another tenant's pool would route that
// tenant's mail through infrastructure it does not own.
func (s *Service) CreateRoutingRule(ctx context.Context, rule RoutingRule, actor AuditActor) (*RoutingRule, error) {
	if rule.PoolID == 0 {
		return nil, kernel.ValidationError(map[string]string{"pool_id": "a target pool is required"})
	}
	if len(strings.TrimSpace(rule.SenderPattern)) > maxSenderPatternLen {
		return nil, kernel.ValidationError(map[string]string{"sender_pattern": "pattern is too long"})
	}
	pool, err := s.repo.GetPool(ctx, rule.PoolID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get relay pool", err)
	}
	if pool == nil {
		return nil, ErrPoolNotFound
	}
	if pool.TenantID != 0 && rule.TenantID != 0 && pool.TenantID != rule.TenantID {
		return nil, ErrCrossTenantProvider
	}

	rule.CreatedAt = s.clock.Now()
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin routing rule create", err)
	}
	defer tx.Rollback()

	if err := s.repo.CreateRoutingRuleTx(ctx, tx, &rule); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create routing rule", err)
	}
	if s.outbox != nil {
		if err := s.outbox.Enqueue(ctx, tx, "relay.routing_rule.created", fmt.Sprintf("%d", rule.ID), map[string]any{
			"tenant_id": rule.TenantID, "domain_id": rule.DomainID, "pool_id": rule.PoolID,
		}, rule.CreatedAt); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue routing rule event", err)
		}
	}
	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(actor, "platform.relay.routing_rule.create",
			fmt.Sprintf("relay_routing_rule:%d", rule.ID), rule.TenantID, "success", "")); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record routing rule audit", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit routing rule create", err)
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
	// The override forces traffic through a specific pool; that pool must be
	// reachable by this tenant.
	pool, err := s.repo.GetPool(ctx, poolID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get relay pool", err)
	}
	if pool == nil {
		return nil, ErrPoolNotFound
	}
	if pool.TenantID != 0 && tenantID != 0 && pool.TenantID != tenantID {
		return nil, ErrCrossTenantProvider
	}

	o := EmergencyOverride{TenantID: tenantID, PoolID: poolID, Reason: reason, ActorID: actorID, ExpiresAt: expiresAt, CreatedAt: now}

	// An emergency override is one of the most security-sensitive mutations in
	// the platform: it redirects every matching message. It previously carried
	// no audit entry at all. Row and evidence now commit together.
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin emergency override", err)
	}
	defer tx.Rollback()

	if err := s.repo.CreateOverrideTx(ctx, tx, &o); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create emergency override", err)
	}
	if s.outbox != nil {
		if err := s.outbox.Enqueue(ctx, tx, "relay.override.created", fmt.Sprintf("%d", o.ID), map[string]any{
			"tenant_id": tenantID, "pool_id": poolID, "expires_at": expiresAt.UTC().Format(time.RFC3339),
		}, now); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue emergency override event", err)
		}
	}
	if s.audit != nil {
		// The operator-supplied reason is recorded: an emergency control
		// without a justification on the record is not auditable.
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(AuditActor{ID: actorID}, "platform.relay.emergency_override.create",
			fmt.Sprintf("relay_override:%d", o.ID), tenantID, "success", reason)); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "record emergency override audit", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit emergency override", err)
	}
	o.Active = true
	return &o, nil
}

// RevokeEmergencyOverride deactivates an override before its natural expiry.
// Revocation is as security-sensitive as creation - it restores normal
// routing - so it is transactional and audited on the same terms. Revoking an
// override that is already inactive or absent is reported, never silently
// treated as success.
func (s *Service) RevokeEmergencyOverride(ctx context.Context, id, tenantID, actorID uint, reason string) error {
	now := s.clock.Now()
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "begin emergency override revoke", err)
	}
	defer tx.Rollback()

	ok, err := s.repo.RevokeOverrideTx(ctx, tx, id, tenantID)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "revoke emergency override", err)
	}
	if !ok {
		// Either no such override, already inactive, or owned by another
		// tenant. All three are refusals, not successes.
		return ErrOverrideNotFound
	}
	if s.outbox != nil {
		if err := s.outbox.Enqueue(ctx, tx, "relay.override.revoked", fmt.Sprintf("%d", id), map[string]any{
			"tenant_id": tenantID,
		}, now); err != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "enqueue emergency override revoke event", err)
		}
	}
	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, s.auditEntry(AuditActor{ID: actorID}, "platform.relay.emergency_override.revoke",
			fmt.Sprintf("relay_override:%d", id), tenantID, "success", reason)); err != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "record emergency override revoke audit", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "commit emergency override revoke", err)
	}
	return nil
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
	// POOL VISIBILITY (F2): a pool owned by another tenant must never be
	// selected for this tenant's traffic, even when a legacy rule or
	// override row references it. Tenant-0 pools are platform-managed and
	// globally usable; a tenant-owned pool is usable only by its owner.
	if pool.TenantID != 0 && req.TenantID != 0 && pool.TenantID != req.TenantID {
		return nil, ErrCrossTenantPool
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
	// The state change and the event announcing it commit together. They were
	// two independent statements with the enqueue error DISCARDED, so a
	// provider could trip its circuit open - taking itself out of rotation -
	// with no operational event ever emitted, leaving operators to discover a
	// dead relay from delivery metrics instead of an alert.
	//
	// This runs on the delivery hot path, AFTER the SMTP transaction has
	// completed. The returned error therefore tells the caller "bookkeeping
	// failed", never "redeliver": the delivery worker surfaces it through
	// RelayBookkeepingFailed and does not retry, because retrying would send
	// the recipient a second copy of a message the provider already accepted.
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "begin relay attempt bookkeeping", err)
	}
	defer tx.Rollback()

	if err := s.repo.UpdateProviderCircuitTx(ctx, tx, providerID, nextState, failures, openedAt, now); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "update relay circuit state", err)
	}
	if nextState != p.CircuitState && s.outbox != nil {
		// Non-secret operational fields only.
		if err := s.outbox.Enqueue(ctx, tx, "relay.circuit.transition", fmt.Sprintf("%d", providerID), map[string]any{
			"from": p.CircuitState, "to": nextState, "provider": p.Name,
		}, now); err != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "enqueue relay circuit event", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "commit relay attempt bookkeeping", err)
	}
	return nil
}
