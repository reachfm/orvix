package relay

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
	_ "modernc.org/sqlite"
)

type fakeSecretCodec struct{}

func (fakeSecretCodec) Encrypt(plaintext string) (string, error) { return "enc:" + plaintext, nil }
func (fakeSecretCodec) Decrypt(ciphertext string) (string, error) {
	if len(ciphertext) >= 4 && ciphertext[:4] == "enc:" {
		return ciphertext[4:], nil
	}
	return ciphertext, nil
}

func newTestService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	svc := NewService(repo, nil, nil).WithSecretCodec(fakeSecretCodec{})
	return db, svc
}

func TestNewAdministrativeServiceRequiresEvidenceDependencies(t *testing.T) {
	_, svc := newTestService(t)
	if _, err := NewAdministrativeService(svc.repo, nil, nil, nil); !errors.Is(err, ErrEvidenceUnavailable) {
		t.Fatalf("missing administrative evidence dependencies must fail closed, got %v", err)
	}
}

func TestNewRepositoryCheckedRejectsUnavailableDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRepositoryChecked(db); err == nil {
		t.Fatal("closed database must not silently fall back to the SQLite dialect")
	}
}

func TestCreateProvider_EncryptsCredentialAndRedactsOnList(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "primary", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	p, err := svc.CreateProvider(ctx, Provider{PoolID: pool.ID, Scope: ScopeGlobal, Name: "sendgrid", Host: "smtp.sendgrid.test", Port: 587, Username: "apikey", ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict}, "super-secret-password", testActor)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if p.SecretRef != "enc:super-secret-password" {
		t.Fatalf("expected the encrypted form to be stored, got %q", p.SecretRef)
	}

	listed, err := svc.ListProvidersRedacted(ctx, pool.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(listed))
	}
	if listed[0].SecretRef != "" {
		t.Fatal("SecretRef must never be present on a redacted provider")
	}
	if !listed[0].HasCredential {
		t.Fatal("expected HasCredential=true")
	}
}

func TestSelectRoute_NoRuleDefaultsToDirect(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	route, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if !route.Direct {
		t.Fatal("expected direct delivery when no routing rule or override matches")
	}
}

func TestSelectRoute_InternalOnlySenderNeverGetsARelayRoute(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "p", Strategy: StrategyPriority}, testActor)
	svc.CreateProvider(ctx, Provider{PoolID: pool.ID, Host: "relay.test", Port: 587, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor)

	_, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, RecipientDomain: "external.test", SenderMailAccessMode: "internal_only"})
	if err != ErrPolicyBlocked {
		t.Fatalf("expected ErrPolicyBlocked, got %v", err)
	}
}

func TestSelectRoute_DomainScopedRuleBeatsGlobal(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	globalPool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "global", Strategy: StrategyPriority}, testActor)
	domainPool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeDomain, Name: "domain-specific", Strategy: StrategyPriority}, testActor)
	svc.CreateProvider(ctx, Provider{PoolID: globalPool.ID, Host: "global.relay.test", Port: 587, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateProvider(ctx, Provider{PoolID: domainPool.ID, Host: "domain.relay.test", Port: 587, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateRoutingRule(ctx, RoutingRule{PoolID: globalPool.ID, Priority: 1}, testActor) // global default
	svc.CreateRoutingRule(ctx, RoutingRule{DomainID: 5, PoolID: domainPool.ID, Priority: 1}, testActor)

	route, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, DomainID: 5, RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if route.Host != "domain.relay.test" {
		t.Fatalf("expected the domain-scoped rule to win, got host=%q", route.Host)
	}
}

func TestSelectRoute_CircuitOpenProviderIsSkippedInFavorOfFallback(t *testing.T) {
	_, svc := newTestService(t)
	svc = svc.WithCircuitBreaker(NewCircuitBreaker(1, time.Hour)) // opens after 1 failure, long cooldown
	ctx := context.Background()
	pool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "p", Strategy: StrategyPriority}, testActor)
	primary, _ := svc.CreateProvider(ctx, Provider{PoolID: pool.ID, Host: "primary.relay.test", Port: 587, Priority: 1, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateProvider(ctx, Provider{PoolID: pool.ID, Host: "fallback.relay.test", Port: 587, Priority: 2, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor)

	// Trip the primary's circuit.
	if err := svc.RecordAttemptResult(ctx, primary.ID, false); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	route, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if route.Host != "fallback.relay.test" {
		t.Fatalf("expected failover to the fallback provider once primary's circuit opened, got host=%q", route.Host)
	}
}

func TestSelectRoute_AllProvidersUnavailableReturnsNoRoute(t *testing.T) {
	_, svc := newTestService(t)
	svc = svc.WithCircuitBreaker(NewCircuitBreaker(1, time.Hour))
	ctx := context.Background()
	pool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "p", Strategy: StrategyPriority}, testActor)
	only, _ := svc.CreateProvider(ctx, Provider{PoolID: pool.ID, Host: "only.relay.test", Port: 587, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor)
	svc.RecordAttemptResult(ctx, only.ID, false)

	_, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, RecipientDomain: "external.test"})
	if err != ErrNoRouteAvailable {
		t.Fatalf("expected ErrNoRouteAvailable, got %v", err)
	}
}

func TestSelectRoute_RateLimitExhaustionSkipsProvider(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "p", Strategy: StrategyPriority}, testActor)
	svc.CreateProvider(ctx, Provider{PoolID: pool.ID, Host: "limited.relay.test", Port: 587, Priority: 1, RateLimitPerMin: 2, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateProvider(ctx, Provider{PoolID: pool.ID, Host: "unlimited.relay.test", Port: 587, Priority: 2, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor)

	req := RouteRequest{TenantID: 1, RecipientDomain: "external.test"}
	r1, err := svc.SelectRoute(ctx, req)
	if err != nil || r1.Host != "limited.relay.test" {
		t.Fatalf("attempt 1: expected limited provider, got %+v err=%v", r1, err)
	}
	r2, err := svc.SelectRoute(ctx, req)
	if err != nil || r2.Host != "limited.relay.test" {
		t.Fatalf("attempt 2: expected limited provider (within limit), got %+v err=%v", r2, err)
	}
	r3, err := svc.SelectRoute(ctx, req)
	if err != nil {
		t.Fatalf("attempt 3: %v", err)
	}
	if r3.Host != "unlimited.relay.test" {
		t.Fatalf("attempt 3: expected fallback to unlimited provider once rate limit exhausted, got %+v", r3)
	}
}

func TestSelectRoute_DirectOnlyPoolNeverReturnsARelayProvider(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "direct", Strategy: StrategyPriority, DirectOnly: true}, testActor)
	svc.CreateRoutingRule(ctx, RoutingRule{PoolID: pool.ID, Priority: 1}, testActor)

	route, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if !route.Direct {
		t.Fatal("expected a direct_only pool to always resolve to direct delivery")
	}
}

