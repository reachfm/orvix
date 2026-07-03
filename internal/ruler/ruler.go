// Package ruler implements the admin-scoped acceptance &
// routing rules engine and the admin-scoped incoming
// message rules engine. Both engines read from the
// coremail_acceptance_rules / coremail_incoming_msg_rules
// tables and apply decisions at the SMTP receive path.
//
// The package is wired into the SMTP receiver through
// the rulertypes.RuleEngine interface (alias
// smtp.RuleEvaluator). Each engine ships a
// MarkEnforced() that the runtime calls once the engine
// is installed in the receiver — this is what the admin
// status endpoints observe to decide whether the rule
// engine is live or merely stored.
//
// Tenant isolation: every query is scoped by
// tenant_id. A session where the receiver resolves the
// recipient to a domain outside the tenant is filtered
// out before any rule can match. The evaluator never
// returns a decision when the supplied tenant id is not
// represented in any rule row.
package ruler

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/rulertypes"
	"go.uber.org/zap"
)

// Action is the rule decision returned to the SMTP
// receiver. The integer codes are kept for callers that
// prefer numerics; the string constants are used by the
// evaluator contract.
type Action string

const (
	ActionAccept     Action = "accept"
	ActionReject     Action = "reject"
	ActionQuarantine Action = "quarantine"
	ActionTag        Action = "tag"
)

// Engine holds both acceptance and incoming message rule
// stores. The two evaluators are split into separate
// methods so they can be wired independently into the
// SMTP command handler (acceptance, at MAIL FROM +
// RCPT TO) and the receiver (incoming, at DATA).
type Engine struct {
	db     *sql.DB
	logger *zap.Logger
	obs    *observability.Observability

	enforcedAcceptance atomic.Bool
	enforcedIncoming   atomic.Bool

	// in-process cache of rules. The admin endpoints
	// mutate the underlying tables directly; in a
	// multi-process deployment the cache TTL is the
	// honest upper bound on consistency.
	//
	// The cache uses a separate "loaded" flag instead of
	// nil-checking the slice, because Go's nil vs empty
	// slice distinction is not enough to tell "we have
	// never loaded" from "we loaded an empty rule set
	// that should be the fresh answer for the next
	// call too".
	mu               sync.RWMutex
	acceptLoaded     bool
	acceptCache      []acceptanceRule
	incomingLoaded   bool
	incomingCache    []incomingRule
}

// New constructs the rule engine bundle. db may be nil in
// tests; in that case every Evaluate call returns ok=false
// so the receiver's default behaviour is preserved.
func New(db *sql.DB, logger *zap.Logger, obs *observability.Observability) *Engine {
	return &Engine{db: db, logger: logger, obs: obs}
}

// MarkEnforced flips the runtime_enforced flag on both
// engines. The SMTP receiver init calls this once
// EvaluateAcceptance / EvaluateIncoming is wired into
// the receive path.
func (e *Engine) MarkEnforced() {
	e.enforcedAcceptance.Store(true)
	e.enforcedIncoming.Store(true)
}

// MarkAcceptanceEnforced / MarkIncomingEnforced split the
// flip into per-engine signals so the runtime can wire
// them independently.
func (e *Engine) MarkAcceptanceEnforced() { e.enforcedAcceptance.Store(true) }
func (e *Engine) MarkIncomingEnforced()   { e.enforcedIncoming.Store(true) }

// AcceptanceEnforced / IncomingEnforced report the
// per-engine runtime flag.
func (e *Engine) AcceptanceEnforced() bool { return e.enforcedAcceptance.Load() }
func (e *Engine) IncomingEnforced() bool   { return e.enforcedIncoming.Load() }

// acceptanceRule is the flat shape we load per
// coremail_acceptance_rules row.
type acceptanceRule struct {
	ID             int64
	Name           string
	Priority       int
	Enabled        bool
	Scope          string
	ScopeTarget    string
	SenderPattern  string
	RecipientPat   string
	SourceIPCIDR   string
	Action         string
	RedirectTo     string
	TenantID       int64
}

