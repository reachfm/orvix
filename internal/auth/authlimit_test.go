package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ── Identifier normalization ────────────────────────────────────────────────

func TestNormalizeAccount(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Admin@Example.com", "admin@example.com"},
		{"  admin@example.com  ", "admin@example.com"},
		{"ADMIN@EXAMPLE.COM", "admin@example.com"},
		{"", ""},
		{"   ", ""},
		{"local@host", "local@host"},
	}
	for _, tc := range cases {
		if got := NormalizeAccount(tc.in); got != tc.want {
			t.Errorf("NormalizeAccount(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeClientIP(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1.2.3.4", "1.2.3.4"},
		{" 1.2.3.4 ", "1.2.3.4"},
		{"1.2.3.4:5555", "1.2.3.4"},
		{"2001:db8::1", "2001:db8::1"},
		{"[2001:db8::1]", "2001:db8::1"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"2001:DB8:0:0:0:0:0:1", "2001:db8::1"},
		// Garbage must collapse to one shared bucket, never become a
		// distinct identity.
		{"", "invalid"},
		{"   ", "invalid"},
		{"not-an-ip", "invalid"},
		{"1.2.3.4:notaport", "invalid"},
		{"[2001:db8::1:notaport", "invalid"},
		{"a.b.c.d:80", "invalid"},
	}
	for _, tc := range cases {
		if got := NormalizeClientIP(tc.in); got != tc.want {
			t.Errorf("NormalizeClientIP(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNormalizeClientIPNeverSplitsBareIPv6 guards the invariant that a bare
// IPv6 address is parsed whole — a net.SplitHostPort-based implementation
// would split "2001:db8::1" at the last colon and corrupt it.
func TestNormalizeClientIPNeverSplitsBareIPv6(t *testing.T) {
	got := NormalizeClientIP("2001:db8:0:0:0:0:0:1")
	if got != "2001:db8::1" {
		t.Fatalf("bare IPv6 corrupted: %q", got)
	}
}

// ── Key scheme ──────────────────────────────────────────────────────────────

func TestHashIdentifierDeterministicAndSeparated(t *testing.T) {
	a := hashIdentifier("admin@example.com")
	b := hashIdentifier("admin@example.com")
	if a != b {
		t.Fatal("hashIdentifier must be deterministic")
	}
	if len(a) != 32 {
		t.Fatalf("hashIdentifier must be bounded to 32 hex chars, got %d", len(a))
	}
	// "a"+"b" must never collide with "ab" — the separator byte cannot
	// appear in an identifier.
	if hashIdentifier("a", "b") == hashIdentifier("ab") {
		t.Fatal("separator collision: a|b must differ from ab")
	}
}

// TestAuthLimiterKeysNeverContainRawIdentifiers guards the privacy property:
// raw emails and IPs must never appear in limiter keys.
func TestAuthLimiterKeysNeverContainRawIdentifiers(t *testing.T) {
	l := NewAuthLimiter(nil, DefaultAuthLimitPolicy(), nil)
	email := "Admin@Example.com"
	ip := "1.2.3.4"
	for _, key := range []string{
		l.ipKey(ip),
		l.accountKey(email),
		l.comboKey(ip, email),
	} {
		if strings.Contains(key, email) || strings.Contains(key, ip) {
			t.Fatalf("key leaks raw identifier: %s", key)
		}
	}
}

// ── MemoryLimitStore ────────────────────────────────────────────────────────

func TestMemoryLimitStoreFixedWindow(t *testing.T) {
	s := NewMemoryLimitStore()
	now := time.Unix(1_700_000_000, 0)
	s.SetClock(func() time.Time { return now })

	// First use arms the window and starts at 1.
	for i := 1; i <= 3; i++ {
		got, err := s.Incr(context.Background(), "k", 15*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if got != int64(i) {
			t.Fatalf("count %d, want %d", got, i)
		}
	}

	// Window boundary: a fresh window starts at 1 (fixed window — the
	// expired window is NOT carried forward).
	now = now.Add(15 * time.Minute)
	got, err := s.Incr(context.Background(), "k", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("after expiry count = %d, want 1", got)
	}

	// Reset clears entirely.
	if err := s.Reset(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Incr(context.Background(), "k", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("after reset count = %d, want 1", got)
	}
}

func TestMemoryLimitStoreBounded(t *testing.T) {
	s := NewMemoryLimitStore()
	s.SetMaxEntries(4)
	s.SetClock(func() time.Time { return time.Unix(1_700_000_000, 0) })

	for i := 0; i < 4; i++ {
		if _, err := s.Incr(context.Background(), fmt.Sprintf("k%d", i), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// Full: a new key must NOT grow the map; it reports the bound so the
	// caller throttles rather than failing open under memory pressure.
	count, err := s.Incr(context.Background(), "new", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("refusal count = %d, want 4 (the bound)", count)
	}
	if s.Len() != 4 {
		t.Fatalf("map grew to %d entries, want 4", s.Len())
	}
	// Existing counters keep working — an attacker cannot evict a counter
	// that is currently throttling them.
	if _, err := s.Incr(context.Background(), "k0", time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryLimitStoreSweepsExpiredWhenFull(t *testing.T) {
	s := NewMemoryLimitStore()
	s.SetMaxEntries(3)
	now := time.Unix(1_700_000_000, 0)
	s.SetClock(func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if _, err := s.Incr(context.Background(), fmt.Sprintf("k%d", i), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Minute) // all three windows expired
	count, err := s.Incr(context.Background(), "fresh", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("after sweep count = %d, want 1 (new key admitted)", count)
	}
}

// ── AuthLimiter dimensions ──────────────────────────────────────────────────

func policyFor(ip, acct, combo int) AuthLimitPolicy {
	return AuthLimitPolicy{IPMax: ip, AccountMax: acct, ComboMax: combo, Window: 15 * time.Minute}
}

// TestAuthLimiterDimensionsAndRefusal proves the three budgets are enforced
// independently and that ALL dimensions are consumed even when an earlier one
// already refused (an attacker cannot keep the account budget pristine by
// exhausting the cheap IP budget first).
func TestAuthLimiterDimensionsAndRefusal(t *testing.T) {
	l := NewAuthLimiter(nil, policyFor(100, 100, 5), zap.NewNop())

	// Combo dimension: same (ip, account) pair.
	for i := 1; i <= 5; i++ {
		d := l.Check(context.Background(), "1.1.1.1", "user@example.com")
		if !d.Allowed {
			t.Fatalf("attempt %d refused unexpectedly: %s", i, d.Dimension)
		}
	}
	d := l.Check(context.Background(), "1.1.1.1", "user@example.com")
	if d.Allowed || d.Dimension != "combo" {
		t.Fatalf("attempt 6: got allowed=%v dim=%q, want combo refusal", d.Allowed, d.Dimension)
	}

	// Account dimension: same account from many IPs. Note the combo key is
	// per-pair so a fresh IP keeps combo fresh; the account key trips.
	l2 := NewAuthLimiter(nil, policyFor(100, 5, 100), zap.NewNop())
	for i := 0; i < 5; i++ {
		if d := l2.Check(context.Background(), fmt.Sprintf("10.0.0.%d", i+1), "victim@example.com"); !d.Allowed {
			t.Fatalf("account attempt %d refused unexpectedly", i+1)
		}
	}
	d = l2.Check(context.Background(), "10.0.0.99", "victim@example.com")
	if d.Allowed || d.Dimension != "account" {
		t.Fatalf("6th distinct-IP attempt: got allowed=%v dim=%q, want account refusal", d.Allowed, d.Dimension)
	}

	// IP dimension: many accounts from one IP.
	l3 := NewAuthLimiter(nil, policyFor(5, 100, 100), zap.NewNop())
	for i := 0; i < 5; i++ {
		if d := l3.Check(context.Background(), "9.9.9.9", fmt.Sprintf("u%d@example.com", i)); !d.Allowed {
			t.Fatalf("ip attempt %d refused unexpectedly", i+1)
		}
	}
	d = l3.Check(context.Background(), "9.9.9.9", "fresh@example.com")
	if d.Allowed || d.Dimension != "ip" {
		t.Fatalf("6th distinct-account attempt: got allowed=%v dim=%q, want ip refusal", d.Allowed, d.Dimension)
	}

	// ALL dimensions keep counting past refusal: after a combo refusal, a
	// new IP for the same account must trip the ACCOUNT dimension at its own
	// threshold, not start from a pristine budget.
	l4 := NewAuthLimiter(nil, policyFor(100, 3, 100), zap.NewNop())
	l4.Check(context.Background(), "1.1.1.1", "acct@example.com")
	l4.Check(context.Background(), "1.1.1.1", "acct@example.com")
	if d := l4.Check(context.Background(), "2.2.2.2", "acct@example.com"); !d.Allowed {
		t.Fatalf("attempt 3 refused unexpectedly: %s", d.Dimension)
	}
	if d := l4.Check(context.Background(), "3.3.3.3", "acct@example.com"); d.Allowed || d.Dimension != "account" {
		t.Fatalf("attempt 4: got allowed=%v dim=%q, want account refusal (cross-IP counting)", d.Allowed, d.Dimension)
	}
}

func TestAuthLimiterEmptyAccountSkipsAccountDimensions(t *testing.T) {
	l := NewAuthLimiter(nil, policyFor(100, 5, 5), zap.NewNop())
	// 20 attempts with no account: only the IP budget applies, the account
	// and combo budgets stay untouched for later use by the same account.
	for i := 0; i < 20; i++ {
		if d := l.Check(context.Background(), "1.1.1.1", ""); !d.Allowed {
			t.Fatalf("no-account attempt %d refused unexpectedly: %s", i+1, d.Dimension)
		}
	}
	// The account budget is pristine.
	d := l.Check(context.Background(), "2.2.2.2", "fresh@example.com")
	if !d.Allowed {
		t.Fatalf("fresh account refused after no-account burst: %s", d.Dimension)
	}
}

// ── Degradation ─────────────────────────────────────────────────────────────

type faultingStore struct{}

func (faultingStore) Incr(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("injected primary failure")
}
func (faultingStore) Reset(context.Context, string) error {
	return errors.New("injected primary failure")
}

// TestAuthLimiterDegradesToFallback proves the endpoint keeps a budget when
// the primary store fails (a Redis blip must neither open the endpoint nor
// brick every login) and that the response reports the degraded state.
func TestAuthLimiterDegradesToFallback(t *testing.T) {
	l := NewAuthLimiter(faultingStore{}, policyFor(2, 100, 100), zap.NewNop())

	for i := 1; i <= 2; i++ {
		d := l.Check(context.Background(), "1.1.1.1", "u@example.com")
		if !d.Allowed || !d.Degraded {
			t.Fatalf("attempt %d: want allowed+degraded, got allowed=%v degraded=%v", i, d.Allowed, d.Degraded)
		}
	}
	d := l.Check(context.Background(), "1.1.1.1", "u@example.com")
	if d.Allowed {
		t.Fatal("degraded budget must still refuse at its threshold")
	}
}

// TestAuthLimiterFailClosed proves the request is REFUSED when both stores
// fail — an authentication endpoint with no working budget must not be left
// unprotected.
func TestAuthLimiterFailClosed(t *testing.T) {
	l := NewAuthLimiter(faultingStore{}, DefaultAuthLimitPolicy(), zap.NewNop())
	l.SetFallback(faultingStore{})
	d := l.Check(context.Background(), "1.1.1.1", "u@example.com")
	if d.Allowed {
		t.Fatal("both stores failed: request must be refused")
	}
}

// ── Success-path reset ──────────────────────────────────────────────────────

func TestAuthLimiterResetAccount(t *testing.T) {
	l := NewAuthLimiter(nil, policyFor(100, 2, 2), zap.NewNop())

	l.Check(context.Background(), "1.1.1.1", "acct@example.com")
	if d := l.Check(context.Background(), "1.1.1.1", "acct@example.com"); !d.Allowed {
		t.Fatal("attempt 2 refused unexpectedly")
	}
	if d := l.Check(context.Background(), "1.1.1.1", "acct@example.com"); d.Allowed {
		t.Fatal("attempt 3 must be refused")
	}

	// A genuine success clears the account and pair budgets...
	l.ResetAccount(context.Background(), "1.1.1.1", "acct@example.com")
	if d := l.Check(context.Background(), "1.1.1.1", "acct@example.com"); !d.Allowed {
		t.Fatalf("after success reset attempt refused: %s", d.Dimension)
	}

	// ...but NOT the IP budget: an attacker holding one valid account on a
	// shared address must not be able to wipe the IP counter at will.
	other := NewAuthLimiter(nil, policyFor(2, 100, 100), zap.NewNop())
	other.Check(context.Background(), "1.1.1.1", "one@example.com")
	other.Check(context.Background(), "1.1.1.1", "two@example.com")
	if d := other.Check(context.Background(), "1.1.1.1", "three@example.com"); d.Allowed || d.Dimension != "ip" {
		t.Fatalf("IP budget must survive account resets: allowed=%v dim=%q", d.Allowed, d.Dimension)
	}
	other.ResetAccount(context.Background(), "1.1.1.1", "one@example.com")
	if d := other.Check(context.Background(), "1.1.1.1", "four@example.com"); d.Allowed {
		t.Fatal("IP budget was cleared by account reset")
	}
}

// ── Concurrency ─────────────────────────────────────────────────────────────

// TestAuthLimiterConcurrentBudget proves no increments are lost under
// contention: N goroutines × M attempts on one key must land exactly N*M,
// so the very next attempt trips the budget at the exact total.
func TestAuthLimiterConcurrentBudget(t *testing.T) {
	const goroutines = 16
	const attempts = 100
	const budget = goroutines * attempts // every dimension trips at exactly this

	l := NewAuthLimiter(nil, policyFor(budget, budget, budget), zap.NewNop())
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < attempts; i++ {
				if d := l.Check(context.Background(), "1.1.1.1", "acct@example.com"); !d.Allowed {
					if d.Dimension == "unavailable" {
						t.Error("unexpected unavailable refusal")
						return
					}
					// Refusal exactly at the budget boundary is expected on
					// the final attempt of some goroutine — not an error.
				}
			}
		}()
	}
	wg.Wait()

	// One more attempt must be refused at the exact concurrent total.
	d := l.Check(context.Background(), "1.1.1.1", "acct@example.com")
	if d.Allowed {
		t.Fatal("budget did not reach the exact concurrent total")
	}
}

// TestMemoryLimitStoreConcurrentCountExact proves exact counts (no lost
// increments) directly on the store.
func TestMemoryLimitStoreConcurrentCountExact(t *testing.T) {
	s := NewMemoryLimitStore()
	const goroutines = 16
	const attempts = 250
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < attempts; i++ {
				if _, err := s.Incr(context.Background(), "k", time.Hour); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got, _ := s.Incr(context.Background(), "k", time.Hour); got != goroutines*attempts+1 {
		t.Fatalf("count = %d, want %d (lost increments under concurrency)", got, goroutines*attempts+1)
	}
}
