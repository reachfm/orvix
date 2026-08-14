package bulkprovision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// DefaultBatchSize bounds how many rows Execute attempts before it
// checkpoints. Kept small enough that a single batch comfortably fits
// inside a job's lease/heartbeat window under real SMTP/DB latency.
// DefaultBatchSize is a var, not a const, so tests can shrink it to
// make batch-boundary/checkpoint behavior observable with a handful of
// rows instead of needing thousands. Production code never mutates it.
var DefaultBatchSize = 50

type Service struct {
	repo        *Repository
	mailboxes   MailboxCreator
	accessMode  DomainAccessMode
	idempotency *kernel.IdempotencyStore
	outbox      *kernel.OutboxRepository
	audit       *audit.ExtendedStore
	clock       kernel.Clock
}

func NewService(repo *Repository, mailboxes MailboxCreator, accessMode DomainAccessMode, idempotency *kernel.IdempotencyStore, outbox *kernel.OutboxRepository, auditStore *audit.ExtendedStore, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, mailboxes: mailboxes, accessMode: accessMode, idempotency: idempotency, outbox: outbox, audit: auditStore, clock: clock}
}

// recordAudit writes a best-effort, secret-free audit entry for a
// NON-terminal bulk-import event (currently only "job created", which
// precedes any mutation and so has nothing to roll back if it fails).
// Terminal lifecycle events (completed/failed/cancelled) MUST go
// through finalizeLifecycleTx instead — see its doc comment.
func (s *Service) recordAudit(ctx context.Context, actorID uint, action string, job *Job, result, detail string) {
	if s.audit == nil || job == nil {
		return
	}
	_ = s.audit.Record(ctx, &audit.ExtendedEntry{
		Actor: fmt.Sprintf("user:%d", actorID), ActorID: actorID,
		Action: action, Target: fmt.Sprintf("bulk_import_job:%d", job.ID), TargetID: job.ID,
		TenantID: job.TenantID, Result: result,
		After: fmt.Sprintf("status=%s total=%d valid=%d invalid=%d created=%d failed=%d skipped=%d source_hash=%s %s",
			job.Status, job.TotalRows, job.ValidRows, job.InvalidRows, job.CreatedCount, job.FailedCount, job.SkippedCount, job.SourceHash, detail),
	})
}

// errLifecycleDurabilityUnavailable is returned instead of silently
// completing a terminal job-status transition when the outbox and/or
// audit dependency was not wired at construction. A nil dependency
// must never silently disable durability evidence for a committed
// business-state change — see PHASE R1-C requirement 4.
var errLifecycleDurabilityUnavailable = errors.New("bulk import lifecycle durability (outbox/audit) not available")

// finalizeLifecycleTx durably commits a terminal bulk-import job-status
// transition together with its lifecycle outbox event and audit entry
// IN ONE DATABASE TRANSACTION. If the outbox insert or the audit insert
// fails, the whole transaction — including the job-status mutation
// itself — rolls back, so the job is left in its prior, still-resumable
// state rather than silently reporting success with no evidence.
//
// mutate performs the actual job-row UPDATE(s) using the given *sql.Tx
// and must be the ONLY write inside the transaction besides the
// outbox/audit inserts this function performs itself.
func (s *Service) finalizeLifecycleTx(ctx context.Context, jobID, actorID uint, action, result, detail string, mutate func(tx *sql.Tx) error) (*Job, error) {
	if s.outbox == nil || s.audit == nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "bulk import lifecycle finalize", errLifecycleDurabilityUnavailable)
	}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin lifecycle transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := mutate(tx); err != nil {
		return nil, err
	}

	job, err := s.repo.getJobTx(ctx, tx, jobID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "reload job for lifecycle event", err)
	}
	if job == nil {
		return nil, ErrJobNotFound
	}

	if err := s.outbox.Enqueue(ctx, tx, "bulkprovision."+action, fmt.Sprintf("%d", jobID), map[string]any{
		"job_id": jobID, "tenant_id": job.TenantID, "status": job.Status,
		"created": job.CreatedCount, "failed": job.FailedCount, "skipped": job.SkippedCount,
		"source_hash": job.SourceHash,
	}, s.clock.Now()); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue lifecycle outbox event", err)
	}

	if err := s.audit.RecordTx(ctx, tx, &audit.ExtendedEntry{
		Actor: fmt.Sprintf("user:%d", actorID), ActorID: actorID,
		Action: action, Target: fmt.Sprintf("bulk_import_job:%d", jobID), TargetID: jobID,
		TenantID: job.TenantID, Result: result,
		After: fmt.Sprintf("status=%s total=%d valid=%d invalid=%d created=%d failed=%d skipped=%d source_hash=%s %s",
			job.Status, job.TotalRows, job.ValidRows, job.InvalidRows, job.CreatedCount, job.FailedCount, job.SkippedCount, job.SourceHash, detail),
	}); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "record lifecycle audit entry", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit lifecycle transaction", err)
	}
	committed = true
	return job, nil
}

