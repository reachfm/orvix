package policy

import (
	"context"
	"fmt"
	"sync"
)

// ── Policy Modes ─────────────────────────────────────────────

type PolicyMode int

const (
	AllowAll PolicyMode = iota
	InternalOnly
	ExternalOnly
	SendOnly
	ReceiveOnly
	Disabled
)

func (m PolicyMode) String() string {
	switch m {
	case AllowAll:
		return "allow_all"
	case InternalOnly:
		return "internal_only"
	case ExternalOnly:
		return "external_only"
	case SendOnly:
		return "send_only"
	case ReceiveOnly:
		return "receive_only"
	case Disabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// ── Policy Entities ─────────────────────────────────────────

type TenantPolicy struct {
	TenantID  uint
	Mode      PolicyMode
	UpdatedAt int64 // unix timestamp
}

type DomainPolicy struct {
	Domain    string
	Mode      PolicyMode
	UpdatedAt int64
}

type MailboxPolicy struct {
	MailboxID uint
	Mode      PolicyMode
	UpdatedAt int64
}

// ResolvedPolicy is the result of policy resolution.
type ResolvedPolicy struct {
	Mode   PolicyMode
	Level  string      // "default", "tenant", "domain", "mailbox"
	Source interface{} // the specific policy that was applied
}

// ── Policy Actions ──────────────────────────────────────────

type Action int

const (
	ActionAllow Action = iota
	ActionBlock
)

type Direction int

const (
	Send Direction = iota
	Receive
)

type Scope int

const (
	Internal Scope = iota
	External
)

// EvaluationRequest defines what is being checked.
type EvaluationRequest struct {
	Direction Direction
	Scope     Scope
	TenantID  uint
	Domain    string
	MailboxID *uint // nil if not a specific mailbox
}

// EvaluationResult is the outcome of a policy evaluation.
type EvaluationResult struct {
	Action Action
	Reason string
	Policy *ResolvedPolicy
}

// ── Engine ──────────────────────────────────────────────────

type Engine struct {
	mu sync.RWMutex

	tenants   map[uint]TenantPolicy
	domains   map[string]DomainPolicy
	mailboxes map[uint]MailboxPolicy

	defaultMode PolicyMode
	repo        *Repository
}

func NewEngine() *Engine {
	return &Engine{
		tenants:     make(map[uint]TenantPolicy),
		domains:     make(map[string]DomainPolicy),
		mailboxes:   make(map[uint]MailboxPolicy),
		defaultMode: AllowAll,
	}
}

// SetRepository attaches a persistent repository. Call before any mutations.
func (e *Engine) SetRepository(repo *Repository) {
	e.repo = repo
}

// LoadFromDB loads persisted policy state from the repository.
func (e *Engine) LoadFromDB(ctx context.Context) error {
	if e.repo == nil {
		return nil
	}
	snap, err := e.repo.LoadAll(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.defaultMode = snap.DefaultMode
	e.tenants = snap.Tenants
	e.domains = snap.Domains
	e.mailboxes = snap.Mailboxes
	return nil
}

// ── Policy CRUD ─────────────────────────────────────────────

func (e *Engine) SetTenantPolicy(tenantID uint, mode PolicyMode) {
	e.mu.Lock()
	e.tenants[tenantID] = TenantPolicy{TenantID: tenantID, Mode: mode}
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.SavePolicy(context.Background(), "tenant", fmt.Sprintf("%d", tenantID), mode)
	}
}

func (e *Engine) GetTenantPolicy(tenantID uint) (TenantPolicy, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.tenants[tenantID]
	return p, ok
}

func (e *Engine) SetDomainPolicy(domain string, mode PolicyMode) {
	e.mu.Lock()
	e.domains[domain] = DomainPolicy{Domain: domain, Mode: mode}
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.SavePolicy(context.Background(), "domain", domain, mode)
	}
}

func (e *Engine) GetDomainPolicy(domain string) (DomainPolicy, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.domains[domain]
	return p, ok
}

func (e *Engine) SetMailboxPolicy(mailboxID uint, mode PolicyMode) {
	e.mu.Lock()
	e.mailboxes[mailboxID] = MailboxPolicy{MailboxID: mailboxID, Mode: mode}
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.SavePolicy(context.Background(), "mailbox", fmt.Sprintf("%d", mailboxID), mode)
	}
}

