package importer

import (
	"context"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── Idempotency for execute/resume/compensate (Fix 7) ────────────────

// setupIdempotentHarness builds a service with a real jobs service so
// Execute can submit durable jobs, plus a staging service.
func setupIdempotentHarness(t *testing.T) (*Service, *Repository) {
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
	return svc, repo
}

// makeValidatedImport stages + validates a small CSV so it is ready to
// execute. It returns the import job.
func makeValidatedImport(t *testing.T, svc *Service) *ImportJob {
	t.Helper()
	data := []byte("entity,name,domain\norganization,Acme,acme.test\n")
	job, err := svc.Create(context.Background(), CreateImportParams{
		Scope: "platform", Actor: "idem-user", SourceType: SourceCSV, SourceName: "idem.csv",
	}, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Validate(context.Background(), job.ID, 0, "platform"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return job
}

func TestExecuteSameKeySameRequestReplaysResult(t *testing.T) {
	svc, repo := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	first, err := svc.Execute(context.Background(), job.ID, 0, "platform", "key-execute-1", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	// Second identical request with the same key must replay the original
	// result and not re-submit a second durable job.
	second, err := svc.Execute(context.Background(), job.ID, 0, "platform", "key-execute-1", "EXECUTE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if first.ID != second.ID || first.Status != second.Status {
		t.Fatalf("replay mismatch: %+v vs %+v", first, second)
	}
	var durableCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM platform_jobs WHERE type='platform.import'`).Scan(&durableCount); err != nil {
		t.Fatal(err)
	}
	if durableCount != 1 {
		t.Fatalf("expected exactly 1 durable job, got %d", durableCount)
	}
}

func TestExecuteSameKeyChangedRequestReturnsConflict(t *testing.T) {
	svc, _ := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	if _, err := svc.Execute(context.Background(), job.ID, 0, "platform", "key-execute-conflict", "EXECUTE-IMPORT-"+itoa(job.ID)); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	// Same key, different action (resume vs execute) must be a conflict.
	_, err := svc.Resume(context.Background(), job.ID, 0, "platform", "key-execute-conflict")
	if err == nil {
		t.Fatal("expected idempotency conflict on key reuse with a different request")
	}
	kerr := kernel.AsAPIError(err)
	if kerr.Code != kernel.ErrCodeIdempotencyReuse {
		t.Fatalf("expected ErrCodeIdempotencyReuse, got %s: %v", kerr.Code, err)
	}
}

func TestExecuteConcurrentIdenticalRequestsExecuteOnce(t *testing.T) {
	svc, repo := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	const workers = 4
	keys := make(chan int, workers)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Execute(context.Background(), job.ID, 0, "platform", "key-execute-race", "EXECUTE-IMPORT-"+itoa(job.ID))
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(keys)
	for err := range errs {
		if err == nil {
			continue
		}
		// In-flight duplicates surface as a conflict, which is the expected
		// outcome for concurrent identical requests — never double-execution.
		kerr := kernel.AsAPIError(err)
		if kerr.Code != kernel.ErrCodeIdempotencyReuse && kerr.Code != kernel.ErrCodeConflict {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	var durableCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM platform_jobs WHERE type='platform.import'`).Scan(&durableCount); err != nil {
		t.Fatal(err)
	}
	if durableCount > 1 {
		t.Fatalf("concurrent identical executes produced %d durable jobs", durableCount)
	}
}

func TestCompensateSameKeyReplaysResult(t *testing.T) {
	svc, _ := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)

	// Execute to completion first.
	if _, err := svc.Execute(context.Background(), job.ID, 0, "platform", "key-comp-1", "EXECUTE-IMPORT-"+itoa(job.ID)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Force import to Completed (the durable worker would normally do this).
	afterExecute, _ := svc.repo.Get(context.Background(), job.ID)
	if err := svc.repo.UpdateStatus(context.Background(), job.ID, afterExecute.Status, StatusCompleted, afterExecute.Version); err != nil {
		t.Fatalf("mark completed: %v", err)
	}

	first, err := svc.Compensate(context.Background(), job.ID, 0, "platform", "key-compensate-1", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("first compensate: %v", err)
	}
	second, err := svc.Compensate(context.Background(), job.ID, 0, "platform", "key-compensate-1", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("second compensate: %v", err)
	}
	if first.ID != second.ID || first.Status != second.Status {
		t.Fatalf("compensate replay mismatch: %+v vs %+v", first, second)
	}
}

func TestCompensateRequiresIdempotencyKey(t *testing.T) {
	svc, _ := setupIdempotentHarness(t)
	job := makeValidatedImport(t, svc)
	_, err := svc.Compensate(context.Background(), job.ID, 0, "platform", "", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected idempotency-key-required error")
	}
	kerr := kernel.AsAPIError(err)
	if kerr.Code != kernel.ErrCodeValidation {
		t.Fatalf("expected validation error, got %s", kerr.Code)
	}
}
