package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// BatchExecution is the narrow slice of the durable job execution contract
// the executor needs: heartbeats during long batches, cooperative
// cancellation between batches, and durable progress reporting. It is
// satisfied by the jobs package's Execution in production; tests may supply
// a lightweight stub.
type BatchExecution interface {
	Heartbeat(context.Context) error
	CancellationRequested(context.Context) (bool, error)
	SetProgress(context.Context, int) error
}

type Executor struct {
	adapters       *Adapters
	repo           *Repository
	tenantID       uint
	idempotencyKey string
	// BatchSize controls how many rows are processed between checkpoints.
	// It defaults to the package BatchSize constant when zero. Tests shrink
	// it to exercise crash-and-resume with small fixtures.
	BatchSize int
	// Execution is optional; when nil the executor runs without heartbeats,
	// cancellation checks or progress reporting (unit tests).
	Execution BatchExecution
}

func NewExecutor(adapters *Adapters, repo *Repository, tenantID uint, idempotencyKey string) *Executor {
	return &Executor{
		adapters:       adapters,
		repo:           repo,
		tenantID:       tenantID,
		idempotencyKey: idempotencyKey,
	}
}

type ExecutionResult struct {
	Processed int               `json:"processed"`
	Succeeded int               `json:"succeeded"`
	Skipped   int               `json:"skipped"`
	Failed    int               `json:"failed"`
	Created   []CreatedResource `json:"created_resources,omitempty"`
}

type CreatedResource struct {
	EntityType ImportEntityType `json:"entity_type"`
	ResourceID uint             `json:"resource_id"`
	RowKey     string           `json:"row_key"`
}

// Execute processes the source in bounded batches. Between batches it:
//   - sends a heartbeat to extend the durable lease,
//   - checks cooperative cancellation,
//   - persists a checkpoint so a crash resumes from the last committed
//     batch,
//   - reports progress to the durable job.
//
// Resume is idempotent at the row level: any row whose compensation record
// was already persisted (i.e. its entity was created before a crash) is
// skipped, so a resumed run never duplicates entities.
func (e *Executor) Execute(ctx context.Context, job *ImportJob, data []byte) (*ExecutionResult, error) {
	source, parseErr := ParseSource(data, job.SourceType)
	if parseErr != nil {
		return nil, parseErr
	}

	var startFrom int
	lastCp, cpErr := e.repo.LastCheckpoint(ctx, job.ID)
	if cpErr != nil {
		return nil, cpErr
	}
	if lastCp != nil {
		startFrom = lastCp.ProcessedCount
	}

	ordered := reorderEntities(source.Entities)

	result := &ExecutionResult{}
	processed := startFrom
	succeeded := 0
	skipped := 0
	failed := 0

	if startFrom >= len(ordered) {
		return &ExecutionResult{Processed: len(ordered)}, nil
	}

	for i := startFrom; i < len(ordered); i++ {
		entity := ordered[i]

		// Idempotent resume: if this row's entity was already created (its
		// compensation record is present), skip it rather than duplicating.
		exists, checkErr := e.repo.CompensationExistsForRow(ctx, job.ID, entity.Line)
		if checkErr != nil {
			return nil, checkErr
		}
		if exists {
			processed++
			skipped++
			continue
		}

		if len(entity.Errors) > 0 {
			processed++
			skipped++
			continue
		}

		outcome, execErr := e.executeEntity(ctx, entity, job)
		if execErr != nil {
			failed++
			processed++
			continue
		}

		if outcome.resourceID > 0 {
			succeeded++
			result.Created = append(result.Created, CreatedResource{
				EntityType: entity.Entity,
				ResourceID: outcome.resourceID,
				RowKey:     fmt.Sprintf("%s_%d", entity.Entity, outcome.resourceID),
			})
			if recErr := e.repo.SaveCompensationRecord(ctx, &CompensationRecord{
				ImportID:     job.ID,
				ResourceID:   outcome.resourceID,
				EntityType:   entity.Entity,
				RowKey:       fmt.Sprintf("%s_%d", entity.Entity, outcome.resourceID),
				RowIndex:     entity.Line,
				MutationType: outcome.mutationType,
				BeforeImage:  outcome.beforeImage,
				AfterImage:   outcome.afterImage,
				Status:       "pending",
				CreatedAt:    time.Now().UTC(),
			}); recErr != nil {
				return nil, recErr
			}
		}

		processed++

		// Bounded batch: checkpoint, heartbeat, cancellation, progress.
		batchSize := e.BatchSize
		if batchSize <= 0 {
			batchSize = BatchSize
		}
		if processed%batchSize == 0 {
			if err := e.checkpointBatch(ctx, job, entity, processed, succeeded, skipped, failed); err != nil {
				return nil, err
			}
		}
	}

	result.Processed = processed
	result.Succeeded = succeeded
	result.Skipped = skipped
	result.Failed = failed
	return result, nil
}

