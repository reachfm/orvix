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

// ConflictPolicy governs what happens when a requested mailbox address
// already exists at execution time. This is intentionally a SEPARATE
// axis from Strategy: Strategy controls whether a row FAILURE affects
// its siblings, ConflictPolicy controls whether an already-existing
// address is a failure at all. Only two safe, explicit values are
// supported — bulk import never updates an existing mailbox's
// password, quota, identity, status, permissions, or access mode.
type ConflictPolicy string

const (
	// ConflictFail: an existing mailbox is a row failure (ErrCodeDuplicateInDatabase).
	ConflictFail ConflictPolicy = "fail"
	// ConflictSkipExisting: an existing mailbox is left completely
	// unmutated and the row is recorded as RowSkipped, not a failure.
	ConflictSkipExisting ConflictPolicy = "skip_existing"
)

func (p ConflictPolicy) Valid() bool {
	return p == ConflictFail || p == ConflictSkipExisting
}

// AccessMode is the per-row requested mailbox access mode, mirroring
// internal/coremail/mailpolicy.Mode / internal/admin/mailbox.MailAccessMode
// (deliberately redefined here as plain strings rather than importing
// either package's type, so this package's wire format stays stable
// independent of internal renames — validated against the same three
// values at the service boundary).
type AccessMode string

const (
	AccessInherit          AccessMode = "inherit"
	AccessInternalOnly     AccessMode = "internal_only"
	AccessInternalExternal AccessMode = "internal_external"
)

func (m AccessMode) Valid() bool {
	switch m {
	case AccessInherit, AccessInternalOnly, AccessInternalExternal, "":
		return true
	default:
		return false
	}
}

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

// Row is one requested mailbox within an import job. Password is
// intentionally absent from this type: bulk-provisioned mailboxes are
// always created with a discarded random password and ForcePasswordChange
// — the real credential path is the platform's existing forgot-password
// / activation flow, never a plaintext value that enters this package,
// is stored, logged, or returned.
type Row struct {
	ID          uint       `json:"id"`
	JobID       uint       `json:"job_id"`
	RowNumber   int        `json:"row_number"`
	ExternalRef string     `json:"external_ref,omitempty"`
	Email       string     `json:"email"`
	Name        string     `json:"name,omitempty"`
	QuotaMB     int64      `json:"quota_mb,omitempty"`
	AccessMode  AccessMode `json:"access_mode,omitempty"`
	Status      RowStatus  `json:"status"`
	ErrorCode   ErrorCode  `json:"error_code,omitempty"`
	ErrorDetail string     `json:"error_detail,omitempty"`
	MailboxID   uint       `json:"mailbox_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Job is the durable import job.
type Job struct {
	ID             uint           `json:"id"`
	TenantID       uint           `json:"tenant_id"`
	DomainID       uint           `json:"domain_id"`
	Status         JobStatus      `json:"status"`
	Strategy       Strategy       `json:"strategy"`
	ConflictPolicy ConflictPolicy `json:"conflict_policy"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	// SourceHash and SchemaVersion bind this job to the EXACT
	// validated upload: Execute refuses to run against a source that
	// no longer hashes to the value validation ran against, and a
	// schema-version mismatch means the template contract itself has
	// moved on and the caller must revalidate.
	SourceHash    string `json:"source_hash,omitempty"`
	SchemaVersion int    `json:"schema_version"`
	TotalRows     int    `json:"total_rows"`
	ValidRows     int    `json:"valid_rows"`
	InvalidRows   int    `json:"invalid_rows"`
	CreatedCount  int    `json:"created_count"`
	FailedCount   int    `json:"failed_count"`
	SkippedCount  int    `json:"skipped_count"`
	// NextRowNumber is the durable execution checkpoint: the row
	// number of the next row Execute has not yet attempted. A worker
	// that crashes mid-run and is re-claimed resumes strictly from
	// here — rows below it are already terminal (created/failed/skipped)
	// and are never re-attempted.
	NextRowNumber int       `json:"next_row_number"`
	Version       int       `json:"version"`
	CreatedBy     uint      `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// RawRow is the pre-validation input parsed from CSV, XLSX, or JSON,
// before normalization.
type RawRow struct {
	RowNumber   int
	ExternalRef string
	Email       string
	Name        string
	QuotaMB     int64
	AccessMode  AccessMode
}
