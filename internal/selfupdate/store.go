// This file implements the persistent job/event/rollback-snapshot store for
// the Admin Console self-update feature (Phase D). It follows the same
// dbdialect-based, raw database/sql convention used elsewhere in this repo
// (see internal/billing/setup.go) so the same schema and code work
// unmodified against both SQLite (single-node/dev deployments) and
// PostgreSQL (multi-node deployments), without an ORM.
package selfupdate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Store is the persistence boundary for update jobs, their event history,
// and rollback snapshots. All methods are safe for concurrent use from
// multiple goroutines (and, for PostgreSQL, multiple daemon processes)
// backed by the same database.
type Store interface {
	// CreateJob creates a new job, or — if idempotencyKey matches an
	// existing job that is still active (non-terminal) or already
	// completed — returns that EXISTING job instead of creating a new one.
	// CreateJob also enforces that at most one job is active
	// (non-terminal) at any time: if a different active job already
	// exists, ErrJobAlreadyActive is returned.
	CreateJob(job Job) (Job, error)

	// GetJob returns the job with the given ID.
	GetJob(id string) (Job, error)

	// GetActiveJob returns the current non-terminal job, if any. It
	// returns ErrNoActiveJob if there is none.
	GetActiveJob() (Job, error)

	// UpdateJobPhase transitionally moves job id to newPhase, updating
	// progressPercent and message atomically along with the phase. It
	// rejects the transition (ErrInvalidPhaseTransition) if newPhase is
	// not a legal successor of the job's current phase.
	UpdateJobPhase(id string, newPhase Phase, progressPercent int, message string) (Job, error)

	// AppendEvent appends an immutable, strictly-ordered event to a job's
	// history. Seq is assigned by the store and is always
	// previous-max-seq+1 for that job.
	AppendEvent(jobID string, phase Phase, message string) (Event, error)

	// ListEvents returns all events for jobID in ascending seq order.
	ListEvents(jobID string) ([]Event, error)

	// ListJobs returns job history, most recently created first.
	ListJobs(limit int) ([]Job, error)

	// CreateSnapshot records a new rollback snapshot.
	CreateSnapshot(snap RollbackSnapshot) (RollbackSnapshot, error)

	// ListSnapshots returns all rollback snapshots, most recently created
	// first.
	ListSnapshots() ([]RollbackSnapshot, error)

	// MarkLastKnownGood marks snapshot id as the last-known-good snapshot,
	// clearing that flag from any other snapshot.
	MarkLastKnownGood(id string) error

	// RecoverActiveJob is called once at daemon startup. It returns the
	// job left in a non-terminal phase by an unclean shutdown, if any, so
	// the caller can decide whether to resume, fail, or roll it back. It
	// never silently discards such a job.
	RecoverActiveJob() (Job, bool, error)
}

var (
	// ErrJobAlreadyActive is returned by CreateJob when a different job is
	// already in a non-terminal phase.
	ErrJobAlreadyActive = errors.New("selfupdate: a job is already active")
	// ErrNoActiveJob is returned by GetActiveJob when no job is currently
	// non-terminal.
	ErrNoActiveJob = errors.New("selfupdate: no active job")
	// ErrJobNotFound is returned when a job ID does not exist.
	ErrJobNotFound = errors.New("selfupdate: job not found")
	// ErrSnapshotNotFound is returned when a snapshot ID does not exist.
	ErrSnapshotNotFound = errors.New("selfupdate: snapshot not found")
	// ErrInvalidPhaseTransition is returned by UpdateJobPhase when newPhase
	// is not a legal successor of the job's current phase.
	ErrInvalidPhaseTransition = errors.New("selfupdate: invalid phase transition")
)

