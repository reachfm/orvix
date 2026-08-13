package relay

// F2 ownership regression tests. The independent review reproduced a
// cross-tenant provider injection: a tenant admin POSTed a provider
// into ANOTHER tenant's pool with tenant_id left 0, the row became
// "platform-shared", and the victim tenant's mail was routed through
// the attacker's SMTP endpoint.
//
// These tests exercise the REAL service/repository paths (real SQLite)
// and prove every layer of the repair:
//   - tenant-0 provider injection into a tenant pool is rejected;
//   - tenant providers cannot attach to another tenant's pool;
//   - tenant callers cannot create tenant-0 (global) providers;
//   - tenant providers cannot attach to platform-global pools (default
//     deny);
//   - the exact reproduced vulnerable row is excluded at delivery;
//   - cross-tenant list/test/rule/fallback references are denied;
//   - pool visibility is enforced during route selection.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestF2_InjectedTenantZeroProviderRejectedAtCreate reproduces the
// exact attack shape at the write boundary: a caller attempts to
// create a provider with tenant_id 0 (the handler no longer allows
// this — it stamps the authenticated tenant — but the service must
// reject it too, since the row would be treated as platform-shared by
// delivery).
func TestF2_InjectedTenantZeroProviderRejectedAtCreate(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	victimPool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "victim-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create victim pool: %v", err)
	}
	// The attacker's provider targets the VICTIM's pool with tenant_id 0.
	_, err = svc.CreateProvider(ctx, Provider{
		PoolID: victimPool.ID, Host: "attacker.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true,
	}, "attacker-secret", testActor)
	if !errors.Is(err, ErrCrossTenantPool) {
		t.Fatalf("expected ErrCrossTenantPool for tenant-0 provider into tenant pool, got %v", err)
	}
}

// TestF2_TenantProviderCannotJoinAnotherTenantsPool proves a tenant
// admin cannot attach a provider to another tenant's pool.
func TestF2_TenantProviderCannotJoinAnotherTenantsPool(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	victimPool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "victim-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create victim pool: %v", err)
	}
	_, err = svc.CreateProvider(ctx, Provider{
		PoolID: victimPool.ID, TenantID: 2, Host: "attacker.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true,
	}, "", testActor)
	if !errors.Is(err, ErrCrossTenantPool) {
		t.Fatalf("expected ErrCrossTenantPool for tenant-2 provider into tenant-1 pool, got %v", err)
	}
}

// TestF2_TenantProviderCannotAttachToGlobalPool proves the default-deny
// policy: a tenant-owned provider cannot join a platform-global pool.
func TestF2_TenantProviderCannotAttachToGlobalPool(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	globalPool, err := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "global", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create global pool: %v", err)
	}
	_, err = svc.CreateProvider(ctx, Provider{
		PoolID: globalPool.ID, TenantID: 3, Host: "attacker.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true,
	}, "", testActor)
	if !errors.Is(err, ErrCrossTenantPool) {
		t.Fatalf("expected ErrCrossTenantPool for tenant provider into global pool, got %v", err)
	}
}

// TestF2_TenantOwnedProviderJoinsOwnPool proves the legitimate case
// still works: a tenant-owned provider in the tenant's own pool.
func TestF2_TenantOwnedProviderJoinsOwnPool(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "t1-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	created, err := svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, TenantID: 1, Host: "relay.t1.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true,
	}, "", testActor)
	if err != nil {
		t.Fatalf("tenant provider into own pool must succeed: %v", err)
	}
	if created.TenantID != 1 {
		t.Fatalf("provider must carry its owner tenant, got %d", created.TenantID)
	}
}

// TestF2_InjectedLegacyRowExcludedAtDelivery proves the delivery
// boundary also excludes the exact reproduced vulnerable row even when
// it already exists in the database (created before the API fix, or by
// direct repository access): a tenant-0 provider attached to a
// tenant-owned pool must never be dialled for that tenant.
func TestF2_InjectedLegacyRowExcludedAtDelivery(t *testing.T) {
	_, svc := newTestService(t)
	adapter := NewDeliveryAdapter(svc)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "victim-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	// Simulate the legacy corruption: a tenant-0 provider row injected
	// directly into the repository, bypassing the (now fixed) service
	// boundary.
	legacy := Provider{
		PoolID: pool.ID, TenantID: 0, Host: "attacker.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true,
	}
	now := time.Now().UTC()
	legacy.CreatedAt, legacy.UpdatedAt = now, now
	legacy.CircuitState = CircuitClosed
	if err := svc.repo.CreateProvider(ctx, &legacy); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	route, err := adapter.resolveProviderRoute(ctx,
		SelectedRoute{PoolID: pool.ID, ProviderID: legacy.ID, Host: legacy.Host, Port: legacy.Port}, 1)
	if !errors.Is(err, ErrCrossTenantProvider) {
		t.Fatalf("expected ErrCrossTenantProvider for injected tenant-0 row, got %v", err)
	}
	if route != nil {
		t.Fatal("injected row must never produce a route")
	}
}

