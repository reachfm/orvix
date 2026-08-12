package importer

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── Fix 2: abandon idempotency on every pre-completion failure (R1E) ──
//
// runIdempotent guarantees a reservation created by IdempotencyBegin is
// abandoned (or finalized) on EVERY failure before IdempotencyComplete, so a
// retried request is a fresh attempt instead of a stale-window-bound
// "in flight".

func countInflightIdempotency(t *testing.T, db *sql.DB, key string) int {
	t.Helper()
	var c int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_import_idempotency WHERE idempotency_key=? AND completed_at IS NULL`, key).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestAbandonOnInvalidStateTransition proves the state-transition boundary
// abandons the reservation: the retry is fresh and fails with the SAME typed
// error, never an idempotency in-flight rejection.
func TestAbandonOnInvalidStateTransition(t *testing.T) {
	svc, repo := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	current, _ := repo.Get(context.Background(), job.ID)
	if err := repo.UpdateStatus(context.Background(), job.ID, current.Status, StatusCancelled, current.Version); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-ist", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected state transition failure")
	}
	if kerr := kernel.AsAPIError(err); kerr.Code != kernel.ErrCodeStateTransition {
		t.Fatalf("expected INVALID_STATE_TRANSITION, got %s: %v", kerr.Code, err)
	}
	if n := countInflightIdempotency(t, repo.db, "k-ist"); n != 0 {
		t.Fatalf("state-transition failure left %d in-flight idempotency records", n)
	}

	// Immediate retry with the same key is a fresh attempt: same typed error,
	// NOT an idempotency in-flight rejection.
	_, err2 := svc.Execute(context.Background(), job.ID, 0, "platform", "k-ist", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err2 == nil {
		t.Fatal("expected state transition failure on retry")
	}
	if k2 := kernel.AsAPIError(err2); k2.Code != kernel.ErrCodeStateTransition {
		t.Fatalf("retry code = %s, want INVALID_STATE_TRANSITION (not in-flight)", k2.Code)
	}
	if n := countInflightIdempotency(t, repo.db, "k-ist"); n != 0 {
		t.Fatalf("retry leaked an in-flight record: %d", n)
	}
}

// TestAbandonOnStagingVerifyFailureThenRetrySucceeds proves the staging/hash
// verification boundary abandons the reservation and a retried request (after
// the underlying condition is repaired) succeeds immediately.
func TestAbandonOnStagingVerifyFailureThenRetrySucceeds(t *testing.T) {
	svc, repo := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	goodHash := job.SourceHash
	if _, err := repo.db.Exec(`UPDATE platform_imports SET source_hash='corrupt' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-verify", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected hash verification failure")
	}
	if n := countInflightIdempotency(t, repo.db, "k-verify"); n != 0 {
		t.Fatalf("verify failure left %d in-flight idempotency records", n)
	}

	// Repair the underlying condition and retry with the SAME key.
	if _, err := repo.db.Exec(`UPDATE platform_imports SET source_hash=? WHERE id=?`, goodHash, job.ID); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-verify", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("immediate retry after verify failure: %v", err)
	}
	if result.Status != StatusRunning {
		t.Fatalf("retry status = %s, want running", result.Status)
	}
}

