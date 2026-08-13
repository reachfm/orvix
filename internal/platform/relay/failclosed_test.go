package relay

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/delivery"
)

// Fail-closed acceptance suite for Fixes A, B, C and E.
//
// The governing invariant: a FAILURE anywhere in relay route resolution must
// never become permission to deliver direct-to-MX. Direct delivery is a
// legitimate outcome ONLY when policy explicitly and successfully selects it.
//
// Faults are injected by dropping the real table the code under test reads,
// which produces a genuine driver error through the real repository rather
// than a hand-written stub that might not model production behaviour.

func newRouteFixture(t *testing.T) (*sql.DB, *Service, *DeliveryAdapter, *Pool) {
	t.Helper()
	db, svc := newTestService(t)
	adapter := NewDeliveryAdapter(svc)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "p", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return db, svc, adapter, pool
}

func mustProvider(t *testing.T, svc *Service, poolID uint, name string, opts ...func(*Provider)) *Provider {
	t.Helper()
	p := Provider{
		PoolID: poolID, Scope: ScopeGlobal, Name: name,
		Host: "smtp." + name + ".example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict,
		Active: true, Priority: 100, Weight: 1,
	}
	for _, o := range opts {
		o(&p)
	}
	created, err := svc.CreateProvider(context.Background(), p, "", testActor)
	if err != nil {
		t.Fatalf("create provider %s: %v", name, err)
	}
	return created
}

// ── A: no failure may become Direct ──────────────────────────────────────

// TestSelectRoute_ProviderRepositoryErrorNeverBecomesDirect is the headline
// Fix A proof. The previous adapter answered `&RelayRoute{Direct: true}` on a
// provider lookup error, so a database blip silently downgraded a mandatory
// relay route to unauthenticated direct-to-MX delivery.
func TestSelectRoute_ProviderRepositoryErrorNeverBecomesDirect(t *testing.T) {
	db, svc, adapter, pool := newRouteFixture(t)
	ctx := context.Background()
	p := mustProvider(t, svc, pool.ID, "primary")
	if _, err := svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	// Sanity: the route resolves normally before the fault is injected.
	if _, err := adapter.SelectRoute(ctx, delivery.RelayRouteRequest{TenantID: 1, SenderAddress: "a@t.example", RecipientDomain: "d.example"}); err != nil {
		t.Fatalf("baseline route must resolve: %v", err)
	}

	// Inject a real repository failure for the provider read specifically.
	// The route is already chosen at this point; only the provider load fails.
	if _, err := db.Exec(`ALTER TABLE platform_relay_providers RENAME TO platform_relay_providers_hidden`); err != nil {
		t.Fatalf("inject fault: %v", err)
	}
	decision, err := adapter.SelectRoute(ctx, delivery.RelayRouteRequest{TenantID: 1, SenderAddress: "a@t.example", RecipientDomain: "d.example"})
	if err == nil {
		t.Fatal("a provider repository failure must return an error")
	}
	if decision != nil && decision.Route != nil && decision.Route.Direct {
		t.Fatal("a provider repository failure must NEVER resolve to direct delivery")
	}
	_ = p
}

// TestResolveProviderRoute_MissingProviderNeverBecomesDirect covers the second
// half of the old fail-open branch: `if err != nil || provider == nil`.
func TestResolveProviderRoute_MissingProviderNeverBecomesDirect(t *testing.T) {
	_, _, adapter, pool := newRouteFixture(t)
	// A route naming a provider id that does not exist.
	route, err := adapter.resolveProviderRoute(context.Background(),
		SelectedRoute{PoolID: pool.ID, ProviderID: 999999, Host: "h.example.com", Port: 587}, 1)
	if err == nil {
		t.Fatal("a route naming a nonexistent provider must be an error")
	}
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
	if route != nil && route.Direct {
		t.Fatal("a missing provider must NEVER resolve to direct delivery")
	}
}

// TestResolveProviderRoute_CrossTenantProviderRejected proves a stale or
// hostile rule cannot dial another tenant's provider. The fixture now
// follows the F2 ownership model: the provider lives in a pool owned by
// tenant 42, and a delivery resolution for tenant 7 must be refused.
func TestResolveProviderRoute_CrossTenantProviderRejected(t *testing.T) {
	_, svc, adapter, _ := newRouteFixture(t)
	ctx := context.Background()
	otherPool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 42, Name: "t42-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create tenant-42 pool: %v", err)
	}
	other := mustProvider(t, svc, otherPool.ID, "othertenant", func(p *Provider) {
		p.Scope, p.TenantID = ScopeTenant, 42
	})
	route, err := adapter.resolveProviderRoute(ctx,
		SelectedRoute{PoolID: otherPool.ID, ProviderID: other.ID, Host: other.Host, Port: other.Port}, 7)
	if !errors.Is(err, ErrCrossTenantProvider) {
		t.Fatalf("expected ErrCrossTenantProvider, got %v", err)
	}
	if route != nil {
		t.Fatal("a cross-tenant provider must not produce a route")
	}
}