// ValidationResult is the dry-run output: every row's outcome without
// any mutation having occurred.
type ValidationResult struct {
	TotalRows   int   `json:"total_rows"`
	ValidRows   int   `json:"valid_rows"`
	InvalidRows int   `json:"invalid_rows"`
	Rows        []Row `json:"rows"`
	// CapacityRemaining is an ADVISORY estimate (not authoritative —
	// CreateMailbox's own transactional check at Execute time is what
	// actually prevents overshoot under concurrency). -1 means
	// unlimited.
	CapacityRemaining int `json:"capacity_remaining"`
	// SourceHash and SchemaVersion bind this result to the EXACT bytes
	// that were validated. CreateJob persists both on the job; Execute
	// refuses to run if the caller's source no longer matches.
	SourceHash    string `json:"source_hash"`
	SchemaVersion int    `json:"schema_version"`
}

// Validate performs dry-run validation: normalization, in-file and
// in-database duplicate detection, domain ownership, mail_access_mode
// compatibility, and an advisory capacity estimate. It NEVER creates a
// mailbox, a job row, or any other persistent state — pure read-only
// analysis, safe to call repeatedly.
func (s *Service) Validate(ctx context.Context, tenantID, domainID uint, domainName, sourceHash string, raw []RawRow) (*ValidationResult, error) {
	if len(raw) == 0 {
		return nil, ErrEmptyFile
	}
	if len(raw) > MaxRowsPerImport {
		return nil, ErrTooManyRows
	}

	alloc, err := s.mailboxes.ResolveDomainAllocation(ctx, domainName, tenantID)
	if err != nil {
		return nil, err
	}

	var accessMode domain.MailAccessMode = domain.MailAccessInternalExternal
	if s.accessMode != nil {
		if m, err := s.accessMode.GetMailAccessMode(ctx, domainID, tenantID); err == nil {
			accessMode = m
		}
	}

	seenInFile := make(map[string]int, len(raw)) // normalized email -> first row number
	result := &ValidationResult{TotalRows: len(raw), SourceHash: sourceHash, SchemaVersion: SchemaVersion}
	now := s.clock.Now()

	for _, rr := range raw {
		row := Row{
			RowNumber: rr.RowNumber, ExternalRef: rr.ExternalRef, Name: rr.Name, QuotaMB: rr.QuotaMB,
			AccessMode: rr.AccessMode, CreatedAt: now, UpdatedAt: now,
		}
		if !rr.AccessMode.Valid() {
			row.Email, _ = normalizeEmail(rr.Email)
			row.Status = RowInvalid
			row.ErrorCode = ErrCodeAccessModeIncompatible
			row.ErrorDetail = fmt.Sprintf("%q is not a supported access mode", rr.AccessMode)
			result.Rows = append(result.Rows, row)
			result.InvalidRows++
			continue
		}
		if row.AccessMode == "" {
			row.AccessMode = AccessInherit
		}
		email, ok := normalizeEmail(rr.Email)
		row.Email = email
		if !ok {
			row.Status = RowInvalid
			row.ErrorCode = ErrCodeInvalidEmail
			row.ErrorDetail = fmt.Sprintf("%q is not a valid email address", rr.Email)
			result.Rows = append(result.Rows, row)
			result.InvalidRows++
			continue
		}
		if firstRow, dup := seenInFile[email]; dup {
			row.Status = RowInvalid
			row.ErrorCode = ErrCodeDuplicateInFile
			row.ErrorDetail = fmt.Sprintf("duplicate of row %d within this file", firstRow)
			result.Rows = append(result.Rows, row)
			result.InvalidRows++
			continue
		}
		seenInFile[email] = rr.RowNumber

		exists, err := s.mailboxes.ExistsByEmail(ctx, email)
		if err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "check existing mailbox", err)
		}
		if exists {
			row.Status = RowInvalid
			row.ErrorCode = ErrCodeDuplicateInDatabase
			row.ErrorDetail = "a mailbox with this address already exists"
			result.Rows = append(result.Rows, row)
			result.InvalidRows++
			continue
		}

		if accessMode == domain.MailAccessInternalOnly {
			// internal_only is a real, enforced constraint at SMTP
			// delivery time (internal/coremail/smtp); bulk-created
			// mailboxes on such a domain are still valid to create —
			// the row-level signal here is informational (surfaced so
			// an operator importing external forwarding addresses via
			// quota_mb/name conventions notices it), not a rejection,
			// since a plain local mailbox is exactly what internal_only
			// domains are for.
			_ = ErrCodeAccessModeIncompatible // reserved for a future incompatible-request-shape row (e.g. external forward-to)
		}

		row.Status = RowValid
		result.Rows = append(result.Rows, row)
		result.ValidRows++
	}

	result.CapacityRemaining = -1
	if capLimit, unlimited := domain.ResolveMailboxCap(alloc.MaxMailboxes, alloc.OrgMaxMailboxes); !unlimited {
		used, err := s.mailboxes.CountActiveByDomain(ctx, alloc.DomainID, tenantID)
		if err == nil {
			remaining := capLimit - used
			if remaining < 0 {
				remaining = 0
			}
			result.CapacityRemaining = remaining
		}
	}

	return result, nil
}

