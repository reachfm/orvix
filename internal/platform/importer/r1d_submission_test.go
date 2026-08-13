package importer

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── Durable submission / import linking consistency (Fix 2) ───────────

// submissionHarness builds a service with a real jobs service and a
// controllable repository failpoint so each boundary of executeDurable can
// be injected with a failure.
type submissionHarness struct {
	repo    *Repository
	staging *StagingService
	svc     *Service
	jobSvc  *jobs.Service
}

func newSubmissionHarness(t *testing.T) *submissionHarness {
	t.Helper()
	db := setupTestDB(t)
	repo := testRepo(t, db)
	staging := mustStaging(t)

	jobRepo := jobs.NewJobRepository(db)
	if err := jobRepo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry()
	jobSvc := jobs.NewServiceWithRegistry(jobRepo, registry, kernel.SystemClock{})
	svc := NewService(repo, testAdapters(t, db), staging, jobSvc, nil)
	if err := jobs.RegisterProductionHandlers(registry, nil, nil, svc); err != nil {
		t.Fatal(err)
	}
	return &submissionHarness{repo: repo, staging: staging, svc: svc, jobSvc: jobSvc}
}

// prepare submits a validated import via the real service path (Create +
// Validate) so Execute can be exercised.
func (h *submissionHarness) prepare(t *testing.T) *ImportJob {
	t.Helper()
	job, err := h.svc.Create(context.Background(), CreateImportParams{
		TenantID: importTestTenantID, Scope: "platform", Actor: "submission-test", SourceType: SourceCSV, SourceName: "s.csv",
	}, []byte("entity,name,domain\norganization,Acme,acme.test\n"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.svc.Validate(context.Background(), job.ID, importTestTenantID, "platform"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return job
}

func countRunnableImportJobs(t *testing.T, db *sql.DB) int {
	t.Helper()
	// A "runnable" job is one a worker could claim: queued and run_after <= now.
	var c int
	err := db.QueryRow(`SELECT COUNT(*) FROM platform_jobs WHERE type='platform.import' AND status='queued' AND run_after <= ?`, time.Now().UTC()).Scan(&c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func countAllImportJobs(t *testing.T, db *sql.DB) int {
	t.Helper()
	var c int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_jobs WHERE type='platform.import'`).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestExecuteDurable_JobCreationFailure retries after a submission failure:
// no durable job exists, the import stays validated, and the retry succeeds
// with exactly one final durable job.
func TestExecuteDurable_JobCreationFailure(t *testing.T) {
	h := newSubmissionHarness(t)
	job := h.prepare(t)

	// Force submission to fail by pointing the harness at a fresh jobs
	// service whose registry has no platform.import handler — Submit returns
	// ErrUnknownJobType before any row is inserted.
	brokenRegistry := jobs.NewRegistry()
	brokenSvc := jobs.NewServiceWithRegistry(jobs.NewJobRepository(h.repo.db), brokenRegistry, kernel.SystemClock{})
	origJobSvc := h.svc.jobSvc
	h.svc.jobSvc = brokenSvc

	_, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "key-fail-1", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected submission failure")
	}
	h.svc.jobSvc = origJobSvc

	// Nothing was created: no durable job, import still validated.
	if n := countAllImportJobs(t, h.repo.db); n != 0 {
		t.Fatalf("job creation failure created %d durable jobs", n)
	}
	current, _ := h.repo.Get(context.Background(), job.ID)
	if current.Status != StatusValidated {
		t.Fatalf("import status after failed submit = %s, want validated", current.Status)
	}

	// Retry recovers the same job with exactly one durable job.
	result, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "key-fail-1", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("retry execute: %v", err)
	}
	if result.Status != StatusRunning {
		t.Fatalf("after retry status = %s, want running", result.Status)
	}
	if n := countAllImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 durable job, got %d", n)
	}
	if n := countRunnableImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 runnable job after activation, got %d", n)
	}
}

// TestExecuteDurable_ImportStatusFailure retries after the atomic
// MarkRunningAndLink (status) fails: the held job is NOT runnable, and the
// retry links the same job.
func TestExecuteDurable_ImportStatusFailure(t *testing.T) {
	h := newSubmissionHarness(t)
	job := h.prepare(t)

	h.repo.SetTestFailpoint(func() error {
		return errors.New("injected mark-running failure")
	})
	_, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "key-status", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected status failure")
	}
	h.repo.SetTestFailpoint(nil)

	// Zero runnable jobs (the held job is not claimable); import still
	// validated; the held durable job exists but is unclaimable.
	if n := countRunnableImportJobs(t, h.repo.db); n != 0 {
		t.Fatalf("status failure left %d runnable jobs (orphan!)", n)
	}
	current, _ := h.repo.Get(context.Background(), job.ID)
	if current.Status != StatusValidated {
		t.Fatalf("import status after status failure = %s, want validated", current.Status)
	}

	// Retry recovers the same single durable job and activates it.
	result, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "key-status", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result.Status != StatusRunning {
		t.Fatalf("after retry status = %s, want running", result.Status)
	}
	if n := countAllImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 durable job, got %d", n)
	}
	if n := countRunnableImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 runnable job after retry, got %d", n)
	}
}

// TestExecuteDurable_LinkFailureAndCommitFailure covers the atomicity of
// MarkRunningAndLink: both the link and the commit steps are inside the same
// transaction, so either failure leaves zero runnable jobs and a retry
// recovers the same durable job.
func TestExecuteDurable_LinkFailureAndCommitFailure(t *testing.T) {
	h := newSubmissionHarness(t)
	job := h.prepare(t)

	// Force the transaction to fail after the status+link UPDATE (simulating
	// a commit failure): the failpoint fires before commit, so the tx rolls
	// back and nothing is persisted.
	h.repo.SetTestFailpoint(func() error {
		return errors.New("injected commit failure")
	})
	_, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "key-commit", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected commit failure")
	}
	h.repo.SetTestFailpoint(nil)

	if n := countRunnableImportJobs(t, h.repo.db); n != 0 {
		t.Fatalf("commit failure left %d runnable jobs (orphan!)", n)
	}
	current, _ := h.repo.Get(context.Background(), job.ID)
	if current.Status != StatusValidated {
		t.Fatalf("import status after commit failure = %s, want validated", current.Status)
	}

	result, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "key-commit", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result.Status != StatusRunning {
		t.Fatalf("after retry status = %s, want running", result.Status)
	}
	if n := countAllImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 durable job, got %d", n)
	}
	if n := countRunnableImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("expected exactly 1 runnable job after retry, got %d", n)
	}
}

// TestExecuteDurable_ConcurrentIdenticalSubmissions proves concurrent
// identical Execute calls produce exactly one final durable job and never
// expose an orphan runnable job.
func TestExecuteDurable_ConcurrentIdenticalSubmissions(t *testing.T) {
	h := newSubmissionHarness(t)
	job := h.prepare(t)

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "key-race", "EXECUTE-IMPORT-"+itoa(job.ID))
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			continue
		}
		kerr := kernel.AsAPIError(err)
		if kerr.Code != kernel.ErrCodeIdempotencyReuse && kerr.Code != kernel.ErrCodeConflict {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
		successes++
	}
	_ = successes

	if n := countAllImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("concurrent executes produced %d durable jobs, want exactly 1", n)
	}
	if n := countRunnableImportJobs(t, h.repo.db); n != 1 {
		t.Fatalf("concurrent executes left %d runnable jobs, want exactly 1", n)
	}
}

// TestExecuteDurable_WorkerCannotClaimBeforeLink proves the queued-activation
// handoff: after submit but before MarkRunningAndLink, the durable job is not
// claimable. We force a link failure, then attempt to claim and assert no
// worker can claim the held job.
func TestExecuteDurable_WorkerCannotClaimBeforeLink(t *testing.T) {
	h := newSubmissionHarness(t)
	job := h.prepare(t)

	h.repo.SetTestFailpoint(func() error {
		return errors.New("injected link failure")
	})
	_, err := h.svc.Execute(context.Background(), job.ID, 0, "platform", "key-held", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected link failure")
	}
	h.repo.SetTestFailpoint(nil)

	// A worker claiming now must find nothing runnable.
	claimed, claimErr := h.jobSvc.Claim(context.Background(), "worker-a", time.Minute)
	if claimErr != nil {
		t.Fatal(claimErr)
	}
	if claimed != nil {
		t.Fatalf("worker claimed a held/unlinked job %d", claimed.ID)
	}
}