func (e *Executor) checkpointBatch(ctx context.Context, job *ImportJob, entity ParsedEntity, processed, succeeded, skipped, failed int) error {
	checkpoint := &Checkpoint{
		ImportID:       job.ID,
		Entity:         entity.Entity,
		RowIndex:       entity.Line,
		ProcessedCount: processed,
		CommittedAt:    time.Now().UTC(),
	}
	if cpErr := e.repo.SaveCheckpoint(ctx, checkpoint); cpErr != nil {
		return cpErr
	}
	if progErr := e.repo.UpdateProgress(ctx, job.ID, processed, succeeded+job.SucceededRows, skipped+job.SkippedRows, failed+job.FailedRows, processed, entity.Entity, entity.Line); progErr != nil {
		return progErr
	}

	if e.Execution == nil {
		return nil
	}
	// Cooperative cancellation between batches.
	requested, cancelErr := e.Execution.CancellationRequested(ctx)
	if cancelErr != nil {
		return cancelErr
	}
	if requested {
		return ErrCancelled
	}
	// Heartbeat to extend the lease before the next batch.
	if hbErr := e.Execution.Heartbeat(ctx); hbErr != nil {
		return hbErr
	}
	return e.Execution.SetProgress(ctx, processed)
}

type entityOutcome struct {
	resourceID   uint
	mutationType string
	beforeImage  string
	afterImage   string
}

func (e *Executor) executeEntity(ctx context.Context, entity ParsedEntity, job *ImportJob) (*entityOutcome, error) {
	switch entity.Entity {
	case EntityOrganization:
		return e.dispatchOrg(ctx, entity, job)
	case EntityTenantAdmin:
		return e.dispatchAdmin(ctx, entity, job)
	case EntityDomain:
		return e.dispatchDomain(ctx, entity, job)
	case EntityMailbox:
		return e.dispatchMailbox(ctx, entity, job)
	case EntityAlias:
		return e.createAliasOutcome(ctx, entity, job)
	case EntityGroup:
		return e.dispatchGroup(ctx, entity, job)
	case EntityGroupMembership:
		return e.createGroupMembershipOutcome(ctx, entity, job)
	default:
		return nil, fmt.Errorf("unsupported entity type: %s", entity.Entity)
	}
}

// ── Dispatch helpers: check existence, then create or update ─────────

func (e *Executor) dispatchOrg(ctx context.Context, entity ParsedEntity, job *ImportJob) (*entityOutcome, error) {
	domain := fieldStr(entity.Raw, "domain")
	info, _ := e.repo.GetOrg(ctx, domain, job.TenantID)
	if info == nil {
		return e.createOutcome(ctx, func() (uint, error) { return e.createOrganization(ctx, entity, job) }), nil
	}
	switch job.ConflictPolicy {
	case ConflictSkip:
		return &entityOutcome{}, nil
	case ConflictUpdateSafe:
		return e.updateOrganization(ctx, entity, job, info)
	default:
		return nil, fmt.Errorf("organization with domain %s already exists", domain)
	}
}

func (e *Executor) dispatchAdmin(ctx context.Context, entity ParsedEntity, job *ImportJob) (*entityOutcome, error) {
	email := fieldStr(entity.Raw, "email")
	info, _ := e.repo.GetUser(ctx, email)
	if info == nil {
		return e.createOutcome(ctx, func() (uint, error) { return e.createTenantAdmin(ctx, entity, job) }), nil
	}
	switch job.ConflictPolicy {
	case ConflictSkip:
		return &entityOutcome{}, nil
	case ConflictUpdateSafe:
		return e.updateTenantAdmin(ctx, entity, job, info)
	default:
		return nil, fmt.Errorf("user with email %s already exists", email)
	}
}

