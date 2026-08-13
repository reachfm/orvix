package relay

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Phase 3A Fix F acceptance suite, executed against REAL PostgreSQL.
//
// Every defect this suite covers was invisible to the SQLite suites:
// LastInsertId is supported on SQLite and unsupported on PostgreSQL, so
// `id, _ := res.LastInsertId()` produced correct ids locally and a silent
// zero in production. These tests therefore assert on the ONE thing that
// distinguishes the two dialects — a real generated id read back from the row
// — plus the guarded-mutation and state-machine behaviour that rides on it.
//
// Gated on PGHOST + ORVIX_RUN_POSTGRES_DML_TEST=1 (the repository convention).
// The PostgreSQL Runtime DML workflow FAILS if any of these merely skip: a
// skipped acceptance test proves nothing.

// TestPostgresRelay_RoutingRuleCreateReturnsRealID is the direct regression
// for the CreateRoutingRule LastInsertId defect. On PostgreSQL the old code
// returned ID 0, so the rule could not be addressed for update or delete and
// id-based precedence tie-breaking had nothing real to work with.
func TestPostgresRelay_RoutingRuleCreateReturnsRealID(t *testing.T) {
	db, svc := newPostgresRelayService(t)
	ctx := context.Background()
	const tenant = uint(11)

	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: tenant, Name: uniqueName("pgpool"), Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if pool.ID == 0 {
		t.Fatal("pool insert must return a real generated id on PostgreSQL")
	}

	rule, err := svc.CreateRoutingRule(ctx, RoutingRule{
		TenantID: tenant, DomainID: 3, SenderPattern: "billing@acme.test",
		RecipientDomain: "partner.test", PoolID: pool.ID, Priority: 5,
	}, testActor)
	if err != nil {
		t.Fatalf("create routing rule: %v", err)
	}
	if rule.ID == 0 {
		t.Fatal("routing rule insert must return a real generated id (RETURNING id), got 0")
	}

	// The id must correspond to an actual row.
	var got uint
	if err := db.QueryRow(`SELECT id FROM platform_relay_routing_rules WHERE id = $1`, rule.ID).Scan(&got); err != nil {
		t.Fatalf("routing rule row not found for returned id %d: %v", rule.ID, err)
	}
	if got != rule.ID {
		t.Fatalf("row id %d != returned id %d", got, rule.ID)
	}

	// And the selectors must have round-tripped intact.
	var pattern, recipient string
	if err := db.QueryRow(`SELECT sender_pattern, recipient_domain FROM platform_relay_routing_rules WHERE id = $1`, rule.ID).
		Scan(&pattern, &recipient); err != nil {
		t.Fatalf("read back selectors: %v", err)
	}
	if pattern != "billing@acme.test" || recipient != "partner.test" {
		t.Fatalf("selectors did not round-trip: pattern=%q recipient=%q", pattern, recipient)
	}
}