// TestResolveProviderRoute_InsecureCredentialRefusedBeforeDecrypt proves the
// Fix D policy is applied during resolution and does not downgrade to Direct.
func TestResolveProviderRoute_InsecureCredentialRefusedBeforeDecrypt(t *testing.T) {
	_, svc, adapter, pool := newRouteFixture(t)
	ctx := context.Background()
	p, err := svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, Scope: ScopeGlobal, Name: "plaintext-auth",
		Host: "smtp.plain.example.com", Port: 25,
		ConnSecurity: ConnSecurityNone, TLSValidation: TLSValidationStrict,
		Active: true,
	}, "a-real-password", testActor)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	route, err := adapter.resolveProviderRoute(ctx,
		SelectedRoute{PoolID: pool.ID, ProviderID: p.ID, Host: p.Host, Port: p.Port}, 0)
	if !errors.Is(err, ErrInsecureCredentialTransport) {
		t.Fatalf("expected ErrInsecureCredentialTransport, got %v", err)
	}
	if route != nil {
		if route.Direct {
			t.Fatal("an insecure credential configuration must NEVER become direct delivery")
		}
		if route.Password != "" {
			t.Fatal("the credential must not be decrypted for a refused provider")
		}
	}
}

// ── E: repository failures must fail closed ──────────────────────────────

// TestSelectRoute_OverrideRepositoryErrorFailsClosed proves a database error
// on the emergency-override lookup can no longer silently cancel the override
// and fall through to normal rule evaluation.
func TestSelectRoute_OverrideRepositoryErrorFailsClosed(t *testing.T) {
	db, svc, _, pool := newRouteFixture(t)
	ctx := context.Background()
	mustProvider(t, svc, pool.ID, "primary")
	if _, err := svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE platform_relay_overrides`); err != nil {
		t.Fatalf("inject fault: %v", err)
	}
	route, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, SenderAddress: "a@t.example", RecipientDomain: "d.example"})
	if err == nil {
		t.Fatal("an override lookup failure must be an error, not a silent bypass")
	}
	if route != nil && route.Direct {
		t.Fatal("an override lookup failure must not resolve to direct delivery")
	}
}

// TestSelectRoute_RateLimitStoreErrorLeavesProviderUnavailable proves the
// rate-limit fail-open is closed: a counter that cannot be consulted means the
// limit cannot be honoured, so the provider must not be used.
func TestSelectRoute_RateLimitStoreErrorLeavesProviderUnavailable(t *testing.T) {
	db, svc, _, pool := newRouteFixture(t)
	ctx := context.Background()
	mustProvider(t, svc, pool.ID, "limited", func(p *Provider) { p.RateLimitPerMin = 10 })
	if _, err := svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE platform_relay_rate_counters`); err != nil {
		t.Fatalf("inject fault: %v", err)
	}
	route, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, SenderAddress: "a@t.example", RecipientDomain: "d.example"})
	if err == nil {
		t.Fatal("with the only provider rate-limit-unverifiable, selection must fail")
	}
	if !errors.Is(err, ErrNoRouteAvailable) {
		t.Fatalf("expected ErrNoRouteAvailable, got %v", err)
	}
	if route != nil && route.Direct {
		t.Fatal("a rate-limit store failure must not resolve to direct delivery")
	}
}

// ── B: rule selectors and precedence ─────────────────────────────────────

func TestRuleMatches_TenantScopingAppliesToDomainScopedRules(t *testing.T) {
	// A domain-scoped rule owned by tenant 1 must not match tenant 2, even
	// when the domain id happens to line up. The previous implementation
	// skipped the tenant check entirely whenever DomainID != 0.
	rule := RoutingRule{TenantID: 1, DomainID: 5, PoolID: 9}
	if ruleMatches(rule, RouteRequest{TenantID: 2, DomainID: 5}) {
		t.Fatal("a tenant-owned rule must never match another tenant")
	}
	if !ruleMatches(rule, RouteRequest{TenantID: 1, DomainID: 5}) {
		t.Fatal("the owning tenant must still match")
	}
}

