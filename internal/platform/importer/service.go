package importer

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/orvix/orvix/internal/platform/kernel"
)

type Service struct {
	repo     *Repository
	adapters *ServiceAdapters
	clock    kernel.Clock
	maxRows  int
	maxBytes int64
}

func NewService(repo *Repository, adapters *ServiceAdapters, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{
		repo:     repo,
		adapters: adapters,
		clock:    clock,
		maxRows:  MaxSourceRows,
		maxBytes: MaxSourceBytes,
	}
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repo.EnsureSchema(ctx)
}

func (s *Service) Create(ctx context.Context, params CreateImportParams) (*ImportJob, error) {
	params.validate()

	// Reject active import for same source
	if existing, _ := s.repo.GetActiveForSource(ctx, params.SourceHash, params.TenantID); existing != nil {
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
		SourceHash:     params.SourceHash,
		SourceName:     params.SourceName,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}

	if err := s.repo.Create(ctx, job); err != nil {
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

func (s *Service) Validate(ctx context.Context, id, tenantID uint, scope string, data []byte) (*ValidationReport, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	if !job.Status.CanTransition(StatusValidating) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusValidating))
	}

	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusValidating, job.Version); err != nil {
		return nil, err
	}
	job, _ = s.repo.Get(ctx, job.ID)

	source, err := ParseSource(data, job.SourceType)
	if err != nil {
		s.repo.UpdateStatus(ctx, job.ID, StatusValidating, StatusValidationFailed, job.Version)
		return nil, err
	}

	planner := NewPlanner(s.adapters.db, tenantID)
	currentHash := HashSource(data)
	report, err := planner.DryRun(ctx, source, job.ConflictPolicy)
	if err != nil {
		s.repo.UpdateStatus(ctx, job.ID, StatusValidating, StatusValidationFailed, job.Version)
		return nil, err
	}
	report.ImportID = job.ID
	report.SourceHash = currentHash
	report.SchemaVersion = source.SchemaVersion

	if err := s.repo.SaveValidationReport(ctx, job.ID, report, currentHash); err != nil {
		return nil, err
	}
	s.repo.UpdateStatus(ctx, job.ID, StatusValidating, StatusValidated, job.Version+1)

	return report, nil
}

func (s *Service) Execute(ctx context.Context, id, tenantID uint, scope string, data []byte, idempotencyKey string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	if !job.Status.CanTransition(StatusRunning) {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusRunning))
	}

	currentHash := HashSource(data)

	planner := NewPlanner(s.adapters.db, tenantID)
	if err := planner.ValidateForExecution(ctx, job, currentHash); err != nil {
		return nil, err
	}

	executor := NewExecutor(s.adapters, s.repo, tenantID, idempotencyKey)
	result, execErr := executor.Execute(ctx, job, data)
	if execErr != nil {
		s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusFailed, job.Version)
		job.Status = StatusFailed
		job.LastError = execErr.Error()
		return job, execErr
	}

	s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusCompleted, job.Version)
	job.Status = StatusCompleted
	_ = result
	return job, nil
}

func (s *Service) ExecuteDryRun(ctx context.Context, data []byte, sourceType ImportSourceType, tenantID uint, conflict ConflictPolicy) (*ValidationReport, error) {
	source, err := ParseSource(data, sourceType)
	if err != nil {
		return nil, err
	}
	planner := NewPlanner(s.adapters.db, tenantID)
	report, err := planner.DryRun(ctx, source, conflict)
	if err != nil {
		return nil, err
	}
	report.SourceHash = HashSource(data)
	report.SchemaVersion = source.SchemaVersion
	return report, nil
}

func (s *Service) Cancel(ctx context.Context, id, tenantID uint, scope string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	if !job.Status.CanTransition(StatusCancelled) && job.Status != StatusCancelled {
		return nil, kernel.InvalidStateTransition(string(job.Status), string(StatusCancelled))
	}
	if err := s.repo.UpdateStatus(ctx, job.ID, job.Status, StatusCancelled, job.Version); err != nil {
		return nil, err
	}
	job.Status = StatusCancelled
	return job, nil
}

func (s *Service) Compensate(ctx context.Context, id, tenantID uint, scope string) (*ImportJob, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
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
	// Reverse dependency order for compensation
	order := EntityDependencyOrder()
	for i := len(order) - 1; i >= 0; i-- {
		entityType := order[i]
		for _, rec := range records {
			if rec.EntityType == entityType && rec.Status == "pending" {
				if err := s.compensateEntity(ctx, &rec); err != nil {
					s.repo.UpdateCompensationStatus(ctx, id, rec.ResourceID, "failed", err.Error())
					allCompensated = false
				} else {
					s.repo.UpdateCompensationStatus(ctx, id, rec.ResourceID, "compensated", "")
				}
			}
		}
	}

	if allCompensated {
		s.repo.UpdateStatus(ctx, job.ID, StatusCompensating, StatusCompensated, job.Version+1)
		job.Status = StatusCompensated
	} else {
		s.repo.UpdateStatus(ctx, job.ID, StatusCompensating, StatusCompensationFailed, job.Version+1)
		job.Status = StatusCompensationFailed
	}
	return job, nil
}

func (s *Service) compensateEntity(ctx context.Context, rec *CompensationRecord) error {
	switch rec.EntityType {
	case EntityOrganization:
		_, err := s.adapters.db.Exec(`UPDATE tenants SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, rec.ResourceID)
		return err
	case EntityTenantAdmin:
		_, err := s.adapters.db.Exec(`UPDATE users SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, rec.ResourceID)
		return err
	case EntityDomain:
		_, err := s.adapters.db.Exec(`UPDATE coremail_domains SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, rec.ResourceID)
		return err
	case EntityMailbox:
		_, err := s.adapters.db.Exec(`UPDATE coremail_mailboxes SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, rec.ResourceID)
		return err
	case EntityAlias:
		_, err := s.adapters.db.Exec(`UPDATE coremail_aliases SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, rec.ResourceID)
		return err
	case EntityGroup:
		_, err := s.adapters.db.Exec(`UPDATE coremail_groups SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, rec.ResourceID)
		return err
	case EntityGroupMembership:
		_, err := s.adapters.db.Exec(`DELETE FROM coremail_group_members WHERE id=?`, rec.ResourceID)
		return err
	}
	return nil
}

func (s *Service) GetReport(ctx context.Context, id, tenantID uint, scope string) (*ValidationReport, error) {
	job, err := s.repo.GetForScope(ctx, id, tenantID, scope)
	if err != nil {
		return nil, err
	}
	var report ValidationReport
	if job.ValidationReportRaw == "" {
		return nil, kernel.NotFound("validation report")
	}
	if err := json.Unmarshal([]byte(job.ValidationReportRaw), &report); err != nil {
		return nil, err
	}
	return &report, nil
}

type CreateImportParams struct {
	TenantID       uint
	Scope          string
	Actor          string
	SourceType     ImportSourceType
	ConflictPolicy ConflictPolicy
	SchemaVersion  int
	SourceHash     string
	SourceName     string
}

func (p *CreateImportParams) validate() {
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