// TestPostgresRelay_OverrideLifecycle proves creation returns a real id and
// that activation, expiry and revocation all work on PostgreSQL. Revocation
// was impossible before this fix because the id was always 0.
func TestPostgresRelay_OverrideLifecycle(t *testing.T) {
	db, svc := newPostgresRelayService(t)
	ctx := context.Background()
	const tenant = uint(21)

	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: tenant, Name: uniqueName("ovrpool"), Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	o, err := svc.SetEmergencyOverride(ctx, tenant, pool.ID, 99, "incident-4242", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("create override: %v", err)
	}
	if o.ID == 0 {
		t.Fatal("override insert must return a real generated id (RETURNING id), got 0")
	}
	if !o.Active {
		t.Fatal("a newly created override must be active")
	}

	// ACTIVATION: the override is visible to routing.
	active, err := svc.repo.ActiveOverride(ctx, tenant, time.Now().UTC())
	if err != nil {
		t.Fatalf("active override: %v", err)
	}
	if active == nil || active.ID != o.ID {
		t.Fatalf("the created override must be the active one, got %+v", active)
	}

	// CROSS-TENANT: another tenant must not see it.
	other, err := svc.repo.ActiveOverride(ctx, tenant+1, time.Now().UTC())
	if err != nil {
		t.Fatalf("active override for other tenant: %v", err)
	}
	if other != nil && other.ID == o.ID {
		t.Fatal("one tenant's emergency override must not apply to another tenant")
	}

	// REVOCATION by a foreign tenant must be refused.
	if err := svc.RevokeEmergencyOverride(ctx, o.ID, tenant+1, 99, "not mine"); !errors.Is(err, ErrOverrideNotFound) {
		t.Fatalf("a foreign tenant must not revoke another tenant's override, got %v", err)
	}
	var stillActive int
	if err := db.QueryRow(`SELECT active FROM platform_relay_overrides WHERE id = $1`, o.ID).Scan(&stillActive); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stillActive != 1 {
		t.Fatal("a refused revocation must not have deactivated the override")
	}

	// REVOCATION by the owner succeeds, exactly once.
	if err := svc.RevokeEmergencyOverride(ctx, o.ID, tenant, 99, "incident resolved"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	gone, err := svc.repo.ActiveOverride(ctx, tenant, time.Now().UTC())
	if err != nil {
		t.Fatalf("active override after revoke: %v", err)
	}
	if gone != nil && gone.ID == o.ID {
		t.Fatal("a revoked override must no longer be active")
	}
	// Replay must be refused, not silently reported as success.
	if err := svc.RevokeEmergencyOverride(ctx, o.ID, tenant, 99, "again"); !errors.Is(err, ErrOverrideNotFound) {
		t.Fatalf("revoking an already-inactive override must be refused, got %v", err)
	}
}

// TestPostgresRelay_OverrideExpiry proves an expired override stops applying
// on PostgreSQL, and that ExpireOverrides reports real RowsAffected.
func TestPostgresRelay_OverrideExpiry(t *testing.T) {
	_, svc := newPostgresRelayService(t)
	ctx := context.Background()
	const tenant = uint(31)

	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: tenant, Name: uniqueName("exppool"), Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	o, err := svc.SetEmergencyOverride(ctx, tenant, pool.ID, 7, "short window", time.Now().UTC().Add(2*time.Second))
	if err != nil {
		t.Fatalf("create override: %v", err)
	}

	// Read at a moment past its expiry: it must not apply, without any sweep
	// having run.
	future := time.Now().UTC().Add(time.Hour)
	expired, err := svc.repo.ActiveOverride(ctx, tenant, future)
	if err != nil {
		t.Fatalf("active override: %v", err)
	}
	if expired != nil && expired.ID == o.ID {
		t.Fatal("an expired override must not apply on PostgreSQL")
	}

	n, err := svc.repo.ExpireOverrides(ctx, future)
	if err != nil {
		t.Fatalf("expire overrides: %v", err)
	}
	if n < 1 {
		t.Fatalf("ExpireOverrides must report the rows it deactivated, got %d", n)
	}
}

// TestPostgresRelay_ProviderMembershipAndSelection proves pool membership,
// provider creation, and rule-driven selection all work end to end on
// PostgreSQL — including the sender-pattern and domain selectors that were
// never evaluated before Fix B.
func TestPostgresRelay_ProviderMembershipAndSelection(t *testing.T) {
	_, svc := newPostgresRelayService(t)
	ctx := context.Background()
	const tenant = uint(41)
	const domainID = uint(9)

	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: tenant, Name: uniqueName("selpool"), Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	p, err := svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, Scope: ScopeTenant, TenantID: tenant, Name: uniqueName("pgprov"),
		Host: "smtp.provider.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict,
		Active: true, Priority: 10, Weight: 1,
	}, "", testActor)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("provider insert must return a real generated id on PostgreSQL")
	}

	// MEMBERSHIP: the provider is listed under its pool.
	members, err := svc.repo.ListProvidersByPool(ctx, pool.ID)
	if err != nil {
		t.Fatalf("list providers by pool: %v", err)
	}
	found := false
	for _, m := range members {
		if m.ID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("the created provider must be a member of its pool on PostgreSQL")
	}

	// The rule selectors must be UNIQUE per run (F8): a reused database
	// retains rules from prior runs, and identical selectors break the
	// precedence tie by lowest ID — a leftover rule would win and point
	// selection at an earlier run's pool. A per-run sender pattern makes
	// every run's rule the only match for its own sender.
	uniqueSender := "billing@" + uniqueName("acme") + ".test"
	if _, err := svc.CreateRoutingRule(ctx, RoutingRule{
		TenantID: tenant, DomainID: domainID, SenderPattern: uniqueSender,
		PoolID: pool.ID, Priority: 1,
	}, testActor); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// The matching sender routes through the pool.
	got, err := svc.SelectRoute(ctx, RouteRequest{
		TenantID: tenant, DomainID: domainID,
		SenderAddress: uniqueSender, SenderDomain: "acme.test",
		RecipientDomain: "dest.test",
	})
	if err != nil {
		t.Fatalf("select route: %v", err)
	}
	if got.Direct {
		t.Fatal("the matching sender must resolve to the relay pool on PostgreSQL")
	}
	if got.ProviderID != p.ID {
		t.Fatalf("expected provider %d, got %d", p.ID, got.ProviderID)
	}

	// A different sender at the same domain must NOT inherit the route.
	other, err := svc.SelectRoute(ctx, RouteRequest{
		TenantID: tenant, DomainID: domainID,
		SenderAddress: "sales@acme.test", SenderDomain: "acme.test",
		RecipientDomain: "dest.test",
	})
	if err != nil {
		t.Fatalf("select route (unmatched sender): %v", err)
	}
	if !other.Direct {
		t.Fatal("sender-pattern scoping must be enforced on PostgreSQL")
	}

	// CROSS-TENANT: another tenant must not pick up this tenant's rule.
	foreign, err := svc.SelectRoute(ctx, RouteRequest{
		TenantID: tenant + 1, DomainID: domainID,
		SenderAddress: uniqueSender, SenderDomain: "acme.test",
		RecipientDomain: "dest.test",
	})
	if err != nil {
		t.Fatalf("select route (foreign tenant): %v", err)
	}
	if !foreign.Direct {
		t.Fatal("a tenant-scoped rule must never route another tenant's mail on PostgreSQL")
	}
}

