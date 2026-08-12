package organization

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/audit"
	_ "modernc.org/sqlite"
)

func newLifecycleTestDB(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
		CREATE TABLE tenants (
			id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, slug TEXT UNIQUE, domain TEXT,
			plan TEXT, max_domains INTEGER, max_mailboxes INTEGER, logo_url TEXT,
			primary_color TEXT, active INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME
		);
		CREATE TABLE users (id INTEGER PRIMARY KEY, tenant_id INTEGER, role TEXT, deleted_at DATETIME, token_version INTEGER NOT NULL DEFAULT 0, active INTEGER NOT NULL DEFAULT 1);
		CREATE TABLE org_suspensions (
			id INTEGER PRIMARY KEY AUTOINCREMENT, organization_id INTEGER NOT NULL, reason TEXT NOT NULL,
			suspended_by INTEGER NOT NULL, note TEXT, suspended_at DATETIME NOT NULL,
			reactivated_at DATETIME, created_at DATETIME NOT NULL
		);
		CREATE TABLE org_deletions (
			id INTEGER PRIMARY KEY AUTOINCREMENT, organization_id INTEGER NOT NULL, requested_by INTEGER NOT NULL,
			state TEXT NOT NULL, retention_expires_at DATETIME, requested_at DATETIME NOT NULL,
			confirmed_at DATETIME, cancelled_at DATETIME, created_at DATETIME NOT NULL
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.NewExtendedStore(db).EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	return db, svc
}

func TestSuspendOrganization_HappyPath(t *testing.T) {
	_, svc := newLifecycleTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "acme", Domain: "acme.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendOrganization(ctx, org.ID, 42, SuspensionManual, "customer requested pause"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	got, err := svc.GetOrganization(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active {
		t.Fatal("expected organization to be inactive after suspend")
	}
}

func TestSuspendOrganization_AlreadySuspendedIsStableConflict(t *testing.T) {
	_, svc := newLifecycleTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "acme", Domain: "acme.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendOrganization(ctx, org.ID, 42, SuspensionManual, "first"); err != nil {
		t.Fatal(err)
	}
	err = svc.SuspendOrganization(ctx, org.ID, 42, SuspensionManual, "second")
	if err != ErrOrganizationSuspended {
		t.Fatalf("expected ErrOrganizationSuspended, got %v", err)
	}
}

func TestSuspendOrganization_ConcurrentSuspendsOnlyOneRecordsRow(t *testing.T) {
	db, svc := newLifecycleTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "acme", Domain: "acme.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 10
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = svc.SuspendOrganization(ctx, org.ID, 42, SuspensionManual, "concurrent")
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent suspends to succeed, got %d", attempts, succeeded)
	}

	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM org_suspensions WHERE organization_id = ?`, org.ID).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly 1 suspension row despite %d concurrent attempts, got %d — the TOCTOU race was not closed", attempts, rowCount)
	}
}

func TestReactivateOrganization_ClosesSuspensionAndIsIdempotent(t *testing.T) {
	db, svc := newLifecycleTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "acme", Domain: "acme.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SuspendOrganization(ctx, org.ID, 42, SuspensionManual, "pause"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReactivateOrganization(ctx, org.ID); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	got, _ := svc.GetOrganization(ctx, org.ID)
	if !got.Active {
		t.Fatal("expected organization active after reactivate")
	}
	var openSuspensions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM org_suspensions WHERE organization_id = ? AND reactivated_at IS NULL`, org.ID).Scan(&openSuspensions); err != nil {
		t.Fatal(err)
	}
	if openSuspensions != 0 {
		t.Fatal("expected the suspension record to be closed (reactivated_at set)")
	}
	// Idempotent: reactivating an already-active org is a no-op, not an error.
	if err := svc.ReactivateOrganization(ctx, org.ID); err != nil {
		t.Fatalf("expected idempotent reactivate of an already-active org to succeed, got %v", err)
	}
}

func TestCreateOrganization_DuplicateSlugConcurrentRaceStillOneWinner(t *testing.T) {
	_, svc := newLifecycleTestDB(t)
	ctx := context.Background()

	const attempts = 8
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "race", Domain: "race.test"}, 1)
			results[i] = err
		}(i)
	}
	wg.Wait()

	succeeded, conflicts := 0, 0
	for _, err := range results {
		switch err {
		case nil:
			succeeded++
		case ErrOrganizationExists:
			conflicts++
		default:
			t.Fatalf("expected either success or the stable ErrOrganizationExists conflict, got %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent same-slug creates to succeed, got %d", attempts, succeeded)
	}
	if conflicts != attempts-1 {
		t.Fatalf("expected the remaining %d attempts to fail with the stable ErrOrganizationExists conflict, got %d", attempts-1, conflicts)
	}
}

func TestRequestDeletion_ThenCancel_RestoresOrganization(t *testing.T) {
	_, svc := newLifecycleTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "acme", Domain: "acme.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RequestDeletion(ctx, org.ID, 42); err != nil {
		t.Fatalf("request deletion: %v", err)
	}
	if err := svc.RequestDeletion(ctx, org.ID, 42); err != ErrDeletionAlreadyRequested {
		t.Fatalf("expected ErrDeletionAlreadyRequested on a second request, got %v", err)
	}
	if err := svc.CancelDeletion(ctx, org.ID); err != nil {
		t.Fatalf("cancel deletion: %v", err)
	}
	// After cancellation, a fresh deletion request must be possible again.
	if err := svc.RequestDeletion(ctx, org.ID, 42); err != nil {
		t.Fatalf("expected a fresh deletion request after cancel to succeed, got %v", err)
	}
}

func TestConfirmDeletion_BlockedDuringRetentionPeriod(t *testing.T) {
	_, svc := newLifecycleTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "acme", Domain: "acme.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RequestDeletion(ctx, org.ID, 42); err != nil {
		t.Fatal(err)
	}
	// RequestDeletion sets a 30-day retention window — confirming
	// immediately must be refused, never silently succeed and purge data
	// during the retention period.
	err = svc.ConfirmDeletion(ctx, org.ID)
	if err != ErrRetentionPeriodActive {
		t.Fatalf("expected ErrRetentionPeriodActive, got %v", err)
	}
}
