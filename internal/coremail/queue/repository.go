package queue

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Repository defines all queue persistence operations.
type Repository interface {
	// Enqueue inserts a new queue entry.
	Enqueue(ctx context.Context, e *QueueEntry, tx interface{}) error

	// Get returns a single entry by ID.
	Get(ctx context.Context, id uint, tx interface{}) (*QueueEntry, error)

	// List returns entries matching the filter with pagination.
	List(ctx context.Context, filter QueueFilter, tx interface{}) ([]QueueEntry, int64, error)

	// LeaseNext atomically claims the next available pending or deferred job for a worker.
	// Only one worker will receive each job. Returns nil if no job is available.
	LeaseNext(ctx context.Context, owner string, leaseSeconds int, allowedStatuses []QueueStatus, tx interface{}) (*QueueEntry, error)

	// LeaseNextTenantFair claims the next available job while respecting per-tenant
	// worker ceilings. Prevents one tenant from monopolizing queue workers.
	LeaseNextTenantFair(ctx context.Context, owner string, leaseSeconds int, allowedStatuses []QueueStatus, maxPerTenant, globalMax int, tx interface{}) (*QueueEntry, error)

	// AckDelivered marks a job as successfully delivered.
	AckDelivered(ctx context.Context, id uint, tx interface{}) error

	// Defer reschedules a job for retry with exponential backoff.
	Defer(ctx context.Context, id uint, nextAttemptAt time.Time, lastError string, tx interface{}) error

	// DeferWithDiagnostics is the preferred entry
	// point for the delivery worker. It records
	// the full remote SMTP diagnostic set
	// (status code, enhanced code, host, IP, TLS
	// state) on the queue row so the admin queue
	// UI shows the exact defer reason without log
	// scraping. The legacy Defer() method remains
	// for callers that have no remote diagnostics.
	DeferWithDiagnostics(ctx context.Context, id uint, nextAttemptAt time.Time, diag DeliveryDiagnostics, tx interface{}) error

	// Bounce marks a job as permanently bounced (hard failure).
	Bounce(ctx context.Context, id uint, lastError string, tx interface{}) error

	// BounceWithDiagnostics is the preferred
	// entry point for permanent recipient
	// rejections. The iCloud bounce from
	// production (queue id=3, status=bounced)
	// is now diagnosable from the admin queue UI.
	BounceWithDiagnostics(ctx context.Context, id uint, diag DeliveryDiagnostics, tx interface{}) error

	// DeadLetter moves a job to the dead letter queue.
	DeadLetter(ctx context.Context, id uint, lastError string, tx interface{}) error

	// DeadLetterWithDiagnostics is the preferred
	// entry point for the dead-letter path. Same
	// diagnostic fields as BounceWithDiagnostics.
	DeadLetterWithDiagnostics(ctx context.Context, id uint, diag DeliveryDiagnostics, tx interface{}) error

	// Cancel marks a job as cancelled.
	Cancel(ctx context.Context, id uint, tx interface{}) error

	// RetryNow resets a dead-lettered or bounced job to pending for immediate retry.
	RetryNow(ctx context.Context, id uint, tx interface{}) error

	// AdminRetryNow performs an operator-triggered retry with a conditional
	// status transition. It must not alter leased/in-flight jobs.
	AdminRetryNow(ctx context.Context, id uint, tx interface{}) error

	// AdminDeadLetter performs an operator-triggered dead-letter transition
	// with a conditional status transition. It must not alter leased/in-flight jobs.
	AdminDeadLetter(ctx context.Context, id uint, lastError string, tx interface{}) error

	// AdminCancel performs an operator-triggered cancellation with a conditional
	// status transition. It must not alter leased/in-flight jobs.
	AdminCancel(ctx context.Context, id uint, tx interface{}) error

	// ReleaseExpiredLeases finds jobs with expired leases and returns them to pending.
	ReleaseExpiredLeases(ctx context.Context, tx interface{}) (int64, error)

	// UpdateStatus is a general-purpose status transition with optional error recording.
	UpdateStatus(ctx context.Context, id uint, status QueueStatus, lastError string, tx interface{}) error

	// ListDeadLetters lists all entries in dead letter status with pagination.
	ListDeadLetters(ctx context.Context, filter QueueFilter, tx interface{}) ([]QueueEntry, int64, error)

	// RestoreDeadLetter moves a dead letter entry back to pending.
	RestoreDeadLetter(ctx context.Context, id uint, maxAttempts int, tx interface{}) error

	// PurgeDeadLetters permanently removes dead letter entries older than the given time.
	PurgeDeadLetters(ctx context.Context, olderThan time.Time, tx interface{}) (int64, error)

	// PurgeCompleted removes completed/delivered/bounced/cancelled entries older than the given time.
	PurgeCompleted(ctx context.Context, olderThan time.Time, tx interface{}) (int64, error)

	// Metrics returns aggregate queue statistics.
	Metrics(ctx context.Context, tenantID *uint, tx interface{}) (*QueueMetrics, error)

	// CountByStatus returns the count of entries in a given status for a tenant.
	CountByStatus(ctx context.Context, status QueueStatus, tenantID *uint, tx interface{}) (int64, error)
}

