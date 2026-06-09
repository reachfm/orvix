package trust

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ── Trust Scores ─────────────────────────────────────────────

// TrustScore represents a numerical trust value.
type TrustScore int

const (
	TrustUnknown TrustScore = iota
	TrustLow
	TrustMedium
	TrustHigh
)

func (t TrustScore) String() string {
	switch t {
	case TrustLow:
		return "low"
	case TrustMedium:
		return "medium"
	case TrustHigh:
		return "high"
	default:
		return "unknown"
	}
}

// ── Trust Entities ───────────────────────────────────────────

type UserTrust struct {
	Username  string
	Score     TrustScore
	Reason    string
	UpdatedAt time.Time
}

type MailboxTrust struct {
	MailboxID uint
	Score     TrustScore
	Reason    string
	UpdatedAt time.Time
}

type DomainTrust struct {
	Domain    string
	Score     TrustScore
	Reason    string
	UpdatedAt time.Time
}

type IPTrust struct {
	IP        string
	Score     TrustScore
	Reason    string
	UpdatedAt time.Time
}

// ── Lockout Policy ──────────────────────────────────────────

type LockoutPolicy struct {
	MaxAttempts      int           // max failed attempts before lockout
	LockoutDuration  time.Duration // how long lockout lasts
	ProgressiveDelay time.Duration // initial delay per failed attempt
	DelayMultiplier  float64       // multiplier for each subsequent attempt
	MaxDelay         time.Duration // max delay cap
}

func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{
		MaxAttempts:      5,
		LockoutDuration:  15 * time.Minute,
		ProgressiveDelay: 1 * time.Second,
		DelayMultiplier:  1.5,
		MaxDelay:         30 * time.Second,
	}
}

// ── Rate Limit Policy ───────────────────────────────────────

type RateLimitPolicy struct {
	MaxPerSecond float64
	MaxPerMinute int
	MaxPerHour   int
	BurstSize    int
}

func DefaultRateLimitPolicy() RateLimitPolicy {
	return RateLimitPolicy{
		MaxPerSecond: 2.0,
		MaxPerMinute: 60,
		MaxPerHour:   500,
		BurstSize:    5,
	}
}

// ── Trust Engine ─────────────────────────────────────────────

type Engine struct {
	mu sync.RWMutex

	// Auth failures.
	authFailures map[string][]time.Time // key: "username" or "ip"
	lockouts     map[string]time.Time   // key: "username" or "ip"

	// Rate limiters.
	mailboxLimiters map[uint]*rateCounter
	domainLimiters  map[string]*rateCounter
	ipLimiters      map[string]*rateCounter

	// Trust scores.
	userTrust    map[string]*UserTrust
	mailboxTrust map[uint]*MailboxTrust
	domainTrust  map[string]*DomainTrust
	ipTrust      map[string]*IPTrust

	// Detectable abuse events.
	outboundCounts map[string]*windowCounter // key: "sender_domain"
	rejectCounts   map[string]*windowCounter // key: "sender_domain"

	policy LockoutPolicy
	rlp    RateLimitPolicy

	nowFn func() time.Time
	repo  *Repository
}

func NewEngine() *Engine {
	return NewEngineWithRepo(nil)
}

func NewEngineWithRepo(repo *Repository) *Engine {
	return &Engine{
		authFailures:    make(map[string][]time.Time),
		lockouts:        make(map[string]time.Time),
		mailboxLimiters: make(map[uint]*rateCounter),
		domainLimiters:  make(map[string]*rateCounter),
		ipLimiters:      make(map[string]*rateCounter),
		userTrust:       make(map[string]*UserTrust),
		mailboxTrust:    make(map[uint]*MailboxTrust),
		domainTrust:     make(map[string]*DomainTrust),
		ipTrust:         make(map[string]*IPTrust),
		outboundCounts:  make(map[string]*windowCounter),
		rejectCounts:    make(map[string]*windowCounter),
		policy:          DefaultLockoutPolicy(),
		rlp:             DefaultRateLimitPolicy(),
		nowFn:           time.Now,
		repo:            repo,
	}
}

// SetRepository attaches a persistent repository.
func (e *Engine) SetRepository(repo *Repository) {
	e.repo = repo
}

