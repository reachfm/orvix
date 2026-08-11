package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
)

const ImportJobType = "platform.import"

// BatchSize is the number of source rows processed between durable-job
// checkpoints. Every checkpoint is persisted transactionally before the
// worker is allowed to continue, so a crash resumes from the last
// committed batch instead of replaying side effects.
const BatchSize = 50

type Service struct {
	repo     *Repository
	adapters *Adapters
	staging  *StagingService
	jobSvc   *jobs.Service
	clock    kernel.Clock

	// executionFactory is an unexported test-only seam: it builds the
	// BatchExecution adapter the durable handler wraps the executor in.
	// Production leaves it nil (newExecutionAdapter is used); tests inject a
	// crashing adapter to exercise the real worker integration path.
	executionFactory func(jobs.Execution, *ImportJob) BatchExecution
}

// SetExecutionFactory installs a test-only BatchExecution factory used by
// the durable handler. Intended only for tests.
func (s *Service) SetExecutionFactory(fn func(jobs.Execution, *ImportJob) BatchExecution) {
	s.executionFactory = fn
}

func NewService(repo *Repository, adapters *Adapters, staging *StagingService, jobSvc *jobs.Service, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{
		repo:     repo,
		adapters: adapters,
		staging:  staging,
		jobSvc:   jobSvc,
		clock:    clock,
	}
}

// RequiredDependencies validates that every mandatory dependency is wired.
// nil staging or adapters are programming errors: the importer can neither
// store nor mutate safely without them.
func (s *Service) RequiredDependencies() error {
	if s.adapters == nil {
		return fmt.Errorf("import service: adapters are required")
	}
	if err := s.adapters.Validate(); err != nil {
		return err
	}
	if s.staging == nil {
		return fmt.Errorf("import service: staging service is required")
	}
	return nil
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repo.EnsureSchema(ctx)
}

// Create stores the source exactly once, persists its staging ID, byte
// size and SHA-256 hash, and returns a job. On any failure the staged file
// is removed so neither an unusable DB job nor an orphaned staged file is
// left behind.
func (s *Service) Create(ctx context.Context, params CreateImportParams, data []byte) (*ImportJob, error) {
	params.normalize()

	// Store source exactly once: temp file + fsync + atomic rename, with
	// traversal and symlink rejection enforced by the staging service.
	stagingID, hash, size, err := s.staging.Store(data, 0)
	if err != nil {
		return nil, err
	}

	// Reject an active import for the same source hash.
	existing, err := s.repo.GetActiveForSource(ctx, hash, params.TenantID)
	if err != nil {
		return nil, errors.Join(err, s.removeStaged(ctx, 0, stagingID, "active-source check failed"))
	}
	if existing != nil {
		return nil, errors.Join(ErrActiveJob, s.removeStaged(ctx, 0, stagingID, "duplicate active import rejected"))
	}

	now := s.clock.Now()
	job := &ImportJob{
		TenantID:       params.TenantID,
		Scope:          params.Scope,
		Actor:          params.Actor,
		SourceType:     params.SourceType,
		ConflictPolicy: params.ConflictPolicy,
		SchemaVersion:  params.SchemaVersion,
		Status:         StatusUploaded,
		SourceHash:     hash,
		SourceName:     params.SourceName,
		StagingID:      stagingID,
		StoredSize:     size,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}

	if err := s.repo.Create(ctx, job); err != nil {
		// Never leave an orphaned staged file behind a failed DB insert. If
		// the removal itself fails, persist a recoverable cleanup record so a
		// later reconciliation retries it instead of silently leaking.
		return nil, errors.Join(err, s.removeStaged(ctx, 0, stagingID, "import row insert failed"))
	}

	return job, nil
}

// removeStaged removes a staged file, and on failure persists a recoverable
// pending-cleanup record so the file is retried later instead of silently
// orphaned. The returned error is a safe, redacted error that never exposes
// the filesystem path or SQL.
func (s *Service) removeStaged(ctx context.Context, importID uint, stagingID, reason string) error {
	if stagingID == "" {
		return nil
	}
	if err := s.staging.Remove(stagingID); err != nil {
		recErr := s.repo.RecordPendingCleanup(ctx, importID, stagingID, reason, s.clock.Now())
		if recErr != nil {
			return errors.Join(err, kernel.Wrap(kernel.ErrCodeInternal, "pending staged-file cleanup could not be persisted", recErr))
		}
		return kernel.Wrap(kernel.ErrCodeInternal, "staged file removal is pending retry", err)
	}
	return nil
}