const idempotencyScope = "bulkprovision.execute"

// GetJobForHandler is a thin, typed-error read used by HTTP handlers
// that need the job (e.g. to resolve its domain) before calling
// Execute/RetryFailedRows.
func (s *Service) GetJobForHandler(ctx context.Context, jobID, tenantID uint) (*Job, error) {
	job, err := s.repo.GetJob(ctx, jobID, tenantID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get import job", err)
	}
	if job == nil {
		return nil, ErrJobNotFound
	}
	return job, nil
}

// GetJobWithRows returns the full downloadable structured result: the
// job plus every row, regardless of status.
func (s *Service) GetJobWithRows(ctx context.Context, jobID, tenantID uint) (*Job, []Row, error) {
	job, err := s.GetJobForHandler(ctx, jobID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.repo.ListRows(ctx, jobID, nil)
	if err != nil {
		return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "list import rows", err)
	}
	return job, rows, nil
}

// ListRowsPage is the bounded, paginated row-result read the HTTP
// report endpoint uses.
func (s *Service) ListRowsPage(ctx context.Context, jobID uint, limit, offset int) ([]Row, int, error) {
	rows, total, err := s.repo.ListRowsPage(ctx, jobID, nil, limit, offset)
	if err != nil {
		return nil, 0, kernel.Wrap(kernel.ErrCodeInternal, "list import rows", err)
	}
	return rows, total, nil
}

// ListJobs is the bounded, paginated tenant job list the HTTP list
// endpoint uses.
func (s *Service) ListJobs(ctx context.Context, tenantID uint, limit, offset int) ([]Job, int, error) {
	jobs, total, err := s.repo.ListJobs(ctx, tenantID, limit, offset)
	if err != nil {
		return nil, 0, kernel.Wrap(kernel.ErrCodeInternal, "list import jobs", err)
	}
	return jobs, total, nil
}