// LoadFromDB loads persisted state from the repository.
func (e *Engine) LoadFromDB(ctx context.Context) error {
	if e.repo == nil {
		return nil
	}
	lockouts, err := e.repo.LoadLockouts(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	for k, v := range lockouts {
		e.lockouts[k] = v
	}
	e.mu.Unlock()

	// Load trust scores.
	scores, err := e.repo.LoadTrustScores(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.userTrust = scores.Users
	e.mailboxTrust = scores.Mailboxes
	e.domainTrust = scores.Domains
	e.ipTrust = scores.IPs
	e.mu.Unlock()
	return nil
}

// ── Authentication Protection ───────────────────────────────

// RecordAuthFailure records a failed login attempt and returns the lockout status.
func (e *Engine) RecordAuthFailure(key string) (lockedOut bool, delay time.Duration) {
	e.mu.Lock()

	if until, ok := e.lockouts[key]; ok {
		if e.nowFn().Before(until) {
			e.mu.Unlock()
			return true, 0
		}
		delete(e.lockouts, key)
	}

	now := e.nowFn()
	e.authFailures[key] = append(e.authFailures[key], now)

	cutoff := now.Add(-1 * time.Hour)
	var recent []time.Time
	for _, t := range e.authFailures[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	e.authFailures[key] = recent

	attempts := len(recent)

	if attempts >= e.policy.MaxAttempts {
		expiresAt := now.Add(e.policy.LockoutDuration)
		e.lockouts[key] = expiresAt
		r := e.repo
		e.mu.Unlock()
		if r != nil {
			r.SaveLockout(context.Background(), key, expiresAt)
		}
		return true, 0
	}

	e.mu.Unlock()

	delay = e.policy.ProgressiveDelay
	for i := 1; i < attempts; i++ {
		delay = time.Duration(float64(delay) * e.policy.DelayMultiplier)
	}
	if delay > e.policy.MaxDelay {
		delay = e.policy.MaxDelay
	}

	return false, delay
}

func (e *Engine) RecordAuthSuccess(key string) {
	e.mu.Lock()
	delete(e.authFailures, key)
	delete(e.lockouts, key)
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.DeleteLockout(context.Background(), key)
	}
}

func (e *Engine) IsLockedOut(key string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	until, ok := e.lockouts[key]
	if !ok {
		return false
	}
	return e.nowFn().Before(until)
}

// ── Rate Limiting ───────────────────────────────────────────

type rateCounter struct {
	mu    sync.Mutex
	count int
	reset time.Time
}

func (e *Engine) getMailboxRate(mailboxID uint) *rateCounter {
	if r, ok := e.mailboxLimiters[mailboxID]; ok {
		return r
	}
	r := &rateCounter{reset: e.nowFn().Add(1 * time.Minute)}
	e.mailboxLimiters[mailboxID] = r
	return r
}

func (e *Engine) getDomainRate(domain string) *rateCounter {
	if r, ok := e.domainLimiters[domain]; ok {
		return r
	}
	r := &rateCounter{reset: e.nowFn().Add(1 * time.Minute)}
	e.domainLimiters[domain] = r
	return r
}

func (e *Engine) getIPRate(ip string) *rateCounter {
	if r, ok := e.ipLimiters[ip]; ok {
		return r
	}
	r := &rateCounter{reset: e.nowFn().Add(1 * time.Minute)}
	e.ipLimiters[ip] = r
	return r
}

// AllowMailbox checks if a mailbox-level rate limit allows an operation.
func (e *Engine) AllowMailbox(mailboxID uint) bool {
	e.mu.Lock()
	r := e.getMailboxRate(mailboxID)
	e.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	now := e.nowFn()
	if now.After(r.reset) {
		r.count = 0
		r.reset = now.Add(1 * time.Minute)
	}
	r.count++
	return r.count <= e.rlp.MaxPerMinute
}

// AllowDomain checks domain-level rate limit.
func (e *Engine) AllowDomain(domain string) bool {
	e.mu.Lock()
	r := e.getDomainRate(domain)
	e.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	now := e.nowFn()
	if now.After(r.reset) {
		r.count = 0
		r.reset = now.Add(1 * time.Minute)
	}
	r.count++
	return r.count <= e.rlp.MaxPerMinute
}

// AllowIP checks IP-level rate limit.
func (e *Engine) AllowIP(ip string) bool {
	e.mu.Lock()
	r := e.getIPRate(ip)
	e.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	now := e.nowFn()
	if now.After(r.reset) {
		r.count = 0
		r.reset = now.Add(1 * time.Minute)
	}
	r.count++
	return r.count <= e.rlp.MaxPerMinute
}

// ── Outbound Abuse Detection ────────────────────────────────

type windowCounter struct {
	mu        sync.Mutex
	count     int
	window    time.Duration
	startTime time.Time
}

// RecordSend records an outbound send and detects send spikes.
// Returns (isSpike, isExplosion) based on configured thresholds.
func (e *Engine) RecordSend(senderDomain string) (isSpike bool) {
	e.mu.Lock()
	wc, ok := e.outboundCounts[senderDomain]
	if !ok {
		wc = &windowCounter{window: 5 * time.Minute, startTime: e.nowFn()}
		e.outboundCounts[senderDomain] = wc
	}
	e.mu.Unlock()

	wc.mu.Lock()
	defer wc.mu.Unlock()

	now := e.nowFn()
	if now.After(wc.startTime.Add(wc.window)) {
		wc.count = 0
		wc.startTime = now
	}
	wc.count++

	// Spike: >100 sends in 5 minutes.
	return wc.count > 100
}

func (e *Engine) RecordRemoteRejection(senderDomain string) bool {
	e.mu.Lock()
	wc, ok := e.rejectCounts[senderDomain]
	if !ok {
		wc = &windowCounter{window: 15 * time.Minute, startTime: e.nowFn()}
		e.rejectCounts[senderDomain] = wc
	}
	e.mu.Unlock()

	wc.mu.Lock()
	defer wc.mu.Unlock()

	now := e.nowFn()
	if now.After(wc.startTime.Add(wc.window)) {
		wc.count = 0
		wc.startTime = now
	}
	wc.count++

	// Storm: >20 rejections in 15 minutes.
	return wc.count > 20
}

// ── Trust Score Updates ────────────────────────────────────

func (e *Engine) SetUserTrust(username string, score TrustScore, reason string) {
	e.mu.Lock()
	e.userTrust[username] = &UserTrust{
		Username: username, Score: score, Reason: reason, UpdatedAt: e.nowFn(),
	}
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.SaveTrustScore(context.Background(), "user", username, reason, score)
	}
}

func (e *Engine) SetMailboxTrust(mailboxID uint, score TrustScore, reason string) {
	e.mu.Lock()
	e.mailboxTrust[mailboxID] = &MailboxTrust{
		MailboxID: mailboxID, Score: score, Reason: reason, UpdatedAt: e.nowFn(),
	}
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.SaveTrustScore(context.Background(), "mailbox", fmt.Sprintf("%d", mailboxID), reason, score)
	}
}

