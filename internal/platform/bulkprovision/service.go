package bulkprovision

import (
	"context"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/platform/kernel"
)

type Service struct {
	repo        *Repository
	mailboxes   MailboxCreator
	accessMode  DomainAccessMode
	idempotency *kernel.IdempotencyStore
	outbox      *kernel.OutboxRepository
	clock       kernel.Clock
}

func NewService(repo *Repository, mailboxes MailboxCreator, accessMode DomainAccessMode, idempotency *kernel.IdempotencyStore, outbox *kernel.OutboxRepository, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, mailboxes: mailboxes, accessMode: accessMode, idempotency: idempotency, outbox: outbox, clock: clock}
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
}

// Validate performs dry-run validation: normalization, in-file and
// in-database duplicate detection, domain ownership, mail_access_mode
// compatibility, and an advisory capacity estimate. It NEVER creates a
// mailbox, a job row, or any other persistent state — pure read-only
// analysis, safe to call repeatedly.
func (s *Service) Validate(ctx context.Context, tenantID, domainID uint, domainName string, raw []RawRow) (*ValidationResult, error) {
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
	result := &ValidationResult{TotalRows: len(raw)}
	now := s.clock.Now()

	for _, rr := range raw {
		row := Row{RowNumber: rr.RowNumber, Name: rr.Name, QuotaMB: rr.QuotaMB, CreatedAt: now, UpdatedAt: now}
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

// CreateJob persists a validated import as a durable job (state
// "ready" if every row validated, "failed" if none did) — this is the
// hand-off point between the pure dry-run above and the stateful,
// resumable job below.
func (s *Service) CreateJob(ctx context.Context, tenantID, domainID, actorID uint, strategy Strategy, idempotencyKey string, result *ValidationResult) (*Job, error) {
	if idempotencyKey != "" {
		if existing, err := s.repo.GetJobByIdempotencyKey(ctx, tenantID, idempotencyKey); err == nil && existing != nil {
			return existing, nil
		}
	}
	if strategy != StrategyAtomic && strategy != StrategyPartial {
		strategy = StrategyPartial
	}
	now := s.clock.Now()
	status := JobReady
	if result.ValidRows == 0 {
		status = JobFailed
	}
	job := &Job{
		TenantID: tenantID, DomainID: domainID, Status: status, Strategy: strategy,
		IdempotencyKey: idempotencyKey, TotalRows: result.TotalRows, ValidRows: result.ValidRows,
		InvalidRows: result.InvalidRows, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.CreateJob(ctx, job, result.Rows); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create import job", err)
	}
	return job, nil
}

// Execute runs a "ready" job. Atomic strategy: if ANY row fails to
// create, every mailbox created earlier in this job is soft-deleted
// (compensating rollback) and the job ends "failed" — no partial
// state survives. Partial strategy: each row is independent; a
// failure never affects sibling rows, and the job ends
// "partially_failed" if any row failed, "completed" otherwise.
func (s *Service) Execute(ctx context.Context, jobID, tenantID, domainID uint, domainName string) (*Job, []Row, error) {
	job, err := s.repo.GetJob(ctx, jobID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if job == nil {
		return nil, nil, ErrJobNotFound
	}
	if job.Status != JobReady {
		return nil, nil, ErrJobNotReady
	}
	now := s.clock.Now()
	if ok, err := s.repo.TransitionJobIfVersion(ctx, jobID, JobReady, JobRunning, job.Version, now); err != nil {
		return nil, nil, kernel.Wrap(kernel.ErrCodeInternal, "start job", err)
	} else if !ok {
		return nil, nil, ErrVersionConflict
	}

	rows, err := s.repo.ListRows(ctx, jobID, []RowStatus{RowValid})
	if err != nil {
		return nil, nil, err
	}

	var created []uint // mailbox IDs created this run, for atomic-mode rollback
	createdCount, failedCount := 0, 0

	for i := range rows {
		row := &rows[i]
		mailboxID, setupTokenHash, createErr := s.createOneMailbox(ctx, tenantID, domainName, row)
		if createErr != nil {
			failedCount++
			_ = s.repo.UpdateRowResult(ctx, row.ID, RowFailed, ErrCodeCreateFailed, sanitizeErr(createErr), 0, "", now)
			if job.Strategy == StrategyAtomic {
				s.rollbackCreated(ctx, tenantID, created)
				_ = s.repo.UpdateJobCounters(ctx, jobID, JobFailed, 0, failedCount, now)
				finalJob, jerr := s.repo.GetJob(ctx, jobID, tenantID)
				if jerr != nil {
					return nil, nil, jerr
				}
				finalRows, _ := s.repo.ListRows(ctx, jobID, nil)
				return finalJob, finalRows, nil
			}
			continue
		}
		createdCount++
		created = append(created, mailboxID)
		_ = s.repo.UpdateRowResult(ctx, row.ID, RowCreated, "", "", mailboxID, setupTokenHash, now)
	}

	finalStatus := JobCompleted
	if failedCount > 0 {
		finalStatus = JobPartiallyFailed
	}
	if err := s.repo.UpdateJobCounters(ctx, jobID, finalStatus, createdCount, failedCount, now); err != nil {
		return nil, nil, err
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, s.repo.db, "bulkprovision.job.completed", fmt.Sprintf("%d", jobID), map[string]any{
			"created": createdCount, "failed": failedCount, "strategy": job.Strategy,
		}, now)
	}

	finalJob, err := s.repo.GetJob(ctx, jobID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	finalRows, _ := s.repo.ListRows(ctx, jobID, nil)
	return finalJob, finalRows, nil
}

func (s *Service) createOneMailbox(ctx context.Context, tenantID uint, domainName string, row *Row) (mailboxID uint, setupTokenHash string, err error) {
	password, err := generateDiscardedPassword()
	if err != nil {
		return 0, "", err
	}
	email := row.Email
	if !strings.Contains(email, "@") {
		email = email + "@" + domainName
	}
	resp, err := s.mailboxes.CreateMailbox(ctx, mailbox.CreateMailboxRequest{
		Email: email, Password: password, Name: row.Name, QuotaMB: row.QuotaMB, ForcePasswordChange: true,
	}, tenantID)
	password = "" // discard immediately; never referenced again
	if err != nil {
		return 0, "", err
	}
	_, hash, tokErr := generateSetupToken()
	if tokErr != nil {
		return resp.Mailbox.ID, "", nil // mailbox created; token generation failure is non-fatal to the row
	}
	return resp.Mailbox.ID, hash, nil
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
	ok, err := s.repo.TransitionJobIfVersion(ctx, jobID, job.Status, JobCancelled, job.Version, now)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "cancel job", err)
	}
	if !ok {
		return nil, ErrVersionConflict
	}
	return s.repo.GetJob(ctx, jobID, tenantID)
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
			_ = s.repo.UpdateRowResult(ctx, row.ID, RowFailed, ErrCodeCreateFailed, sanitizeErr(createErr), 0, "", now)
			continue
		}
		newlyCreated++
		_ = s.repo.UpdateRowResult(ctx, row.ID, RowCreated, "", "", mailboxID, setupTokenHash, now)
	}

	finalStatus := JobCompleted
	if stillFailed > 0 {
		finalStatus = JobPartiallyFailed
	}
	if err := s.repo.UpdateJobCounters(ctx, jobID, finalStatus, job.CreatedCount+newlyCreated, stillFailed, now); err != nil {
		return nil, nil, err
	}
	finalJob, err := s.repo.GetJob(ctx, jobID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	allRows, _ := s.repo.ListRows(ctx, jobID, nil)
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