// reconcileCleanup retries every pending staged-file removal for an import.
// It returns an error if any removal remains unresolved so the caller knows
// cleanup is incomplete (never silently ignored).
func (s *Service) reconcileCleanup(ctx context.Context, importID uint) error {
	pending, err := s.repo.PendingCleanups(ctx, importID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, pc := range pending {
		if rmErr := s.staging.Remove(pc.StagingID); rmErr != nil {
			bumpErr := s.repo.BumpCleanupAttempt(ctx, pc.ID, rmErr.Error(), s.clock.Now())
			if bumpErr != nil && firstErr == nil {
				firstErr = bumpErr
			}
			if firstErr == nil {
				firstErr = kernel.Wrap(kernel.ErrCodeInternal, "staged file removal remains pending", rmErr)
			}
			continue
		}
		if resolveErr := s.repo.ResolveCleanup(ctx, pc.ID, s.clock.Now()); resolveErr != nil && firstErr == nil {
			firstErr = resolveErr
		}
	}
	return firstErr
}

func (s *Service) Get(ctx context.Context, id, tenantID uint, scope string) (*ImportJob, error) {
	return s.repo.GetForScope(ctx, id, tenantID, scope)
}

func (s *Service) List(ctx context.Context, filter ImportFilter) (kernel.PageResponse[ImportJob], error) {
	page := filter.Page.Normalize()
	jobs, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return kernel.PageResponse[ImportJob]{}, err
	}
	return kernel.NewPageResponse(jobs, page, total), nil
}

func (s *Service) Validate(ctx context.Context, id, tenantID uint, scope string) (*ValidationReport, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	if !job.Status.CanTransition(StatusValidating) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusValidating))
	}

	// Verify staged bytes against the immutable SourceHash captured when the
	// upload was accepted — never against a hash recomputed from the file.
	if err := s.staging.Verify(job.StagingID, job.SourceHash); err != nil {
		return nil, err
	}

	data, err := s.staging.Read(job.StagingID)
	if err != nil {
		return nil, err
	}

	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusValidating, job.Version); err != nil {
		return nil, err
	}

	source, parseErr := ParseSource(data, job.SourceType)
	if parseErr != nil {
		return nil, errors.Join(parseErr, s.failValidation(ctx, job))
	}

	planner := NewPlanner(s.repo, tenantID, s.adapters)
	report, planErr := planner.DryRun(ctx, source, job.ConflictPolicy)
	if planErr != nil {
		return nil, errors.Join(planErr, s.failValidation(ctx, job))
	}
	report.ImportID = job.ID
	report.SourceHash = job.SourceHash
	report.SchemaVersion = source.SchemaVersion

	if saveErr := s.repo.SaveValidationReport(ctx, job.ID, report, job.SourceHash); saveErr != nil {
		return nil, saveErr
	}
	if stateErr := s.repo.UpdateStatus(ctx, job.ID, StatusValidating, StatusValidated, job.Version+1); stateErr != nil {
		return nil, stateErr
	}

	return report, nil
}

// failValidation marks an import as validation_failed. The status write must
// not be silently dropped: it returns the underlying transition error (or nil
// on success) so callers can join it with the primary validation error.
func (s *Service) failValidation(ctx context.Context, job *ImportJob) error {
	return s.repo.UpdateStatus(ctx, job.ID, StatusValidating, StatusValidationFailed, job.Version+1)
}