func (e *Engine) SetDomainTrust(domain string, score TrustScore, reason string) {
	e.mu.Lock()
	e.domainTrust[domain] = &DomainTrust{
		Domain: domain, Score: score, Reason: reason, UpdatedAt: e.nowFn(),
	}
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.SaveTrustScore(context.Background(), "domain", domain, reason, score)
	}
}

func (e *Engine) SetIPTrust(ip string, score TrustScore, reason string) {
	e.mu.Lock()
	e.ipTrust[ip] = &IPTrust{
		IP: ip, Score: score, Reason: reason, UpdatedAt: e.nowFn(),
	}
	r := e.repo
	e.mu.Unlock()
	if r != nil {
		r.SaveTrustScore(context.Background(), "ip", ip, reason, score)
	}
}

func (e *Engine) GetUserTrust(username string) *UserTrust {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.userTrust[username]
}

func (e *Engine) GetMailboxTrust(mailboxID uint) *MailboxTrust {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mailboxTrust[mailboxID]
}

func (e *Engine) GetDomainTrust(domain string) *DomainTrust {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.domainTrust[domain]
}

func (e *Engine) GetIPTrust(ip string) *IPTrust {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ipTrust[ip]
}

// ── Snapshot ────────────────────────────────────────────────

type Snapshot struct {
	Lockouts       int
	AuthFailures   int
	MailboxRates   int
	DomainRates    int
	IPRates        int
	UserTrusts     int
	MailboxTrusts  int
	DomainTrusts   int
	IPTrusts       int
	OutboundActive int
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return Snapshot{
		Lockouts:       len(e.lockouts),
		AuthFailures:   len(e.authFailures),
		MailboxRates:   len(e.mailboxLimiters),
		DomainRates:    len(e.domainLimiters),
		IPRates:        len(e.ipLimiters),
		UserTrusts:     len(e.userTrust),
		MailboxTrusts:  len(e.mailboxTrust),
		DomainTrusts:   len(e.domainTrust),
		IPTrusts:       len(e.ipTrust),
		OutboundActive: len(e.outboundCounts),
	}
}

// LockoutEntry represents a single locked-out key.
type LockoutEntry struct {
	Key       string    `json:"key"`
	ExpiresAt time.Time `json:"expiresAt"`
	Remaining string    `json:"remaining"`
}

// LockoutList returns all current lockouts.
func (e *Engine) LockoutList() []LockoutEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var list []LockoutEntry
	now := e.nowFn()
	for key, until := range e.lockouts {
		if now.Before(until) {
			list = append(list, LockoutEntry{
				Key:       key,
				ExpiresAt: until,
				Remaining: until.Sub(now).Round(time.Second).String(),
			})
		}
	}
	return list
}

// ClearLockout removes a lockout for a specific key.
func (e *Engine) ClearLockout(key string) bool {
	e.mu.Lock()
	if _, ok := e.lockouts[key]; ok {
		delete(e.lockouts, key)
		r := e.repo
		e.mu.Unlock()
		if r != nil {
			r.DeleteLockout(context.Background(), key)
		}
		return true
	}
	e.mu.Unlock()
	return false
}

// ── Needed for windowCounter — add mutex field ──────────────