func (e *Engine) GetMailboxPolicy(mailboxID uint) (MailboxPolicy, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.mailboxes[mailboxID]
	return p, ok
}

func (e *Engine) DeleteTenantPolicy(tenantID uint) {
	e.mu.Lock()
	delete(e.tenants, tenantID)
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.DeletePolicy(context.Background(), "tenant", fmt.Sprintf("%d", tenantID))
	}
}

func (e *Engine) DeleteDomainPolicy(domain string) {
	e.mu.Lock()
	delete(e.domains, domain)
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.DeletePolicy(context.Background(), "domain", domain)
	}
}

func (e *Engine) DeleteMailboxPolicy(mailboxID uint) {
	e.mu.Lock()
	delete(e.mailboxes, mailboxID)
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.DeletePolicy(context.Background(), "mailbox", fmt.Sprintf("%d", mailboxID))
	}
}

func (e *Engine) SetDefaultMode(mode PolicyMode) {
	e.mu.Lock()
	e.defaultMode = mode
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.SavePolicy(context.Background(), "default", "", mode)
	}
}

// ── Resolution ─────────────────────────────────────────────

// Resolve resolves the effective policy for a given tenant/domain/mailbox.
// Precedence: mailbox policy > domain policy > tenant policy > system default.
func (e *Engine) Resolve(tenantID uint, domain string, mailboxID *uint) *ResolvedPolicy {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Mailbox policy (most specific).
	if mailboxID != nil {
		if p, ok := e.mailboxes[*mailboxID]; ok {
			return &ResolvedPolicy{Mode: p.Mode, Level: "mailbox", Source: p}
		}
	}

	// Domain policy.
	if p, ok := e.domains[domain]; ok {
		return &ResolvedPolicy{Mode: p.Mode, Level: "domain", Source: p}
	}

	// Tenant policy.
	if p, ok := e.tenants[tenantID]; ok {
		return &ResolvedPolicy{Mode: p.Mode, Level: "tenant", Source: p}
	}

	// System default.
	return &ResolvedPolicy{Mode: e.defaultMode, Level: "default", Source: nil}
}

// ── Evaluation ─────────────────────────────────────────────

// Evaluate checks whether a mail flow action is allowed.
func (e *Engine) Evaluate(req *EvaluationRequest) *EvaluationResult {
	if req == nil {
		return &EvaluationResult{Action: ActionBlock, Reason: "nil request"}
	}

	policy := e.Resolve(req.TenantID, req.Domain, req.MailboxID)

	allowed := modeAllows(policy.Mode, req.Direction, req.Scope)

	if allowed {
		return &EvaluationResult{
			Action: ActionAllow,
			Reason: fmt.Sprintf("policy %s: %s %s allowed", policy.Level, dirString(req.Direction), scopeString(req.Scope)),
			Policy: policy,
		}
	}

	return &EvaluationResult{
		Action: ActionBlock,
		Reason: fmt.Sprintf("policy %s: %s %s blocked by %s mode", policy.Level, dirString(req.Direction), scopeString(req.Scope), policy.Mode),
		Policy: policy,
	}
}

// modeAllows determines if a policy mode allows the given direction + scope.
func modeAllows(mode PolicyMode, dir Direction, scope Scope) bool {
	switch mode {
	case AllowAll:
		return true
	case InternalOnly:
		return scope == Internal
	case ExternalOnly:
		return scope == External
	case SendOnly:
		return dir == Send
	case ReceiveOnly:
		return dir == Receive
	case Disabled:
		return false
	default:
		return true // default to allow
	}
}

func dirString(d Direction) string {
	if d == Send {
		return "send"
	}
	return "receive"
}

func scopeString(s Scope) string {
	if s == Internal {
		return "internal"
	}
	return "external"
}