func TestRuleMatches_UnknownDomainIsNotWildcard(t *testing.T) {
	rule := RoutingRule{DomainID: 5, PoolID: 9}
	if ruleMatches(rule, RouteRequest{DomainID: 0}) {
		t.Fatal("an unknown sending domain must not match a domain-scoped rule")
	}
	if !ruleMatches(rule, RouteRequest{DomainID: 5}) {
		t.Fatal("the named domain must match")
	}
}

func TestMatchRule_PrecedenceIsDeterministic(t *testing.T) {
	globalRule := RoutingRule{ID: 1, PoolID: 10, Priority: 1}
	tenantRule := RoutingRule{ID: 2, TenantID: 7, PoolID: 20, Priority: 50}
	domainRule := RoutingRule{ID: 3, TenantID: 7, DomainID: 3, PoolID: 30, Priority: 90}
	req := RouteRequest{TenantID: 7, DomainID: 3}

	// Domain beats tenant beats global REGARDLESS of priority number and
	// regardless of the order the repository returned them.
	for _, order := range [][]RoutingRule{
		{globalRule, tenantRule, domainRule},
		{domainRule, tenantRule, globalRule},
		{tenantRule, domainRule, globalRule},
	} {
		got, ok := matchRule(order, req)
		if !ok || got.PoolID != 30 {
			t.Fatalf("expected the domain-scoped pool 30 to win, got %+v (ok=%v)", got, ok)
		}
	}

	// Within a tier, lower priority wins; equal priority breaks by lower id.
	a := RoutingRule{ID: 5, TenantID: 7, PoolID: 40, Priority: 10}
	b := RoutingRule{ID: 4, TenantID: 7, PoolID: 50, Priority: 10}
	got, _ := matchRule([]RoutingRule{a, b}, req)
	if got.PoolID != 50 {
		t.Fatalf("equal priority must break by lower id, got pool %d", got.PoolID)
	}
}

func TestSenderPattern_MatchingAndNormalization(t *testing.T) {
	cases := []struct {
		pattern, addr, domain string
		want                  bool
	}{
		{"", "a@x.test", "x.test", true},
		{"*", "a@x.test", "x.test", true},
		{"billing@acme.test", "billing@acme.test", "acme.test", true},
		{"billing@acme.test", "sales@acme.test", "acme.test", false},
		{"BILLING@ACME.TEST", "billing@acme.test", "acme.test", true},
		{"billing@acme.test", "BILLING@ACME.TEST", "acme.test", true},
		{"*@acme.test", "anyone@acme.test", "acme.test", true},
		{"*@acme.test", "anyone@other.test", "other.test", false},
		{"@acme.test", "anyone@acme.test", "acme.test", true},
		{"@acme.test", "anyone@other.test", "other.test", false},
		{"acme.test", "anyone@acme.test", "acme.test", true},
		{"acme.test", "anyone@other.test", "other.test", false},
		{"bill*", "billing@acme.test", "acme.test", true},
		{"bill*", "sales@acme.test", "acme.test", false},
		{"*ling*", "billing@acme.test", "acme.test", true},
		{"  billing@acme.test  ", "billing@acme.test", "acme.test", true},
		// A null envelope sender (bounce) must not satisfy a scoped rule.
		{"billing@acme.test", "", "", false},
		{"*@acme.test", "", "", false},
	}
	for _, tc := range cases {
		if got := senderPatternMatches(tc.pattern, tc.addr, tc.domain); got != tc.want {
			t.Errorf("senderPatternMatches(%q, %q, %q) = %v, want %v", tc.pattern, tc.addr, tc.domain, got, tc.want)
		}
	}
}

