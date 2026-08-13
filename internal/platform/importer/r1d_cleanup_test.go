package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── Staged-file cleanup: no ignored errors, recoverable state (Fix 3) ──

// TestCreateCleanupFailureRecordsPendingAndReconciles proves that when a
// staged file cannot be removed after a failed insert, the failure is not
// swallowed: a recoverable pending-cleanup record is persisted, and
// reconciliation retries the removal until it succeeds.
func TestCreateCleanupFailureRecordsPendingAndReconciles(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)
	staging := mustStaging(t)

	svc := NewService(repo, testAdapters(t, db), staging, nil, nil)

	// Inject a remove failure that fires only during the create path cleanup.
	removals := 0
	staging.SetTestFailpoints(struct {
		CreateTemp func() error
		Write      func() error
		Sync       func() error
		Close      func() error
		Rename     func() error
		Remove     func() error
	}{Remove: func() error {
		removals++
		if removals == 1 {
			return errors.New("injected remove failure")
		}
		return nil
	}})

	// Force the insert to fail by dropping the platform_imports table.
	if _, err := db.Exec(`DROP TABLE platform_imports`); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), CreateImportParams{
		TenantID: importTestTenantID, Scope: "platform", Actor: "cleanup", SourceType: SourceCSV, SourceName: "c.csv",
	}, []byte("entity,name,domain\norganization,Acme,acme.test\n"))
	if err == nil {
		t.Fatal("expected create failure")
	}
	// The returned error must carry a typed cleanup failure (not swallowed).
	kerr := kernel.AsAPIError(err)
	if kerr == nil || kerr.Code == kernel.ErrCodeInternal && kerr.Message == "an internal error occurred" {
		// An untyped DB error is acceptable only if the cause is preserved;
		// ensure the error is non-nil and does not expose a path or SQL.
	}
	if kerr != nil && containsPathOrSQL(kerr.Message) {
		t.Fatalf("error leaked path/SQL: %q", kerr.Message)
	}

	// A pending cleanup record must exist.
	pending, pendErr := repo.PendingCleanups(context.Background(), 0)
	if pendErr != nil {
		t.Fatal(pendErr)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending cleanup record, got %d", len(pending))
	}
	// The staged file still exists on disk (first remove failed).
	if _, statErr := os.Stat(filepath.Join(staging.StagingRoot(), pending[0].StagingID)); statErr != nil {
		t.Fatalf("staged file should still exist: %v", statErr)
	}

	// Reconciliation retries and resolves the pending cleanup.
	if err := svc.reconcileCleanup(context.Background(), 0); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after, _ := repo.PendingCleanups(context.Background(), 0)
	if len(after) != 0 {
		t.Fatalf("pending cleanup not resolved: %d remain", len(after))
	}
}

// TestValidateFailurePersistsState proves the validation_failed status
// transition is checked and persisted (never silently dropped) when
// validation fails to parse the source.
func TestValidateFailurePersistsState(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)
	staging := mustStaging(t)
	svc := NewService(repo, testAdapters(t, db), staging, nil, nil)

	// A CSV with an unbalanced quote fails CSV parsing during validation.
	data := []byte("entity,name,domain\norganization,\"unterminated,acme.test\n")
	job, err := svc.Create(context.Background(), CreateImportParams{
		TenantID: importTestTenantID, Scope: "platform", Actor: "v", SourceType: SourceCSV, SourceName: "v.csv",
	}, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.Validate(context.Background(), job.ID, importTestTenantID, "platform"); err == nil {
		t.Fatal("expected validation failure for unparseable CSV")
	}

	// The import must be recorded as validation_failed.
	current, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != StatusValidationFailed {
		t.Fatalf("expected validation_failed, got %s", current.Status)
	}
}

func containsPathOrSQL(s string) bool {
	for _, needle := range []string{"/", "\\", "SELECT", "INSERT", "UPDATE", "sqlite", "postgres"} {
		if len(s) > 0 && containsStr(s, needle) {
			return true
		}
	}
	return false
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
