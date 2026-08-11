package importer

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

type ImportStatus string

const (
	StatusUploaded           ImportStatus = "uploaded"
	StatusValidating         ImportStatus = "validating"
	StatusValidated          ImportStatus = "validated"
	StatusRunning            ImportStatus = "running"
	StatusCompleted          ImportStatus = "completed"
	StatusValidationFailed   ImportStatus = "validation_failed"
	StatusPaused             ImportStatus = "paused"
	StatusFailed             ImportStatus = "failed"
	StatusCancelled          ImportStatus = "cancelled"
	StatusCompensating       ImportStatus = "compensating"
	StatusCompensated        ImportStatus = "compensated"
	StatusCompensationFailed ImportStatus = "compensation_failed"
)

var importTransitions = map[ImportStatus]map[ImportStatus]bool{
	StatusUploaded:           {StatusValidating: true, StatusCancelled: true},
	StatusValidating:         {StatusValidated: true, StatusValidationFailed: true, StatusCancelled: true},
	StatusValidated:          {StatusRunning: true, StatusValidating: true, StatusCancelled: true},
	StatusValidationFailed:   {StatusUploaded: true, StatusCancelled: true},
	StatusRunning:            {StatusCompleted: true, StatusPaused: true, StatusFailed: true, StatusCancelled: true, StatusRunning: true},
	StatusPaused:             {StatusRunning: true, StatusCancelled: true},
	StatusFailed:             {StatusValidating: true, StatusCancelled: true},
	StatusCancelled:          {},
	StatusCompleted:          {StatusCompensating: true},
	StatusCompensating:       {StatusCompensated: true, StatusCompensationFailed: true},
	StatusCompensated:        {},
	StatusCompensationFailed: {StatusCompensating: true},
}

func (s ImportStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusCancelled, StatusCompensated, StatusCompensationFailed:
		return true
	default:
		return false
	}
}

func (j *ImportJob) IsTerminal() bool {
	return j.Status.IsTerminal()
}

func (s ImportStatus) CanTransition(to ImportStatus) bool {
	return importTransitions[s][to]
}

type ImportSourceType string

const (
	SourceCSV  ImportSourceType = "csv"
	SourceJSON ImportSourceType = "json"
)

type ImportEntityType string

const (
	EntityOrganization    ImportEntityType = "organization"
	EntityTenantAdmin     ImportEntityType = "tenant_admin"
	EntityDomain          ImportEntityType = "domain"
	EntityMailbox         ImportEntityType = "mailbox"
	EntityAlias           ImportEntityType = "alias"
	EntityGroup           ImportEntityType = "group"
	EntityGroupMembership ImportEntityType = "group_membership"
)

var entityDependencyOrder = []ImportEntityType{
	EntityOrganization,
	EntityTenantAdmin,
	EntityDomain,
	EntityMailbox,
	EntityAlias,
	EntityGroup,
	EntityGroupMembership,
}

func EntityDependencyOrder() []ImportEntityType {
	return entityDependencyOrder
}

func entityOrderIndex(entity ImportEntityType) int {
	for i, e := range entityDependencyOrder {
		if e == entity {
			return i
		}
	}
	return len(entityDependencyOrder)
}

type ConflictPolicy string

const (
	ConflictFail       ConflictPolicy = "fail"
	ConflictSkip       ConflictPolicy = "skip"
	ConflictUpdateSafe ConflictPolicy = "update_safe_fields"
)

func (p ConflictPolicy) Valid() bool {
	switch p {
	case ConflictFail, ConflictSkip, ConflictUpdateSafe:
		return true
	default:
		return false
	}
}

type RowResultStatus string

const (
	RowValid       RowResultStatus = "valid"
	RowInvalid     RowResultStatus = "invalid"
	RowConflict    RowResultStatus = "conflict"
	RowDeferred    RowResultStatus = "deferred"
	RowCreated     RowResultStatus = "created"
	RowUpdated     RowResultStatus = "updated"
	RowSkipped     RowResultStatus = "skipped"
	RowFailed      RowResultStatus = "failed"
	RowCompensated RowResultStatus = "compensated"
)