// legalPhaseTransitions enumerates the allowed forward moves for the phase
// state machine. A phase not present as a key (i.e. any terminal phase) has
// no legal successor except where explicitly listed (e.g. a failed install
// may transition into rolling_back).
var legalPhaseTransitions = map[Phase][]Phase{
	PhaseQueued:           {PhaseChecking, PhaseFailed},
	PhaseChecking:         {PhaseDownloading, PhaseFailed},
	PhaseDownloading:      {PhaseVerifying, PhaseFailed},
	PhaseVerifying:        {PhasePreflight, PhaseFailed},
	PhasePreflight:        {PhaseBackingUp, PhaseFailed},
	PhaseBackingUp:        {PhaseStoppingService, PhaseFailed},
	PhaseStoppingService:  {PhaseMigrating, PhaseFailed},
	PhaseMigrating:        {PhaseReplacingRuntime, PhaseFailed},
	PhaseReplacingRuntime: {PhaseRestarting, PhaseFailed},
	PhaseRestarting:       {PhaseHealthCheck, PhaseFailed},
	// HealthCheck -> RolledBack is legal for a JobKindRollback job's own
	// state machine (Orchestrator.StartRollback): a manual rollback job
	// walks queued -> ... -> health_check and its own terminal success
	// state is "rolled_back", not "completed" (that phase name is
	// reserved for a successful install). An install job never takes
	// this edge — it always resolves health_check into completed or
	// failed; only failed->rolling_back->health_check (via
	// autoRollback's own restore+health-gate call, which does not
	// re-enter this transition map) is used for automatic rollback.
	PhaseHealthCheck: {PhaseCompleted, PhaseFailed, PhaseRolledBack},
	PhaseFailed:      {PhaseRollingBack},
	PhaseRollingBack: {PhaseRolledBack, PhaseFailed},
}

