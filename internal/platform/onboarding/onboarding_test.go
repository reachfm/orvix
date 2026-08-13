package onboarding

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/admin/organization"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/billing"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
	_ "modernc.org/sqlite"
)

func newOnboardingTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE tenants (
			id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, slug TEXT UNIQUE, domain TEXT,
			plan TEXT, max_domains INTEGER, max_mailboxes INTEGER, logo_url TEXT,
			primary_color TEXT, active INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME
		);
		CREATE TABLE users (id INTEGER PRIMARY KEY, tenant_id INTEGER, role TEXT, deleted_at DATETIME, token_version INTEGER NOT NULL DEFAULT 0, active INTEGER NOT NULL DEFAULT 1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := billing.CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := audit.NewExtendedStore(db).EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func newOnboardingService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := newOnboardingTestDB(t)
	orgSvc := organization.NewService(organization.NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	billingSvc := billing.NewService(db)
	if err := billingSvc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		t.Fatal(err)
	}
	idem := kernel.NewIdempotencyStore(db, dialect)
	if err := idem.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	outbox := kernel.NewOutboxRepository(dialect)
	if err := outbox.EnsureSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, orgSvc, billingSvc, idem, outbox, kernel.NewFixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	return svc, db
}

func validDraft() Draft {
	return Draft{
		IdempotencyKey: "key-1",
		Slug:           "acme",
		Name:           "Acme Corp",
		Domain:         "acme.example",
		AdminEmail:     "admin@acme.example",
		PlanID:         billing.PlanFree,
	}
}

func TestCreateDraft_InvalidStaysInDraftStep(t *testing.T) {
	d := CreateDraft(Draft{})
	if d.Step != StepDraft {
		t.Fatalf("expected StepDraft for an invalid draft, got %s", d.Step)
	}
	if len(d.ValidationErrs) == 0 {
		t.Fatal("expected validation errors on an empty draft")
	}
}

func TestCreateDraft_ValidMovesToValidated(t *testing.T) {
	d := CreateDraft(validDraft())
	if d.Step != StepValidated {
		t.Fatalf("expected StepValidated, got %s", d.Step)
	}
	if len(d.ValidationErrs) != 0 {
		t.Fatalf("expected no validation errors, got %v", d.ValidationErrs)
	}
}

func TestCommit_InvalidDraftNeverTouchesDB(t *testing.T) {
	svc, db := newOnboardingService(t)
	_, err := svc.Commit(context.Background(), &Draft{}, 1)
	apiErr, ok := err.(*kernel.Error)
	if !ok || apiErr.Code != kernel.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("an invalid draft must never create a row")
	}
}

func TestCommit_HappyPathCreatesOrgAndOutboxEvent(t *testing.T) {
	svc, db := newOnboardingService(t)
	d := validDraft()
	org, err := svc.Commit(context.Background(), &d, 1)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if org.Slug != "acme" {
		t.Fatalf("expected slug acme, got %s", org.Slug)
	}
	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_outbox_events WHERE topic = 'organization.provisioning.dns_setup'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("expected exactly 1 dns_setup outbox event, got %d", eventCount)
	}
}

func TestCommit_NeverReportsActiveWhileProvisioningIncomplete(t *testing.T) {
	svc, _ := newOnboardingService(t)
	ctx := context.Background()
	d := validDraft()
	org, err := svc.Commit(ctx, &d, 1)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := svc.GetProgress(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Step == StepActive {
		t.Fatal("expected pending_dns, not active, immediately after commit with an unresolved outbox event")
	}
	if progress.Step != StepPendingDNS {
		t.Fatalf("expected StepPendingDNS, got %s", progress.Step)
	}
}

// TestCommit_RetryWithSameKeyNeverDuplicatesOrganization is the core
// "a retry must not create duplicate organizations" proof: the exact
// same Draft+IdempotencyKey submitted twice (simulating a client retry
// after a timeout) must result in exactly one tenants row and one
// dns_setup outbox event, and both calls must return the same
// organization.
func TestCommit_RetryWithSameKeyNeverDuplicatesOrganization(t *testing.T) {
	svc, db := newOnboardingService(t)
	ctx := context.Background()
	d1 := validDraft()
	org1, err := svc.Commit(ctx, &d1, 1)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	d2 := validDraft() // identical content, identical idempotency key
	org2, err := svc.Commit(ctx, &d2, 1)
	if err != nil {
		t.Fatalf("retried commit: %v", err)
	}

	if org1.ID != org2.ID {
		t.Fatalf("expected the retry to return the same organization id, got %d vs %d", org1.ID, org2.ID)
	}
	var orgCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants`).Scan(&orgCount); err != nil {
		t.Fatal(err)
	}
	if orgCount != 1 {
		t.Fatalf("expected exactly 1 organization after a retried commit, got %d", orgCount)
	}
	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_outbox_events WHERE topic = 'organization.provisioning.dns_setup'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("expected exactly 1 outbox event after a retried commit, got %d", eventCount)
	}
}

func TestCommit_DifferentKeySameSlugIsAConflictNotADuplicate(t *testing.T) {
	svc, _ := newOnboardingService(t)
	ctx := context.Background()
	d1 := validDraft()
	if _, err := svc.Commit(ctx, &d1, 1); err != nil {
		t.Fatal(err)
	}
	d2 := validDraft()
	d2.IdempotencyKey = "a-different-key"
	_, err := svc.Commit(ctx, &d2, 1)
	apiErr, ok := err.(*kernel.Error)
	if !ok || apiErr.Code != kernel.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict for a genuinely new request with a colliding slug, got %v", err)
	}
}

func TestGetProgress_NoEventsMeansActive(t *testing.T) {
	svc, _ := newOnboardingService(t)
	ctx := context.Background()
	svc.outbox = nil // simulate outbox not wired
	d := validDraft()
	org, err := svc.Commit(ctx, &d, 1)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := svc.GetProgress(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Step != StepActive {
		t.Fatalf("expected StepActive when no provisioning events exist, got %s", progress.Step)
	}
}

func TestCancel_BeforeActivationSucceeds(t *testing.T) {
	svc, _ := newOnboardingService(t)
	ctx := context.Background()
	d := validDraft()
	org, err := svc.Commit(ctx, &d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(ctx, org.ID, 1); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got, err := svc.orgSvc.GetOrganization(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active {
		t.Fatal("expected the cancelled organization to be inactive")
	}
}
