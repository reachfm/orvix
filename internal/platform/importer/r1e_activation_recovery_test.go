package importer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── Fix 1: activation-failure recovery (R1E) ─────────────────────────
//
// executeDurable submits a held durable job, atomically marks the import
// running and links it, then activates it. If activation fails the import is
// already running+linked but the durable job remains held. The recovery path
// must re-activate the SAME job on retry — never create a second durable job
// and never wait for the 24-hour hold.

func prepareValidated(t *testing.T, h *testWorkerHarness, name, domain string) *ImportJob {
	t.Helper()
	data := []byte("entity,name,domain\norganization," + name + "," + domain + "\n")
	job, err := h.svc.Create(context.Background(), CreateImportParams{
		TenantID: importTestTenantID, Scope: "platform", Actor: "r1e-test", SourceType: SourceCSV, SourceName: "r1e.csv",
	}, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.svc.Validate(context.Background(), job.ID, importTestTenantID, "platform"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return job
}

// TestExecuteDurable_ActivationFailureRecoversSameJob injects an activation
// failure after MarkRunningAndLink succeeded, proves the import is linked
// and the held job is not claimable, then retries with the SAME idempotency
// key: the same durable job is activated, exactly one durable job exists,
// and the real worker completes the import.
func TestExecuteDurable_ActivationFailureRecoversSameJob(t *testing.T) {
	h := newWorkerHarness(t)
	job := prepareValidated(t, h, "Acme", "acme.test")

	// Inject the activation failure AFTER MarkRunningAndLink succeeds.
	h.svc.SetActivateFailpoint(func() error { return errors.New("injected activation failure") })
	_, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "act-key-1", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected activation failure")
	}
	h.svc.SetActivateFailpoint(nil)

	// The import is linked and running; the durable job is NOT claimable
	// (activation-pending state).
	current, err := h.repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != StatusRunning {
		t.Fatalf("status after activation failure = %s, want running", current.Status)
	}
	if current.JobID == 0 {
		t.Fatal("import must be linked to a durable job after activation failure")
	}
	if n := countRunnableImportJobs(t, h.repo.db); n != 0 {
		t.Fatalf("held job must not be claimable after activation failure, got %d runnable", n)
	}
	// The failed attempt must not leave an in-flight idempotency record.
	if n := countInflightIdempotency(t, h.repo.db, "act-key-1"); n != 0 {
		t.Fatalf("activation failure left %d in-flight idempotency records", n)
	}

	// Retry with the SAME idempotency key: recover the same durable job and
	// activate it now (no 24-hour wait, no second durable job).
	result, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "act-key-1", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("retry same key: %v", err)
	}
	if result.JobID != current.JobID {
		t.Fatalf("retry recovered a different durable job %d, want same job %d", result.JobID, current.JobID)
	}
	if n := countAllImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 durable job, got %d", n)
	}
	if n := countRunnableImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 runnable job after activation recovery, got %d", n)
	}

	// The real worker completes the import through the registered handler.
	if err := h.runOnce(t, "worker-act"); err != nil {
		t.Fatalf("worker run: %v", err)
	}
	final, _ := h.repo.Get(context.Background(), job.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("final import status = %s, want completed", final.Status)
	}
	durable, _ := h.jobSvc.Get(context.Background(), current.JobID, 0, jobs.ScopePlatform)
	if durable.Status != jobs.StatusSucceeded {
		t.Fatalf("final durable status = %s, want succeeded", durable.Status)
	}
}

// TestResumeAfterActivationFailureRecoversSameLinkedJob retries with a NEW
// request idempotency key over the Resume path: the same durable job must be
// recovered and activated, exactly one durable job exists, and the worker
// completes the import.
func TestResumeAfterActivationFailureRecoversSameLinkedJob(t *testing.T) {
	h := newWorkerHarness(t)
	job := prepareValidated(t, h, "Beta", "beta.test")

	h.svc.SetActivateFailpoint(func() error { return errors.New("injected activation failure") })
	if _, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "act-key-resume", "EXECUTE-IMPORT-"+itoa(job.ID)); err == nil {
		t.Fatal("expected activation failure")
	}
	h.svc.SetActivateFailpoint(nil)

	current, _ := h.repo.Get(context.Background(), job.ID)
	if current.JobID == 0 {
		t.Fatal("import not linked after activation failure")
	}

	// New key, resume path.
	result, err := h.svc.Resume(context.Background(), job.ID, 0, "platform", "resume-act-recovery")
	if err != nil {
		t.Fatalf("resume after activation failure: %v", err)
	}
	if result.JobID != current.JobID {
		t.Fatalf("resume recovered a different durable job %d, want %d", result.JobID, current.JobID)
	}
	if n := countAllImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 durable job, got %d", n)
	}
	if n := countRunnableImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 runnable job after resume, got %d", n)
	}

	if err := h.runOnce(t, "worker-resume"); err != nil {
		t.Fatalf("worker run: %v", err)
	}
	final, _ := h.repo.Get(context.Background(), job.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("final import status = %s, want completed", final.Status)
	}
}