// TestF2_SelectRouteRejectsForeignPool proves route selection fails
// closed when a stale rule/override references another tenant's pool.
func TestF2_SelectRouteRejectsForeignPool(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	victimPool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "victim-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := svc.CreateProvider(ctx, Provider{
		PoolID: victimPool.ID, TenantID: 1, Host: "relay.victim.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true,
	}, "", testActor); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	// A stale rule (created directly) forces tenant 7's traffic through
	// tenant 1's pool.
	if err := svc.repo.CreateRoutingRule(ctx, &RoutingRule{TenantID: 7, PoolID: victimPool.ID, Priority: 1, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("seed stale rule: %v", err)
	}
	_, err = svc.SelectRoute(ctx, RouteRequest{TenantID: 7, RecipientDomain: "external.test"})
	if !errors.Is(err, ErrCrossTenantPool) {
		t.Fatalf("expected ErrCrossTenantPool from foreign pool selection, got %v", err)
	}
}

// TestF2_TenantScopedListAndTestDenied proves the tenant surface cannot
// enumerate or probe another tenant's relay configuration.
func TestF2_TenantScopedListAndTestDenied(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "t1-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	p, err := svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, TenantID: 1, Host: "relay.t1.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true,
	}, "", testActor)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	// Tenant 2 must not enumerate tenant 1's pool.
	if _, err := svc.ListPoolProvidersScoped(ctx, pool.ID, 2); !errors.Is(err, ErrCrossTenantPool) {
		t.Fatalf("expected ErrCrossTenantPool for foreign pool enumeration, got %v", err)
	}
	// Tenant 2 must not test tenant 1's provider.
	if _, err := svc.TestConnectionScoped(ctx, p.ID, 2, testActor); !errors.Is(err, ErrCrossTenantProvider) {
		t.Fatalf("expected ErrCrossTenantProvider for foreign provider test, got %v", err)
	}
	// Tenant 1 can enumerate its own pool.
	if list, err := svc.ListPoolProvidersScoped(ctx, pool.ID, 1); err != nil || len(list) != 1 {
		t.Fatalf("tenant 1 must enumerate its own pool: err=%v len=%d", err, len(list))
	}
}

// TestF2_RoutingRuleRejectsForeignPool proves rules cannot reference
// another tenant's pool even when the pool row predates the fix.
func TestF2_RoutingRuleRejectsForeignPool(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	victimPool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "victim-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	_, err = svc.CreateRoutingRule(ctx, RoutingRule{TenantID: 2, PoolID: victimPool.ID, Priority: 1}, testActor)
	if !errors.Is(err, ErrCrossTenantProvider) && !errors.Is(err, ErrCrossTenantPool) {
		t.Fatalf("expected cross-tenant pool rejection in rule creation, got %v", err)
	}
}

func TestF2_TenantRoutingRuleCannotBindPlatformGlobalPool(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	globalPool, err := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "global-policy", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create global pool: %v", err)
	}
	_, err = svc.CreateRoutingRule(ctx, RoutingRule{TenantID: 7, PoolID: globalPool.ID, Priority: 1}, testActor)
	if !errors.Is(err, ErrGlobalPoolRequiresPlatform) {
		t.Fatalf("tenant-authored rule must not bind a global pool, got %v", err)
	}
}

func TestF2_TenantOverrideCannotForcePlatformGlobalPool(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	globalPool, err := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "global-emergency", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create global pool: %v", err)
	}
	_, err = svc.SetEmergencyOverride(ctx, 7, globalPool.ID, 11, "tenant attempted global redirect", time.Now().UTC().Add(time.Hour))
	if !errors.Is(err, ErrGlobalPoolRequiresPlatform) {
		t.Fatalf("tenant override must not target a global pool, got %v", err)
	}
}

// TestF2_FallbackChainRejectsCrossTenantProvider proves fallback
// construction cannot route through another tenant's provider.
func TestF2_FallbackChainRejectsCrossTenantProvider(t *testing.T) {
	_, svc := newTestService(t)
	adapter := NewDeliveryAdapter(svc)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "t1-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, TenantID: 1, Host: "primary.t1.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true, Priority: 1,
	}, "", testActor); err != nil {
		t.Fatalf("create primary: %v", err)
	}
	foreign := Provider{
		PoolID: pool.ID, TenantID: 2, Host: "attacker.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true,
	}
	now := time.Now().UTC()
	foreign.CreatedAt, foreign.UpdatedAt = now, now
	foreign.CircuitState = CircuitClosed
	if err := svc.repo.CreateProvider(ctx, &foreign); err != nil {
		t.Fatalf("seed foreign fallback: %v", err)
	}
	if err := svc.repo.CreateRoutingRule(ctx, &RoutingRule{TenantID: 1, PoolID: pool.ID, Priority: 1, CreatedAt: now}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	// The primary route is tenant-1 owned; tenant-1 is the caller, so the
	// primary resolves. The foreign provider is attached to tenant 1's pool
	// with tenant_id 2 — its pool-vs-provider ownership mismatch must
	// exclude it from the fallback chain.
	route, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, SenderAddress: "a@t1.example.com", SenderDomain: "t1.example.com", RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if route.Direct {
		t.Fatal("tenant 1 must get its relay route, not direct delivery")
	}
	_ = adapter
	for _, fb := range route.Fallbacks {
		if fb.ProviderID == foreign.ID {
			t.Fatalf("foreign provider must not appear in the fallback chain: %+v", route.Fallbacks)
		}
	}
}
