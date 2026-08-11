package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
		_ = s.staging.Remove(stagingID)
		return nil, err
	}
	if existing != nil {
		_ = s.staging.Remove(stagingID)
		return nil, ErrActiveJob
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
		// Never leave an orphaned staged file behind a failed DB insert.
		_ = s.staging.Remove(stagingID)
		return nil, err
	}

	return job, nil
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
		s.repo.UpdateStatus(ctx, job.ID, StatusValidating, StatusValidationFailed, job.Version+1)
		return nil, parseErr
	}

	planner := NewPlanner(s.repo, tenantID, s.adapters)
	report, planErr := planner.DryRun(ctx, source, job.ConflictPolicy)
	if planErr != nil {
		s.repo.UpdateStatus(ctx, job.ID, StatusValidating, StatusValidationFailed, job.Version+1)
		return nil, planErr
	}
	report.ImportID = job.ID
	report.SourceHash = job.SourceHash
	report.SchemaVersion = source.SchemaVersion

	if saveErr := s.repo.SaveValidationReport(ctx, job.ID, report, job.SourceHash); saveErr != nil {
		return nil, saveErr
	}
	s.repo.UpdateStatus(ctx, job.ID, StatusValidating, StatusValidated, job.Version+1)

	return report, nil
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

	result, execErr := s.executeDurable(ctx, job, idempotencyKey)
	if execErr != nil {
		// Abandon the in-flight idempotency record so a retry is a fresh
		// attempt rather than a stuck "in flight".
		_ = s.repo.IdempotencyAbandon(ctx, scopeKey, job.Actor, tenantID, idempotencyKey)
		return nil, execErr
	}

	if err := s.repo.IdempotencyComplete(ctx, scopeKey, job.Actor, tenantID, idempotencyKey, 200, result, s.clock.Now()); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) executeDurable(ctx context.Context, job *ImportJob, idempotencyKey string) (*ImportJob, error) {
	if s.jobSvc == nil {
		return nil, ErrJobsUnavailable
	}
	if err := s.staging.Verify(job.StagingID, job.SourceHash); err != nil {
		return nil, err
	}

	submission := jobs.Submission{
		TenantID:       job.TenantID,
		Scope:          mapScope(job.Scope),
		Actor:          job.Actor,
		Type:           ImportJobType,
		PayloadVersion: 1,
		Payload:        marshalJSON(importJobPayload{ImportID: job.ID, TenantID: job.TenantID, Scope: job.Scope, StagingID: job.StagingID}),
		IdempotencyKey: idempotencyKey,
		CorrelationID:  "import_" + itoa(job.ID),
		MaxAttempts:    3,
	}

	durableJob, _, err := s.jobSvc.Submit(ctx, submission)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "submit import durable job", err)
	}

	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusRunning, job.Version); err != nil {
		return nil, err
	}
	if err := s.repo.LinkJobID(ctx, job.ID, durableJob.ID); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, job.ID)
}

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

	result, execErr := s.executeDurable(ctx, job, idempotencyKey)
	if execErr != nil {
		_ = s.repo.IdempotencyAbandon(ctx, scopeKey, job.Actor, tenantID, idempotencyKey)
		return nil, execErr
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
		_, _ = s.jobSvc.RequestCancellation(ctx, job.JobID, job.TenantID, js)
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
		_ = s.repo.IdempotencyAbandon(ctx, scopeKey, job.Actor, tenantID, idempotencyKey)
		return nil, compErr
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
					s.repo.UpdateCompensationStatus(ctx, job.ID, rec.ResourceID, "failed", compErr.Error())
					allCompensated = false
				} else {
					s.repo.UpdateCompensationStatus(ctx, job.ID, rec.ResourceID, "compensated", "")
				}
			}
		}
	}

	if allCompensated {
		s.repo.UpdateStatus(ctx, job.ID, StatusCompensating, StatusCompensated, job.Version+1)
	} else {
		s.repo.UpdateStatus(ctx, job.ID, StatusCompensating, StatusCompensationFailed, job.Version+1)
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
	executor.Execution = newExecutionAdapter(exec, importJob)

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

	resJSON, _ := json.Marshal(result)
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
