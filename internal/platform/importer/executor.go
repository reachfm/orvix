package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type Executor struct {
	adapters       *ServiceAdapters
	repo           *Repository
	tenantID       uint
	idempotencyKey string
}

func NewExecutor(adapters *ServiceAdapters, repo *Repository, tenantID uint, idempotencyKey string) *Executor {
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

	entities := source.Entities
	if startFrom >= len(entities) {
		return &ExecutionResult{}, nil
	}

	result := &ExecutionResult{}
	processed := startFrom
	succeeded := 0
	skipped := 0
	failed := 0

	ordered := reorderEntities(entities)
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
			if err := e.repo.SaveCheckpoint(ctx, checkpoint); err != nil {
				return nil, err
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
	return e.adapters.CreateOrganization(name, domain, job.TenantID)
}

func (e *Executor) createTenantAdmin(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	email := fieldStr(entity.Raw, "email")
	name := fieldStr(entity.Raw, "name")
	password := fieldStr(entity.Raw, "password")
	role := fieldStr(entity.Raw, "role")
	if role == "" {
		role = "tenant_admin"
	}

	var exists int
	if err := e.adapters.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email=? AND deleted_at IS NULL`, email).Scan(&exists); err == nil && exists > 0 {
		return 0, nil
	}

	h := sha256.Sum256([]byte(password))
	hash := hex.EncodeToString(h[:])

	_, err := e.adapters.db.ExecContext(ctx,
		`INSERT INTO users (tenant_id, email, name, password_hash, role, status, created_at, updated_at) VALUES (?,?,?,?,?,'active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		job.TenantID, email, name, hash, role)
	if err != nil {
		return 0, err
	}
	return uint(job.ID*1000 + uint(len(password))), nil
}

func (e *Executor) createDomain(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	name := fieldStr(entity.Raw, "name", "domain")
	return e.adapters.CreateDomain(name, job.TenantID)
}

func (e *Executor) createMailbox(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	email := fieldStr(entity.Raw, "email")
	name := fieldStr(entity.Raw, "name")
	domain := fieldStr(entity.Raw, "domain")
	password := fieldStr(entity.Raw, "password")
	_ = password

	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 && domain == "" {
		domain = parts[1]
	}

	return e.adapters.CreateMailbox(email, name, password, domain, job.TenantID)
}

func (e *Executor) createAlias(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	from := fieldStr(entity.Raw, "from_addr", "from", "alias")
	to := fieldStr(entity.Raw, "to_addr", "to", "forward_to")

	fromParts := strings.SplitN(from, "@", 2)
	var domainID int64
	if len(fromParts) == 2 {
		e.adapters.db.QueryRowContext(ctx, `SELECT id FROM coremail_domains WHERE name=? AND tenant_id=? AND deleted_at IS NULL`, strings.ToLower(fromParts[1]), job.TenantID).Scan(&domainID)
	}

	return e.adapters.CreateAlias(from, to, job.TenantID, uint(domainID))
}

func (e *Executor) createGroup(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	name := fieldStr(entity.Raw, "name")
	description := fieldStr(entity.Raw, "description")
	return e.adapters.CreateGroup(name, description, job.TenantID)
}

func (e *Executor) createGroupMembership(ctx context.Context, entity ParsedEntity, job *ImportJob) (uint, error) {
	groupName := fieldStr(entity.Raw, "group_name", "group")
	email := fieldStr(entity.Raw, "email", "member_email")

	var groupID uint
	if err := e.adapters.db.QueryRowContext(ctx, `SELECT id FROM coremail_groups WHERE name=? AND tenant_id=? AND deleted_at IS NULL`, groupName, job.TenantID).Scan(&groupID); err != nil {
		return 0, fmt.Errorf("group not found: %s", groupName)
	}

	if err := e.adapters.AddGroupMember(groupID, email); err != nil {
		return 0, err
	}
	return groupID, nil
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
