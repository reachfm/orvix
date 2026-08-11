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

// testWorkerHarness wires a real jobs service + registry + importer handler
// over the same sqlite DB used by the importer tests. The worker contract
// tests below are importer-specific: they exercise the platform.import
// handler through the real durable-job machinery, not generic jobs tests.
type testWorkerHarness struct {
	repo     *Repository
	staging  *StagingService
	svc      *Service
	jobSvc   *jobs.Service
	registry *jobs.Registry
}

func newWorkerHarness(t *testing.T) *testWorkerHarness {
	t.Helper()
	db := setupTestDB(t)
	repo := testRepo(t, db)
	staging := mustStaging(t)
	adapters := testAdapters(t, db)

	jobRepo := jobs.NewJobRepository(db)
	if err := jobRepo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry()
	jobSvc := jobs.NewServiceWithRegistry(jobRepo, registry, kernel.SystemClock{})

	svc := NewService(repo, adapters, staging, jobSvc, nil)
	if err := jobs.RegisterProductionHandlers(registry, nil, nil, svc); err != nil {
		t.Fatal(err)
	}
	return &testWorkerHarness{
		repo:     repo,
		staging:  staging,
		svc:      svc,
		jobSvc:   jobSvc,
		registry: registry,
	}
}

// createPlatformJob stages a source, validates it, and submits a durable
// platform.import job for it. Returns the import job and the durable job.
func (h *testWorkerHarness) createPlatformJob(t *testing.T, data []byte) (*ImportJob, *jobs.Job) {
	t.Helper()
	ctx := context.Background()
	job, err := h.svc.Create(ctx, CreateImportParams{
		Scope: "platform", Actor: "worker-test", SourceType: SourceCSV, SourceName: "w.csv",
	}, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.svc.Validate(ctx, job.ID, 0, "platform"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	submission := jobs.Submission{
		Scope: jobs.ScopePlatform, Actor: "worker-test", Type: ImportJobType,
		PayloadVersion: 1, Payload: marshalJSON(importJobPayload{ImportID: job.ID}),
		IdempotencyKey: "worker-test-" + itoa(job.ID), MaxAttempts: 3,
	}
	durable, _, err := h.jobSvc.Submit(ctx, submission)
	if err != nil {
		t.Fatalf("submit durable: %v", err)
	}
	// Mirrors Service.Execute: transition the import to Running before the
	// worker claims the durable job.
	current, _ := h.repo.Get(ctx, job.ID)
	if err := h.repo.UpdateStatus(ctx, job.ID, current.Status, StatusRunning, current.Version); err != nil {
		t.Fatalf("transition import to running: %v", err)
	}
	return job, durable
}

// runOnce drives a single worker iteration, mirroring jobs.Worker.RunOnce
// without the poll loop.
func (h *testWorkerHarness) runOnce(t *testing.T, owner string) error {
	t.Helper()
	ctx := context.Background()
	job, err := h.jobSvc.Claim(ctx, owner, time.Minute)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}
	def, ok := h.registry.Lookup(job.Type)
	if !ok {
		return h.jobSvc.Fail(ctx, leaseForTest(job), "UNREGISTERED", "unregistered", false)
	}
	exec := &testJobExecution{svc: h.jobSvc, lease: leaseForTest(job), tenantID: job.TenantID}
	execCtx, cancel := context.WithTimeout(ctx, def.Timeout)
	defer cancel()
	result, handlerErr := def.Handle(execCtx, exec, job.Payload)
	if handlerErr != nil {
		var ee *jobs.ExecutionError
		if errors.As(handlerErr, &ee) {
			return h.jobSvc.Fail(ctx, exec.lease, ee.Code, ee.Message, ee.Retryable)
		}
		return h.jobSvc.Fail(ctx, exec.lease, "JOB_EXECUTION_FAILED", handlerErr.Error(), true)
	}
	return h.jobSvc.Complete(ctx, exec.lease, result)
}

func leaseForTest(job *jobs.Job) jobs.Lease {
	return jobs.Lease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken, LeaseVersion: job.LeaseVersion}
}

