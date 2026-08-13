package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/netip"
	"strings"
	"time"

	"go.uber.org/zap"
)

// H-6: authentication abuse protection.
//
// A single per-IP counter is trivially bypassed in both directions:
//
//   - One host can walk a password against MANY accounts. A per-account
//     counter is what stops that, and it must be independent of the IP or
//     rotating source addresses defeats it.
//   - A botnet can walk many passwords against ONE account, each source
//     staying under the per-IP budget. A per-account counter stops that too,
//     which is why it must be global (Redis) in a multi-node deployment.
//
// So three dimensions are enforced together, and a request is refused when
// ANY of them is exhausted:
//
//	ip      — this client address, regardless of which account it targets
//	account — this normalized account identifier, regardless of source IP
//	combo   — this (ip, account) pair, the tightest of the three
//
// Identifiers are hashed before they become keys. The account dimension would
// otherwise put plaintext email addresses into Redis and into any key dump,
// which is exactly the kind of quiet PII spill this remediation is meant to
// avoid. Hashing is not a confidentiality control here (the space is
// enumerable) — it bounds key length and keeps plaintext identifiers out of
// the datastore and logs.

// AuthLimitPolicy holds the thresholds for one window.
type AuthLimitPolicy struct {
	// IPMax bounds attempts from one client address. It is deliberately
	// looser than the account budget because NAT and corporate egress make
	// legitimate users share addresses.
	IPMax int
	// AccountMax bounds attempts against one account from anywhere.
	AccountMax int
	// ComboMax bounds attempts for one (ip, account) pair.
	ComboMax int
	// Window is the fixed window all three dimensions share.
	Window time.Duration
}

// DefaultAuthLimitPolicy is the production policy.
//
// IPMax > AccountMax is intentional: the account budget is the one an
// attacker cannot evade, so it is the tightest; the IP budget only needs to
// stop a single host from hammering the endpoint, and being looser keeps
// shared NAT egress usable.
func DefaultAuthLimitPolicy() AuthLimitPolicy {
	return AuthLimitPolicy{
		IPMax:      20,
		AccountMax: 5,
		ComboMax:   5,
		Window:     15 * time.Minute,
	}
}

// AuthLimitDecision reports the outcome of a check.
type AuthLimitDecision struct {
	Allowed bool
	// Dimension names which budget was exhausted ("ip", "account",
	// "combo"), for audit and metrics. It is NEVER returned to the caller:
	// telling a client which dimension tripped leaks whether an account
	// exists and is under attack.
	Dimension string
	// RetryAfter is the conservative hint for the client.
	RetryAfter time.Duration
	// Degraded is true when the primary store failed and the local fallback
	// answered instead.
	Degraded bool
}

// AuthLimiter enforces the three dimensions above.
type AuthLimiter struct {
	primary  LimitStore
	fallback LimitStore
	policy   AuthLimitPolicy
	logger   *zap.Logger
	prefix   string
}

// NewAuthLimiter builds a limiter over primary, with an in-process fallback
// used only when primary errors.
func NewAuthLimiter(primary LimitStore, policy AuthLimitPolicy, logger *zap.Logger) *AuthLimiter {
	if primary == nil {
		primary = NewMemoryLimitStore()
	}
	if policy.Window <= 0 || policy.IPMax <= 0 || policy.AccountMax <= 0 || policy.ComboMax <= 0 {
		policy = DefaultAuthLimitPolicy()
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AuthLimiter{
		primary:  primary,
		fallback: NewMemoryLimitStore(),
		policy:   policy,
		logger:   logger,
		prefix:   "authlimit",
	}
}

// Policy exposes the active thresholds.
func (a *AuthLimiter) Policy() AuthLimitPolicy { return a.policy }

// SetFallback lets tests inject a faulting fallback store to prove the
// fail-closed path. Production never calls it.
func (a *AuthLimiter) SetFallback(fb LimitStore) { a.fallback = fb }

// NormalizeAccount canonicalises an account identifier so the same human
// account always maps to the same key.
//
// Without this, "Admin@Example.com ", "admin@example.com" and
// "ADMIN@EXAMPLE.COM" are three different budgets and the account dimension
// is bypassed by simply varying case. Only case and surrounding whitespace are
// normalised: the local part is case-sensitive per RFC 5321, but every mail
// system this platform targets treats it case-insensitively, and the login
// lookup itself is case-insensitive, so the limiter must match that reality.
func NormalizeAccount(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// hashIdentifier returns a bounded, non-reversible-at-a-glance key fragment.
//
// SHA-256 truncated to 128 bits (32 hex characters) keeps keys bounded and
// keeps plaintext emails/IPs out of the datastore. The 0x1f separator is a
// byte that can never appear in a normalized identifier, so "a"+"b" can never
// collide with "ab".
func hashIdentifier(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:16])
}

