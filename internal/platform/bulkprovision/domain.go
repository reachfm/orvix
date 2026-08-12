// Package bulkprovision implements Feature 8 (Milestone 6): a
// production-grade bulk mailbox provisioning bounded context. It
// reuses internal/admin/mailbox.Service for the actual per-mailbox
// creation (tenant/domain ownership, quota enforcement, audit — all
// already correct there) rather than reimplementing mailbox creation.
// This package owns only what is genuinely new: file parsing, dry-run
// validation, job/row state tracking, execution strategy, and
// idempotent retry.
package bulkprovision

import "time"

// RowStatus is the per-row lifecycle within an import job.
type RowStatus string

const (
	RowPending RowStatus = "pending"
	RowValid   RowStatus = "valid"
	RowInvalid RowStatus = "invalid"
	RowCreated RowStatus = "created"
	RowFailed  RowStatus = "failed"
	RowSkipped RowStatus = "skipped"
)

// JobStatus is the durable import job's lifecycle.
type JobStatus string

const (
	JobQueued          JobStatus = "queued"
	JobValidating      JobStatus = "validating"
	JobReady           JobStatus = "ready"
	JobRunning         JobStatus = "running"
	JobCompleted       JobStatus = "completed"
	JobPartiallyFailed JobStatus = "partially_failed"
	JobFailed          JobStatus = "failed"
	JobCancelled       JobStatus = "cancelled"
)

// Strategy is the execution strategy chosen at Execute time.
type Strategy string

const (
	// StrategyAtomic: the whole job commits or rolls back as one unit.
	// Any invalid/failing row aborts the entire job — no mailboxes are
	// created.
	StrategyAtomic Strategy = "atomic"
	// StrategyPartial: each row commits in its own isolated
	// transaction. A failing row does not affect any other row; the
	// job ends partially_failed if any row failed, completed
	// otherwise.
	StrategyPartial Strategy = "partial"
)

// ErrorCode is a stable, machine-readable per-row validation/execution
// failure code — never a free-text-only message.
type ErrorCode string

const (
	ErrCodeInvalidEmail           ErrorCode = "invalid_email"
	ErrCodeDuplicateInFile        ErrorCode = "duplicate_in_file"
	ErrCodeDuplicateInDatabase    ErrorCode = "duplicate_in_database"
	ErrCodeDomainNotOwned         ErrorCode = "domain_not_owned"
	ErrCodeDomainUnavailable      ErrorCode = "domain_unavailable"
	ErrCodeQuotaExceeded          ErrorCode = "quota_exceeded"
	ErrCodeAccessModeIncompatible ErrorCode = "access_mode_incompatible"
	ErrCodeMissingField           ErrorCode = "missing_field"
	ErrCodeCreateFailed           ErrorCode = "create_failed"
)

// Row is one requested mailbox within an import file. Password is
// intentionally absent from this type: bulk-provisioned mailboxes are
// always created with a discarded random password and a one-time
// setup token — no plaintext password ever enters this package, is
// stored, logged, or returned.
type Row struct {
	ID          uint      `json:"id"`
	JobID       uint      `json:"job_id"`
	RowNumber   int       `json:"row_number"`
	Email       string    `json:"email"`
	Name        string    `json:"name,omitempty"`
	QuotaMB     int64     `json:"quota_mb,omitempty"`
	Status      RowStatus `json:"status"`
	ErrorCode   ErrorCode `json:"error_code,omitempty"`
	ErrorDetail string    `json:"error_detail,omitempty"`
	MailboxID   uint      `json:"mailbox_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Job is the durable import job.
type Job struct {
	ID             uint      `json:"id"`
	TenantID       uint      `json:"tenant_id"`
	DomainID       uint      `json:"domain_id"`
	Status         JobStatus `json:"status"`
	Strategy       Strategy  `json:"strategy"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	TotalRows      int       `json:"total_rows"`
	ValidRows      int       `json:"valid_rows"`
	InvalidRows    int       `json:"invalid_rows"`
	CreatedCount   int       `json:"created_count"`
	FailedCount    int       `json:"failed_count"`
	Version        int       `json:"version"`
	CreatedBy      uint      `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// RawRow is the pre-validation input parsed from CSV or JSON, before
// normalization.
type RawRow struct {
	RowNumber int
	Email     string
	Name      string
	QuotaMB   int64
}