func (e *Executor) dispatchDomain(ctx context.Context, entity ParsedEntity, job *ImportJob) (*entityOutcome, error) {
	name := fieldStr(entity.Raw, "name", "domain")
	info, _ := e.repo.GetDomain(ctx, name)
	if info == nil {
		return e.createOutcome(ctx, func() (uint, error) { return e.createDomain(ctx, entity, job) }), nil
	}
	switch job.ConflictPolicy {
	case ConflictSkip:
		return &entityOutcome{}, nil
	case ConflictUpdateSafe:
		return e.updateDomain(ctx, entity, job, info)
	default:
		return nil, fmt.Errorf("domain %s already exists", name)
	}
}

func (e *Executor) dispatchMailbox(ctx context.Context, entity ParsedEntity, job *ImportJob) (*entityOutcome, error) {
	email := fieldStr(entity.Raw, "email")
	info, _ := e.repo.GetMailbox(ctx, email)
	if info == nil {
		return e.createOutcome(ctx, func() (uint, error) { return e.createMailbox(ctx, entity, job) }), nil
	}
	switch job.ConflictPolicy {
	case ConflictSkip:
		return &entityOutcome{}, nil
	case ConflictUpdateSafe:
		return e.updateMailbox(ctx, entity, job, info)
	default:
		return nil, fmt.Errorf("mailbox %s already exists", email)
	}
}

func (e *Executor) dispatchGroup(ctx context.Context, entity ParsedEntity, job *ImportJob) (*entityOutcome, error) {
	name := fieldStr(entity.Raw, "name")
	info, _ := e.repo.GetGroup(ctx, name, job.TenantID)
	if info == nil {
		return e.createOutcome(ctx, func() (uint, error) { return e.createGroup(ctx, entity, job) }), nil
	}
	switch job.ConflictPolicy {
	case ConflictSkip:
		return &entityOutcome{}, nil
	case ConflictUpdateSafe:
		return e.updateGroup(ctx, entity, job, info)
	default:
		return nil, fmt.Errorf("group %s already exists", name)
	}
}

// createOutcome wraps a create call and labels the result as a create
// mutation (no before/after images — the entity is brand new).
func (e *Executor) createOutcome(ctx context.Context, create func() (uint, error)) *entityOutcome {
	id, _ := create()
	return &entityOutcome{resourceID: id, mutationType: MutationCreated, beforeImage: "", afterImage: ""}
}

// ── Update methods (called when conflict_policy=update_safe_fields) ──

func (e *Executor) updateOrganization(ctx context.Context, entity ParsedEntity, job *ImportJob, info *EntityInfo) (*entityOutcome, error) {
	return e.applySafeUpdate(ctx, entity, job, info, EntityOrganization,
		func(changed map[string]any) error {
			return e.adapters.Org.UpdateOrganization(ctx, info.ID, job.TenantID, changed)
		})
}

func (e *Executor) updateTenantAdmin(ctx context.Context, entity ParsedEntity, job *ImportJob, info *EntityInfo) (*entityOutcome, error) {
	return e.applySafeUpdate(ctx, entity, job, info, EntityTenantAdmin,
		func(changed map[string]any) error {
			return e.adapters.Admin.UpdateTenantAdmin(ctx, info.ID, job.TenantID, changed)
		})
}

func (e *Executor) updateDomain(ctx context.Context, entity ParsedEntity, job *ImportJob, info *EntityInfo) (*entityOutcome, error) {
	return e.applySafeUpdate(ctx, entity, job, info, EntityDomain,
		func(changed map[string]any) error {
			return e.adapters.Domain.UpdateDomain(ctx, info.ID, job.TenantID, changed)
		})
}

func (e *Executor) updateMailbox(ctx context.Context, entity ParsedEntity, job *ImportJob, info *EntityInfo) (*entityOutcome, error) {
	return e.applySafeUpdate(ctx, entity, job, info, EntityMailbox,
		func(changed map[string]any) error {
			return e.adapters.Mailbox.UpdateMailbox(ctx, info.ID, job.TenantID, changed)
		})
}

func (e *Executor) updateGroup(ctx context.Context, entity ParsedEntity, job *ImportJob, info *EntityInfo) (*entityOutcome, error) {
	return e.applySafeUpdate(ctx, entity, job, info, EntityGroup,
		func(changed map[string]any) error {
			return e.adapters.Group.UpdateGroup(ctx, info.ID, job.TenantID, changed)
		})
}

