package importer

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
)

const ImportJobType = "platform.import"

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

func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repo.EnsureSchema(ctx)
}

func (s *Service) Create(ctx context.Context, params CreateImportParams, data []byte) (*ImportJob, error) {
	params.normalize()

	// Store source data to staging
	stagingID, hash, size, err := s.staging.Store(data, 0)
	if err != nil {
		return nil, err
	}
	_ = size

	// Reject active import for same source
	if existing, _ := s.repo.GetActiveForSource(ctx, hash, params.TenantID); existing != nil {
		s.staging.Remove(stagingID)
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
		s.staging.Remove(stagingID)
		return nil, err
	}

	// Update with actual import ID in staging filename
	_ = s.staging.Remove(stagingID)
	newStagingID, _, _, _ := s.staging.Store(data, job.ID)
	s.repo.UpdateStagingID(ctx, job.ID, newStagingID)

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

	// Verify staged source
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

	planner := NewPlanner(nil, tenantID, s.adapters)
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

func (s *Service) Execute(ctx context.Context, id, tenantID uint, scope, idempotencyKey, confirmation string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}

	wantConfirm := "EXECUTE-IMPORT-" + itoa(id)
	if confirmation != wantConfirm {
		return nil, ErrConfirmationRequired
	}

	if !job.Status.CanTransition(StatusRunning) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusRunning))
	}

	// Verify staged source hash before execution
	if err := s.staging.Verify(job.StagingID, job.SourceHash); err != nil {
		return nil, err
	}

	// Validate for execution (hash match, validated status)
	if err := s.validateForExecution(ctx, job); err != nil {
		return nil, err
	}

	if s.jobSvc == nil {
		return s.executeInline(ctx, job, idempotencyKey)
	}

	// Submit durable job
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

	s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusRunning, job.Version)
	s.repo.LinkJobID(ctx, job.ID, durableJob.ID)

	return s.repo.Get(ctx, job.ID)
}

func (s *Service) executeInline(ctx context.Context, job *ImportJob, idempotencyKey string) (*ImportJob, error) {
	data, err := s.staging.Read(job.StagingID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusRunning, job.Version); err != nil {
		return nil, err
	}
	executor := NewExecutor(s.adapters, s.repo, job.TenantID, idempotencyKey)
	_, execErr := executor.Execute(ctx, job, data)
	if execErr != nil {
		s.repo.UpdateStatus(ctx, job.ID, StatusRunning, StatusFailed, job.Version+1)
		return s.repo.Get(ctx, job.ID)
	}
	s.repo.UpdateStatus(ctx, job.ID, StatusRunning, StatusCompleted, job.Version+1)
	return s.repo.Get(ctx, job.ID)
}

func (s *Service) Resume(ctx context.Context, id, tenantID uint, scope, idempotencyKey string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	if !job.Status.CanTransition(StatusRunning) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusRunning))
	}
	// Re-run execute which resumes from last checkpoint
	return s.Execute(ctx, id, tenantID, scope, idempotencyKey, "EXECUTE-IMPORT-"+itoa(id))
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
	// Cancel the durable job first if present
	if job.JobID > 0 && s.jobSvc != nil {
		js := mapScope(job.Scope)
		s.jobSvc.RequestCancellation(ctx, job.JobID, job.TenantID, js)
	}
	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusCancelled, job.Version); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, job.ID)
}

func (s *Service) Compensate(ctx context.Context, id, tenantID uint, scope, confirmation string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	wantConfirm := "COMPENSATE-IMPORT-" + itoa(id)
	if confirmation != wantConfirm {
		return nil, ErrConfirmationRequired
	}
	if !job.Status.CanTransition(StatusCompensating) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusCompensating))
	}
	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusCompensating, job.Version); err != nil {
		return nil, err
	}

	records, err := s.repo.GetCompensationRecords(ctx, id)
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
					s.repo.UpdateCompensationStatus(ctx, id, rec.ResourceID, "failed", compErr.Error())
					allCompensated = false
				} else {
					s.repo.UpdateCompensationStatus(ctx, id, rec.ResourceID, "compensated", "")
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
	switch rec.EntityType {
	case EntityOrganization:
		return s.adapters.Org.SoftDeleteOrganization(ctx, rec.ResourceID)
	case EntityTenantAdmin:
		return s.adapters.Admin.SoftDeleteUser(ctx, rec.ResourceID)
	case EntityDomain:
		return s.adapters.Domain.SoftDeleteDomain(ctx, rec.ResourceID)
	case EntityMailbox:
		return s.adapters.Mailbox.SoftDeleteMailbox(ctx, rec.ResourceID)
	case EntityAlias:
		return s.adapters.Alias.SoftDeleteAlias(ctx, rec.ResourceID)
	case EntityGroup:
		return s.adapters.Group.SoftDeleteGroup(ctx, rec.ResourceID)
	case EntityGroupMembership:
		return s.adapters.Group.RemoveGroupMember(ctx, rec.ResourceID)
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

func (s *Service) HandleImportJob(ctx context.Context, exec jobs.Execution, payload json.RawMessage) (json.RawMessage, error) {
	var ip importJobPayload
	if err := json.Unmarshal(payload, &ip); err != nil {
		return nil, &jobs.ExecutionError{Code: "INVALID_PAYLOAD", Message: "import payload invalid", Retryable: false}
	}
	data, err := s.staging.Read(ip.StagingID)
	if err != nil {
		return nil, &jobs.ExecutionError{Code: "STAGING_READ_FAILED", Message: err.Error(), Retryable: false}
	}
	// Verify hash on resume
	if err := s.staging.Verify(ip.StagingID, HashSource(data)); err != nil {
		return nil, &jobs.ExecutionError{Code: "HASH_MISMATCH", Message: err.Error(), Retryable: false}
	}
	// Look up import job for resume
	importJob, getErr := s.repo.Get(ctx, ip.ImportID)
	if getErr != nil {
		return nil, &jobs.ExecutionError{Code: "IMPORT_NOT_FOUND", Message: getErr.Error(), Retryable: false}
	}
	executor := NewExecutor(s.adapters, s.repo, importJob.TenantID, ip.ImportIDString())
	result, execErr := executor.Execute(ctx, importJob, data)
	if execErr != nil {
		return nil, &jobs.ExecutionError{Code: "EXECUTION_FAILED", Message: execErr.Error(), Retryable: true}
	}
	resJSON, _ := json.Marshal(result)
	return resJSON, nil
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