// Execute submits (or continues) a platform.import durable job. Inline
// execution is intentionally not supported in production: if the durable
// jobs infrastructure is unavailable a stable typed error is returned and
// the import state is never advanced to completed.
func (s *Service) Execute(ctx context.Context, id, tenantID uint, scope, idempotencyKey, confirmation string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}

	wantConfirm := "EXECUTE-IMPORT-" + itoa(id)
	if confirmation != wantConfirm {
		return nil, ErrConfirmationRequired
	}

	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, ErrIdempotencyRequired
	}

	scopeKey := fmt.Sprintf("%s|%d", scope, tenantID)
	requestHash := requestHash("execute", job)

	// Idempotency gate runs before the state-transition check so a replayed
	// request returns the original result even though the import has since
	// advanced (e.g. to running) — the whole point of the key.
	stored, replay, err := s.repo.IdempotencyBegin(ctx, scopeKey, job.Actor, tenantID, idempotencyKey, requestHash, s.clock.Now())
	if err != nil {
		if errors.Is(err, kernel.ErrIdempotencyInFlight) {
			return nil, kernel.NewError(kernel.ErrCodeIdempotencyReuse, "an execute for this import is already in progress")
		}
		return nil, err
	}
	if replay {
		return unmarshalStoredJob(stored, job.ID)
	}

	if !job.Status.CanTransition(StatusRunning) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusRunning))
	}

	// Verify staged bytes against the persisted SourceHash.
	if err := s.staging.Verify(job.StagingID, job.SourceHash); err != nil {
		return nil, err
	}

	if err := s.validateForExecution(ctx, job); err != nil {
		return nil, err
	}

	result, execErr := s.executeDurable(ctx, job)
	if execErr != nil {
		// Abandon the in-flight idempotency record so a retry is a fresh
		// attempt rather than a stuck "in flight". The abandon failure is
		// surfaced alongside the original failure, never swallowed.
		return nil, errors.Join(execErr, s.repo.IdempotencyAbandon(ctx, scopeKey, job.Actor, tenantID, idempotencyKey))
	}

	if err := s.repo.IdempotencyComplete(ctx, scopeKey, job.Actor, tenantID, idempotencyKey, 200, result, s.clock.Now()); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) executeDurable(ctx context.Context, job *ImportJob) (*ImportJob, error) {
	if s.jobSvc == nil {
		return nil, ErrJobsUnavailable
	}
	if err := s.staging.Verify(job.StagingID, job.SourceHash); err != nil {
		return nil, err
	}

	// The durable job's idempotency key is derived from the import identity,
	// NOT from the caller-supplied execute/resume key, so every retry —
	// whether via Execute or Resume — recovers the SAME durable job instead
	// of creating duplicates. The caller key still governs the higher-level
	// execute/resume idempotency replay.
	durableKey := "import-run-" + itoa(job.ID)

	submission := jobs.Submission{
		TenantID:       job.TenantID,
		Scope:          mapScope(job.Scope),
		Actor:          job.Actor,
		Type:           ImportJobType,
		PayloadVersion: 1,
		Payload:        mustMarshalJSON(importJobPayload{ImportID: job.ID, TenantID: job.TenantID, Scope: job.Scope, StagingID: job.StagingID}),
		IdempotencyKey: durableKey,
		CorrelationID:  "import_" + itoa(job.ID),
		MaxAttempts:    3,
		// Hold the job far in the future so it is NOT claimable until the
		// import is atomically linked and marked running (queued-activation
		// handoff). A worker can therefore never claim a job whose import is
		// not linked and running.
		RunAfter: s.clock.Now().Add(activationHold),
	}

	durableJob, _, err := s.jobSvc.Submit(ctx, submission)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "submit import durable job", err)
	}

	// Atomically mark the import running AND link the durable job ID. This is
	// idempotent: a retry that already linked this exact job succeeds instead
	// of creating a duplicate.
	if err := s.repo.MarkRunningAndLink(ctx, job.ID, job.Status, job.Version, durableJob.ID, s.clock.Now()); err != nil {
		return nil, err
	}

	// Only now release the held job so it becomes claimable.
	if err := s.jobSvc.Activate(ctx, durableJob.ID, job.TenantID, mapScope(job.Scope)); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "activate import durable job", err)
	}

	return s.repo.Get(ctx, job.ID)
}

