package mailpolicy

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// ── Pure resolution tests ──────────────────────────────────────────

func TestResolveEffectiveMode_ExplicitMailboxModeWins(t *testing.T) {
	cases := []struct {
		mailbox string
		domain  string
		want    Mode
	}{
		{string(ModeInternalOnly), string(ModeInternalExternal), ModeInternalOnly},
		{string(ModeInternalExternal), string(ModeInternalOnly), ModeInternalExternal},
		{string(ModeInternalOnly), "corrupt", ModeInternalOnly},
		{string(ModeInternalExternal), "", ModeInternalExternal},
	}
	for _, tc := range cases {
		got, corrupt := ResolveEffectiveMode(tc.mailbox, tc.domain)
		if corrupt != "" {
			t.Fatalf("mailbox=%q domain=%q: unexpected corrupt event %q", tc.mailbox, tc.domain, corrupt)
		}
		if got.Effective != tc.want {
			t.Fatalf("mailbox=%q domain=%q: effective=%s want %s", tc.mailbox, tc.domain, got.Effective, tc.want)
		}
	}
}

func TestResolveEffectiveMode_InheritResolvesThroughDomain(t *testing.T) {
	got, _ := ResolveEffectiveMode(string(ModeInherit), string(ModeInternalOnly))
	if got.Effective != ModeInternalOnly {
		t.Fatalf("inherit+internal_only domain: effective=%s want internal_only", got.Effective)
	}
	got, _ = ResolveEffectiveMode(string(ModeInherit), string(ModeInternalExternal))
	if got.Effective != ModeInternalExternal {
		t.Fatalf("inherit+internal_external domain: effective=%s want internal_external", got.Effective)
	}
	// Empty mailbox value (pre-column row) is the same as inherit.
	got, _ = ResolveEffectiveMode("", string(ModeInternalOnly))
	if got.Effective != ModeInternalOnly {
		t.Fatalf("empty mailbox+internal_only domain: effective=%s want internal_only", got.Effective)
	}
	// Empty domain value is the established pre-column default.
	got, _ = ResolveEffectiveMode(string(ModeInherit), "")
	if got.Effective != ModeInternalExternal {
		t.Fatalf("inherit+empty domain: effective=%s want internal_external", got.Effective)
	}
}

func TestResolveEffectiveMode_CorruptDomainFailsClosed(t *testing.T) {
	got, event := ResolveEffectiveMode(string(ModeInherit), "external_only")
	if got.Effective != ModeInternalOnly {
		t.Fatalf("corrupt domain: effective=%s want internal_only (fail closed)", got.Effective)
	}
	if event == "" {
		t.Fatal("corrupt domain must emit a safe observable event")
	}
	// The event must never carry the raw value.
	if got.Configured == ModeInherit && event == "external_only" {
		t.Fatal("security event leaked the corrupt value")
	}
}

func TestResolveEffectiveMode_ConfiguredVsEffectiveNeverConfused(t *testing.T) {
	got, _ := ResolveEffectiveMode(string(ModeInherit), string(ModeInternalOnly))
	if got.Configured != ModeInherit {
		t.Fatalf("configured=%s want inherit", got.Configured)
	}
	if got.Effective != ModeInternalOnly {
		t.Fatalf("effective=%s want internal_only", got.Effective)
	}
}

// ── Fake store for pure policy logic ───────────────────────────────

type fakeStore struct {
	mu           sync.Mutex
	identities   map[string]SenderIdentity
	local        map[string]bool
	rcptModes    map[string]EffectiveMode
	failIdentity bool
	failLocal    bool
	failRcptMode bool
}

func (f *fakeStore) SenderIdentity(_ context.Context, email string) (SenderIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failIdentity {
		return SenderIdentity{}, errors.New("db down")
	}
	id, ok := f.identities[email]
	if !ok {
		return SenderIdentity{}, ErrSenderUnknown
	}
	return id, nil
}

func (f *fakeStore) RecipientIsLocal(_ context.Context, addr string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failLocal {
		return false, errors.New("db down")
	}
	return f.local[addr], nil
}

func (f *fakeStore) RecipientEffectiveMode(_ context.Context, addr string) (EffectiveMode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRcptMode {
		return EffectiveMode{}, errors.New("db down")
	}
	m, ok := f.rcptModes[addr]
	if !ok {
		return EffectiveMode{}, ErrRecipientUnknown
	}
	return m, nil
}

func (f *fakeStore) IsLocalDomain(_ context.Context, domain string) (bool, error) {
	return true, nil
}

func internalSender(email string) SenderIdentity {
	return SenderIdentity{MailboxID: 1, TenantID: 1, DomainID: 1, MailboxEmail: email, EffectiveMode: ModeInternalOnly}
}

