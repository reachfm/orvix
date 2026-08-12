// Package relay implements Feature 10 (Milestone 7): the outbound
// relay control plane. It integrates with the real outbound SMTP
// delivery path (internal/coremail/delivery) as a routing decision
// layer — SelectRoute decides IF and THROUGH WHICH provider a message
// should relay, before the delivery worker dials anything. Credential
// storage reuses internal/config's existing AES-GCM
// encrypt-at-rest primitives (EncryptString/DecryptString) as the
// project's secret-reference mechanism — no new key management
// infrastructure, no plaintext credential column.
package relay

import "time"

// Scope determines precedence when resolving which pool applies:
// domain beats tenant beats global.
type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeTenant Scope = "tenant"
	ScopeDomain Scope = "domain"
)

// ConnSecurity is the connection security mode used to reach a
// provider.
type ConnSecurity string

const (
	ConnSecurityNone        ConnSecurity = "none"
	ConnSecurityStartTLS    ConnSecurity = "starttls"
	ConnSecurityImplicitTLS ConnSecurity = "implicit_tls"
)

func (c ConnSecurity) IsValid() bool {
	switch c {
	case ConnSecurityNone, ConnSecurityStartTLS, ConnSecurityImplicitTLS:
		return true
	default:
		return false
	}
}

// TLSValidation controls certificate validation strictness when
// ConnSecurity requires TLS.
type TLSValidation string

const (
	TLSValidationStrict        TLSValidation = "strict"
	TLSValidationOpportunistic TLSValidation = "opportunistic"
)

// SelectionStrategy is how a pool picks among its healthy providers.
type SelectionStrategy string

const (
	StrategyPriority SelectionStrategy = "priority" // lowest priority number wins; ties broken by weight
	StrategyWeighted SelectionStrategy = "weighted" // weighted-random among providers at the best priority tier
)

// CircuitState mirrors the classic circuit breaker states.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// Provider is a single outbound SMTP relay endpoint. Password is
// NEVER populated on any value returned from the service layer to a
// caller outside this package — only SecretRef (opaque, encrypted)
// travels with the struct after Create/Get.
type Provider struct {
	ID              uint          `json:"id"`
	Scope           Scope         `json:"scope"`
	TenantID        uint          `json:"tenant_id,omitempty"`
	DomainID        uint          `json:"domain_id,omitempty"`
	PoolID          uint          `json:"pool_id"`
	Name            string        `json:"name"`
	Host            string        `json:"host"`
	Port            int           `json:"port"`
	Username        string        `json:"username,omitempty"`
	SecretRef       string        `json:"-"` // encrypted credential; never serialized
	ConnSecurity    ConnSecurity  `json:"conn_security"`
	TLSValidation   TLSValidation `json:"tls_validation"`
	Priority        int           `json:"priority"`
	Weight          int           `json:"weight"`
	Active          bool          `json:"active"`
	RateLimitPerMin int           `json:"rate_limit_per_min,omitempty"` // 0 = unlimited
	CircuitState    CircuitState  `json:"circuit_state"`
	CircuitFailures int           `json:"circuit_failures"`
	CircuitOpenedAt *time.Time    `json:"circuit_opened_at,omitempty"`
	Version         int           `json:"version"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

// HasSecret reports whether a credential is configured, WITHOUT
// exposing it — the only sanctioned way for a caller to know "is
// there a password" is this boolean.
func (p Provider) HasSecret() bool { return p.SecretRef != "" }

// Pool groups providers under one selection strategy plus primary/
// fallback semantics: providers are tried in priority order; when
// StrategyWeighted, providers sharing the best available priority are
// chosen by weighted random among themselves — the tier below only
// gets tried as a fallback if every provider at the current tier is
// unavailable (circuit open, rate-limited, or inactive).
type Pool struct {
	ID         uint              `json:"id"`
	Scope      Scope             `json:"scope"`
	TenantID   uint              `json:"tenant_id,omitempty"`
	DomainID   uint              `json:"domain_id,omitempty"`
	Name       string            `json:"name"`
	Strategy   SelectionStrategy `json:"strategy"`
	DirectOnly bool              `json:"direct_only"` // true = never relay; always deliver direct-to-MX
	Version    int               `json:"version"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// RoutingRule matches an outbound message to a pool. Rules are
// evaluated most-specific-first: domain scope, then tenant, then
// global default. An empty field means "match any".
type RoutingRule struct {
	ID              uint      `json:"id"`
	TenantID        uint      `json:"tenant_id,omitempty"`
	DomainID        uint      `json:"domain_id,omitempty"`
	SenderPattern   string    `json:"sender_pattern,omitempty"`
	RecipientDomain string    `json:"recipient_domain,omitempty"`
	Classification  string    `json:"classification,omitempty"`
	PoolID          uint      `json:"pool_id"`
	Priority        int       `json:"priority"`
	CreatedAt       time.Time `json:"created_at"`
}

// EmergencyOverride forces all matching traffic through (or away
// from) a specific pool, bypassing normal routing-rule resolution,
// for a bounded, audited, auto-expiring window.
type EmergencyOverride struct {
	ID        uint      `json:"id"`
	TenantID  uint      `json:"tenant_id,omitempty"`
	PoolID    uint      `json:"pool_id"`
	Reason    string    `json:"reason"`
	ActorID   uint      `json:"actor_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// SelectedRoute is what SelectRoute returns to the delivery worker —
// enough to dial and to record in delivery history, NEVER the
// credential itself.
type SelectedRoute struct {
	PoolID       uint         `json:"pool_id"`
	ProviderID   uint         `json:"provider_id"`
	ProviderName string       `json:"provider_name"`
	Host         string       `json:"host"`
	Port         int          `json:"port"`
	ConnSecurity ConnSecurity `json:"conn_security"`
	TLSStrict    bool         `json:"tls_strict"`
	Direct       bool         `json:"direct"` // true = no relay; deliver straight to recipient MX
	// Fallbacks are additional providers to try, in order, if Provider
	// itself fails during this delivery attempt — the primary/fallback
	// chain.
	Fallbacks []SelectedRoute `json:"fallbacks,omitempty"`
}