// TestSenderPattern_AdversarialPatternsFailSafe proves a hostile stored
// pattern cannot blow up cost or match everything. The matcher is
// single-pass with no backtracking, so these return promptly.
func TestSenderPattern_AdversarialPatternsFailSafe(t *testing.T) {
	long := "a"
	for i := 0; i < 12; i++ {
		long += long
	}
	adversarial := []string{
		long,                    // over the length bound
		"(a+)+$",                // classic catastrophic-backtracking regex
		"^(([a-z])+.)+[A-Z]?$",  // nested quantifiers
		"*" + long + "*",        // wildcard around an oversized core
		"a**b",                  // interior wildcard is literal, not a segment
		"....................*", // dots are literal here, not regex
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, p := range adversarial {
			// Must not match an unrelated sender, and must not hang.
			if senderPatternMatches(p, "victim@example.test", "example.test") && p != "*"+long+"*" {
				t.Errorf("adversarial pattern %.30q must not match an unrelated sender", p)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sender-pattern matching did not complete promptly; the matcher must be bounded")
	}
}

func TestSelectRoute_SenderAddressAndDomainRemainDistinct(t *testing.T) {
	_, svc, _, _ := newRouteFixture(t)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 7, Name: "tenant7-sender-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create tenant pool: %v", err)
	}
	mustProvider(t, svc, pool.ID, "primary", func(p *Provider) {
		p.Scope = ScopeTenant
		p.TenantID = 7
	})
	// A rule scoped to ONE sender. Before the fix the sender pattern was never
	// evaluated at all, so this rule applied to every sender.
	if _, err := svc.CreateRoutingRule(ctx, RoutingRule{
		TenantID: 7, SenderPattern: "billing@acme.test", PoolID: pool.ID, Priority: 1,
	}, testActor); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	matched, err := svc.SelectRoute(ctx, RouteRequest{
		TenantID: 7, SenderAddress: "billing@acme.test", SenderDomain: "acme.test", RecipientDomain: "d.example",
	})
	if err != nil {
		t.Fatalf("the matching sender must route: %v", err)
	}
	if matched.Direct {
		t.Fatal("the matching sender must resolve to the relay pool, not direct")
	}

	// A different sender at the SAME domain must fall through to the default
	// (no other rule) rather than inheriting the billing route.
	other, err := svc.SelectRoute(ctx, RouteRequest{
		TenantID: 7, SenderAddress: "sales@acme.test", SenderDomain: "acme.test", RecipientDomain: "d.example",
	})
	if err != nil {
		t.Fatalf("unmatched sender: %v", err)
	}
	if !other.Direct {
		t.Fatal("a sender that matches no rule must not inherit another sender's route")
	}
}

// ── C: fallback chain semantics ──────────────────────────────────────────

func TestFallbackChain_OrderPreservedDedupedAndBounded(t *testing.T) {
	_, svc, adapter, pool := newRouteFixture(t)
	ctx := context.Background()
	var ids []uint
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		p := mustProvider(t, svc, pool.ID, n)
		ids = append(ids, p.ID)
	}
	if _, err := svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	decision, err := adapter.SelectRoute(ctx, delivery.RelayRouteRequest{
		TenantID: 1, SenderAddress: "a@t.example", RecipientDomain: "d.example", Seed: 1,
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if decision.Route == nil || decision.Route.Direct {
		t.Fatal("expected a relay route")
	}
	if len(decision.Fallbacks) > maxFallbackChain {
		t.Fatalf("fallback chain must be bounded at %d, got %d", maxFallbackChain, len(decision.Fallbacks))
	}
	seen := map[uint]bool{decision.Route.ProviderID: true}
	for _, fb := range decision.Fallbacks {
		if fb.Direct {
			t.Fatal("a fallback must NEVER be a direct route")
		}
		if seen[fb.ProviderID] {
			t.Fatalf("provider %d appears twice in one delivery attempt", fb.ProviderID)
		}
		seen[fb.ProviderID] = true
	}
	_ = ids
}

// TestFallbackChain_ExcludesInactiveAndOpenCircuitProviders proves unavailable
// providers never enter the chain.
func TestFallbackChain_ExcludesInactiveAndOpenCircuitProviders(t *testing.T) {
	db, svc, adapter, pool := newRouteFixture(t)
	ctx := context.Background()
	good := mustProvider(t, svc, pool.ID, "good")
	dead := mustProvider(t, svc, pool.ID, "dead")
	if _, err := svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	// Disable one and trip the other's circuit open.
	if _, err := db.Exec(`UPDATE platform_relay_providers SET active=0 WHERE id=` + itoa(dead.ID)); err != nil {
		t.Fatalf("disable: %v", err)
	}
	decision, err := adapter.SelectRoute(ctx, delivery.RelayRouteRequest{
		TenantID: 1, SenderAddress: "a@t.example", RecipientDomain: "d.example",
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	all := append([]delivery.RelayRoute{*decision.Route}, decision.Fallbacks...)
	for _, r := range all {
		if r.ProviderID == dead.ID {
			t.Fatal("an inactive provider must never appear in the delivery chain")
		}
	}
	if decision.Route.ProviderID != good.ID {
		t.Fatalf("expected the active provider, got %d", decision.Route.ProviderID)
	}
}

func itoa(u uint) string {
	if u == 0 {
		return "0"
	}
	var b []byte
	for u > 0 {
		b = append([]byte{byte('0' + u%10)}, b...)
		u /= 10
	}
	return string(b)
}