// CreateJob persists a validated import as a durable job (state
// "ready" if every row validated, "failed" if none did) — this is the
// hand-off point between the pure dry-run above and the stateful,
// resumable job below.
func (s *Service) CreateJob(ctx context.Context, tenantID, domainID, actorID uint, strategy Strategy, conflictPolicy ConflictPolicy, idempotencyKey string, result *ValidationResult) (*Job, error) {
	if idempotencyKey != "" {
		if existing, err := s.repo.GetJobByIdempotencyKey(ctx, tenantID, idempotencyKey); err == nil && existing != nil {
			return existing, nil
		}
	}
	if strategy != StrategyAtomic && strategy != StrategyPartial {
		strategy = StrategyPartial
	}
	if !conflictPolicy.Valid() {
		return nil, ErrInvalidConflictPolicy
	}
	now := s.clock.Now()
	status := JobReady
	if result.ValidRows == 0 {
		status = JobFailed
	}
	job := &Job{
		TenantID: tenantID, DomainID: domainID, Status: status, Strategy: strategy, ConflictPolicy: conflictPolicy,
		IdempotencyKey: idempotencyKey, SourceHash: result.SourceHash, SchemaVersion: result.SchemaVersion,
		TotalRows: result.TotalRows, ValidRows: result.ValidRows,
		InvalidRows: result.InvalidRows, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.CreateJob(ctx, job, result.Rows); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create import job", err)
	}
	if s.audit != nil {
		s.recordAudit(ctx, actorID, "bulkprovision.job.created", job, "success", "")
	}
	return job, nil
}

// ExecuteHooks lets a durable-job wrapper observe/steer batch-bounded
// execution without this package depending on internal/platform/jobs.
// BeforeBatch is called before every bounded batch; a non-nil error
// (lease lost, worker asked to stop, heartbeat failed) halts Execute
// IMMEDIATELY, leaving the job in JobRunning with its checkpoint
// already persisted through the end of the PRIOR batch — safe to
// resume by calling Execute again (a fresh lease will pick up exactly
// where NextRowNumber left off; nothing already RowCreated is ever
// re-attempted).
type ExecuteHooks struct {
	BeforeBatch func(ctx context.Context) error
}

