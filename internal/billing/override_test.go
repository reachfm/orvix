package billing

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/audit"
)

func setupOverrideTest(t *testing.T) (*sql.DB, *Service, *QuotaService, *OverrideStore) {
	t.Helper()
	db := setupTestDB(t)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	auditStore := audit.NewExtendedStore(db)
	if err := auditStore.EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	overrides := NewOverrideStore(db, auditStore)
	if err := overrides.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	quota := NewQuotaService(db, svc).WithOverrides(overrides)
	return db, svc, quota, overrides
}

func TestOverride_ExtendsQuotaLimit(t *testing.T) {
	_, svc, quota, overrides := setupOverrideTest(t)
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	plan, err := svc.GetPlan(PlanFree)
	if err != nil {
		t.Fatal(err)
	}

	// Without an override, hitting the plan's real limit is denied.
	before := quota.CanCreateDomain(1, plan.MaxDomains)
	if before.Allowed {
		t.Fatal("expected the plan's base limit to deny at capacity")
	}

	now := time.Now().UTC()
	if _, err := overrides.Set(context.Background(), 1, OverrideMaxDomains, plan.MaxDomains+5, "customer requested temporary bump for a migration", 42, now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("set override: %v", err)
	}

	after := quota.CanCreateDomain(1, plan.MaxDomains)
	if !after.Allowed {
		t.Fatal("expected the override to raise the effective limit and allow the create")
	}
	if after.Limit != plan.MaxDomains+5 {
		t.Fatalf("expected reported limit to reflect the override, got %d", after.Limit)
	}
}

func TestOverride_ExpiredOverrideIsIgnored(t *testing.T) {
	_, svc, _, overrides := setupOverrideTest(t)
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	plan, _ := svc.GetPlan(PlanFree)

	now := time.Now().UTC()
	// Set an override that expires in the past relative to "now+1h" we'll
	// check against below — simulate by setting expiresAt just barely in
	// the future at creation time, then checking at a later "now".
	if _, err := overrides.Set(context.Background(), 1, OverrideMaxDomains, plan.MaxDomains+5, "short-lived test override", 42, now, now.Add(time.Second)); err != nil {
		t.Fatalf("set override: %v", err)
	}

	active, err := overrides.ActiveOverride(context.Background(), 1, OverrideMaxDomains, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if active != nil {
		t.Fatal("expected an override past its expires_at to no longer be active")
	}
}

func TestOverride_RequiresNonEmptyReason(t *testing.T) {
	_, _, _, overrides := setupOverrideTest(t)
	now := time.Now().UTC()
	_, err := overrides.Set(context.Background(), 1, OverrideMaxDomains, 100, "", 42, now, now.Add(time.Hour))
	if err != ErrOverrideReasonRequired {
		t.Fatalf("expected ErrOverrideReasonRequired, got %v", err)
	}
}

func TestOverride_RequiresFutureExpiry(t *testing.T) {
	_, _, _, overrides := setupOverrideTest(t)
	now := time.Now().UTC()
	_, err := overrides.Set(context.Background(), 1, OverrideMaxDomains, 100, "reason", 42, now, now.Add(-time.Hour))
	if err != ErrOverrideExpiryRequired {
		t.Fatalf("expected ErrOverrideExpiryRequired, got %v", err)
	}
}

func TestOverride_AuditedExactlyOnce(t *testing.T) {
	db, _, _, overrides := setupOverrideTest(t)
	now := time.Now().UTC()
	if _, err := overrides.Set(context.Background(), 1, OverrideMaxDomains, 100, "audit check", 42, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("set override: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action = 'billing.override.set'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 audit row for the override, got %d", count)
	}
}

func TestOverride_RevokeStopsEnforcement(t *testing.T) {
	_, svc, quota, overrides := setupOverrideTest(t)
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	plan, _ := svc.GetPlan(PlanFree)
	now := time.Now().UTC()

	o, err := overrides.Set(context.Background(), 1, OverrideMaxDomains, plan.MaxDomains+5, "temp bump", 42, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("set override: %v", err)
	}
	if !quota.CanCreateDomain(1, plan.MaxDomains).Allowed {
		t.Fatal("expected override to be in effect before revoke")
	}

	if err := overrides.Revoke(context.Background(), o.ID, 42, now); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if quota.CanCreateDomain(1, plan.MaxDomains).Allowed {
		t.Fatal("expected the plan's base limit to apply again after revoke")
	}
}

func TestOverride_RevokeAlreadyRevokedFails(t *testing.T) {
	_, _, _, overrides := setupOverrideTest(t)
	now := time.Now().UTC()
	o, err := overrides.Set(context.Background(), 1, OverrideMaxDomains, 100, "reason", 42, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := overrides.Revoke(context.Background(), o.ID, 42, now); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := overrides.Revoke(context.Background(), o.ID, 42, now); err == nil {
		t.Fatal("expected a second revoke of the same override to fail, not silently no-op")
	}
}

func TestOverride_NoOverrideFallsBackToPlanLimit(t *testing.T) {
	_, svc, quota, _ := setupOverrideTest(t)
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	plan, _ := svc.GetPlan(PlanFree)
	res := quota.CanCreateDomain(1, plan.MaxDomains-1)
	if res.Limit != plan.MaxDomains {
		t.Fatalf("expected plan limit %d with no override, got %d", plan.MaxDomains, res.Limit)
	}
}