// activationHold is the far-future run_after used to hold a freshly
// submitted durable job until the import is linked and running. It is much
// longer than any realistic submission-to-activation window so a worker can
// never claim a held job by time-expiry alone.
const activationHold = 24 * time.Hour

func (s *Service) Resume(ctx context.Context, id, tenantID uint, scope, idempotencyKey string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, ErrIdempotencyRequired
	}

	scopeKey := fmt.Sprintf("%s|%d", scope, tenantID)
	requestHash := requestHash("resume", job)

	stored, replay, err := s.repo.IdempotencyBegin(ctx, scopeKey, job.Actor, tenantID, idempotencyKey, requestHash, s.clock.Now())
	if err != nil {
		if errors.Is(err, kernel.ErrIdempotencyInFlight) {
			return nil, kernel.NewError(kernel.ErrCodeIdempotencyReuse, "a resume for this import is already in progress")
		}
		return nil, err
	}
	if replay {
		return unmarshalStoredJob(stored, job.ID)
	}

	if !job.Status.CanTransition(StatusRunning) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusRunning))
	}
	if err := s.staging.Verify(job.StagingID, job.SourceHash); err != nil {
		return nil, err
	}

	result, execErr := s.executeDurable(ctx, job)
	if execErr != nil {
		return nil, errors.Join(execErr, s.repo.IdempotencyAbandon(ctx, scopeKey, job.Actor, tenantID, idempotencyKey))
	}
	if err := s.repo.IdempotencyComplete(ctx, scopeKey, job.Actor, tenantID, idempotencyKey, 200, result, s.clock.Now()); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) Cancel(ctx context.Context, id, tenantID uint, scope string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	if job.IsTerminal() {
		return job, nil
	}
	if !job.Status.CanTransition(StatusCancelled) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusCancelled))
	}
	if job.JobID > 0 && s.jobSvc != nil {
		js := mapScope(job.Scope)
		if _, cancelErr := s.jobSvc.RequestCancellation(ctx, job.JobID, job.TenantID, js); cancelErr != nil {
			// The import is still marked cancelled below; the durable job's
			// cooperative cancellation is best-effort coordination. Surface
			// the failure rather than silently dropping it.
			if stateErr := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusCancelled, job.Version); stateErr != nil {
				return nil, errors.Join(cancelErr, stateErr)
			}
			return s.repo.Get(ctx, job.ID)
		}
	}
	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusCancelled, job.Version); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, job.ID)
}

// Compensate undoes the entities an import created. It requires an
// idempotency key so a retried compensation replays the original outcome
// instead of double-compensating.
func (s *Service) Compensate(ctx context.Context, id, tenantID uint, scope, idempotencyKey, confirmation string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	wantConfirm := "COMPENSATE-IMPORT-" + itoa(id)
	if confirmation != wantConfirm {
		return nil, ErrConfirmationRequired
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, ErrIdempotencyRequired
	}

	scopeKey := fmt.Sprintf("%s|%d", scope, tenantID)
	requestHash := requestHash("compensate", job)

	stored, replay, err := s.repo.IdempotencyBegin(ctx, scopeKey, job.Actor, tenantID, idempotencyKey, requestHash, s.clock.Now())
	if err != nil {
		if errors.Is(err, kernel.ErrIdempotencyInFlight) {
			return nil, kernel.NewError(kernel.ErrCodeIdempotencyReuse, "a compensation for this import is already in progress")
		}
		return nil, err
	}
	if replay {
		return unmarshalStoredJob(stored, job.ID)
	}

	if !job.Status.CanTransition(StatusCompensating) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusCompensating))
	}

	result, compErr := s.doCompensate(ctx, job)
	if compErr != nil {
		return nil, errors.Join(compErr, s.repo.IdempotencyAbandon(ctx, scopeKey, job.Actor, tenantID, idempotencyKey))
	}
	if err := s.repo.IdempotencyComplete(ctx, scopeKey, job.Actor, tenantID, idempotencyKey, 200, result, s.clock.Now()); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) doCompensate(ctx context.Context, job *ImportJob) (*ImportJob, error) {
	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusCompensating, job.Version); err != nil {
		return nil, err
	}

	records, err := s.repo.GetCompensationRecords(ctx, job.ID)
	if err != nil {
		return nil, err
	}

	allCompensated := true
	order := EntityDependencyOrder()
	for i := len(order) - 1; i >= 0; i-- {
		entityType := order[i]
		for _, rec := range records {
			if rec.EntityType == entityType && rec.Status == "pending" {
				if compErr := s.compensateEntity(ctx, &rec); compErr != nil {
					if stErr := s.repo.UpdateCompensationStatus(ctx, job.ID, rec.ResourceID, "failed", compErr.Error()); stErr != nil {
						return nil, errors.Join(compErr, stErr)
					}
					allCompensated = false
				} else {
					if stErr := s.repo.UpdateCompensationStatus(ctx, job.ID, rec.ResourceID, "compensated", ""); stErr != nil {
						return nil, stErr
					}
				}
			}
		}
	}

	to := ImportStatus(StatusCompensated)
	if !allCompensated {
		to = StatusCompensationFailed
	}
	if err := s.repo.UpdateStatus(ctx, job.ID, StatusCompensating, to, job.Version+1); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, job.ID)
}