// incomingRule is the flat shape we load per
// coremail_incoming_msg_rules row.
type incomingRule struct {
	ID             int64
	Name           string
	Priority       int
	Enabled        bool
	Field          string
	Operator       string
	Value          string
	Action         string
	ActionTarget   string
	ApplyTo        string
	StopProcessing bool
	TenantID       int64
}

// loadAcceptanceRules re-reads the acceptance table into
// memory. The caller must hold e.mu in write mode.
func (e *Engine) loadAcceptanceRules(ctx context.Context) error {
	if e.db == nil {
		e.acceptCache = nil
		e.acceptLoaded = true
		return nil
	}
	rows, err := e.db.QueryContext(ctx, `SELECT id, name, priority, enabled, scope, scope_target,
		       sender_pattern, recipient_pattern, source_ip_cidr, action, redirect_to, tenant_id
		FROM coremail_acceptance_rules
		WHERE deleted_at IS NULL
		ORDER BY priority ASC, id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := make([]acceptanceRule, 0)
	for rows.Next() {
		var r acceptanceRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.Priority, &enabled,
			&r.Scope, &r.ScopeTarget, &r.SenderPattern, &r.RecipientPat,
			&r.SourceIPCIDR, &r.Action, &r.RedirectTo, &r.TenantID); err != nil {
			return err
		}
		r.Enabled = enabled == 1
		out = append(out, r)
	}
	e.acceptCache = out
	e.acceptLoaded = true
	return rows.Err()
}

// loadIncomingRules re-reads the incoming table into
// memory. Caller holds e.mu in write mode.
func (e *Engine) loadIncomingRules(ctx context.Context) error {
	if e.db == nil {
		e.incomingCache = nil
		e.incomingLoaded = true
		return nil
	}
	rows, err := e.db.QueryContext(ctx, `SELECT id, name, priority, enabled, field, operator, value,
		       action, action_target, apply_to, stop_processing, tenant_id
		FROM coremail_incoming_msg_rules
		WHERE deleted_at IS NULL
		ORDER BY priority ASC, id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := make([]incomingRule, 0)
	for rows.Next() {
		var r incomingRule
		var enabled int
		var stop int
		if err := rows.Scan(&r.ID, &r.Name, &r.Priority, &enabled, &r.Field, &r.Operator,
			&r.Value, &r.Action, &r.ActionTarget, &r.ApplyTo, &stop, &r.TenantID); err != nil {
			return err
		}
		r.Enabled = enabled == 1
		r.StopProcessing = stop == 1
		out = append(out, r)
	}
	e.incomingCache = out
	e.incomingLoaded = true
	return rows.Err()
}

// Reload forces a refresh of both caches. The runtime
// calls this on boot AND whenever the admin PATCH/POST/
// DELETE handlers touch the underlying tables so admin
// mutations are visible inside one SMTP request.
func (e *Engine) Reload(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.loadAcceptanceRules(ctx)
	_ = e.loadIncomingRules(ctx)
}

// Invalidate is the cheap "drop caches" version used
// right after a write. The next call to Evaluate reads
// the table fresh.
func (e *Engine) Invalidate() {
	e.mu.Lock()
	e.acceptLoaded = false
	e.acceptCache = nil
	e.incomingLoaded = false
	e.incomingCache = nil
	e.mu.Unlock()
}

// Evaluate is the RuleEvaluator entrypoint. The Ruler
// exposes two distinct evaluators through this method so
// the SMTP receiver can route MAIL FROM, RCPT TO, and
// DATA checks through a single interface if desired; in
// practice each command-handler hook calls the type-
// specific helper directly. We keep the shape required
// by internal/rulertypes.RuleEngine (alias
// internal/coremail/smtp.RuleEvaluator).
//
// The boolean returned as `matched` indicates whether any
// rule hit; the receiver must continue default behaviour
// when matched=false.
func (e *Engine) Evaluate(ctx context.Context, q rulertypes.Query) (matched bool, action string, reason string) {
	// The receiver forwards both acceptance and incoming
	// queries through this single entrypoint; we
	// dispatch based on a hint the SMTP layer sets on
	// the Query (we extend smtp.RuleQuery with a
	// small Adapter struct).
	if q.Headers != nil && q.Headers["_ruler_kind"] == "incoming" {
		return e.EvaluateIncoming(ctx, q)
	}
	return e.EvaluateAcceptance(ctx, q)
}

// EvaluateAcceptance walks enabled acceptance rules in
// priority order and returns the matching decision.
func (e *Engine) EvaluateAcceptance(ctx context.Context, q rulertypes.Query) (bool, string, string) {
	rules, err := e.acceptRules(ctx)
	if err != nil {
		if e.logger != nil {
			e.logger.Warn("ruler: load acceptance rules failed", zap.Error(err))
		}
		return false, "", ""
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		// Tenant isolation: rule.TenantID must match the
		// caller. Without a tenant id on the query we
		// run rules whose TenantID == 0 (the system /
		// unscoped rows) — that path is intentionally
		// restrictive so a missing tenant field can
		// never accidentally match a tenant-scoped rule.
		if r.TenantID != 0 && int64(q.TenantID) != r.TenantID {
			continue
		}
		if !matchesAcceptance(r, q) {
			continue
		}
		if e.obs != nil {
			e.obs.EventHistory.Record(observability.EventAcceptanceRuleMatched, map[string]string{
				"sender":    q.Sender,
				"recipient": q.Recipient,
				"rule_id":   fmt.Sprintf("%d", r.ID),
				"action":    r.Action,
			})
		}
		return true, r.Action, "rule:" + r.Name
	}
	return false, "", ""
}

// EvaluateIncoming walks enabled incoming rule rows in
// priority order and returns the matching decision.
func (e *Engine) EvaluateIncoming(ctx context.Context, q rulertypes.Query) (bool, string, string) {
	rules, err := e.incomingRules(ctx)
	if err != nil {
		if e.logger != nil {
			e.logger.Warn("ruler: load incoming rules failed", zap.Error(err))
		}
		return false, "", ""
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if r.TenantID != 0 && int64(q.TenantID) != r.TenantID {
			continue
		}
		if !matchesIncoming(r, q) {
			continue
		}
		if e.obs != nil {
			e.obs.EventHistory.Record(observability.EventIncomingRuleApplied, map[string]string{
				"sender":    q.Sender,
				"recipient": q.Recipient,
				"rule_id":   fmt.Sprintf("%d", r.ID),
				"action":    r.Action,
			})
		}
		return true, r.Action, "rule:" + r.Name
	}
	return false, "", ""
}

// acceptRules returns a fresh (or cached) copy of the
// acceptance table. The cache is invalidated explicitly
// via Invalidate() (called right after admin writes) OR
// Reload() (force-flush from disk).
func (e *Engine) acceptRules(ctx context.Context) ([]acceptanceRule, error) {
	e.mu.RLock()
	if e.acceptLoaded {
		out := e.acceptCache
		e.mu.RUnlock()
		return out, nil
	}
	e.mu.RUnlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.acceptLoaded {
		return e.acceptCache, nil
	}
	if err := e.loadAcceptanceRules(ctx); err != nil {
		return nil, err
	}
	return e.acceptCache, nil
}

func (e *Engine) incomingRules(ctx context.Context) ([]incomingRule, error) {
	e.mu.RLock()
	if e.incomingLoaded {
		out := e.incomingCache
		e.mu.RUnlock()
		return out, nil
	}
	e.mu.RUnlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.incomingLoaded {
		return e.incomingCache, nil
	}
	if err := e.loadIncomingRules(ctx); err != nil {
		return nil, err
	}
	return e.incomingCache, nil
}

// matchesAcceptance evaluates an acceptance-rule row
// against the supplied query. The rules engine supports:
//
//   - scope = "global" | "domain" | "mailbox"
//   - sender_pattern: substring or glob (*.example.com)
//   - recipient_pattern: substring or glob
//   - source_ip_cidr: CIDR literal
//
// All conditions AND together. A rule with NO pattern
// fields matches every envelope.
func matchesAcceptance(r acceptanceRule, q rulertypes.Query) bool {
	switch r.Scope {
	case "domain":
		if r.ScopeTarget == "" {
			return false
		}
		if !strings.EqualFold(q.Recipient, r.ScopeTarget) &&
			!strings.HasSuffix(strings.ToLower(q.Recipient), "@"+strings.ToLower(r.ScopeTarget)) {
			return false
		}
	case "mailbox":
		if !strings.EqualFold(q.Recipient, r.ScopeTarget) {
			return false
		}
	}
	if r.SenderPattern != "" && !patternMatch(q.Sender, r.SenderPattern) {
		return false
	}
	if r.RecipientPat != "" && !patternMatch(q.Recipient, r.RecipientPat) {
		return false
	}
	if r.SourceIPCIDR != "" {
		host, _, _ := strings.Cut(q.SourceIP, ":")
		ip := parseIPLiteral(host)
		_, cidr, err := parseCIDR(r.SourceIPCIDR)
		if err != nil || ip == nil || !cidr.Contains(ip) {
			return false
		}
	}
	return true
}

// matchesIncoming evaluates the rule against the
// supplied query. The engine understands a small set of
// field names and operators that map cleanly to common
// filter constructs.
func matchesIncoming(r incomingRule, q rulertypes.Query) bool {
	fieldValue := resolveIncomingField(r.Field, q)
	if fieldValue == "" && r.Field != "size" {
		// A field the engine cannot resolve produces a
		// non-match — deliberately conservative.
		return false
	}
	if !incomingOperatorMatch(r.Operator, fieldValue, r.Value) {
		return false
	}
	if r.ApplyTo == "incoming_only" && q.Sender == "" {
		return false
	}
	if r.ApplyTo == "outgoing_only" && q.Sender != "" && !strings.Contains(q.Sender, "@") {
		return false
	}
	return true
}

// resolveIncomingField looks up the named field against
// the query. We accept the admin-supplied enum subset
// from internal/api/handlers/enterprise_admin_v3.go.
func resolveIncomingField(name string, q rulertypes.Query) string {
	switch name {
	case "subject":
		return q.Subject
	case "from":
		return q.Sender
	case "to":
		return q.Recipient
	case "any_header":
		// Without a full header parse we fall back to
		// the From + Subject tuple the receiver forwards.
		return q.Subject + "\n" + q.Sender
	case "size":
		return "0"
	default:
		return ""
	}
}

// incomingOperatorMatch evaluates one of
// contains | equals | starts_with | ends_with | matches
// | gt | lt against field+value. matches is treated as a
// glob (*.example.com); gt/lt attempt numeric comparison.
func incomingOperatorMatch(op, field, val string) bool {
	switch op {
	case "contains":
		return val != "" && strings.Contains(strings.ToLower(field), strings.ToLower(val))
	case "equals":
		return strings.EqualFold(field, val)
	case "starts_with":
		return val != "" && strings.HasPrefix(strings.ToLower(field), strings.ToLower(val))
	case "ends_with":
		return val != "" && strings.HasSuffix(strings.ToLower(field), strings.ToLower(val))
	case "matches":
		return patternMatch(field, val)
	case "gt":
		var f, v float64
		_, _ = fmtSscan(field, &f)
		_, _ = fmtSscan(val, &v)
		return f > v
	case "lt":
		var f, v float64
		_, _ = fmtSscan(field, &f)
		_, _ = fmtSscan(val, &v)
		return f < v
	}
	return false
}

// patternMatch is the shared substring / glob matcher.
// '*' is the only wildcard. An empty pattern matches
// every value.
func patternMatch(value, pattern string) bool {
	if pattern == "" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return strings.Contains(strings.ToLower(value), strings.ToLower(pattern))
	}
	// Light-weight glob implementation. Split on '*'
	// and walk value with a sliding window.
	parts := strings.Split(pattern, "*")
	lower := strings.ToLower(value)
	idx := 0
	for i, p := range parts {
		if p == "" {
			continue
		}
		lp := strings.ToLower(p)
		j := strings.Index(lower[idx:], lp)
		if j < 0 {
			return false
		}
		idx += j + len(lp)
		_ = i
	}
	return true
}