func externalSender(email string) SenderIdentity {
	return SenderIdentity{MailboxID: 2, TenantID: 1, DomainID: 1, MailboxEmail: email, EffectiveMode: ModeInternalExternal}
}

func newFakePolicy(f *fakeStore) *Policy {
	return New(f, &recordingSink{})
}

type recordingSink struct {
	mu      sync.Mutex
	denied  []string
	corrupt []string
	unavail []string
}

func (r *recordingSink) PolicyDenied(_ context.Context, kind, sender, recipient string, reason DeniedReason, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied = append(r.denied, kind)
}

func (r *recordingSink) PolicyCorrupt(_ context.Context, kind, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.corrupt = append(r.corrupt, kind)
}

func (r *recordingSink) PolicyUnavailable(_ context.Context, kind, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unavail = append(r.unavail, kind)
}

func (r *recordingSink) deniedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.denied)
}

// ── Outbound enforcement matrix (pure policy) ──────────────────────

func TestCheckOutbound_EnforcementMatrix(t *testing.T) {
	cases := []struct {
		name        string
		sender      string
		recipients  []string
		wantAllowed bool
	}{
		{"internal authenticated mailbox -> local mailbox", "alice@internal.test", []string{"bob@internal.test"}, true},
		{"internal authenticated mailbox -> external address", "alice@internal.test", []string{"bob@external.test"}, false},
		{"external-enabled authenticated mailbox -> local mailbox", "carol@open.test", []string{"bob@internal.test"}, true},
		{"external-enabled authenticated mailbox -> external address", "carol@open.test", []string{"bob@external.test"}, true},
		{"internal mailbox via alias to external", "alice@internal.test", []string{"alias-target@external.test"}, false},
		{"internal mailbox via group member to external", "alice@internal.test", []string{"group-member@external.test"}, false},
		{"internal mailbox mixed recipients denies whole send", "alice@internal.test", []string{"bob@internal.test", "bob@external.test"}, false},
	}
	store := &fakeStore{
		identities: map[string]SenderIdentity{
			"alice@internal.test": internalSender("alice@internal.test"),
			"carol@open.test":     externalSender("carol@open.test"),
		},
		local: map[string]bool{
			"bob@internal.test": true,
		},
	}
	p := newFakePolicy(store)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := p.CheckOutbound(context.Background(), "test", tc.sender, tc.recipients)
			if d.Allowed != tc.wantAllowed {
				t.Fatalf("allowed=%v want %v (denied=%v unavailable=%v reason=%s)", d.Allowed, tc.wantAllowed, d.Denied, d.Unavailable, d.Reason)
			}
			if !tc.wantAllowed && !d.Denied {
				t.Fatalf("expected a stable denial, got %+v", d)
			}
		})
	}
}

func TestCheckOutbound_FailClosedOnStoreFailure(t *testing.T) {
	store := &fakeStore{
		identities: map[string]SenderIdentity{"alice@internal.test": internalSender("alice@internal.test")},
		local:      map[string]bool{},
		failLocal:  true,
	}
	p := newFakePolicy(store)
	d := p.CheckOutbound(context.Background(), "test", "alice@internal.test", []string{"bob@external.test"})
	if d.Allowed || !d.Unavailable {
		t.Fatalf("store failure must be unavailable (fail closed), got %+v", d)
	}
}

func TestCheckOutbound_UnknownSenderDenied(t *testing.T) {
	store := &fakeStore{identities: map[string]SenderIdentity{}}
	p := newFakePolicy(store)
	d := p.CheckOutbound(context.Background(), "test", "ghost@nowhere.test", []string{"bob@external.test"})
	if d.Allowed || !d.Denied {
		t.Fatalf("unknown sender must be denied, got %+v", d)
	}
}

func TestCheckOutbound_DeniedOperationEmitsEvent(t *testing.T) {
	store := &fakeStore{
		identities: map[string]SenderIdentity{"alice@internal.test": internalSender("alice@internal.test")},
		local:      map[string]bool{},
	}
	sink := &recordingSink{}
	p := New(store, sink)
	p.CheckOutbound(context.Background(), "webmail", "alice@internal.test", []string{"bob@external.test"})
	if sink.deniedCount() != 1 {
		t.Fatalf("expected exactly one policy-denied event, got %d", sink.deniedCount())
	}
}

// ── Inbound enforcement matrix (pure policy) ───────────────────────

