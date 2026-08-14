package bulkprovision

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
)

// setBatchSizeForTest temporarily shrinks DefaultBatchSize so
// batch/checkpoint boundaries are observable with a small row count.
func setBatchSizeForTest(t *testing.T, n int) {
	t.Helper()
	DefaultBatchSize = n
}

// ── Fix I / Stage 5+7: checkpoint, crash-resume, conflict policy ────

// TestExecute_CrashMidBatchResumesWithoutDuplication is the core
// durability proof: a hook that fails mid-run simulates a lost lease
// or process crash. Execute stops immediately, leaving the job
// "running" with its checkpoint persisted through the LAST completed
// batch. Calling Execute again — as a fresh worker with a new lease
// would — resumes strictly from the checkpoint and never re-creates a
// mailbox that already succeeded.
func TestExecute_CrashMidBatchResumesWithoutDuplication(t *testing.T) {
	_, repo, ob, as := newDurableTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, as, nil)
	ctx := context.Background()

	var raw []RawRow
	for i := 0; i < 5; i++ {
		raw = append(raw, RawRow{RowNumber: i + 2, Email: fmt.Sprintf("u%d@x.test", i)})
	}
	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-1", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Force a tiny batch size so a "crash" partway through is
	// observable with only 5 rows.
	origBatch := DefaultBatchSize
	setBatchSizeForTest(t, 2)
	defer setBatchSizeForTest(t, origBatch)

	batchesSeen := 0
	crashAfterBatch := 1
	hooks := &ExecuteHooks{BeforeBatch: func(ctx context.Context) error {
		batchesSeen++
		if batchesSeen > crashAfterBatch+1 {
			return errors.New("simulated lease loss")
		}
		return nil
	}}

	paused, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-1", hooks)
	if err != nil {
		t.Fatalf("execute (crash): %v", err)
	}
	if paused.Status != JobRunning {
		t.Fatalf("expected the job to remain running after a simulated crash, got %s", paused.Status)
	}
	if paused.CreatedCount == 0 || paused.CreatedCount >= 5 {
		t.Fatalf("expected a PARTIAL created count (crash mid-run), got %d", paused.CreatedCount)
	}
	checkpointAt := paused.NextRowNumber
	if checkpointAt <= 1 {
		t.Fatalf("expected the checkpoint to have advanced, got NextRowNumber=%d", paused.NextRowNumber)
	}

	// A fresh "worker" resumes with hooks that no longer fail.
	final, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-1", nil)
	if err != nil {
		t.Fatalf("resume execute: %v", err)
	}
	if final.Status != JobCompleted {
		t.Fatalf("expected the resumed job to complete, got %s", final.Status)
	}
	if final.CreatedCount != 5 {
		t.Fatalf("expected exactly 5 mailboxes created across both runs, got %d", final.CreatedCount)
	}

	// The decisive assertion: exactly 5 distinct mailboxes exist, never
	// 6+ from a duplicate creation of an already-succeeded row.
	fm.mu.Lock()
	distinct := len(fm.byEmail)
	fm.mu.Unlock()
	if distinct != 5 {
		t.Fatalf("expected exactly 5 distinct mailboxes, got %d (crash/resume duplicated a row)", distinct)
	}
}

// TestExecute_CancelDuringRunStopsFutureBatches proves cooperative
// cancellation: Cancel() bumps the job's version; the version-guarded
// CheckpointBatch that a concurrently-running Execute attempts next
// therefore fails, and Execute stops without processing further rows
// or transitioning to a success terminal state.
func TestExecute_CancelDuringRunStopsFutureBatches(t *testing.T) {
	_, repo := newTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	ctx := context.Background()

	var raw []RawRow
	for i := 0; i < 4; i++ {
		raw = append(raw, RawRow{RowNumber: i + 2, Email: fmt.Sprintf("c%d@x.test", i)})
	}
	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-2", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	setBatchSizeForTest(t, 1)
	defer setBatchSizeForTest(t, DefaultBatchSize)

	cancelled := false
	hooks := &ExecuteHooks{BeforeBatch: func(ctx context.Context) error {
		if !cancelled {
			cancelled = true
			// Simulate a concurrent Cancel() landing between batches:
			// transition Running -> Cancelled directly, which bumps
			// the job's version out from under this Execute call.
			current, _ := repo.GetJob(ctx, job.ID, 1)
			_, _ = repo.TransitionJobIfVersion(ctx, job.ID, JobRunning, JobCancelled, current.Version, svc.clock.Now())
		}
		return nil
	}}

	result, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-2", hooks)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != JobCancelled {
		t.Fatalf("expected the job to end cancelled, got %s", result.Status)
	}
	if result.CreatedCount >= 4 {
		t.Fatalf("expected execution to stop before all rows were processed, got created=%d", result.CreatedCount)
	}
}

// TestExecute_SkipExistingConflictPolicyLeavesMailboxUnmodified proves
// the skip_existing policy: an already-existing mailbox is neither a
// failure nor touched, and the row is recorded skipped.
func TestExecute_SkipExistingConflictPolicyLeavesMailboxUnmodified(t *testing.T) {
	_, repo, ob, as := newDurableTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, as, nil)
	ctx := context.Background()

	// Pre-existing mailbox not created through this package (simulates
	// a mailbox that already existed before the import ran).
	if _, err := fm.CreateMailbox(ctx, mailboxReq("existing@x.test"), 1); err != nil {
		t.Fatalf("seed existing mailbox: %v", err)
	}
	preID := fm.byEmail["existing@x.test"]

	// Validate only sees NEW rows (existing@x.test would be flagged
	// invalid by Validate's own duplicate check) — skip_existing is
	// specifically for the race where a row was valid at validate time
	// but the address was created by something else before Execute ran.
	// We simulate that race directly by bypassing Validate's dup check:
	// insert the row as RowValid directly through CreateJob's row list.
	res := &ValidationResult{TotalRows: 1, ValidRows: 1, SourceHash: "hash-3", SchemaVersion: SchemaVersion,
		Rows: []Row{{RowNumber: 2, Email: "existing@x.test", Status: RowValid, AccessMode: AccessInherit}}}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictSkipExisting, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	final, rows, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-3", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if final.Status != JobCompleted {
		t.Fatalf("skip_existing must not fail the job, got %s", final.Status)
	}
	if final.SkippedCount != 1 || final.CreatedCount != 0 || final.FailedCount != 0 {
		t.Fatalf("expected 1 skipped, 0 created, 0 failed, got created=%d failed=%d skipped=%d",
			final.CreatedCount, final.FailedCount, final.SkippedCount)
	}
	if len(rows) != 1 || rows[0].Status != RowSkipped {
		t.Fatalf("expected the row to be recorded RowSkipped, got %+v", rows)
	}
	// The pre-existing mailbox's ID must be completely untouched.
	if fm.byEmail["existing@x.test"] != preID {
		t.Fatal("the existing mailbox must not have been recreated or reassigned")
	}
}

// TestExecute_SourceHashMismatchRefused proves a job cannot execute
// against a source that no longer matches what was validated.
func TestExecute_SourceHashMismatchRefused(t *testing.T) {
	_, repo := newTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-original", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-tampered", nil); !errors.Is(err, ErrSourceHashMismatch) {
		t.Fatalf("expected ErrSourceHashMismatch, got %v", err)
	}
}

func mailboxReq(email string) mailbox.CreateMailboxRequest {
	return mailbox.CreateMailboxRequest{Email: email, Password: "seed-password", ForcePasswordChange: true}
}