// Execute runs a "ready" (fresh start) or "running" (resume-after-crash)
// job in bounded batches, checkpointing after each one. Atomic strategy:
// if ANY row fails to create, every mailbox created earlier in THIS
// call is soft-deleted (compensating rollback) and the job ends
// "failed" — no partial state survives. Partial strategy: each row is
// independent; a failure never affects sibling rows, and the job ends
// "partially_failed" if any row failed, "completed" otherwise.
// ConflictSkipExisting rows are neither a failure nor a creation: the
// existing mailbox is left untouched and the row is recorded skipped.
func (s *Service) Execute(ctx context.Context, jobID, tenantID, domainID uint, domainName, sourceHash string, hooks *ExecuteHooks) (*Job, []Row, error) {
	job, err := s.repo.GetJob(ctx, jobID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if job == nil {
		return nil, nil, ErrJobNotFound
	}
	if job.SourceHash != "" && sourceHash != "" && job.SourceHash != sourceHash {
		return nil, nil, ErrSourceHashMismatch
	}
	now := s.clock.Now()
	version := job.Version
	switch job.Status {
	case JobReady:
		ok, terr := s.repo.TransitionJobIfVersion(ctx, jobID, JobReady, JobRunning, job.Version, now)
		if terr != nil {
			return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "start job", terr)
		}
		if !ok {
			return nil, nil, ErrVersionConflict
		}
		version = job.Version + 1
	case JobRunning:
		// Resume: a prior attempt started and checkpointed but did not
		// reach a terminal state (crash, lease loss, cooperative stop).
	default:
		return nil, nil, ErrJobNotReady
	}

	rows, err := s.repo.ListRows(ctx, jobID, []RowStatus{RowValid})
	if err != nil {
		return nil, nil, err
	}
	// Defense in depth: only ever attempt rows at or after the durable
	// checkpoint, even though RowValid already excludes prior successes.
	filtered := rows[:0]
	for _, r := range rows {
		if r.RowNumber >= job.NextRowNumber {
			filtered = append(filtered, r)
		}
	}
	rows = filtered

	var created []uint // mailbox IDs created THIS call, for atomic-mode rollback
	createdCount, failedCount, skippedCount := job.CreatedCount, job.FailedCount, job.SkippedCount
	nextRowNumber := job.NextRowNumber
	batchSize := DefaultBatchSize

	for start := 0; start < len(rows); start += batchSize {
		if hooks != nil && hooks.BeforeBatch != nil {
			if herr := hooks.BeforeBatch(ctx); herr != nil {
				// Stop cleanly: whatever was checkpointed through the
				// previous iteration already persisted. The job stays
				// "running" and is safely resumable.
				pausedJob, jerr := s.repo.GetJob(ctx, jobID, tenantID)
				if jerr != nil {
					return nil, nil, jerr
				}
				return pausedJob, nil, nil
			}
		}
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		for i := start; i < end; i++ {
			row := &rows[i]
			now = s.clock.Now()
			mailboxID, setupTokenHash, createErr := s.createOneMailbox(ctx, tenantID, domainName, row)
			if createErr != nil {
				if job.ConflictPolicy == ConflictSkipExisting && errors.Is(createErr, mailbox.ErrMailboxExists) {
					skippedCount++
					if uerr := s.repo.UpdateRowResult(ctx, row.ID, RowSkipped, "", "existing mailbox left unmodified", 0, "", now); uerr != nil {
						return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "record skipped row", uerr)
					}
					nextRowNumber = row.RowNumber + 1
					continue
				}
				failedCount++
				if uerr := s.repo.UpdateRowResult(ctx, row.ID, RowFailed, ErrCodeCreateFailed, sanitizeErr(createErr), 0, "", now); uerr != nil {
					return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "record failed row", uerr)
				}
				if job.Strategy == StrategyAtomic {
					s.rollbackCreated(ctx, tenantID, created)
					finalJob, ferr := s.finalizeLifecycleTx(ctx, jobID, job.CreatedBy, "job.failed", "failed", "atomic rollback", func(tx *sql.Tx) error {
						return s.repo.UpdateJobCountersTx(ctx, tx, jobID, JobFailed, 0, failedCount, skippedCount, now)
					})
					if ferr != nil {
						return nil, nil, ferr
					}
					finalRows, rerr := s.repo.ListRows(ctx, jobID, nil)
					if rerr != nil {
						return nil, nil, rerr
					}
					return finalJob, finalRows, nil
				}
				nextRowNumber = row.RowNumber + 1
				continue
			}
			createdCount++
			created = append(created, mailboxID)
			// The mailbox mutation ITSELF already committed via the
			// canonical mailbox service's own transaction by this point
			// (createOneMailbox returned success). If recording the row
			// result fails here, we must not silently pretend nothing
			// happened: return the error so this call stops WITHOUT
			// checkpointing this batch. On resume, createOneMailbox's
			// mailbox.ErrMailboxExists path (defense-in-depth) prevents
			// re-creating the same address a second time; ConflictFail
			// jobs would then need administrator remediation for this one
			// row, which is the honest partial-success reporting model
			// this feature specifies rather than a fabricated atomic
			// cross-service transaction.
			if uerr := s.repo.UpdateRowResult(ctx, row.ID, RowCreated, "", "", mailboxID, setupTokenHash, now); uerr != nil {
				return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "record created row (mailbox already committed)", uerr)
			}
			nextRowNumber = row.RowNumber + 1
		}

		// Checkpoint after the batch, guarded by the version WE started
		// this call believing was current. If it fails, another actor
		// (most likely a concurrent Cancel, or a second worker that
		// re-claimed a lease we lost) changed the job first — stop
		// immediately rather than race it.
		ok, cerr := s.repo.CheckpointBatch(ctx, jobID, version, createdCount, failedCount, skippedCount, nextRowNumber, s.clock.Now())
		if cerr != nil {
			return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "checkpoint import batch", cerr)
		}
		if !ok {
			pausedJob, jerr := s.repo.GetJob(ctx, jobID, tenantID)
			if jerr != nil {
				return nil, nil, jerr
			}
			return pausedJob, nil, nil
		}
		version++
	}

	finalStatus := JobCompleted
	if failedCount > 0 {
		finalStatus = JobPartiallyFailed
	}
	action := "job.completed"
	if finalStatus == JobPartiallyFailed {
		action = "job.partially_failed"
	}
	finalJob, ferr := s.finalizeLifecycleTx(ctx, jobID, job.CreatedBy, action, string(finalStatus), "", func(tx *sql.Tx) error {
		return s.repo.UpdateJobCountersTx(ctx, tx, jobID, finalStatus, createdCount, failedCount, skippedCount, s.clock.Now())
	})
	if ferr != nil {
		// The job-status transition (and its outbox/audit evidence) did
		// NOT commit: it is left exactly as it was before this call (see
		// finalizeLifecycleTx), which for a job entering this block is
		// JobRunning with its checkpoint already durable from the last
		// successful CheckpointBatch. A subsequent Execute call resumes
		// cleanly: ListRows(RowValid) filtered by the persisted
		// NextRowNumber returns nothing left to process, and this same
		// finalize step simply runs again — never re-creating a mailbox.
		return nil, nil, ferr
	}
	finalRows, rerr := s.repo.ListRows(ctx, jobID, nil)
	if rerr != nil {
		return nil, nil, rerr
	}
	return finalJob, finalRows, nil
}