// NormalizeClientIP validates and canonicalises a client address.
//
// It accepts only what netip.ParseAddr accepts, so a malformed or absent
// value can never become a key. An unparseable address collapses to the fixed
// sentinel "invalid" — one shared bucket — rather than being trusted as a
// distinct identity, so garbage input cannot be used to mint unlimited fresh
// budgets. IPv6 addresses are normalised to their canonical form so
// "2001:DB8::1" and "2001:db8:0:0:0:0:0:1" share one budget.
//
// A host:port form is tolerated (bracketed IPv6 and single-colon IPv4 forms).
// bare IPs — including bare IPv6 with multiple colons — are passed to
// netip.ParseAddr directly and never split with net.SplitHostPort.
func NormalizeClientIP(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "invalid"
	}
	switch {
	case strings.HasPrefix(trimmed, "["):
		// Bracketed IPv6: "[2001:db8::1]" (no port), "[2001:db8::1]:443".
		if host, port, err := net.SplitHostPort(trimmed); err == nil {
			if isNumericPort(port) {
				trimmed = host
			}
		} else if end := strings.LastIndex(trimmed, "]"); end > 0 {
			trimmed = trimmed[1:end]
		}
	case strings.Count(trimmed, ":") == 1:
		// Host:port with an IPv4 (or hostname) address: "1.2.3.4:5555".
		// The port must be numeric — "1.2.3.4:notaport" must NOT be
		// silently accepted as "1.2.3.4".
		if host, port, err := net.SplitHostPort(trimmed); err == nil && isNumericPort(port) {
			trimmed = host
		}
	}
	addr, err := netip.ParseAddr(trimmed)
	if err != nil {
		return "invalid"
	}
	return addr.String()
}

func isNumericPort(port string) bool {
	if port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (a *AuthLimiter) ipKey(ip string) string {
	return a.prefix + ":ip:" + hashIdentifier(ip)
}

func (a *AuthLimiter) accountKey(account string) string {
	return a.prefix + ":acct:" + hashIdentifier(account)
}

func (a *AuthLimiter) comboKey(ip, account string) string {
	return a.prefix + ":combo:" + hashIdentifier(ip, account)
}

// incr runs against the primary store and degrades to the local fallback on
// error.
//
// This is the "fail securely without a global outage" rule: failing CLOSED on
// a Redis blip would lock every user out of the platform, turning a cache
// outage into a total authentication outage; failing OPEN would remove the
// control exactly when an attacker might be causing the outage. Falling back
// to a per-process counter keeps a real budget in force (tighter than none,
// looser than a platform-wide denial) and is loudly logged. Only when BOTH
// stores fail does the check refuse the request (see Check).
func (a *AuthLimiter) incr(ctx context.Context, key string) (int64, bool, error) {
	count, err := a.primary.Incr(ctx, key, a.policy.Window)
	if err == nil {
		return count, false, nil
	}
	a.logger.Error("auth limiter primary store unavailable; using degraded in-process budget",
		zap.Error(err))
	count, ferr := a.fallback.Incr(ctx, key, a.policy.Window)
	if ferr != nil {
		return 0, true, ferr
	}
	return count, true, nil
}

// Check consumes one attempt across all three dimensions and reports whether
// the request may proceed.
//
// Every dimension is incremented even when an earlier one already refused the
// request. Short-circuiting would let an attacker who has exhausted the cheap
// IP budget keep the account budget pristine, so a later distributed attempt
// against the same account would start from zero.
//
// account may be empty (endpoints with no account in the request, e.g. MFA
// verification); the account and combo dimensions are then skipped and only
// the IP budget applies.
func (a *AuthLimiter) Check(ctx context.Context, rawIP, rawAccount string) AuthLimitDecision {
	ip := NormalizeClientIP(rawIP)
	account := NormalizeAccount(rawAccount)

	decision := AuthLimitDecision{Allowed: true, RetryAfter: a.policy.Window}

	ipCount, degraded, err := a.incr(ctx, a.ipKey(ip))
	decision.Degraded = decision.Degraded || degraded
	if err != nil {
		// Both stores failed. Refuse: an authentication endpoint with no
		// working budget must not be left unprotected.
		a.logger.Error("auth limiter unavailable in both stores; refusing", zap.Error(err))
		return AuthLimitDecision{Allowed: false, Dimension: "unavailable", RetryAfter: a.policy.Window, Degraded: true}
	}
	if ipCount > int64(a.policy.IPMax) {
		decision.Allowed = false
		decision.Dimension = "ip"
	}

	if account == "" {
		return decision
	}

	acctCount, degraded, err := a.incr(ctx, a.accountKey(account))
	decision.Degraded = decision.Degraded || degraded
	if err == nil && acctCount > int64(a.policy.AccountMax) && decision.Allowed {
		decision.Allowed = false
		decision.Dimension = "account"
	}

	comboCount, degraded, err := a.incr(ctx, a.comboKey(ip, account))
	decision.Degraded = decision.Degraded || degraded
	if err == nil && comboCount > int64(a.policy.ComboMax) && decision.Allowed {
		decision.Allowed = false
		decision.Dimension = "combo"
	}

	return decision
}

// ResetAccount clears the account and combo budgets after a genuine success.
//
// The IP budget is deliberately NOT cleared. An attacker who owns one valid
// account on a shared address would otherwise log in successfully to wipe the
// IP counter and resume spraying other accounts from the same host with a
// fresh budget every few attempts.
func (a *AuthLimiter) ResetAccount(ctx context.Context, rawIP, rawAccount string) {
	account := NormalizeAccount(rawAccount)
	if account == "" {
		return
	}
	ip := NormalizeClientIP(rawIP)
	if err := a.primary.Reset(ctx, a.accountKey(account)); err != nil {
		a.logger.Warn("auth limiter: account budget reset failed", zap.Error(err))
	}
	if err := a.primary.Reset(ctx, a.comboKey(ip, account)); err != nil {
		a.logger.Warn("auth limiter: combo budget reset failed", zap.Error(err))
	}
	_ = a.fallback.Reset(ctx, a.accountKey(account))
	_ = a.fallback.Reset(ctx, a.comboKey(ip, account))
}