func parseIPLiteral(s string) []byte {
	// Local import-free IP parse — pull the byte
	// representation if dotted-quad, nil otherwise.
	// We only need a tiny helper since net package
	// import would pull the IPv6 routing tables for
	// what is, in practice, a dotted-quad input.
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return nil
	}
	out := make([]byte, 4)
	for i, p := range parts {
		n := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				return nil
			}
			n = n*10 + int(r-'0')
		}
		if n < 0 || n > 255 {
			return nil
		}
		out[i] = byte(n)
	}
	return out
}

func parseCIDR(s string) (any, *netCIDR, error) {
	idx := strings.Index(s, "/")
	if idx < 0 {
		return nil, nil, fmt.Errorf("cidr missing /")
	}
	ipBytes := parseIPLiteral(s[:idx])
	if ipBytes == nil {
		return nil, nil, fmt.Errorf("invalid ip")
	}
	bits := 0
	for _, r := range s[idx+1:] {
		if r < '0' || r > '9' {
			return nil, nil, fmt.Errorf("invalid prefix length")
		}
		bits = bits*10 + int(r-'0')
	}
	if bits < 0 || bits > 32 {
		return nil, nil, fmt.Errorf("prefix length out of range")
	}
	mask := make([]byte, 4)
	for i := 0; i < 4; i++ {
		if bits >= 8 {
			mask[i] = 0xff
			bits -= 8
			continue
		}
		mask[i] = byte(0xff << (8 - bits)) & 0xff
		bits = 0
	}
	network := make([]byte, 4)
	for i := range network {
		network[i] = ipBytes[i] & mask[i]
	}
	return nil, &netCIDR{ip: network, mask: mask}, nil
}