// Ensure SQLRepo implements Repository at compile time.
var _ Repository = (*SQLRepo)(nil)

// SQLRepo implements Repository using database/sql.
type SQLRepo struct {
	db *sql.DB
}

func NewSQLRepo(db *sql.DB) *SQLRepo {
	return &SQLRepo{db: db}
}

func (r *SQLRepo) exec(tx interface{}) interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
} {
	if tx != nil {
		if t, ok := tx.(*sql.Tx); ok {
			return t
		}
	}
	return r.db
}

const queueCols = `id, tenant_id, domain_id, mailbox_id, message_id, from_address, to_address,
	recipient_domain, direction, status, priority, attempt_count, max_attempts,
	next_attempt_at, last_attempt_at, last_error, delivery_mode,
	remote_host, remote_ip, tls_used, last_status_code, last_enhanced_code,
	lease_owner, lease_expires_at,
	created_at, updated_at, completed_at, dead_letter_at, deleted_at`

func (r *SQLRepo) Enqueue(ctx context.Context, e *QueueEntry, tx interface{}) error {
	now := nowFn()
	e.CreatedAt = now
	e.UpdatedAt = now
	if e.Status == "" {
		e.Status = StatusPending
	}
	if e.MaxAttempts <= 0 {
		e.MaxAttempts = DefaultMaxAttempts
	}
	// Set initial next_attempt_at for pending jobs.
	if e.NextAttemptAt == nil {
		e.NextAttemptAt = &now
	}

	e2 := r.exec(tx)
	res, err := e2.ExecContext(ctx, `
		INSERT INTO coremail_queue
			(tenant_id, domain_id, mailbox_id, message_id, from_address, to_address,
			 recipient_domain, direction, status, priority, attempt_count, max_attempts,
			 next_attempt_at, last_attempt_at, last_error, delivery_mode,
			 remote_host, remote_ip, tls_used,
			 lease_owner, lease_expires_at,
			 created_at, updated_at, completed_at, dead_letter_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, 0, '', NULL, ?, ?, NULL, NULL, NULL)`,
		e.TenantID, e.DomainID, e.MailboxID, e.MessageID, e.FromAddress, e.ToAddress,
		e.RecipientDomain, string(e.Direction), string(e.Status), e.Priority, e.MaxAttempts,
		e.NextAttemptAt, e.LastAttemptAt, e.LastError, string(e.DeliveryMode),
		e.RemoteHost, e.RemoteIP,
		e.CreatedAt, e.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	e.ID = uint(id)
	return nil
}

func (r *SQLRepo) Get(ctx context.Context, id uint, tx interface{}) (*QueueEntry, error) {
	e := r.exec(tx)
	row := e.QueryRowContext(ctx, "SELECT "+queueCols+" FROM coremail_queue WHERE id=? AND deleted_at IS NULL", id)
	return scanEntry(row)
}

func (r *SQLRepo) List(ctx context.Context, filter QueueFilter, tx interface{}) ([]QueueEntry, int64, error) {
	e := r.exec(tx)
	if filter.Limit < 1 || filter.Limit > MaxPageSize {
		filter.Limit = DefaultPageSize
	}

	var where []string
	var args []interface{}
	where = append(where, "deleted_at IS NULL")

	if filter.TenantID != nil {
		where = append(where, "tenant_id=?")
		args = append(args, *filter.TenantID)
	}
	if filter.DomainID != nil {
		where = append(where, "domain_id=?")
		args = append(args, *filter.DomainID)
	}
	if filter.MailboxID != nil {
		where = append(where, "mailbox_id=?")
		args = append(args, *filter.MailboxID)
	}
	if filter.Direction != nil {
		where = append(where, "direction=?")
		args = append(args, string(*filter.Direction))
	}
	if filter.Status != nil {
		where = append(where, "status=?")
		args = append(args, string(*filter.Status))
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, s := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, string(s))
		}
		where = append(where, "status IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.DeliveryMode != nil {
		where = append(where, "delivery_mode=?")
		args = append(args, string(*filter.DeliveryMode))
	}
	if filter.RecipientDomain != "" {
		where = append(where, "recipient_domain=?")
		args = append(args, filter.RecipientDomain)
	}
	if filter.Search != "" {
		where = append(where, "(from_address LIKE ? OR to_address LIKE ?)")
		s := "%" + filter.Search + "%"
		args = append(args, s, s)
	}

	clause := strings.Join(where, " AND ")

	var total int64
	if err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_queue WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list count: %w", err)
	}

	rows, err := e.QueryContext(ctx, "SELECT "+queueCols+" FROM coremail_queue WHERE "+clause+" ORDER BY priority DESC, created_at ASC LIMIT ? OFFSET ?",
		append(args, filter.Limit, filter.Offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []QueueEntry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		entries = append(entries, *entry)
	}
	return entries, total, rows.Err()
}