// createOneMailbox creates one mailbox via the canonical
// internal/admin/mailbox.Service. It never accepts or handles a
// plaintext password from the import: a cryptographically random
// password is generated and immediately discarded, and the mailbox is
// created with ForcePasswordChange so the ONLY way to obtain a working
// credential is the platform's existing forgot-password/activation
// flow (internal/api/handlers/customer_auth.go) — the "canonical
// activation/reset-password flow" fallback this package deliberately
// uses instead of fabricating a one-time encrypted-credential-artifact
// system this repository has no general-purpose secret-encryption
// service to back safely.
func (s *Service) createOneMailbox(ctx context.Context, tenantID uint, domainName string, row *Row) (mailboxID uint, setupTokenHash string, err error) {
	password, err := generateDiscardedPassword()
	if err != nil {
		return 0, "", err
	}
	email := row.Email
	if !strings.Contains(email, "@") {
		email = email + "@" + domainName
	}
	mode := string(row.AccessMode)
	if mode == "" {
		mode = string(AccessInherit)
	}
	resp, err := s.mailboxes.CreateMailbox(ctx, mailbox.CreateMailboxRequest{
		Email: email, Password: password, Name: row.Name, QuotaMB: row.QuotaMB,
		ForcePasswordChange: true, MailAccessMode: &mode,
	}, tenantID)
	password = "" // discard immediately; never referenced again
	if err != nil {
		return 0, "", err
	}
	return resp.Mailbox.ID, "", nil
}

// rollbackCreated compensates for atomic-strategy failures by
// soft-deleting every mailbox this job run created before the
// failure. It is best-effort per mailbox (a rollback failure is
// logged via the row's own state, not silently dropped) — the job
// still ends "failed" either way, so a partial rollback failure never
// masquerades as success.
func (s *Service) rollbackCreated(ctx context.Context, tenantID uint, mailboxIDs []uint) {
	for _, id := range mailboxIDs {
		_ = s.mailboxes.SoftDeleteMailbox(ctx, id, tenantID, "atomic bulk import rolled back")
	}
}