// TestExecuteDurable_ConcurrentActivationRecovery proves concurrent
// activation retries (different keys) all recover the SAME held durable job
// and never create a second one.
func TestExecuteDurable_ConcurrentActivationRecovery(t *testing.T) {
	h := newWorkerHarness(t)
	job := prepareValidated(t, h, "Gamma", "gamma.test")

	h.svc.SetActivateFailpoint(func() error { return errors.New("injected activation failure") })
	if _, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "con-act-primer", "EXECUTE-IMPORT-"+itoa(job.ID)); err == nil {
		t.Fatal("expected activation failure")
	}
	h.svc.SetActivateFailpoint(nil)

	linked, _ := h.repo.Get(context.Background(), job.ID)
	if linked.JobID == 0 {
		t.Fatal("import not linked after activation failure")
	}

	const workers = 6
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "con-act-"+itoa(uint(n)), "EXECUTE-IMPORT-"+itoa(job.ID))
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent activation retry failed: %v", err)
		}
	}
	if n := countAllImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("concurrent activation retries produced %d durable jobs, want exactly 1", n)
	}
	if n := countRunnableImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("concurrent activation retries left %d runnable jobs, want exactly 1", n)
	}
	// The activated job is the SAME held job.
	var runnableID uint
	if err := h.repo.db.QueryRow(`SELECT id FROM platform_jobs WHERE type='platform.import' AND status='queued' AND run_after <= ?`, time.Now().UTC()).Scan(&runnableID); err != nil {
		t.Fatal(err)
	}
	if runnableID != linked.JobID {
		t.Fatalf("activated job %d != linked job %d", runnableID, linked.JobID)
	}
}

// TestExecuteDurable_LinkedToForeignJobFailsClosed corrupts an import's
// linkage to point at another import's durable job: execution must fail
// closed and must never activate the foreign job.
func TestExecuteDurable_LinkedToForeignJobFailsClosed(t *testing.T) {
	h := newWorkerHarness(t)
	jobA := prepareValidated(t, h, "Delta", "delta.test")
	jobB := prepareValidated(t, h, "Epsilon", "epsilon.test")

	// Submit B's durable platform.import job (held) so a foreign job exists
	// with a DIFFERENT derived idempotency key.
	foreign, _, err := h.jobSvc.Submit(context.Background(), jobs.Submission{
		Scope: jobs.ScopePlatform, Actor: "r1e-test", Type: ImportJobType,
		PayloadVersion: 1, Payload: mustMarshalJSON(importJobPayload{ImportID: jobB.ID, TenantID: jobB.TenantID, Scope: jobB.Scope}),
		IdempotencyKey: "import-run-" + itoa(jobB.ID), CorrelationID: "import_" + itoa(jobB.ID),
		MaxAttempts: 3, RunAfter: time.Now().UTC().Add(activationHold),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt A's linkage: running but pointed at B's durable job.
	if _, err := h.repo.db.Exec(`UPDATE platform_imports SET status='running', job_id=? WHERE id=?`, foreign.ID, jobA.ID); err != nil {
		t.Fatal(err)
	}

	_, err = h.svc.Execute(context.Background(), jobA.ID, 0, "platform", "foreign-key", "EXECUTE-IMPORT-"+itoa(jobA.ID))
	if err == nil {
		t.Fatal("expected fail-closed conflict for foreign linkage")
	}
	kerr := kernel.AsAPIError(err)
	if kerr.Code != kernel.ErrCodeConflict {
		t.Fatalf("expected CONFLICT, got %s: %v", kerr.Code, err)
	}
	// The foreign job must NOT have been activated: still held, not runnable.
	if n := countRunnableImportJobs(t, h.repo.db); n != 0 {
		t.Fatalf("foreign job must not be activated, got %d runnable", n)
	}
	foreignCurrent, _ := h.jobSvc.Get(context.Background(), foreign.ID, 0, jobs.ScopePlatform)
	if foreignCurrent == nil {
		t.Fatal("foreign durable job not found")
	}
	if foreignCurrent.Status != jobs.StatusQueued {
		t.Fatalf("foreign job status = %s, want queued", foreignCurrent.Status)
	}
	if !foreignCurrent.RunAfter.After(time.Now().UTC()) {
		t.Fatal("foreign job run_after was moved to the past: it was activated")
	}
}
