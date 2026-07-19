package billing

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

func setupBackfillTestDB(t *testing.T) (*sql.DB, *dbdialect.Info) {
	t.Helper()
	db := newTestDB(t)
	dial, err := dbdialect.Detect(db)
	if err != nil {
		dial = dbdialect.FromDriver("sqlite")
	}
	ts := dial.TimestampType()
	bt := dial.BooleanType()
	tl := dial.TrueLiteral()
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		slug TEXT NOT NULL DEFAULT '',
		domain TEXT NOT NULL DEFAULT '',
		plan TEXT DEFAULT 'smb',
		active ` + bt + ` NOT NULL DEFAULT ` + tl + `,
		deleted_at ` + ts + `,
		created_at ` + ts + ` NOT NULL,
		updated_at ` + ts + ` NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create tenants table: %v", err)
	}
	return db, dial
}

func seedTenant(t *testing.T, db *sql.DB, dial *dbdialect.Info, id uint, plan string, active bool) {
	t.Helper()
	now := time.Now().UTC()
	query := fmt.Sprintf("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (%s)",
		dial.Placeholders(8))
	_, err := db.Exec(query,
		id, now, now, "tenant-"+plan, "tenant-"+plan, "tenant-"+plan+".example.com", plan, active)
	if err != nil {
		t.Fatalf("seed tenant %d: %v", id, err)
	}
}

func TestBackfillSubscriptions_ExistingTenantWithoutSubGetsOne(t *testing.T) {
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}

	seedTenant(t, dt, dial, 1, "enterprise", true)

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
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "enterprise", true)
	seedTenant(t, dt, dial, 2, "enterprise", true)

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
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "business", true)

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
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "starter", true)

	svc.BackfillSubscriptions()
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanStarter {
		t.Fatalf("expected starter, got %s", sub.PlanID)
	}
}

func TestBackfillSubscriptions_FreeLegacyMapsToFree(t *testing.T) {
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "free", true)

	svc.BackfillSubscriptions()
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanFree {
		t.Fatalf("expected free, got %s", sub.PlanID)
	}
}

func TestBackfillSubscriptions_UnknownLegacyMapsToFree(t *testing.T) {
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "smb", true)

	svc.BackfillSubscriptions()
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanFree {
		t.Fatalf("expected free (fallback), got %s", sub.PlanID)
	}
}

func TestBackfillSubscriptions_ExistingSubIsUnchanged(t *testing.T) {
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "enterprise", true)

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

	existing, _ := svc.GetSubscription(1)
	if existing.PlanID != sub.PlanID {
		t.Fatalf("existing subscription was modified: expected plan %s, got %s", sub.PlanID, existing.PlanID)
	}
}

func TestBackfillSubscriptions_Idempotent(t *testing.T) {
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "enterprise", true)

	count1, _ := svc.BackfillSubscriptions()
	count2, _ := svc.BackfillSubscriptions()
	if count2 != 0 {
		t.Fatalf("second run should backfill 0, got %d", count2)
	}
	_ = count1
}

func TestBackfillSubscriptions_InactiveTenantSkipped(t *testing.T) {
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "enterprise", false)

	count, err := svc.BackfillSubscriptions()
	if err != nil {
		t.Fatalf("BackfillSubscriptions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 backfilled for inactive tenant, got %d", count)
	}
}

func TestBackfillSubscriptions_SmbMapsToFree(t *testing.T) {
	dt, dial := setupBackfillTestDB(t)
	svc := NewService(dt)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	seedTenant(t, dt, dial, 1, "smb", true)

	svc.BackfillSubscriptions()
	sub, _ := svc.GetSubscription(1)
	if sub.PlanID != PlanFree {
		t.Fatalf("expected free for smb, got %s", sub.PlanID)
	}
}

// TestBackfillFixtureSQLGeneratesDialectCorrectSQL proves the test fixture
// DDL and INSERT SQL are valid for both SQLite and PostgreSQL.
func TestBackfillFixtureSQLGeneratesDialectCorrectSQL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dialect *dbdialect.Info
	}{
		{"SQLite", dbdialect.FromDriver("sqlite")},
		{"PostgreSQL", dbdialect.FromDriver("postgres")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.dialect
			ts := d.TimestampType()
			bt := d.BooleanType()
			tl := d.TrueLiteral()

			ddl := `CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		slug TEXT NOT NULL DEFAULT '',
		domain TEXT NOT NULL DEFAULT '',
		plan TEXT DEFAULT 'smb',
		active ` + bt + ` NOT NULL DEFAULT ` + tl + `,
		deleted_at ` + ts + `,
		created_at ` + ts + ` NOT NULL,
		updated_at ` + ts + ` NOT NULL
	)`

			switch tc.name {
			case "SQLite":
				if !strings.Contains(ddl, "INTEGER") {
					t.Errorf("SQLite DDL missing INTEGER for active: %s", ddl)
				}
				if !strings.Contains(ddl, "DATETIME") {
					t.Errorf("SQLite DDL missing DATETIME: %s", ddl)
				}
				if !strings.Contains(ddl, "DEFAULT 1") {
					t.Errorf("SQLite DDL missing DEFAULT 1: %s", ddl)
				}
			case "PostgreSQL":
				if !strings.Contains(ddl, "BOOLEAN") {
					t.Errorf("PostgreSQL DDL missing BOOLEAN for active: %s", ddl)
				}
				if !strings.Contains(ddl, "TIMESTAMP") {
					t.Errorf("PostgreSQL DDL missing TIMESTAMP: %s", ddl)
				}
				if !strings.Contains(ddl, "DEFAULT TRUE") {
					t.Errorf("PostgreSQL DDL missing DEFAULT TRUE: %s", ddl)
				}
			}

			insert := fmt.Sprintf("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (%s)",
				d.Placeholders(8))

			switch tc.name {
			case "SQLite":
				if strings.Contains(insert, "$") {
					t.Errorf("SQLite INSERT should use ?, got: %s", insert)
				}
				if !strings.Contains(insert, "?") {
					t.Errorf("SQLite INSERT missing ? placeholders: %s", insert)
				}
			case "PostgreSQL":
				for i := 1; i <= 8; i++ {
					want := fmt.Sprintf("$%d", i)
					if !strings.Contains(insert, want) {
						t.Errorf("PostgreSQL INSERT missing %s: %s", want, insert)
					}
				}
				if strings.Contains(insert, "?") {
					t.Errorf("PostgreSQL INSERT should not contain ?: %s", insert)
				}
			}
		})
	}
}