func (s *Service) compensateEntity(ctx context.Context, rec *CompensationRecord) error {
	job, err := s.repo.Get(ctx, rec.ImportID)
	if err != nil {
		return err
	}
	switch rec.EntityType {
	case EntityOrganization:
		return s.adapters.Org.SoftDeleteOrganization(ctx, rec.ResourceID, job.TenantID)
	case EntityTenantAdmin:
		return s.adapters.Admin.SoftDeleteUser(ctx, rec.ResourceID, job.TenantID)
	case EntityDomain:
		return s.adapters.Domain.SoftDeleteDomain(ctx, rec.ResourceID, job.TenantID)
	case EntityMailbox:
		return s.adapters.Mailbox.SoftDeleteMailbox(ctx, rec.ResourceID, job.TenantID)
	case EntityAlias:
		return s.adapters.Alias.SoftDeleteAlias(ctx, rec.ResourceID, job.TenantID)
	case EntityGroup:
		return s.adapters.Group.SoftDeleteGroup(ctx, rec.ResourceID, job.TenantID)
	case EntityGroupMembership:
		return s.adapters.Group.RemoveGroupMember(ctx, rec.ResourceID, job.TenantID)
	default:
		return nil
	}
}

func (s *Service) GetReport(ctx context.Context, id, tenantID uint, scope string) (*ValidationReport, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	if job.ValidationReportRaw == "" {
		return nil, kernel.NotFound("validation report")
	}
	var report ValidationReport
	if err := json.Unmarshal([]byte(job.ValidationReportRaw), &report); err != nil {
		return nil, err
	}
	return &report, nil
}

func (s *Service) validateForExecution(ctx context.Context, job *ImportJob) error {
	if job.Status != StatusValidated {
		return ErrDryRunRequired
	}
	return nil
}

func (s *Service) ImportHandler() func(ctx context.Context, exec jobs.Execution, payload json.RawMessage) (json.RawMessage, error) {
	return s.HandleImportJob
}