func TestEmergencyOverride_ForcesRouteAndAutoExpires(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	globalPool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "global", Strategy: StrategyPriority}, testActor)
	overridePool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "override", Strategy: StrategyPriority}, testActor)
	svc.CreateProvider(ctx, Provider{PoolID: globalPool.ID, Host: "global.relay.test", Port: 587, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateProvider(ctx, Provider{PoolID: overridePool.ID, Host: "override.relay.test", Port: 587, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateRoutingRule(ctx, RoutingRule{PoolID: globalPool.ID, Priority: 1}, testActor)

	fixedNow := time.Now()
	fc := kernel.NewFixedClock(fixedNow)
	svc.clock = fc
	if _, err := svc.SetEmergencyOverride(ctx, 1, overridePool.ID, 99, "incident-1234", fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("set override: %v", err)
	}

	route, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if route.Host != "override.relay.test" {
		t.Fatalf("expected the emergency override pool to win over the routing rule, got host=%q", route.Host)
	}

	// Advance past expiry — the override must stop applying automatically.
	fc.Advance(2 * time.Hour)
	route, err = svc.SelectRoute(ctx, RouteRequest{TenantID: 1, RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route after expiry: %v", err)
	}
	if route.Host != "global.relay.test" {
		t.Fatalf("expected the override to have auto-expired back to the normal rule, got host=%q", route.Host)
	}
}

func TestEmergencyOverride_RejectsPastExpiry(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "p", Strategy: StrategyPriority}, testActor)
	_, err := svc.SetEmergencyOverride(ctx, 1, pool.ID, 99, "reason", time.Now().Add(-time.Hour))
	if err == nil {
		t.Fatal("expected an expiry in the past to be rejected")
	}
}

func TestEmergencyOverride_RejectsEmptyReason(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: "p", Strategy: StrategyPriority}, testActor)
	_, err := svc.SetEmergencyOverride(ctx, 1, pool.ID, 99, "", time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("expected an empty reason to be rejected")
	}
}

// TestSelectRoute_TenantIsolation proves a tenant-scoped rule for
// tenant 1 never applies to tenant 2's traffic (which must fall back
// to the global default / direct delivery).
func TestSelectRoute_TenantIsolation(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, _ := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, Name: "tenant1-pool", Strategy: StrategyPriority}, testActor)
	svc.CreateProvider(ctx, Provider{PoolID: pool.ID, Host: "tenant1.relay.test", Port: 587, ConnSecurity: ConnSecurityStartTLS, Active: true}, "", testActor)
	svc.CreateRoutingRule(ctx, RoutingRule{TenantID: 1, PoolID: pool.ID, Priority: 1}, testActor)

	routeT2, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 2, RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if !routeT2.Direct {
		t.Fatalf("expected tenant 2 to be unaffected by tenant 1's routing rule, got %+v", routeT2)
	}

	routeT1, err := svc.SelectRoute(ctx, RouteRequest{TenantID: 1, RecipientDomain: "external.test"})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if routeT1.Host != "tenant1.relay.test" {
		t.Fatalf("expected tenant 1's own rule to apply, got %+v", routeT1)
	}
}