// TestAbandonOnValidationPreconditionFailure proves the validation-precondition
// boundary (executeDurable's fresh path rejecting a non-validated import)
// abandons the reservation.
func TestAbandonOnValidationPreconditionFailure(t *testing.T) {
	svc, repo := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	// A running-but-unlinked import passes CanTransition(running) but must be
	// rejected by the fresh-submission dry-run precondition.
	if _, err := repo.db.Exec(`UPDATE platform_imports SET status='running' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-precond", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected dry-run precondition failure")
	}
	if !errors.Is(err, ErrDryRunRequired) {
		t.Fatalf("expected ErrDryRunRequired, got %v", err)
	}
	if n := countInflightIdempotency(t, repo.db, "k-precond"); n != 0 {
		t.Fatalf("precondition failure left %d in-flight idempotency records", n)
	}

	_, err2 := svc.Execute(context.Background(), job.ID, 0, "platform", "k-precond", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err2 == nil {
		t.Fatal("expected precondition failure on retry")
	}
	if !errors.Is(err2, ErrDryRunRequired) {
		t.Fatalf("retry err = %v, want ErrDryRunRequired (not in-flight)", err2)
	}
	if n := countInflightIdempotency(t, repo.db, "k-precond"); n != 0 {
		t.Fatalf("retry leaked an in-flight record: %d", n)
	}
}

// TestAbandonOnDurableLinkFailure proves the durable submission/link
// boundary abandons the reservation: after a MarkRunningAndLink failure the
// retry is a fresh attempt that recovers the same held job.
func TestAbandonOnDurableLinkFailure(t *testing.T) {
	svc, repo := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	repo.SetTestFailpoint(func() error { return errors.New("injected link failure") })
	_, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-link", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected link failure")
	}
	repo.SetTestFailpoint(nil)

	if n := countInflightIdempotency(t, repo.db, "k-link"); n != 0 {
		t.Fatalf("link failure left %d in-flight idempotency records", n)
	}
	// The held job is not claimable.
	if n := countRunnableImportJobs(t, repo.db); n != 0 {
		t.Fatalf("link failure left %d runnable jobs (orphan)", n)
	}

	result, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-link", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("immediate retry after link failure: %v", err)
	}
	if result.Status != StatusRunning {
		t.Fatalf("retry status = %s, want running", result.Status)
	}
	if n := countAllImportJobs(t, repo.db); n != 1 {
		t.Fatalf("expected exactly 1 durable job after retry, got %d", n)
	}
}

// TestAbandonOnCompensationFailure proves the compensation boundary abandons
// the reservation: the retry is fresh and is NOT rejected for being
// in-flight.
func TestAbandonOnCompensationFailure(t *testing.T) {
	svc, repo := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	// Run execute then mark completed so compensation is allowed.
	if _, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-comp-ex", "EXECUTE-IMPORT-"+itoa(job.ID)); err != nil {
		t.Fatal(err)
	}
	afterExecute, _ := repo.Get(context.Background(), job.ID)
	if err := repo.UpdateStatus(context.Background(), job.ID, afterExecute.Status, StatusCompleted, afterExecute.Version); err != nil {
		t.Fatal(err)
	}

	// Force the compensation read to fail after the reservation begins.
	if _, err := repo.db.Exec(`DROP TABLE platform_import_compensations`); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Compensate(context.Background(), job.ID, 0, "platform", "k-comp", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected compensation failure")
	}
	if n := countInflightIdempotency(t, repo.db, "k-comp"); n != 0 {
		t.Fatalf("compensation failure left %d in-flight idempotency records", n)
	}

	// Immediate retry with the same key must be a fresh attempt: it fails with
	// its own (state) error, never an idempotency in-flight rejection.
	_, err2 := svc.Compensate(context.Background(), job.ID, 0, "platform", "k-comp", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err2 == nil {
		t.Fatal("expected compensation failure on retry")
	}
	if k2 := kernel.AsAPIError(err2); k2.Code == kernel.ErrCodeIdempotencyReuse {
		t.Fatalf("retry was rejected as in-flight: %v", err2)
	}
	if n := countInflightIdempotency(t, repo.db, "k-comp"); n != 0 {
		t.Fatalf("retry leaked an in-flight record: %d", n)
	}
}

// TestAbandonOnIdempotencyCompleteFailureThenRetrySucceeds proves the
// response-persistence boundary: if IdempotencyComplete fails after the work
// succeeded, the reservation is abandoned and an immediate retry with the
// same key recovers and replays the same outcome instead of a second durable
// job.
func TestAbandonOnIdempotencyCompleteFailureThenRetrySucceeds(t *testing.T) {
	svc, repo := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	repo.SetIdempotencyCompleteFailpoint(func() error { return errors.New("injected complete failure") })
	_, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-complete", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected complete failure")
	}
	repo.SetIdempotencyCompleteFailpoint(nil)

	if n := countInflightIdempotency(t, repo.db, "k-complete"); n != 0 {
		t.Fatalf("complete failure left %d in-flight idempotency records", n)
	}

	result, err := svc.Execute(context.Background(), job.ID, 0, "platform", "k-complete", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("immediate retry after complete failure: %v", err)
	}
	if result.Status != StatusRunning {
		t.Fatalf("retry status = %s, want running", result.Status)
	}
	if n := countAllImportJobs(t, repo.db); n != 1 {
		t.Fatalf("expected exactly 1 durable job after retry, got %d", n)
	}
	if n := countInflightIdempotency(t, repo.db, "k-complete"); n != 0 {
		t.Fatalf("successful retry left %d in-flight records", n)
	}
}