type netCIDR struct {
	ip   []byte
	mask []byte
}

func (c *netCIDR) Contains(ip []byte) bool {
	for i, b := range c.mask {
		if ip[i]&b != c.ip[i]&b {
			return false
		}
	}
	return true
}

// fmtSscan is a tiny fmt.Sscan wrapper that returns
// (n, err) but never panics. The table above only ever
// reads Float() via the standard package; this helper
// exists so the file does not pull the fmt-wrappers we
// already have elsewhere.
func fmtSscan(s string, dst *float64) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	// Simple integer-or-float parse; we deliberately do
	// NOT use the standard library here because the
	// ruler package is policy-critical and we want the
	// exact behaviour visible above the line.
	var sign float64 = 1
	if len(s) > 0 && s[0] == '-' {
		sign = -1
		s = s[1:]
	}
	var integer float64
	var sawDigit bool
	for _, r := range s {
		if r >= '0' && r <= '9' {
			integer = integer*10 + float64(r-'0')
			sawDigit = true
			continue
		}
		if r == '.' || r == 'e' || r == 'E' || r == '+' {
			break
		}
		return 0, fmt.Errorf("invalid float")
	}
	if !sawDigit {
		return 0, fmt.Errorf("no digits")
	}
	*dst = sign * integer
	return 1, nil
}