func (r *SQLRepo) LeaseNext(ctx context.Context, owner string, leaseSeconds int, allowedStatuses []QueueStatus, tx interface{}) (*QueueEntry, error) {
	e := r.exec(tx)

	statusPlaceholders := make([]string, len(allowedStatuses))
	statusArgs := make([]interface{}, len(allowedStatuses))
	for i, s := range allowedStatuses {
		statusPlaceholders[i] = "?"
		statusArgs[i] = string(s)
	}

	// For the UPDATE ... LIMIT 1 pattern, we need to find the next job.
	// In SQLite, we use a subquery with ORDER BY + LIMIT 1 inside the UPDATE.
	now := nowFn()

	var entry *QueueEntry

	// Step 1: Find the next candidate inside a transaction (ensured by caller).
	// We use a two-step approach: SELECT then UPDATE where status matches.
	row := e.QueryRowContext(ctx, `
		SELECT `+queueCols+` FROM coremail_queue
		WHERE status IN (`+strings.Join(statusPlaceholders, ",")+`)
		  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		  AND deleted_at IS NULL
		ORDER BY priority DESC, next_attempt_at ASC, id ASC
		LIMIT 1`,
		append(statusArgs, now)...,
	)
	entry, err := scanEntry(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	// Step 2: Atomically claim it (only if still in allowed status).
	leaseExp := now.Add(time.Duration(leaseSeconds) * time.Second)
	updateArgs := []interface{}{
		string(StatusLeased), owner, leaseExp, now, entry.ID,
	}
	updateArgs = append(updateArgs, statusArgs...)
	res, err := e.ExecContext(ctx, `
		UPDATE coremail_queue SET
			status=?, lease_owner=?, lease_expires_at=?, attempt_count=attempt_count+1, updated_at=?
		WHERE id=? AND status IN (`+strings.Join(statusPlaceholders, ",")+`) AND deleted_at IS NULL`,
		updateArgs...,
	)
	if err != nil {
		return nil, err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return nil, nil // someone else got it
	}

	entry.Status = StatusLeased
	entry.LeaseOwner = owner
	entry.LeaseExpiresAt = &leaseExp
	entry.AttemptCount++
	entry.UpdatedAt = now
	return entry, nil
}

// LeaseNextTenantFair claims the next available job while respecting per-tenant
// worker ceilings. Prevents one tenant from monopolizing the queue.
func (r *SQLRepo) LeaseNextTenantFair(ctx context.Context, owner string, leaseSeconds int, allowedStatuses []QueueStatus, maxPerTenant, globalMax int, tx interface{}) (*QueueEntry, error) {
	e := r.exec(tx)

	// Check global ceiling.
	if globalMax > 0 {
		var active int
		e.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM coremail_queue WHERE status='leased' AND lease_expires_at>datetime('now') AND deleted_at IS NULL").Scan(&active)
		if active >= globalMax {
			return nil, nil
		}
	}

	statusPlaceholders := make([]string, len(allowedStatuses))
	statusArgs := make([]interface{}, len(allowedStatuses))
	for i, s := range allowedStatuses {
		statusPlaceholders[i] = "?"
		statusArgs[i] = string(s)
	}

	// Find least-loaded tenant with pending work (tenant fairness).
	var chosenTenant *uint
	if maxPerTenant > 0 {
		candidates, err := e.QueryContext(ctx,
			`SELECT q.tenant_id, COUNT(q2.id) as active
			FROM coremail_queue q
			LEFT JOIN coremail_queue q2 ON q2.tenant_id = q.tenant_id AND q2.status='leased' AND q2.lease_expires_at>datetime('now') AND q2.deleted_at IS NULL
			WHERE q.status IN (`+strings.Join(statusPlaceholders, ",")+`) AND q.deleted_at IS NULL
			GROUP BY q.tenant_id
			ORDER BY active ASC LIMIT 1`, statusArgs...)
		if err == nil {
			if candidates.Next() {
				var tid uint
				var activeCount int
				candidates.Scan(&tid, &activeCount)
				if activeCount < maxPerTenant {
					chosenTenant = &tid
				}
			}
			candidates.Close()
		}
	}

	// Build WHERE clause.
	now := nowFn()
	whereStatus := "status IN (" + strings.Join(statusPlaceholders, ",") + ")"
	whereArgs := append([]interface{}{}, statusArgs...)

	if chosenTenant != nil {
		whereStatus += " AND tenant_id = ?"
		whereArgs = append(whereArgs, *chosenTenant)
	}

	// Step 1: SELECT candidate.
	row := e.QueryRowContext(ctx,
		`SELECT `+queueCols+` FROM coremail_queue
		WHERE `+whereStatus+` AND (next_attempt_at IS NULL OR next_attempt_at <= ?) AND deleted_at IS NULL
		ORDER BY priority DESC, next_attempt_at ASC, id ASC LIMIT 1`,
		append(whereArgs, now)...,
	)
	entry, err := scanEntry(row)
	if err != nil || entry == nil {
		return nil, nil
	}

	// Step 2: atomic claim.
	leaseExp := now.Add(time.Duration(leaseSeconds) * time.Second)
	claimArgs := []interface{}{StatusLeased, owner, leaseExp, now, entry.ID}
	claimArgs = append(claimArgs, statusArgs...)
	res, err := e.ExecContext(ctx,
		`UPDATE coremail_queue SET status=?, lease_owner=?, lease_expires_at=?, attempt_count=attempt_count+1, updated_at=?
		WHERE id=? AND status IN (`+strings.Join(statusPlaceholders, ",")+`) AND deleted_at IS NULL`,
		claimArgs...,
	)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}

	entry.Status = StatusLeased
	entry.LeaseOwner = owner
	entry.LeaseExpiresAt = &leaseExp
	entry.AttemptCount++
	entry.UpdatedAt = now
	return entry, nil
}