// HandleImportJob implements the durable platform.import worker contract:
//   - lease/fencing ownership is validated on every heartbeat and progress
//     update (a lost lease surfaces as an error, never a silent success);
//   - heartbeats are sent during long batches;
//   - cooperative cancellation is checked between batches;
//   - a checkpoint is persisted every BatchSize rows and execution resumes
//     from the last committed checkpoint;
//   - stale workers are rejected via the jobs lease;
//   - max attempts are respected by returning retryable errors for
//     transient failures and non-retryable errors otherwise;
//   - success is only reported after all writes, the final checkpoint, and
//     the import state update succeed.
func (s *Service) HandleImportJob(ctx context.Context, exec jobs.Execution, payload json.RawMessage) (json.RawMessage, error) {
	var ip importJobPayload
	if err := json.Unmarshal(payload, &ip); err != nil {
		return nil, &jobs.ExecutionError{Code: "INVALID_PAYLOAD", Message: "import payload invalid", Retryable: false}
	}
	if exec == nil {
		return nil, &jobs.ExecutionError{Code: "MISSING_EXECUTION", Message: "import handler requires a jobs execution", Retryable: false}
	}

	importJob, getErr := s.repo.Get(ctx, ip.ImportID)
	if getErr != nil {
		return nil, &jobs.ExecutionError{Code: "IMPORT_NOT_FOUND", Message: "import job not found", Retryable: false}
	}
	// A durable platform.import job must never execute against an import that
	// is not linked and running: this is the worker-side guard of the
	// queued-activation handoff. If the import is still validated (submission
	// raced ahead of linking) the job is held and this cannot happen; if it
	// ever does, fail non-retryable rather than mutate an unlinked import.
	if importJob.JobID == 0 || importJob.Status != StatusRunning {
		return nil, &jobs.ExecutionError{Code: "IMPORT_NOT_RUNNING", Message: "import is not linked and running", Retryable: false}
	}
	if importJob.IsTerminal() {
		// Already completed/cancelled/compensated — nothing to do.
		return json.RawMessage(`{"status":"noop"}`), nil
	}

	// Tamper gate: compare the staged bytes against the immutable SourceHash
	// persisted when the upload was accepted.
	if err := s.staging.Verify(importJob.StagingID, importJob.SourceHash); err != nil {
		return nil, &jobs.ExecutionError{Code: "HASH_MISMATCH", Message: "staged source does not match the accepted upload hash", Retryable: false}
	}
	data, readErr := s.staging.Read(importJob.StagingID)
	if readErr != nil {
		return nil, &jobs.ExecutionError{Code: "STAGING_READ_FAILED", Message: "staged source could not be read", Retryable: true}
	}

	// Enforce the durable-job lease before doing any work.
	if err := exec.Heartbeat(ctx); err != nil {
		return nil, leaseExecutionError(err)
	}

	executor := NewExecutor(s.adapters, s.repo, importJob.TenantID, "import_"+itoa(importJob.ID))
	if s.executionFactory != nil {
		executor.Execution = s.executionFactory(exec, importJob)
	} else {
		executor.Execution = newExecutionAdapter(exec, importJob)
	}

	result, execErr := executor.Execute(ctx, importJob, data)
	if errors.Is(execErr, ErrCancelled) {
		return nil, &jobs.ExecutionError{Code: "CANCELLED", Message: "import cancelled during execution", Retryable: false}
	}
	if execErr != nil {
		// Transient failures (DB hiccups, lease races) are retryable so the
		// durable job respects max attempts; permanent failures are not.
		if isRetryable(execErr) {
			return nil, &jobs.ExecutionError{Code: "EXECUTION_FAILED", Message: "import execution failed", Retryable: true}
		}
		return nil, &jobs.ExecutionError{Code: "EXECUTION_FAILED", Message: "import execution failed", Retryable: false}
	}

	// Final checkpoint + import state update must succeed before success is
	// reported to the worker.
	finalCp := &Checkpoint{
		ImportID:       importJob.ID,
		Entity:         importJob.CheckpointEntity,
		RowIndex:       importJob.CheckpointRow,
		ProcessedCount: importJob.TotalRows,
		CommittedAt:    s.clock.Now(),
	}
	if cpErr := s.repo.SaveCheckpoint(ctx, finalCp); cpErr != nil {
		return nil, &jobs.ExecutionError{Code: "CHECKPOINT_FAILED", Message: "final checkpoint could not be persisted", Retryable: true}
	}
	if stateErr := s.repo.UpdateStatus(ctx, importJob.ID, StatusRunning, StatusCompleted, importJob.Version); stateErr != nil {
		return nil, &jobs.ExecutionError{Code: "STATE_UPDATE_FAILED", Message: "import completion state could not be persisted", Retryable: true}
	}

	resJSON, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return nil, &jobs.ExecutionError{Code: "RESULT_ENCODE_FAILED", Message: "import result could not be encoded", Retryable: true}
	}
	return resJSON, nil
}

// executionAdapter bridges jobs.Execution into the executor's bounded-batch
// progress, heartbeat and cancellation contract.
type executionAdapter struct {
	exec      jobs.Execution
	importJob *ImportJob
	reported  int
}