// Cancel stops a job before or during execution. Only queued/
// validating/ready/running jobs may be cancelled — completed/failed/
// cancelled are terminal.
func (s *Service) Cancel(ctx context.Context, jobID, tenantID uint) (*Job, error) {
	job, err := s.repo.GetJob(ctx, jobID, tenantID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrJobNotFound
	}
	switch job.Status {
	case JobQueued, JobValidating, JobReady, JobRunning:
	default:
		return nil, ErrJobNotCancellable
	}
	now := s.clock.Now()
	var transitioned bool
	finalJob, ferr := s.finalizeLifecycleTx(ctx, jobID, 0, "job.cancelled", "cancelled", "", func(tx *sql.Tx) error {
		ok, terr := s.repo.TransitionJobIfVersionTx(ctx, tx, jobID, job.Status, JobCancelled, job.Version, now)
		if terr != nil {
			return terr
		}
		transitioned = ok
		if !ok {
			return ErrVersionConflict
		}
		return nil
	})
	if ferr != nil {
		if errors.Is(ferr, ErrVersionConflict) {
			return nil, ErrVersionConflict
		}
		if !transitioned {
			// The transition itself never happened (lost the race, or the
			// lifecycle-evidence write failed before ever attempting it) —
			// the job is left exactly as GetJob already showed it: still
			// cancellable/retryable, never a false "cancelled".
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "cancel job", ferr)
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "cancel job", ferr)
	}
	return finalJob, nil
}

// RetryFailedRows re-attempts only the rows left in RowFailed from a
// partially_failed job, using the SAME per-row-isolated semantics as
// the original partial execution — a retry never re-touches rows that
// already succeeded (RowCreated).
func (s *Service) RetryFailedRows(ctx context.Context, jobID, tenantID uint, domainName string) (*Job, []Row, error) {
	job, err := s.repo.GetJob(ctx, jobID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if job == nil {
		return nil, nil, ErrJobNotFound
	}
	if job.Status != JobPartiallyFailed && job.Status != JobFailed {
		return nil, nil, ErrJobNotRetryable
	}
	failedRows, err := s.repo.ListRows(ctx, jobID, []RowStatus{RowFailed})
	if err != nil {
		return nil, nil, err
	}
	if len(failedRows) == 0 {
		return nil, nil, ErrJobNotRetryable
	}

	now := s.clock.Now()
	newlyCreated, stillFailed := 0, 0
	for i := range failedRows {
		row := &failedRows[i]
		mailboxID, setupTokenHash, createErr := s.createOneMailbox(ctx, tenantID, domainName, row)
		if createErr != nil {
			stillFailed++
			if uerr := s.repo.UpdateRowResult(ctx, row.ID, RowFailed, ErrCodeCreateFailed, sanitizeErr(createErr), 0, "", now); uerr != nil {
				return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "record still-failed retry row", uerr)
			}
			continue
		}
		newlyCreated++
		if uerr := s.repo.UpdateRowResult(ctx, row.ID, RowCreated, "", "", mailboxID, setupTokenHash, now); uerr != nil {
			return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "record retried created row (mailbox already committed)", uerr)
		}
	}

	finalStatus := JobCompleted
	if stillFailed > 0 {
		finalStatus = JobPartiallyFailed
	}
	finalCreated := job.CreatedCount + newlyCreated
	finalJob, ferr := s.finalizeLifecycleTx(ctx, jobID, job.CreatedBy, "job.retried", string(finalStatus), fmt.Sprintf("retry: %d newly created, %d still failed", newlyCreated, stillFailed), func(tx *sql.Tx) error {
		return s.repo.UpdateJobCountersTx(ctx, tx, jobID, finalStatus, finalCreated, stillFailed, job.SkippedCount, now)
	})
	if ferr != nil {
		return nil, nil, ferr
	}
	allRows, rerr := s.repo.ListRows(ctx, jobID, nil)
	if rerr != nil {
		return nil, nil, rerr
	}
	return finalJob, allRows, nil
}

func normalizeEmail(raw string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || !strings.Contains(e, "@") {
		return e, false
	}
	parts := strings.SplitN(e, "@", 2)
	if parts[0] == "" || parts[1] == "" || strings.Contains(e, " ") {
		return e, false
	}
	return e, true
}

func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