func (r *SQLRepo) AckDelivered(ctx context.Context, id uint, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, completed_at=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		string(StatusDelivered), now, now, id)
	return err
}

func (r *SQLRepo) Defer(ctx context.Context, id uint, nextAttemptAt time.Time, lastError string, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, next_attempt_at=?, last_attempt_at=?, last_error=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		string(StatusDeferred), nextAttemptAt, now, lastError, now, id)
	return err
}

// DeferWithDiagnostics records a defer with the
// full remote SMTP diagnostic set so the operator
// can see the exact reason in the admin UI
// without grepping the worker log. The legacy
// Defer() method remains for callers that have no
// diagnostics to record (e.g. local delivery
// failures with no remote host).
func (r *SQLRepo) DeferWithDiagnostics(ctx context.Context, id uint, nextAttemptAt time.Time, diag DeliveryDiagnostics, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET
			status=?, next_attempt_at=?, last_attempt_at=?,
			last_error=?, last_status_code=?, last_enhanced_code=?,
			remote_host=?, remote_ip=?, tls_used=?,
			updated_at=?
		WHERE id=? AND deleted_at IS NULL`,
		string(StatusDeferred), nextAttemptAt, now,
		diag.LastError, diag.StatusCode, diag.EnhancedCode,
		diag.RemoteHost, diag.RemoteIP, boolToInt(diag.TLSUsed),
		now, id)
	return err
}

func (r *SQLRepo) Bounce(ctx context.Context, id uint, lastError string, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, completed_at=?, last_error=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		string(StatusBounced), now, lastError, now, id)
	return err
}

// BounceWithDiagnostics records a permanent
// recipient rejection with the full remote SMTP
// diagnostic set. The iCloud bounce case from
// production (queue id=3, status=bounced,
// attempt_count=1) was previously stored with
// only last_error — the operator had to cross-
// reference logs to see the SMTP code, the remote
// MX, and the TLS state. This method stores the
// full set so the admin queue UI can show
// "550 5.1.1 from mx-in-001.icloud.com (17.57.x.x,
// TLS) — invalid recipient" without any log
// scraping.
func (r *SQLRepo) BounceWithDiagnostics(ctx context.Context, id uint, diag DeliveryDiagnostics, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET
			status=?, completed_at=?, last_error=?,
			last_status_code=?, last_enhanced_code=?,
			remote_host=?, remote_ip=?, tls_used=?,
			updated_at=?
		WHERE id=? AND deleted_at IS NULL`,
		string(StatusBounced), now,
		diag.LastError, diag.StatusCode, diag.EnhancedCode,
		diag.RemoteHost, diag.RemoteIP, boolToInt(diag.TLSUsed),
		now, id)
	return err
}