// applySafeUpdate computes the safe-field diff, applies it through the
// production adapter, and returns an outcome labeled as an update mutation
// with before/after images for compensation. A no-op (nothing changed) is
// not a mutation and returns a zero outcome so no compensation is recorded.
func (e *Executor) applySafeUpdate(ctx context.Context, entity ParsedEntity, job *ImportJob, info *EntityInfo, entityType ImportEntityType, apply func(map[string]any) error) (*entityOutcome, error) {
	safe, _ := ExtractSafeFields(entity.Raw, entityType)
	changed := SafeFieldsChanged(info.Fields, safe, entityType)
	if len(changed) == 0 {
		return &entityOutcome{}, nil
	}
	beforeJSON, _ := json.Marshal(info.Fields)
	afterJSON, _ := json.Marshal(changed)
	if err := apply(changed); err != nil {
		return nil, err
	}
	return &entityOutcome{
		resourceID:   info.ID,
		mutationType: MutationUpdated,
		beforeImage:  string(beforeJSON),
		afterImage:   string(afterJSON),
	}, nil
}

func (e *Executor) createAliasOutcome(ctx context.Context, entity ParsedEntity, job *ImportJob) (*entityOutcome, error) {
	id, err := e.createAlias(ctx, entity, job)
	return &entityOutcome{resourceID: id, mutationType: MutationCreated}, err
}

func (e *Executor) createGroupMembershipOutcome(ctx context.Context, entity ParsedEntity, job *ImportJob) (*entityOutcome, error) {
	if err := e.adapters.Group.AddGroupMember(ctx, fieldStr(entity.Raw, "group_name", "group"), fieldStr(entity.Raw, "email", "member_email"), job.TenantID); err != nil {
		return nil, err
	}
	return &entityOutcome{}, nil
}

func (e *Executor) createOrganization(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	name := fieldStr(entity.Raw, "name")
	domain := fieldStr(entity.Raw, "domain")
	return e.adapters.Org.CreateOrganization(ctx, name, domain, job.TenantID)
}

func (e *Executor) createTenantAdmin(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	email := fieldStr(entity.Raw, "email")
	name := fieldStr(entity.Raw, "name")
	password := fieldStr(entity.Raw, "password")
	role := fieldStr(entity.Raw, "role")
	if role == "" {
		role = "tenant_admin"
	}
	return e.adapters.Admin.CreateTenantAdmin(ctx, email, name, password, role, job.TenantID)
}

func (e *Executor) createDomain(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	name := fieldStr(entity.Raw, "name", "domain")
	return e.adapters.Domain.CreateDomain(ctx, name, job.TenantID)
}

func (e *Executor) createMailbox(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	email := fieldStr(entity.Raw, "email")
	name := fieldStr(entity.Raw, "name")
	domain := fieldStr(entity.Raw, "domain")
	password := fieldStr(entity.Raw, "password")

	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 && domain == "" {
		domain = parts[1]
	}

	return e.adapters.Mailbox.CreateMailbox(ctx, email, name, password, domain, job.TenantID)
}

func (e *Executor) createAlias(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	from := fieldStr(entity.Raw, "from_addr", "from", "alias")
	to := fieldStr(entity.Raw, "to_addr", "to", "forward_to")
	return e.adapters.Alias.CreateAlias(ctx, from, to, job.TenantID, 0)
}

func (e *Executor) createGroup(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	name := fieldStr(entity.Raw, "name")
	description := fieldStr(entity.Raw, "description")
	return e.adapters.Group.CreateGroup(ctx, name, description, job.TenantID)
}

func (e *Executor) createGroupMembership(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	groupName := fieldStr(entity.Raw, "group_name", "group")
	email := fieldStr(entity.Raw, "email", "member_email")
	if err := e.adapters.Group.AddGroupMember(ctx, groupName, email, job.TenantID); err != nil {
		return 0, err
	}
	return 0, nil
}

func reorderEntities(entities []ParsedEntity) []ParsedEntity {
	orderMap := make(map[ImportEntityType]int)
	for i, entity := range EntityDependencyOrder() {
		orderMap[entity] = i
	}
	sorted := make([]ParsedEntity, len(entities))
	copy(sorted, entities)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if orderMap[sorted[i].Entity] > orderMap[sorted[j].Entity] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}