func newExecutionAdapter(exec jobs.Execution, importJob *ImportJob) *executionAdapter {
	return &executionAdapter{exec: exec, importJob: importJob}
}

func (a *executionAdapter) Heartbeat(ctx context.Context) error {
	return a.exec.Heartbeat(ctx)
}

func (a *executionAdapter) CancellationRequested(ctx context.Context) (bool, error) {
	return a.exec.CancellationRequested(ctx)
}

func (a *executionAdapter) SetProgress(ctx context.Context, processed int) error {
	total := a.importJob.TotalRows
	if total <= 0 {
		return nil
	}
	pct := processed * 100 / total
	if pct > 100 {
		pct = 100
	}
	a.reported = pct
	return a.exec.SetProgress(ctx, pct)
}

type importJobPayload struct {
	ImportID  uint   `json:"import_id"`
	TenantID  uint   `json:"tenant_id"`
	Scope     string `json:"scope"`
	StagingID string `json:"staging_id"`
}

func (p importJobPayload) ImportIDString() string { return itoa(p.ImportID) }

type CreateImportParams struct {
	TenantID       uint
	Scope          string
	Actor          string
	SourceType     ImportSourceType
	ConflictPolicy ConflictPolicy
	SchemaVersion  int
	SourceName     string
}

func (p *CreateImportParams) normalize() {
	if p.Scope == "" {
		p.Scope = "tenant"
	}
	if p.SchemaVersion == 0 {
		p.SchemaVersion = 1
	}
	if p.SourceType == "" {
		p.SourceType = SourceCSV
	}
	if !p.ConflictPolicy.Valid() {
		p.ConflictPolicy = ConflictFail
	}
	p.Actor = strings.TrimSpace(p.Actor)
	if p.Actor == "" {
		p.Actor = "system"
	}
}

func mapScope(s string) jobs.Scope {
	if s == "platform" {
		return jobs.ScopePlatform
	}
	return jobs.ScopeTenant
}

func marshalJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// mustMarshalJSON marshals a fixed internal struct. Encoding a struct never
// fails in practice, but the error is surfaced as a panic (programming
// error) rather than silently dropped.
func mustMarshalJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic("importer: marshal internal payload: " + err.Error())
	}
	return data
}

func itoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}
	return string(buf[i:])
}

// requestHash is a stable hash of the normalized request (action + import
// identity) used to detect idempotency-key reuse with a different request.
func requestHash(action string, job *ImportJob) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%s", action, job.Scope, job.SourceHash, job.ID, job.ConflictPolicy)
	return hex.EncodeToString(h.Sum(nil))
}

func unmarshalStoredJob(stored *StoredResult, id uint) (*ImportJob, error) {
	if stored == nil || stored.ResponseBody == "" {
		return nil, kernel.NewError(kernel.ErrCodeInternal, "stored idempotency result is empty")
	}
	var job ImportJob
	if err := json.Unmarshal([]byte(stored.ResponseBody), &job); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "decode stored import result", err)
	}
	if job.ID == 0 {
		job.ID = id
	}
	return &job, nil
}

func leaseExecutionError(err error) error {
	if errors.Is(err, jobs.ErrLeaseLost) {
		return &jobs.ExecutionError{Code: "LEASE_LOST", Message: "import lease lost to another worker", Retryable: false}
	}
	if errors.Is(err, jobs.ErrCancellationAsked) {
		return &jobs.ExecutionError{Code: "CANCELLED", Message: "import cancellation requested", Retryable: false}
	}
	return &jobs.ExecutionError{Code: "LEASE_ERROR", Message: "import lease operation failed", Retryable: true}
}

func isRetryable(err error) bool {
	var ie *ImportError
	if errors.As(err, &ie) {
		switch ie.Code {
		case CodeHashMismatch, CodeInvalidSource, CodeParseError, CodeInvalidUTF8, CodeUnknownSchema, CodeUnknownField, CodeOversizedInput, CodeTooManyRows, CodeDuplicateRow, CodeInvalidField, CodeUnsupportedEntity, CodePlatformRoleInj, CodeCrossTenant:
			return false
		}
	}
	return true
}