func (r *SQLRepo) DeadLetter(ctx context.Context, id uint, lastError string, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, dead_letter_at=?, last_error=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		string(StatusDeadLetter), now, lastError, now, id)
	return err
}

// DeadLetterWithDiagnostics records a permanent
// dead-letter with the full remote SMTP
// diagnostic set. The diagnostics are the same
// fields as BounceWithDiagnostics — we keep a
// separate method so the dead-letter path can be
// distinguished from the bounce path in the
// admin UI without parsing the status code.
func (r *SQLRepo) DeadLetterWithDiagnostics(ctx context.Context, id uint, diag DeliveryDiagnostics, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET
			status=?, dead_letter_at=?, last_error=?,
			last_status_code=?, last_enhanced_code=?,
			remote_host=?, remote_ip=?, tls_used=?,
			updated_at=?
		WHERE id=? AND deleted_at IS NULL`,
		string(StatusDeadLetter), now,
		diag.LastError, diag.StatusCode, diag.EnhancedCode,
		diag.RemoteHost, diag.RemoteIP, boolToInt(diag.TLSUsed),
		now, id)
	return err
}

func (r *SQLRepo) Cancel(ctx context.Context, id uint, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, completed_at=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		string(StatusCancelled), now, now, id)
	return err
}

func (r *SQLRepo) RetryNow(ctx context.Context, id uint, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, next_attempt_at=?, attempt_count=0, dead_letter_at=NULL, last_error='', updated_at=? WHERE id=? AND deleted_at IS NULL`,
		string(StatusPending), now, now, id)
	return err
}

func (r *SQLRepo) AdminRetryNow(ctx context.Context, id uint, tx interface{}) error {
	now := nowFn()
	return r.transitionStatus(ctx, id, []QueueStatus{StatusDeferred, StatusBounced, StatusDeadLetter}, tx,
		`UPDATE coremail_queue SET status=?, next_attempt_at=?, attempt_count=0, dead_letter_at=NULL, last_error='', updated_at=?
		 WHERE id=? AND deleted_at IS NULL AND status IN (?, ?, ?)`,
		string(StatusPending), now, now, id,
		string(StatusDeferred), string(StatusBounced), string(StatusDeadLetter))
}

