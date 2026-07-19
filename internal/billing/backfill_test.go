package billing

import (
	"database/sql"
	"testing"
	"time"
)

func setupBackfillTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newTestDB(t)
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		slug TEXT NOT NULL DEFAULT '',
		domain TEXT NOT NULL DEFAULT '',
		plan TEXT DEFAULT 'smb',
		active INTEGER DEFAULT 1,
		deleted_at DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create tenants table: %v", err)
	}
	return db
}

func seedTenant(t *testing.T, db *sql.DB, id uint, plan string, active bool) {
	t.Helper()
	now := time.Now().UTC()
	activeInt := 0
	if active {
		activeInt = 1
	}
	_, err := db.Exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, now, now, "tenant-"+plan, "tenant-"+plan, "tenant-"+plan+".example.com", plan, activeInt)
	if err != nil {
		t.Fatalf("seed tenant %d: %v", id, err)
	}
}

func TestBackfillSubscriptions_ExistingTenantWithoutSubGetsOne(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}

	seedTenant(t, dt, 1, "enterprise", true)

	count, err := svc.BackfillSubscriptions()
	if err != nil {
		t.Fatalf("BackfillSubscriptions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 backfilled, got %d", count)
	}

	sub, err := svc.GetSubscription(1)
	if err != nil {
		t.Fatalf("GetSubscription after backfill: %v", err)
	}
	if sub.PlanID != PlanEnterprise {
		t.Fatalf("expected plan enterprise, got %s", sub.PlanID)
	}
	if sub.Status != SubActive {
		t.Fatalf("expected status active, got %s", sub.Status)
	}
}

func TestBackfillSubscriptions_EnterpriseLegacyMapsToEnterprise(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "enterprise", true)
	seedTenant(t, dt, 2, "enterprise", true)

	count, err := svc.BackfillSubscriptions()
	if err != nil {
		t.Fatalf("BackfillSubscriptions: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 backfilled, got %d", count)
	}
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanEnterprise {
		t.Fatalf("tenant 1: expected enterprise, got %s", sub.PlanID)
	}
}

func TestBackfillSubscriptions_BusinessLegacyMapsToBusiness(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "business", true)

	count, err := svc.BackfillSubscriptions()
	if err != nil {
		t.Fatalf("BackfillSubscriptions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 backfilled, got %d", count)
	}
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanBusiness {
		t.Fatalf("expected business, got %s", sub.PlanID)
	}
}

func TestBackfillSubscriptions_StarterLegacyMapsToStarter(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "starter", true)

	svc.BackfillSubscriptions()
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanStarter {
		t.Fatalf("expected starter, got %s", sub.PlanID)
	}
}

func TestBackfillSubscriptions_FreeLegacyMapsToFree(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "free", true)

	svc.BackfillSubscriptions()
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanFree {
		t.Fatalf("expected free, got %s", sub.PlanID)
	}
}

func TestBackfillSubscriptions_UnknownLegacyMapsToFree(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "smb", true)

	svc.BackfillSubscriptions()
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanFree {
		t.Fatalf("expected free (fallback), got %s", sub.PlanID)
	}
}

func TestBackfillSubscriptions_ExistingSubIsUnchanged(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "enterprise", true)

	// Manually create a subscription
	sub, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	if err != nil {
		t.Fatal(err)
	}

	count, err := svc.BackfillSubscriptions()
	if err != nil {
		t.Fatalf("BackfillSubscriptions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 backfilled (already has sub), got %d", count)
	}

	// Verify the existing subscription was NOT overwritten
	existing, _ := svc.GetSubscription(1)
	if existing.PlanID != sub.PlanID {
		t.Fatalf("existing subscription was modified: expected plan %s, got %s", sub.PlanID, existing.PlanID)
	}
}

func TestBackfillSubscriptions_Idempotent(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "enterprise", true)

	count1, _ := svc.BackfillSubscriptions()
	count2, _ := svc.BackfillSubscriptions()
	if count2 != 0 {
		t.Fatalf("second run should backfill 0, got %d", count2)
	}
	_ = count1
}

func TestBackfillSubscriptions_InactiveTenantSkipped(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "enterprise", false) // inactive

	count, err := svc.BackfillSubscriptions()
	if err != nil {
		t.Fatalf("BackfillSubscriptions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 backfilled for inactive tenant, got %d", count)
	}
}

func TestBackfillSubscriptions_SmbMapsToFree(t *testing.T) {
	dt := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, 1, "smb", true)

	svc.BackfillSubscriptions()
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanFree {
		t.Fatalf("expected free for smb, got %s", sub.PlanID)
	}
}