type ImportRow struct {
	Line     int                  `json:"line"`
	Entity   ImportEntityType     `json:"entity"`
	RowKey   string               `json:"row_key"`
	Data     json.RawMessage      `json:"-"`
	SafeData json.RawMessage      `json:"data,omitempty"`
	Errors   []RowValidationError `json:"errors,omitempty"`
	Status   RowResultStatus      `json:"status"`
}

type RowValidationError struct {
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationReport struct {
	ImportID      uint        `json:"import_id"`
	SourceHash    string      `json:"source_hash"`
	SchemaVersion int         `json:"schema_version"`
	Valid         int         `json:"valid"`
	Invalid       int         `json:"invalid"`
	Conflict      int         `json:"conflict"`
	Unchanged     int         `json:"unchanged"`
	Deferred      int         `json:"deferred"`
	Total         int         `json:"total"`
	Rows          []ImportRow `json:"rows,omitempty"`
	GeneratedAt   time.Time   `json:"generated_at"`
}

type ImportJob struct {
	ID             uint             `json:"id"`
	TenantID       uint             `json:"tenant_id"`
	Scope          string           `json:"scope"`
	Actor          string           `json:"actor"`
	SourceType     ImportSourceType `json:"source_type"`
	ConflictPolicy ConflictPolicy   `json:"conflict_policy"`
	SchemaVersion  int              `json:"schema_version"`
	Status         ImportStatus     `json:"status"`
	SourceHash     string           `json:"source_hash"`
	SourceName     string           `json:"source_name"`

	TotalRows     int `json:"total_rows"`
	ProcessedRows int `json:"processed_rows"`
	SucceededRows int `json:"succeeded_rows"`
	SkippedRows   int `json:"skipped_rows"`
	FailedRows    int `json:"failed_rows"`

	CurrentCheckpoint int              `json:"current_checkpoint"`
	CheckpointEntity  ImportEntityType `json:"checkpoint_entity"`
	CheckpointRow     int              `json:"checkpoint_row"`

	LastError string `json:"last_error,omitempty"`

	JobID      uint   `json:"job_id,omitempty"`
	StagingID  string `json:"-"`
	StoredSize int64  `json:"stored_size,omitempty"`

	LeaseOwner string `json:"-"`
	LeaseToken string `json:"-"`

	ValidationReportRaw string `json:"-"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int       `json:"version"`
}

type ImportFilter struct {
	TenantID uint
	Scope    string
	Status   ImportStatus
	Page     kernel.PageRequest
}

type Checkpoint struct {
	ImportID       uint             `json:"import_id"`
	Entity         ImportEntityType `json:"entity"`
	RowIndex       int              `json:"row_index"`
	SucceededIDs   []uint           `json:"succeeded_ids,omitempty"`
	FailedRows     []int            `json:"failed_rows,omitempty"`
	ProcessedCount int              `json:"processed_count"`
	CommittedAt    time.Time        `json:"committed_at"`
}

type CompensationRecord struct {
	ImportID      uint             `json:"import_id"`
	ResourceID    uint             `json:"resource_id"`
	EntityType    ImportEntityType `json:"entity_type"`
	RowKey        string           `json:"row_key"`
	RowIndex      int              `json:"row_index"`
	Status        string           `json:"status"`
	CompensatedAt *time.Time       `json:"compensated_at,omitempty"`
	Error         string           `json:"error,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
}

var (
	ErrNotFound             = kernel.NotFound("import job")
	ErrInvalidTransition    = kernel.NewError(kernel.ErrCodeStateTransition, "invalid import job state transition")
	ErrHashMismatch         = kernel.NewError(kernel.ErrCodePreconditionFail, "source hash does not match last validated hash")
	ErrDryRunRequired       = kernel.NewError(kernel.ErrCodePreconditionFail, "dry-run validation must succeed before execution")
	ErrActiveJob            = kernel.NewError(kernel.ErrCodeConflict, "another import is already active for this source")
	ErrConfirmationRequired = kernel.NewError(kernel.ErrCodeValidation, "confirmation is required for this action")
	ErrJobsUnavailable      = kernel.NewError(kernel.ErrCodeUnavailable, "durable job service is unavailable")
	ErrIdempotencyRequired  = kernel.NewError(kernel.ErrCodeValidation, "Idempotency-Key is required for this action")
	ErrCancelled            = errors.New("import cancelled during execution")
)

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