func (r *SQLRepo) AdminDeadLetter(ctx context.Context, id uint, lastError string, tx interface{}) error {
	now := nowFn()
	return r.transitionStatus(ctx, id, []QueueStatus{StatusPending, StatusDeferred, StatusBounced}, tx,
		`UPDATE coremail_queue SET status=?, dead_letter_at=?, last_error=?, updated_at=?
		 WHERE id=? AND deleted_at IS NULL AND status IN (?, ?, ?)`,
		string(StatusDeadLetter), now, lastError, now, id,
		string(StatusPending), string(StatusDeferred), string(StatusBounced))
}

func (r *SQLRepo) AdminCancel(ctx context.Context, id uint, tx interface{}) error {
	now := nowFn()
	return r.transitionStatus(ctx, id, []QueueStatus{StatusPending, StatusDeferred}, tx,
		`UPDATE coremail_queue SET status=?, completed_at=?, updated_at=?
		 WHERE id=? AND deleted_at IS NULL AND status IN (?, ?)`,
		string(StatusCancelled), now, now, id,
		string(StatusPending), string(StatusDeferred))
}

func (r *SQLRepo) transitionStatus(ctx context.Context, id uint, allowed []QueueStatus, tx interface{}, query string, args ...interface{}) error {
	e := r.exec(tx)
	res, err := e.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var current string
	if err := e.QueryRowContext(ctx, `SELECT status FROM coremail_queue WHERE id=? AND deleted_at IS NULL`, id).Scan(&current); err != nil {
		return fmt.Errorf("queue entry %d not found", id)
	}
	return fmt.Errorf("queue entry %d is in status %q; allowed statuses: %v", id, current, allowed)
}

// DeliveryDiagnostics is the structured set of
// fields the delivery worker captures when a
// remote SMTP attempt completes. The fields are
// the operator's "why was this row deferred /
// bounced" answer: the exact SMTP code, the
// enhanced status code, the remote host, the IP,
// and the TLS state. The Admin queue UI surfaces
// these verbatim so the operator can diagnose
// deliverability issues without grepping the
// worker log.
//
// DeliveryDiagnostics is intentionally separate
// from delivery.DeliveryResult — the queue
// package must not import the delivery package
// (it would create an import cycle since delivery
// imports queue). The conversion is done in
// delivery.worker.deliver.
type DeliveryDiagnostics struct {
	LastError    string
	StatusCode   int
	EnhancedCode string
	RemoteHost   string
	RemoteIP     string
	TLSUsed      bool
}

// DeliveryDiagnosticsFromResult is a convenience
// constructor for the conversion. Defined in
// the delivery package to avoid the import
// cycle.

// boolToInt converts a Go bool to the 0/1 form
// SQLite stores in INTEGER columns. We could use
// a bool column directly (modernc.org/sqlite
// supports it), but every other boolean column
// in coremail_queue uses 0/1 — keeping the
// convention.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *SQLRepo) ReleaseExpiredLeases(ctx context.Context, tx interface{}) (int64, error) {
	now := nowFn()
	e := r.exec(tx)
	res, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, lease_owner='', lease_expires_at=NULL, updated_at=? WHERE status=? AND lease_expires_at < ? AND deleted_at IS NULL`,
		string(StatusPending), now, string(StatusLeased), now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *SQLRepo) UpdateStatus(ctx context.Context, id uint, status QueueStatus, lastError string, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, last_error=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		string(status), lastError, now, id)
	return err
}

func (r *SQLRepo) ListDeadLetters(ctx context.Context, filter QueueFilter, tx interface{}) ([]QueueEntry, int64, error) {
	filter.Status = statusPtr(StatusDeadLetter)
	return r.List(ctx, filter, nil)
}

func (r *SQLRepo) RestoreDeadLetter(ctx context.Context, id uint, maxAttempts int, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	_, err := e.ExecContext(ctx, `UPDATE coremail_queue SET status=?, max_attempts=?, attempt_count=0, dead_letter_at=NULL, last_error='', next_attempt_at=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		string(StatusPending), maxAttempts, now, now, id)
	return err
}