// TestPostgresRelay_CircuitStateAndRateLimitPersist proves the two pieces of
// availability state survive a round trip on PostgreSQL, including the
// RowsAffected verification added to UpdateProviderCircuit and the
// RETURNING-count rate-limit upsert.
func TestPostgresRelay_CircuitStateAndRateLimitPersist(t *testing.T) {
	_, svc := newPostgresRelayService(t)
	ctx := context.Background()

	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: uniqueName("statepool"), Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	p, err := svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, Scope: ScopeGlobal, Name: uniqueName("stateprov"),
		Host: "smtp.state.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict,
		Active: true, RateLimitPerMin: 3,
	}, "", testActor)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	// CIRCUIT STATE round trip.
	openedAt := time.Now().UTC()
	if err := svc.repo.UpdateProviderCircuit(ctx, p.ID, CircuitOpen, 5, &openedAt, openedAt); err != nil {
		t.Fatalf("update circuit: %v", err)
	}
	reloaded, err := svc.repo.GetProvider(ctx, p.ID)
	if err != nil {
		t.Fatalf("get provider: %v", err)
	}
	if reloaded.CircuitState != CircuitOpen || reloaded.CircuitFailures != 5 {
		t.Fatalf("circuit state did not persist on PostgreSQL: %+v", reloaded)
	}

	// A transition against a nonexistent provider must be reported, not
	// silently swallowed (the RowsAffected verification).
	if err := svc.repo.UpdateProviderCircuit(ctx, 99999999, CircuitOpen, 1, &openedAt, openedAt); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("a circuit update against a missing provider must be reported, got %v", err)
	}

	// RATE LIMIT counting on PostgreSQL (the RETURNING-count upsert path).
	window := time.Now().UTC().Truncate(time.Minute)
	for i := 1; i <= 3; i++ {
		ok, err := svc.repo.IncrementAndCheck(ctx, p.ID, window, 3)
		if err != nil {
			t.Fatalf("increment %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("attempt %d must be within the limit of 3", i)
		}
	}
	ok, err := svc.repo.IncrementAndCheck(ctx, p.ID, window, 3)
	if err != nil {
		t.Fatalf("increment 4: %v", err)
	}
	if ok {
		t.Fatal("the 4th attempt in the window must exceed a limit of 3 on PostgreSQL")
	}
}

