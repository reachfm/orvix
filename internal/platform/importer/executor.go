package importer

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Executor struct {
	adapters       *Adapters
	repo           *Repository
	tenantID       uint
	idempotencyKey string
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

func (e *Executor) Execute(ctx context.Context, job *ImportJob, data []byte) (*ExecutionResult, error) {
	source, parseErr := ParseSource(data, job.SourceType)
	if parseErr != nil {
		return nil, parseErr
	}

	var startFrom int
	lastCp, _ := e.repo.LastCheckpoint(ctx, job.ID)
	if lastCp != nil {
		startFrom = lastCp.ProcessedCount
	}

	if startFrom >= len(source.Entities) {
		return &ExecutionResult{}, nil
	}

	result := &ExecutionResult{}
	processed := startFrom
	succeeded := 0
	skipped := 0
	failed := 0

	ordered := reorderEntities(source.Entities)
	for i := startFrom; i < len(ordered); i++ {
		entity := ordered[i]
		if len(entity.Errors) > 0 {
			skipped++
			processed++
			continue
		}

		resourceID, execErr := e.executeEntity(ctx, entity, job)
		if execErr != nil {
			failed++
			processed++
			continue
		}

		if resourceID > 0 {
			succeeded++
			result.Created = append(result.Created, CreatedResource{
				EntityType: entity.Entity,
				ResourceID: resourceID,
				RowKey:     fmt.Sprintf("%s_%d", entity.Entity, resourceID),
			})
			e.repo.SaveCompensationRecord(ctx, &CompensationRecord{
				ImportID:   job.ID,
				ResourceID: resourceID,
				EntityType: entity.Entity,
				RowKey:     fmt.Sprintf("%s_%d", entity.Entity, resourceID),
				RowIndex:   entity.Line,
				Status:     "pending",
				CreatedAt:  time.Now().UTC(),
			})
		}

		processed++

		if processed%50 == 0 {
			checkpoint := &Checkpoint{
				ImportID:       job.ID,
				Entity:         entity.Entity,
				RowIndex:       entity.Line,
				ProcessedCount: processed,
				CommittedAt:    time.Now().UTC(),
			}
			if cpErr := e.repo.SaveCheckpoint(ctx, checkpoint); cpErr != nil {
				return nil, cpErr
			}
			e.repo.UpdateProgress(ctx, job.ID, processed, succeeded+job.SucceededRows, skipped+job.SkippedRows, failed+job.FailedRows, processed, entity.Entity, entity.Line)
		}
	}

	result.Processed = processed
	result.Succeeded = succeeded
	result.Skipped = skipped
	result.Failed = failed
	return result, nil
}

func (e *Executor) executeEntity(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	switch entity.Entity {
	case EntityOrganization:
		return e.createOrganization(ctx, entity, job)
	case EntityTenantAdmin:
		return e.createTenantAdmin(ctx, entity, job)
	case EntityDomain:
		return e.createDomain(ctx, entity, job)
	case EntityMailbox:
		return e.createMailbox(ctx, entity, job)
	case EntityAlias:
		return e.createAlias(ctx, entity, job)
	case EntityGroup:
		return e.createGroup(ctx, entity, job)
	case EntityGroupMembership:
		return e.createGroupMembership(ctx, entity, job)
	default:
		return 0, fmt.Errorf("unsupported entity type: %s", entity.Entity)
	}
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