func (r *SQLRepo) PurgeDeadLetters(ctx context.Context, olderThan time.Time, tx interface{}) (int64, error) {
	e := r.exec(tx)
	res, err := e.ExecContext(ctx, `DELETE FROM coremail_queue WHERE status=? AND dead_letter_at < ?`, string(StatusDeadLetter), olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *SQLRepo) PurgeCompleted(ctx context.Context, olderThan time.Time, tx interface{}) (int64, error) {
	e := r.exec(tx)
	res, err := e.ExecContext(ctx, `DELETE FROM coremail_queue WHERE status IN (?,?,?,?) AND completed_at < ?`,
		string(StatusDelivered), string(StatusBounced), string(StatusCancelled), string(StatusDeadLetter), olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *SQLRepo) Metrics(ctx context.Context, tenantID *uint, tx interface{}) (*QueueMetrics, error) {
	e := r.exec(tx)
	m := &QueueMetrics{}

	var where string
	var args []interface{}
	if tenantID != nil {
		where = "WHERE tenant_id=? AND deleted_at IS NULL"
		args = append(args, *tenantID)
	} else {
		where = "WHERE deleted_at IS NULL"
	}

	// Status counts.
	var oldestPendingStr sql.NullString
	row := e.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='leased' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='delivering' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='deferred' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='delivered' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='bounced' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='dead_letter' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='cancelled' THEN 1 ELSE 0 END), 0),
			COUNT(*),
			COALESCE(AVG(CAST(attempt_count AS REAL)), 0),
			MIN(CASE WHEN status='pending' THEN created_at ELSE NULL END)
		FROM coremail_queue `+where, args...)

	err := row.Scan(&m.Pending, &m.Leased, &m.Delivering, &m.Deferred,
		&m.Delivered, &m.Bounced, &m.DeadLetter, &m.Cancelled,
		&m.Total, &m.AvgAttempts, &oldestPendingStr)
	if oldestPendingStr.Valid && oldestPendingStr.String != "" {
		t, parseErr := time.Parse("2006-01-02 15:04:05", oldestPendingStr.String)
		if parseErr == nil {
			m.OldestPending = &t
		}
	}
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	return m, nil
}

func (r *SQLRepo) CountByStatus(ctx context.Context, status QueueStatus, tenantID *uint, tx interface{}) (int64, error) {
	e := r.exec(tx)
	var count int64
	var err error
	if tenantID != nil {
		err = e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_queue WHERE status=? AND tenant_id=? AND deleted_at IS NULL", string(status), *tenantID).Scan(&count)
	} else {
		err = e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_queue WHERE status=? AND deleted_at IS NULL", string(status)).Scan(&count)
	}
	return count, err
}

// ── Helper functions ─────────────────────────────────────────

func statusPtr(s QueueStatus) *QueueStatus {
	return &s
}

func scanEntry(row interface {
	Scan(dest ...interface{}) error
}) (*QueueEntry, error) {
	var e QueueEntry
	var direction, status, deliveryMode string
	var tlsUsed int
	err := row.Scan(
		&e.ID, &e.TenantID, &e.DomainID, &e.MailboxID, &e.MessageID, &e.FromAddress, &e.ToAddress,
		&e.RecipientDomain, &direction, &status, &e.Priority, &e.AttemptCount, &e.MaxAttempts,
		&e.NextAttemptAt, &e.LastAttemptAt, &e.LastError, &deliveryMode,
		&e.RemoteHost, &e.RemoteIP, &tlsUsed,
		&e.LastStatusCode, &e.LastEnhancedCode,
		&e.LeaseOwner, &e.LeaseExpiresAt,
		&e.CreatedAt, &e.UpdatedAt, &e.CompletedAt, &e.DeadLetterAt, &e.DeletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan queue entry: %w", err)
	}
	e.Direction = Direction(direction)
	e.Status = QueueStatus(status)
	e.DeliveryMode = DeliveryMode(deliveryMode)
	e.TLSUsed = tlsUsed == 1
	return &e, nil
}