// TestPostgresRelay_AttemptBookkeepingPersists proves delivery-attempt
// bookkeeping (the circuit-breaker feedback path) round-trips on PostgreSQL
// and reports its errors rather than discarding them.
func TestPostgresRelay_AttemptBookkeepingPersists(t *testing.T) {
	_, svc := newPostgresRelayService(t)
	ctx := context.Background()

	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: uniqueName("bkpool"), Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	p, err := svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, Scope: ScopeGlobal, Name: uniqueName("bkprov"),
		Host: "smtp.bk.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict, Active: true,
	}, "", testActor)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	// A failure outcome must be recorded and reflected in the row.
	if err := svc.RecordAttemptResult(ctx, p.ID, false); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	after, err := svc.repo.GetProvider(ctx, p.ID)
	if err != nil {
		t.Fatalf("get provider: %v", err)
	}
	if after.CircuitFailures == 0 {
		t.Fatal("a failed attempt must be recorded against the provider on PostgreSQL")
	}

	// A success resets the failure count.
	if err := svc.RecordAttemptResult(ctx, p.ID, true); err != nil {
		t.Fatalf("record success: %v", err)
	}
	reset, err := svc.repo.GetProvider(ctx, p.ID)
	if err != nil {
		t.Fatalf("get provider: %v", err)
	}
	if reset.CircuitFailures != 0 {
		t.Fatalf("a successful attempt must clear the failure count, got %d", reset.CircuitFailures)
	}

	// Bookkeeping against a provider that does not exist must be an error, not
	// a silent no-op — the worker relies on this to report a broken breaker.
	if err := svc.RecordAttemptResult(ctx, 99999999, true); err == nil {
		t.Fatal("bookkeeping against a missing provider must be reported")
	}
}

// TestPostgresRelay_NoSQLitePlaceholdersReachPostgres is a belt-and-braces
// guard: it exercises every statement family through the real driver. A raw
// `?` reaching PostgreSQL fails with SQLSTATE 42601 (syntax error), so any
// regression here surfaces as a test failure rather than a production 500.
func TestPostgresRelay_NoSQLitePlaceholdersReachPostgres(t *testing.T) {
	_, svc := newPostgresRelayService(t)
	ctx := context.Background()

	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeGlobal, Name: uniqueName("phpool"), Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	p, err := svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, Scope: ScopeGlobal, Name: uniqueName("phprov"),
		Host: "smtp.ph.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict, Active: true,
	}, "", testActor)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	for name, fn := range map[string]func() error{
		"GetProvider":         func() error { _, e := svc.repo.GetProvider(ctx, p.ID); return e },
		"GetPool":             func() error { _, e := svc.repo.GetPool(ctx, pool.ID); return e },
		"ListProvidersByPool": func() error { _, e := svc.repo.ListProvidersByPool(ctx, pool.ID); return e },
		"ListRoutingRules":    func() error { _, e := svc.repo.ListRoutingRules(ctx, 1, 1); return e },
		"ActiveOverride":      func() error { _, e := svc.repo.ActiveOverride(ctx, 1, time.Now().UTC()); return e },
		"ListProviders": func() error {
			_, _, e := svc.repo.ListProviders(ctx, ProviderFilter{Limit: 10, Search: "ph"})
			return e
		},
		"IncrementAndCheck": func() error {
			_, e := svc.repo.IncrementAndCheck(ctx, p.ID, time.Now().UTC().Truncate(time.Minute), 100)
			return e
		},
		"ExpireOverrides": func() error { _, e := svc.repo.ExpireOverrides(ctx, time.Now().UTC()); return e },
		"SetTestResult": func() error {
			cur, e := svc.repo.GetProvider(ctx, p.ID)
			if e != nil {
				return e
			}
			return svc.repo.SetTestResult(ctx, p.ID, time.Now().UTC(), "ok", cur.Version)
		},
	} {
		if err := fn(); err != nil {
			if strings.Contains(err.Error(), "42601") || strings.Contains(strings.ToLower(err.Error()), "syntax error") {
				t.Fatalf("%s sent SQLite-style SQL to PostgreSQL: %v", name, err)
			}
			t.Fatalf("%s failed on PostgreSQL: %v", name, err)
		}
	}
}

// TestPostgresRelay_SchemaIsIdempotent proves the additive migration can be
// re-run safely against PostgreSQL, which is what every boot does.
func TestPostgresRelay_SchemaIsIdempotent(t *testing.T) {
	_, svc := newPostgresRelayService(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := svc.repo.EnsureSchema(ctx); err != nil {
			t.Fatalf("EnsureSchema pass %d must be idempotent on PostgreSQL: %v", i+1, err)
		}
	}
}

// uniqueName keeps names distinct across runs because this suite shares one
// PostgreSQL database with the other acceptance steps (the repository's
// established convention) and providers carry a unique scope+name index.
func uniqueName(prefix string) string {
	return prefix + "-" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
}