func TestCheckInboundRecipient_EnforcementMatrix(t *testing.T) {
	internalRcpt := EffectiveMode{Configured: ModeInherit, Effective: ModeInternalOnly}
	openRcpt := EffectiveMode{Configured: ModeInherit, Effective: ModeInternalExternal}
	cases := []struct {
		name        string
		rcpt        string
		sender      Sender
		wantAllowed bool
	}{
		{"remote unauthenticated sender -> internal mailbox", "bob@internal.test", Sender{Authenticated: false}, false},
		{"trusted local authenticated sender -> internal mailbox", "bob@internal.test", Sender{Authenticated: true, MailboxEmail: "alice@internal.test"}, true},
		{"spoofed local MAIL FROM from remote path -> internal mailbox", "bob@internal.test", Sender{Authenticated: false, MailboxEmail: ""}, false},
		{"remote unauthenticated sender -> external-enabled mailbox", "bob@open.test", Sender{Authenticated: false}, true},
		{"external sender -> internal mailbox via alias to internal_only target", "alias@internal.test", Sender{Authenticated: false}, false},
	}
	store := &fakeStore{
		identities: map[string]SenderIdentity{"alice@internal.test": internalSender("alice@internal.test")},
		rcptModes: map[string]EffectiveMode{
			"bob@internal.test":   internalRcpt,
			"alias@internal.test": internalRcpt,
			"bob@open.test":       openRcpt,
		},
	}
	p := newFakePolicy(store)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := p.CheckInboundRecipient(context.Background(), "smtp_inbound", tc.sender, tc.rcpt)
			if d.Allowed != tc.wantAllowed {
				t.Fatalf("allowed=%v want %v (denied=%v unavailable=%v reason=%s)", d.Allowed, tc.wantAllowed, d.Denied, d.Unavailable, d.Reason)
			}
		})
	}
}

func TestCheckInboundRecipient_AuthenticatedButNonLocalSenderDenied(t *testing.T) {
	store := &fakeStore{
		identities: map[string]SenderIdentity{},
		rcptModes: map[string]EffectiveMode{
			"bob@internal.test": {Configured: ModeInherit, Effective: ModeInternalOnly},
		},
	}
	p := newFakePolicy(store)
	// The session claims authentication but the mailbox does not
	// resolve as a local mailbox — never trust the claim alone.
	d := p.CheckInboundRecipient(context.Background(), "smtp_inbound", Sender{Authenticated: true, MailboxEmail: "ghost@nowhere.test"}, "bob@internal.test")
	if d.Allowed {
		t.Fatalf("non-local authenticated sender must be denied, got %+v", d)
	}
}

func TestCheckInboundRecipient_FailClosedOnStoreFailure(t *testing.T) {
	store := &fakeStore{
		failRcptMode: true,
	}
	p := newFakePolicy(store)
	d := p.CheckInboundRecipient(context.Background(), "smtp_inbound", Sender{Authenticated: false}, "bob@internal.test")
	if d.Allowed || !d.Unavailable {
		t.Fatalf("store failure must be unavailable (fail closed), got %+v", d)
	}
}

func TestCheckInboundRecipient_UnknownRecipientAllowedForValidation(t *testing.T) {
	store := &fakeStore{rcptModes: map[string]EffectiveMode{}}
	p := newFakePolicy(store)
	// An address with no local mailbox target is left to the caller's
	// recipient validation (pre-existing 5.1.1 behavior).
	d := p.CheckInboundRecipient(context.Background(), "smtp_inbound", Sender{Authenticated: false}, "ghost@internal.test")
	if !d.Allowed {
		t.Fatalf("absent recipient must be left to caller validation, got %+v", d)
	}
}

// ── Concurrency ────────────────────────────────────────────────────

func TestCheckOutbound_ConcurrentCallsStable(t *testing.T) {
	store := &fakeStore{
		identities: map[string]SenderIdentity{"alice@internal.test": internalSender("alice@internal.test")},
		local:      map[string]bool{"bob@internal.test": true},
	}
	p := New(store, &recordingSink{})
	var wg sync.WaitGroup
	results := make([]bool, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := p.CheckOutbound(context.Background(), "test", "alice@internal.test", []string{"bob@internal.test"})
			results[i] = d.Allowed
		}(i)
	}
	wg.Wait()
	for i, allowed := range results {
		if !allowed {
			t.Fatalf("concurrent call %d: local-to-local must be allowed, got denied", i)
		}
	}
}

func TestCheckOutbound_ConcurrentDenialsStable(t *testing.T) {
	store := &fakeStore{
		identities: map[string]SenderIdentity{"alice@internal.test": internalSender("alice@internal.test")},
		local:      map[string]bool{},
	}
	p := New(store, &recordingSink{})
	var wg sync.WaitGroup
	denied := make([]bool, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := p.CheckOutbound(context.Background(), "test", "alice@internal.test", []string{"bob@external.test"})
			denied[i] = d.Denied
		}(i)
	}
	wg.Wait()
	for i, d := range denied {
		if !d {
			t.Fatalf("concurrent call %d: external recipient must be denied, got %+v", i, d)
		}
	}
}