// isLegalPhaseTransition reports whether moving from -> to is allowed.
// Staying on the same phase (a pure progress-percent/message update) is
// always allowed.
func isLegalPhaseTransition(from, to Phase) bool {
	if from == to {
		return true
	}
	for _, next := range legalPhaseTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// sqlStore is the Store implementation shared by SQLite and PostgreSQL. The
// only per-dialect difference is SQL text (placeholders, timestamp/boolean
// types, row-locking clause), all resolved through dbdialect.Info — the
// same convention internal/billing uses.
//
// Locking strategy:
//   - PostgreSQL: the "at most one active job" check-then-insert and the
//     idempotency-key replay lookup both run inside a transaction that
//     takes `SELECT ... FOR UPDATE` on the relevant row(s), so a second,
//     concurrent transaction blocks until the first commits/rolls back
//     rather than racing past the check.
//   - SQLite: modernc.org/sqlite serializes writers at the connection
//     level, but the default BEGIN DEFERRED can still let two goroutines
//     both pass a SELECT check before either writes. This store instead
//     opens SQLite write transactions with `BEGIN IMMEDIATE` (see
//     beginWriteTx), which acquires SQLite's RESERVED lock up front —
//     the second concurrent transaction blocks at BEGIN IMMEDIATE itself
//     (or fails fast with SQLITE_BUSY, retried by the busy_timeout PRAGMA
//     set in CreateTables) instead of at COMMIT, giving the same
//     check-then-insert safety as Postgres's row lock without requiring a
//     partial/expression unique index that SQLite's older grammar may not
//     support identically to Postgres.
type sqlStore struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewStore returns a Store backed by db, auto-detecting the SQL dialect.
// Callers must call CreateTables(db) once before first use (typically from
// the same initialization path that opens db).
func NewStore(db *sql.DB) (Store, error) {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &sqlStore{db: db, dialect: dialect}, nil
}

// CreateTables creates the update_jobs, update_events, and
// rollback_snapshots tables (and their indexes) if they do not already
// exist. Safe to call on every process startup.
func CreateTables(db *sql.DB) error {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	autoInc := dialect.AutoIncrement()
	ts := dialect.TimestampType()

	ddl := func(sqlText string) string {
		sqlText = strings.ReplaceAll(sqlText, "__AUTOINC__", autoInc)
		sqlText = strings.ReplaceAll(sqlText, "__TS__", ts)
		return sqlText
	}

	templates := []string{
		`CREATE TABLE IF NOT EXISTS update_jobs (
			row_id __AUTOINC__,
			id TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			requested_version TEXT NOT NULL DEFAULT '',
			initiated_by TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL,
			progress_percent INTEGER NOT NULL DEFAULT 0,
			created_at __TS__ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at __TS__ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			artifact_sha256 TEXT NOT NULL DEFAULT '',
			artifact_version TEXT NOT NULL DEFAULT '',
			artifact_commit TEXT NOT NULL DEFAULT '',
			rollback_snapshot_id TEXT NOT NULL DEFAULT '',
			failure_code TEXT NOT NULL DEFAULT '',
			failure_message TEXT NOT NULL DEFAULT '',
			rollback_result TEXT NOT NULL DEFAULT '',
			is_terminal INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS update_events (
			row_id __AUTOINC__,
			job_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			at __TS__ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			phase TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			UNIQUE(job_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS rollback_snapshots (
			row_id __AUTOINC__,
			id TEXT NOT NULL UNIQUE,
			source_version TEXT NOT NULL DEFAULT '',
			source_commit TEXT NOT NULL DEFAULT '',
			checksum_sha256 TEXT NOT NULL DEFAULT '',
			verified INTEGER NOT NULL DEFAULT 0,
			created_at __TS__ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_known_good INTEGER NOT NULL DEFAULT 0,
			retained INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, t := range templates {
		if _, err := db.Exec(ddl(t)); err != nil {
			return fmt.Errorf("selfupdate: create table: %w", err)
		}
	}

	// Idempotency-key uniqueness is only meaningful while a job is active
	// or completed — nothing in the requirements says a *failed* job's key
	// can never be retried, so the unique index covers all rows (an
	// idempotency key is the caller's promise of "this exact request",
	// and reusing it after a terminal failure would otherwise silently
	// resurrect a stale row). CreateJob's own logic decides whether a
	// match on this index should be returned as-is or is eligible for a
	// fresh attempt.
	idxStatements := []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_update_jobs_idempotency_key ON update_jobs(idempotency_key)",
		"CREATE INDEX IF NOT EXISTS idx_update_jobs_created_at ON update_jobs(created_at)",
		"CREATE INDEX IF NOT EXISTS idx_update_events_job_id ON update_events(job_id)",
		"CREATE INDEX IF NOT EXISTS idx_rollback_snapshots_created_at ON rollback_snapshots(created_at)",
	}
	if dialect.IsPostgres() {
		// Partial unique index: PostgreSQL supports WHERE on a unique
		// index, so "at most one active (non-terminal) job" is enforced
		// by the database itself, not just by application logic.
		idxStatements = append(idxStatements,
			"CREATE UNIQUE INDEX IF NOT EXISTS idx_update_jobs_one_active ON update_jobs((1)) WHERE is_terminal = 0")
	}
	// SQLite (as shipped via modernc.org/sqlite, and standard SQLite in
	// general) supports partial indexes with the same WHERE syntax, so
	// the same statement works there too — but the id column differs in
	// nothing dialect-specific, so we can share the statement outright
	// for SQLite by just running it unconditionally when not Postgres.
	if !dialect.IsPostgres() {
		idxStatements = append(idxStatements,
			"CREATE UNIQUE INDEX IF NOT EXISTS idx_update_jobs_one_active ON update_jobs((1)) WHERE is_terminal = 0")
	}
	for _, stmt := range idxStatements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("selfupdate: create index: %w", err)
		}
	}

	if !dialect.IsPostgres() {
		// PRAGMA busy_timeout only affects the single connection that
		// executes it, not the whole database/sql pool — a second pooled
		// connection opened later would still fail fast with
		// SQLITE_BUSY instead of waiting. Since every access in this
		// package (reads via s.db, writes via the dedicated BEGIN
		// IMMEDIATE connection in beginWriteTx) needs to observe a
		// single consistent lock state anyway, the simplest correct fix
		// is to cap the pool at exactly one connection: database/sql
		// then queues any concurrent caller until that connection is
		// free, so two writers are naturally serialized before either
		// ever reaches SQLite's own locking, and busy_timeout becomes
		// unnecessary defense-in-depth rather than the primary control.
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
			return fmt.Errorf("selfupdate: set busy_timeout: %w", err)
		}
	}

	return nil
}

// wtx is the minimal transaction surface store methods need. *sql.Tx
// already satisfies it directly (used for PostgreSQL). For SQLite,
// sqliteImmediateTx below implements it on top of a dedicated *sql.Conn so
// that the transaction can be opened with a raw `BEGIN IMMEDIATE`
// statement, which database/sql's db.Begin has no portable way to request.
type wtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
	Commit() error
	Rollback() error
}

// sqliteImmediateTx wraps a single dedicated *sql.Conn opened with `BEGIN
// IMMEDIATE`, which acquires SQLite's RESERVED lock immediately rather than
// on first write. This closes the race a plain deferred transaction would
// have: two goroutines could otherwise both execute their check SELECT
// (e.g. "is there an active job") before either has written, and both
// proceed to insert. With BEGIN IMMEDIATE, the second goroutine's
// transaction blocks (retried via the busy_timeout PRAGMA set in
// CreateTables) until the first commits or rolls back, giving the same
// check-then-insert safety PostgreSQL gets from `SELECT ... FOR UPDATE`.
type sqliteImmediateTx struct {
	ctx  context.Context
	conn *sql.Conn
	done bool
}

func (t *sqliteImmediateTx) Exec(query string, args ...any) (sql.Result, error) {
	return t.conn.ExecContext(t.ctx, query, args...)
}
func (t *sqliteImmediateTx) QueryRow(query string, args ...any) *sql.Row {
	return t.conn.QueryRowContext(t.ctx, query, args...)
}
func (t *sqliteImmediateTx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.conn.QueryContext(t.ctx, query, args...)
}
func (t *sqliteImmediateTx) Commit() error {
	if t.done {
		return nil
	}
	t.done = true
	_, err := t.conn.ExecContext(t.ctx, "COMMIT")
	closeErr := t.conn.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (t *sqliteImmediateTx) Rollback() error {
	if t.done {
		return nil
	}
	t.done = true
	_, err := t.conn.ExecContext(t.ctx, "ROLLBACK")
	closeErr := t.conn.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// beginWriteTx opens a transaction suitable for a check-then-write
// operation. See the sqlStore and sqliteImmediateTx doc comments for the
// per-dialect locking strategy.
func (s *sqlStore) beginWriteTx() (wtx, error) {
	if s.dialect.IsPostgres() {
		return s.db.Begin()
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		return nil, err
	}
	return &sqliteImmediateTx{ctx: ctx, conn: conn}, nil
}

func (s *sqlStore) now() time.Time { return time.Now().UTC() }

func newID(prefix string) string {
	return prefix + "_" + uuid.NewString()
}

// CreateJob implements Store.
func (s *sqlStore) CreateJob(job Job) (Job, error) {
	if job.IdempotencyKey == "" {
		return Job{}, errors.New("selfupdate: idempotency key is required")
	}
	if job.ID == "" {
		job.ID = newID("job")
	}
	if job.Phase == "" {
		job.Phase = PhaseQueued
	}

	tx, err := s.beginWriteTx()
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	// 1. Idempotency-key replay: if a job with this key already exists
	// and is either active or completed, return it as-is rather than
	// creating a duplicate. Lock the row (Postgres) so a concurrent
	// CreateJob with the same key cannot also pass this check before
	// either commits.
	existing, found, err := s.findByIdempotencyKeyLocked(tx, job.IdempotencyKey)
	if err != nil {
		return Job{}, err
	}
	if found {
		if !existing.Phase.Terminal() || existing.Phase == PhaseCompleted || existing.Phase == PhaseRolledBack {
			return existing, nil
		}
		// A prior attempt with this key failed terminally (Failed) —
		// allow a fresh attempt by falling through, but keep the same
		// idempotency key value replaced is not possible under the
		// UNIQUE index, so surface a clear error instead of a confusing
		// constraint violation.
		return Job{}, fmt.Errorf("selfupdate: idempotency key %q was already used by failed job %s; use a new key to retry", job.IdempotencyKey, existing.ID)
	}

	// 2. At most one active (non-terminal) job. Lock any existing active
	// row so a concurrent CreateJob cannot also pass this check.
	activeExists, err := s.activeJobExistsLocked(tx)
	if err != nil {
		return Job{}, err
	}
	if activeExists {
		return Job{}, ErrJobAlreadyActive
	}

	now := s.now()
	job.CreatedAt = now
	job.UpdatedAt = now
	isTerminal := 0
	if job.Phase.Terminal() {
		isTerminal = 1
	}

	insertSQL := s.dialect.Rewrite(`INSERT INTO update_jobs
		(id, kind, idempotency_key, requested_version, initiated_by, phase, progress_percent,
		 created_at, updated_at, artifact_sha256, artifact_version, artifact_commit,
		 rollback_snapshot_id, failure_code, failure_message, rollback_result, is_terminal)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err = tx.Exec(insertSQL,
		job.ID, string(job.Kind), job.IdempotencyKey, job.RequestedVersion, job.InitiatedBy,
		string(job.Phase), job.ProgressPercent, job.CreatedAt, job.UpdatedAt,
		job.ArtifactSHA256, job.ArtifactVersion, job.ArtifactCommit,
		job.RollbackSnapshot, job.FailureCode, job.FailureMessage, job.RollbackResult, isTerminal)
	if err != nil {
		if isUniqueViolation(err) {
			// Lost a race despite the lock (e.g. Postgres unique-index
			// violation surfacing at INSERT time rather than at the
			// FOR-UPDATE SELECT, which can happen if the conflicting row
			// didn't exist yet when we took our lock). Treat exactly like
			// the found-by-idempotency-key path above.
			existing, found, ferr := s.findByIdempotencyKeyLocked(tx, job.IdempotencyKey)
			if ferr == nil && found {
				tx.Rollback()
				return existing, nil
			}
		}
		return Job{}, fmt.Errorf("selfupdate: insert job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *sqlStore) findByIdempotencyKeyLocked(tx wtx, key string) (Job, bool, error) {
	q := `SELECT id, kind, idempotency_key, requested_version, initiated_by, phase, progress_percent,
		created_at, updated_at, artifact_sha256, artifact_version, artifact_commit,
		rollback_snapshot_id, failure_code, failure_message, rollback_result
		FROM update_jobs WHERE idempotency_key = ?`
	q = s.lockClause(q)
	row := tx.QueryRow(s.dialect.Rewrite(q), key)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

func (s *sqlStore) activeJobExistsLocked(tx wtx) (bool, error) {
	q := `SELECT id, kind, idempotency_key, requested_version, initiated_by, phase, progress_percent,
		created_at, updated_at, artifact_sha256, artifact_version, artifact_commit,
		rollback_snapshot_id, failure_code, failure_message, rollback_result
		FROM update_jobs WHERE is_terminal = 0`
	q = s.lockClause(q)
	row := tx.QueryRow(s.dialect.Rewrite(q))
	_, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// lockClause appends `FOR UPDATE` on PostgreSQL. SQLite has no row-level
// locking clause; its concurrency safety instead comes from beginWriteTx's
// BEGIN IMMEDIATE (documented on sqlStore).
func (s *sqlStore) lockClause(q string) string {
	if s.dialect.IsPostgres() {
		return q + " FOR UPDATE"
	}
	return q
}

func scanJob(row *sql.Row) (Job, error) {
	var j Job
	var kind, phase string
	if err := row.Scan(&j.ID, &kind, &j.IdempotencyKey, &j.RequestedVersion, &j.InitiatedBy,
		&phase, &j.ProgressPercent, &j.CreatedAt, &j.UpdatedAt, &j.ArtifactSHA256, &j.ArtifactVersion,
		&j.ArtifactCommit, &j.RollbackSnapshot, &j.FailureCode, &j.FailureMessage, &j.RollbackResult); err != nil {
		return Job{}, err
	}
	j.Kind = JobKind(kind)
	j.Phase = Phase(phase)
	j.CreatedAt = j.CreatedAt.UTC()
	j.UpdatedAt = j.UpdatedAt.UTC()
	return j, nil
}

func scanJobRows(rows *sql.Rows) (Job, error) {
	var j Job
	var kind, phase string
	if err := rows.Scan(&j.ID, &kind, &j.IdempotencyKey, &j.RequestedVersion, &j.InitiatedBy,
		&phase, &j.ProgressPercent, &j.CreatedAt, &j.UpdatedAt, &j.ArtifactSHA256, &j.ArtifactVersion,
		&j.ArtifactCommit, &j.RollbackSnapshot, &j.FailureCode, &j.FailureMessage, &j.RollbackResult); err != nil {
		return Job{}, err
	}
	j.Kind = JobKind(kind)
	j.Phase = Phase(phase)
	j.CreatedAt = j.CreatedAt.UTC()
	j.UpdatedAt = j.UpdatedAt.UTC()
	return j, nil
}

// GetJob implements Store.
func (s *sqlStore) GetJob(id string) (Job, error) {
	q := s.dialect.Rewrite(`SELECT id, kind, idempotency_key, requested_version, initiated_by, phase, progress_percent,
		created_at, updated_at, artifact_sha256, artifact_version, artifact_commit,
		rollback_snapshot_id, failure_code, failure_message, rollback_result
		FROM update_jobs WHERE id = ?`)
	row := s.db.QueryRow(q, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

// GetActiveJob implements Store.
func (s *sqlStore) GetActiveJob() (Job, error) {
	q := s.dialect.Rewrite(`SELECT id, kind, idempotency_key, requested_version, initiated_by, phase, progress_percent,
		created_at, updated_at, artifact_sha256, artifact_version, artifact_commit,
		rollback_snapshot_id, failure_code, failure_message, rollback_result
		FROM update_jobs WHERE is_terminal = 0 ORDER BY created_at DESC`)
	row := s.db.QueryRow(q)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNoActiveJob
	}
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

// RecoverActiveJob implements Store. It is functionally the same query as
// GetActiveJob but named separately per the interface contract (called
// once, at startup, to make crash recovery an explicit, auditable step
// rather than an implicit side effect of some other call).
func (s *sqlStore) RecoverActiveJob() (Job, bool, error) {
	job, err := s.GetActiveJob()
	if errors.Is(err, ErrNoActiveJob) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

// UpdateJobPhase implements Store.
func (s *sqlStore) UpdateJobPhase(id string, newPhase Phase, progressPercent int, message string) (Job, error) {
	tx, err := s.beginWriteTx()
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	q := s.lockClause(`SELECT id, kind, idempotency_key, requested_version, initiated_by, phase, progress_percent,
		created_at, updated_at, artifact_sha256, artifact_version, artifact_commit,
		rollback_snapshot_id, failure_code, failure_message, rollback_result
		FROM update_jobs WHERE id = ?`)
	row := tx.QueryRow(s.dialect.Rewrite(q), id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, err
	}

	if !isLegalPhaseTransition(job.Phase, newPhase) {
		return Job{}, fmt.Errorf("%w: %s -> %s", ErrInvalidPhaseTransition, job.Phase, newPhase)
	}

	job.Phase = newPhase
	job.ProgressPercent = progressPercent
	job.UpdatedAt = s.now()
	isTerminal := 0
	if newPhase.Terminal() {
		isTerminal = 1
	}

	updateSQL := s.dialect.Rewrite(`UPDATE update_jobs SET phase = ?, progress_percent = ?, updated_at = ?, is_terminal = ? WHERE id = ?`)
	if _, err := tx.Exec(updateSQL, string(newPhase), progressPercent, job.UpdatedAt, isTerminal, id); err != nil {
		return Job{}, fmt.Errorf("selfupdate: update job phase: %w", err)
	}

	if err := s.appendEventTx(tx, id, newPhase, message); err != nil {
		return Job{}, err
	}

	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// AppendEvent implements Store.
func (s *sqlStore) AppendEvent(jobID string, phase Phase, message string) (Event, error) {
	tx, err := s.beginWriteTx()
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()

	ev, err := s.appendEventTxReturning(tx, jobID, phase, message)
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, err
	}
	return ev, nil
}

func (s *sqlStore) appendEventTx(tx wtx, jobID string, phase Phase, message string) error {
	_, err := s.appendEventTxReturning(tx, jobID, phase, message)
	return err
}

func (s *sqlStore) appendEventTxReturning(tx wtx, jobID string, phase Phase, message string) (Event, error) {
	// Lock the job row so seq assignment is race-free even under
	// concurrent AppendEvent calls for the same job.
	q := s.lockClause(`SELECT id FROM update_jobs WHERE id = ?`)
	row := tx.QueryRow(s.dialect.Rewrite(q), jobID)
	var existingID string
	if err := row.Scan(&existingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, ErrJobNotFound
		}
		return Event{}, err
	}

	var maxSeq sql.NullInt64
	maxQ := s.dialect.Rewrite(`SELECT MAX(seq) FROM update_events WHERE job_id = ?`)
	if err := tx.QueryRow(maxQ, jobID).Scan(&maxSeq); err != nil {
		return Event{}, err
	}
	nextSeq := 1
	if maxSeq.Valid {
		nextSeq = int(maxSeq.Int64) + 1
	}

	ev := Event{JobID: jobID, Seq: nextSeq, At: s.now(), Phase: phase, Message: message}
	insertSQL := s.dialect.Rewrite(`INSERT INTO update_events (job_id, seq, at, phase, message) VALUES (?, ?, ?, ?, ?)`)
	if _, err := tx.Exec(insertSQL, ev.JobID, ev.Seq, ev.At, string(ev.Phase), ev.Message); err != nil {
		return Event{}, fmt.Errorf("selfupdate: insert event: %w", err)
	}
	return ev, nil
}

// ListEvents implements Store.
func (s *sqlStore) ListEvents(jobID string) ([]Event, error) {
	q := s.dialect.Rewrite(`SELECT job_id, seq, at, phase, message FROM update_events WHERE job_id = ? ORDER BY seq ASC`)
	rows, err := s.db.Query(q, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var ev Event
		var phase string
		if err := rows.Scan(&ev.JobID, &ev.Seq, &ev.At, &phase, &ev.Message); err != nil {
			return nil, err
		}
		ev.Phase = Phase(phase)
		ev.At = ev.At.UTC()
		events = append(events, ev)
	}
	return events, rows.Err()
}

// ListJobs implements Store.
func (s *sqlStore) ListJobs(limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 100
	}
	q := s.dialect.Rewrite(`SELECT id, kind, idempotency_key, requested_version, initiated_by, phase, progress_percent,
		created_at, updated_at, artifact_sha256, artifact_version, artifact_commit,
		rollback_snapshot_id, failure_code, failure_message, rollback_result
		FROM update_jobs ORDER BY created_at DESC LIMIT ?`)
	rows, err := s.db.Query(q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		job, err := scanJobRows(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// CreateSnapshot implements Store.
func (s *sqlStore) CreateSnapshot(snap RollbackSnapshot) (RollbackSnapshot, error) {
	if snap.ID == "" {
		snap.ID = newID("snap")
	}
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = s.now()
	}
	verified, lkg, retained := 0, 0, 0
	if snap.Verified {
		verified = 1
	}
	if snap.LastKnownGood {
		lkg = 1
	}
	if snap.Retained {
		retained = 1
	}
	insertSQL := s.dialect.Rewrite(`INSERT INTO rollback_snapshots
		(id, source_version, source_commit, checksum_sha256, verified, created_at, last_known_good, retained)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if _, err := s.db.Exec(insertSQL, snap.ID, snap.SourceVersion, snap.SourceCommit, snap.ChecksumSHA256,
		verified, snap.CreatedAt, lkg, retained); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: insert snapshot: %w", err)
	}
	return snap, nil
}

// ListSnapshots implements Store.
func (s *sqlStore) ListSnapshots() ([]RollbackSnapshot, error) {
	q := s.dialect.Rewrite(`SELECT id, source_version, source_commit, checksum_sha256, verified, created_at, last_known_good, retained
		FROM rollback_snapshots ORDER BY created_at DESC`)
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snaps []RollbackSnapshot
	for rows.Next() {
		var snap RollbackSnapshot
		var verified, lkg, retained int
		if err := rows.Scan(&snap.ID, &snap.SourceVersion, &snap.SourceCommit, &snap.ChecksumSHA256,
			&verified, &snap.CreatedAt, &lkg, &retained); err != nil {
			return nil, err
		}
		snap.Verified = verified != 0
		snap.LastKnownGood = lkg != 0
		snap.Retained = retained != 0
		snap.CreatedAt = snap.CreatedAt.UTC()
		snaps = append(snaps, snap)
	}
	return snaps, rows.Err()
}

// MarkLastKnownGood implements Store.
func (s *sqlStore) MarkLastKnownGood(id string) error {
	tx, err := s.beginWriteTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Confirm the target exists first, so callers get ErrSnapshotNotFound
	// instead of a silent no-op update.
	checkQ := s.lockClause(s.dialect.Rewrite(`SELECT id FROM rollback_snapshots WHERE id = ?`))
	var existingID string
	if err := tx.QueryRow(checkQ, id).Scan(&existingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSnapshotNotFound
		}
		return err
	}

	clearSQL := s.dialect.Rewrite(`UPDATE rollback_snapshots SET last_known_good = 0 WHERE last_known_good = 1`)
	if _, err := tx.Exec(clearSQL); err != nil {
		return fmt.Errorf("selfupdate: clear last_known_good: %w", err)
	}
	setSQL := s.dialect.Rewrite(`UPDATE rollback_snapshots SET last_known_good = 1 WHERE id = ?`)
	if _, err := tx.Exec(setSQL, id); err != nil {
		return fmt.Errorf("selfupdate: set last_known_good: %w", err)
	}
	return tx.Commit()
}

// isUniqueViolation reports whether err is a unique-constraint/index
// violation from either supported driver — same detection logic as
// internal/billing/setup.go's helper of the same name (kept as a local,
// unexported copy here since Go packages cannot share unexported helpers
// across package boundaries and this package intentionally has no
// dependency on internal/billing).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "duplicate key value violates unique constraint")
}