// testJobExecution implements jobs.Execution through the real jobs service,
// so lease/fencing, heartbeats and progress are enforced by the real repo.
type testJobExecution struct {
	svc      *jobs.Service
	lease    jobs.Lease
	tenantID uint
	progress int
	hbCount  int
}

func (e *testJobExecution) TenantID() uint { return e.tenantID }
func (e *testJobExecution) Heartbeat(ctx context.Context) error {
	e.hbCount++
	return e.svc.Heartbeat(ctx, e.lease, time.Minute)
}
func (e *testJobExecution) SetProgress(ctx context.Context, progress int) error {
	e.progress = progress
	return e.svc.UpdateProgress(ctx, e.lease, progress)
}
func (e *testJobExecution) CancellationRequested(ctx context.Context) (bool, error) {
	return e.svc.CancellationRequested(ctx, e.lease)
}

// ── Two workers claiming the same import ─────────────────────────────

func TestTwoWorkersCannotBothClaimSameImport(t *testing.T) {
	h := newWorkerHarness(t)
	_, durable := h.createPlatformJob(t, []byte("entity,name,domain\norganization,Acme,acme.test\n"))

	// Two workers race to claim. Exactly one may win the lease.
	const workers = 2
	claimed := make(chan *jobs.Job, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			got, _ := h.jobSvc.Claim(context.Background(), "w"+itoa(uint(id)), time.Minute)
			claimed <- got
		}(i)
	}
	wg.Wait()
	close(claimed)
	winners := 0
	for got := range claimed {
		if got != nil {
			winners++
			if got.ID != durable.ID {
				t.Fatalf("claimed unexpected job %d", got.ID)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly one worker to claim, got %d", winners)
	}
}

// ── Stale lease / fencing rejection ──────────────────────────────────

func TestStaleLeaseRejectedOnHeartbeat(t *testing.T) {
	h := newWorkerHarness(t)
	h.createPlatformJob(t, []byte("entity,name,domain\norganization,Acme,acme.test\n"))

	claimed, err := h.jobSvc.Claim(context.Background(), "worker-a", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	// Worker B re-claims after expiry via recovery, then worker A's lease is
	// stale and must be rejected.
	lease := leaseForTest(claimed)
	stale := lease
	stale.Token = "stale-token"
	if err := h.jobSvc.Heartbeat(context.Background(), stale, time.Minute); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("stale heartbeat err=%v, want ErrLeaseLost", err)
	}
}

// ── Heartbeat preservation during long batches ───────────────────────

func TestHeartbeatsSentDuringLongBatch(t *testing.T) {
	h := newWorkerHarness(t)
	// A large CSV (> BatchSize rows) forces multiple bounded batches and
	// therefore multiple heartbeats.
	var b [][]byte
	b = append(b, []byte("entity,name,domain"))
	for i := 0; i < 120; i++ {
		b = append(b, []byte("organization,Org"+itoa(uint(i))+",org"+itoa(uint(i))+".test"))
	}
	var data []byte
	for i, line := range b {
		if i > 0 {
			data = append(data, '\n')
		}
		data = append(data, line...)
	}

	_, durable := h.createPlatformJob(t, data)
	claimed, err := h.jobSvc.Claim(context.Background(), "worker-a", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	exec := &testJobExecution{svc: h.jobSvc, lease: leaseForTest(claimed), tenantID: claimed.TenantID}
	def, _ := h.registry.Lookup(ImportJobType)
	if _, handlerErr := def.Handle(context.Background(), exec, claimed.Payload); handlerErr != nil {
		t.Fatalf("handler: %v", handlerErr)
	}
	if exec.hbCount < 2 {
		t.Fatalf("expected >=2 heartbeats across batches, got %d", exec.hbCount)
	}
	stored, _ := h.jobSvc.Get(context.Background(), durable.ID, 0, jobs.ScopePlatform)
	if stored.HeartbeatAt == nil {
		t.Fatal("heartbeat_at not persisted on durable job")
	}
}

// ── Cooperative cancellation during execution ─────────────────────────

func TestCancellationDuringExecution(t *testing.T) {
	h := newWorkerHarness(t)
	var data []byte
	rows := []string{"entity,name,domain"}
	for i := 0; i < 200; i++ {
		rows = append(rows, "organization,Org"+itoa(uint(i))+",org"+itoa(uint(i))+".test")
	}
	for i, r := range rows {
		if i > 0 {
			data = append(data, '\n')
		}
		data = append(data, r...)
	}

	_, durable := h.createPlatformJob(t, data)
	claimed, err := h.jobSvc.Claim(context.Background(), "worker-a", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if _, err := h.jobSvc.RequestCancellation(context.Background(), durable.ID, 0, jobs.ScopePlatform); err != nil {
		t.Fatal(err)
	}
	exec := &testJobExecution{svc: h.jobSvc, lease: leaseForTest(claimed), tenantID: claimed.TenantID}
	def, _ := h.registry.Lookup(ImportJobType)
	_, handlerErr := def.Handle(context.Background(), exec, claimed.Payload)
	if handlerErr == nil {
		t.Fatal("expected cancellation to abort handler")
	}
	var ee *jobs.ExecutionError
	if !errors.As(handlerErr, &ee) || ee.Code != "CANCELLED" {
		t.Fatalf("expected CANCELLED execution error, got %v", handlerErr)
	}
}

// ── Crash after checkpoint, then resume ──────────────────────────────

func TestCrashAfterCheckpointResumesWithoutDuplicates(t *testing.T) {
	h := newWorkerHarness(t)
	var data []byte
	rows := []string{"entity,name,domain"}
	for i := 0; i < 110; i++ {
		rows = append(rows, "organization,Org"+itoa(uint(i))+",org"+itoa(uint(i))+".test")
	}
	for i, r := range rows {
		if i > 0 {
			data = append(data, '\n')
		}
		data = append(data, r...)
	}

	job, durable := h.createPlatformJob(t, data)
	importJob, _ := h.repo.Get(context.Background(), job.ID)
	dataBytes, _ := h.staging.Read(importJob.StagingID)

	// First run: the executor commits the first batch checkpoint (BatchSize
	// rows) and then the process "crashes" — the batch hook fails after that
	// checkpoint, so Execute returns an error, no completion is recorded, and
	// the import stays Running. This is the durable-contract crash-after-
	// checkpoint shape.
	firstExec := NewExecutor(testAdapters(t, h.repo.db), h.repo, 0, "crash-key")
	firstExec.BatchSize = BatchSize
	firstExec.Execution = &crashAfterCheckpointExecution{limit: 0}
	if _, err := firstExec.Execute(context.Background(), importJob, dataBytes); err == nil {
		t.Fatal("expected first (crashing) run to fail")
	}
	_ = durable

	// Import must still be Running and a checkpoint for the first batch exists.
	before, _ := h.repo.Get(context.Background(), job.ID)
	if before.Status != StatusRunning {
		t.Fatalf("import status after crash-run = %s, want running", before.Status)
	}
	cp, _ := h.repo.LastCheckpoint(context.Background(), job.ID)
	if cp == nil || cp.ProcessedCount != BatchSize {
		t.Fatalf("checkpoint after first batch = %+v, want ProcessedCount=%d", cp, BatchSize)
	}

	// Resume with the real adapters: the executor resumes from the last
	// committed checkpoint and the row-level compensation records prevent
	// re-creating rows created before the crash.
	resumeExec := NewExecutor(testAdapters(t, h.repo.db), h.repo, 0, "resume-key")
	resumeExec.BatchSize = BatchSize
	importJob2, _ := h.repo.Get(context.Background(), job.ID)
	dataBytes2, _ := h.staging.Read(importJob2.StagingID)
	if _, err := resumeExec.Execute(context.Background(), importJob2, dataBytes2); err != nil {
		t.Fatalf("resumed executor run: %v", err)
	}

	// Exactly 110 organizations must exist — no duplicates.
	if count := countOrgs(t, h.repo.db); count != 110 {
		t.Fatalf("expected 110 orgs after crash+resume, got %d", count)
	}
}

// crashAfterCheckpointExecution simulates a worker process crash: it lets a
// fixed number of batch progress updates through (so the checkpoint commits)
// and then fails, aborting execution before completion.
type crashAfterCheckpointExecution struct {
	limit int
	seen  int
}

func (c *crashAfterCheckpointExecution) Heartbeat(context.Context) error { return nil }
func (c *crashAfterCheckpointExecution) SetProgress(context.Context, int) error {
	c.seen++
	if c.seen > c.limit {
		return errors.New("injected crash after checkpoint")
	}
	return nil
}
func (c *crashAfterCheckpointExecution) CancellationRequested(context.Context) (bool, error) {
	return false, nil
}

// failAfterAdapters wraps the real test adapters so the org create fails
// after limit successful creates — a deterministic mid-batch crash.
func failAfterAdapters(t *testing.T, h *testWorkerHarness, limit int) *Adapters {
	t.Helper()
	real := testAdapters(t, h.repo.db)
	return NewAdapters(
		&failingOrgPort{inner: real.Org, limit: limit},
		real.Admin, real.Domain, real.Mailbox, real.Alias, real.Group,
	)
}

type failingOrgPort struct {
	inner OrganizationPort
	limit int
	used  int
}

func (f *failingOrgPort) CreateOrganization(ctx context.Context, name, domain string, tenantID uint) (uint, error) {
	if f.used >= f.limit {
		return 0, errors.New("injected org crash after checkpoint")
	}
	f.used++
	return f.inner.CreateOrganization(ctx, name, domain, tenantID)
}
func (f *failingOrgPort) SoftDeleteOrganization(ctx context.Context, id, tenantID uint) error {
	return f.inner.SoftDeleteOrganization(ctx, id, tenantID)
}

// ── Resume without duplicate entities (direct executor proof) ────────

func TestExecutorResumeFromCheckpointNoDuplicates(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)
	staging := mustStaging(t)
	svc := NewService(repo, testAdapters(t, db), staging, nil, nil)

	var data []byte
	rows := []string{"entity,name,domain"}
	for i := 0; i < 100; i++ {
		rows = append(rows, "organization,Org"+itoa(uint(i))+",org"+itoa(uint(i))+".test")
	}
	for i, r := range rows {
		if i > 0 {
			data = append(data, '\n')
		}
		data = append(data, r...)
	}

	job, err := svc.Create(context.Background(), CreateImportParams{
		Scope: "platform", Actor: "resume", SourceType: SourceCSV, SourceName: "r.csv",
	}, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Validate(context.Background(), job.ID, 0, "platform"); err != nil {
		t.Fatal(err)
	}

	// First executor run processes everything (checkpoint every 50 rows).
	exec := NewExecutor(testAdapters(t, db), repo, 0, "resume-key")
	exec.BatchSize = 50
	loaded, _ := repo.Get(context.Background(), job.ID)
	loaded.Status = StatusRunning
	if _, err := exec.Execute(context.Background(), loaded, data); err != nil {
		t.Fatal(err)
	}
	firstCount := countOrgs(t, db)
	if firstCount != 100 {
		t.Fatalf("first run created %d orgs, want 100", firstCount)
	}

	// Second run (e.g. a crashed-then-resumed worker) must create nothing new
	// because row-level compensation records prevent duplicates.
	secondExec := NewExecutor(testAdapters(t, db), repo, 0, "resume-key")
	secondExec.BatchSize = 50
	loaded2, _ := repo.Get(context.Background(), job.ID)
	if _, err := secondExec.Execute(context.Background(), loaded2, data); err != nil {
		t.Fatal(err)
	}
	if secondCount := countOrgs(t, db); secondCount != 100 {
		t.Fatalf("resumed run created %d orgs (duplicates), want 100", secondCount)
	}
}

func countOrgs(t *testing.T, db *sql.DB) int {
	t.Helper()
	var c int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants`).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}
