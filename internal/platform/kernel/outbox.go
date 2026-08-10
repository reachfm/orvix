package kernel

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// OutboxEvent is one durable record of "something must happen after this
// transaction commits" — a DNS provider call, a webhook delivery, a
// provisioning step. Writing the event row happens INSIDE the same DB
// transaction as the domain mutation it follows from, so the two can never
// diverge: either both commit, or neither does. A separate worker (not
// implemented in this milestone — cross-cutting foundation only) later
// polls Pending rows and executes them, moving each to Done or Failed.
type OutboxEvent struct {
	ID            int64
	Topic         string // e.g. "organization.provisioning.dns_setup"
	AggregateID   string // the domain entity this event is about (organization id, domain id, ...)
	Payload       json.RawMessage
	Status        OutboxStatus
	Attempts      int
	LastError     string // safe, redacted — never a raw provider/SQL error
	CreatedAt     time.Time
	NextAttemptAt time.Time
	CompletedAt   *time.Time
}

type OutboxStatus string

const (
	OutboxPending    OutboxStatus = "pending"
	OutboxProcessing OutboxStatus = "processing"
	OutboxDone       OutboxStatus = "done"
	OutboxFailed     OutboxStatus = "failed" // exhausted retries; requires operator attention
)

type OutboxRepository struct {
	dialect *dbdialect.Info
}

func NewOutboxRepository(dialect *dbdialect.Info) *OutboxRepository {
	return &OutboxRepository{dialect: dialect}
}

func (r *OutboxRepository) EnsureSchema(ctx context.Context, db *sql.DB) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_outbox_events (
		id `+autoInc+`,
		topic TEXT NOT NULL,
		aggregate_id TEXT NOT NULL,
		payload TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at `+ts+` NOT NULL,
		next_attempt_at `+ts+` NOT NULL,
		completed_at `+ts+`
	)`)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_outbox_status_next_attempt ON platform_outbox_events(status, next_attempt_at)`)
	return err
}

// Enqueue must be called with a *sql.Tx (via the Querier interface) that is
// the SAME transaction as the domain mutation the event follows from —
// that is the entire point of the outbox pattern. Enqueueing with the
// top-level *sql.DB defeats the guarantee and is a bug in the caller.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (r *OutboxRepository) Enqueue(ctx context.Context, q Querier, topic, aggregateID string, payload any, now time.Time) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return Wrap(ErrCodeInternal, "encode outbox payload", err)
	}
	_, err = q.ExecContext(ctx,
		`INSERT INTO platform_outbox_events (topic, aggregate_id, payload, status, attempts, last_error, created_at, next_attempt_at) VALUES (`+r.dialect.Placeholders(8)+`)`,
		topic, aggregateID, string(body), string(OutboxPending), 0, "", now, now,
	)
	if err != nil {
		return Wrap(ErrCodeInternal, "enqueue outbox event", err)
	}
	return nil
}

// ClaimBatch atomically claims up to limit pending-and-due events by
// flipping them to Processing, so two concurrent workers never process the
// same event. SQLite has no SELECT ... FOR UPDATE SKIP LOCKED, so this uses
// a portable claim: select candidate IDs, then UPDATE ... WHERE status =
// 'pending' AND id IN (...) — the WHERE-status guard makes the claim
// atomic per-row even without row locking, since a losing concurrent
// UPDATE simply affects 0 rows.
func (r *OutboxRepository) ClaimBatch(ctx context.Context, db *sql.DB, limit int, now time.Time) ([]OutboxEvent, error) {
	return r.claimBatch(ctx, db, "", limit, now)
}

// ClaimTopicBatch claims only one exact topic, allowing independent durable
// workers to share the kernel outbox without consuming each other's jobs.
func (r *OutboxRepository) ClaimTopicBatch(ctx context.Context, db *sql.DB, topic string, limit int, now time.Time) ([]OutboxEvent, error) {
	return r.claimBatch(ctx, db, topic, limit, now)
}

func (r *OutboxRepository) claimBatch(ctx context.Context, db *sql.DB, topic string, limit int, now time.Time) ([]OutboxEvent, error) {
	where := `status = ` + r.dialect.Placeholder(1) + ` AND next_attempt_at <= ` + r.dialect.Placeholder(2)
	args := []any{string(OutboxPending), now}
	if topic != "" {
		where += ` AND topic = ` + r.dialect.Placeholder(3)
		args = append(args, topic)
	}
	args = append(args, limit)
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM platform_outbox_events WHERE `+where+` ORDER BY id ASC LIMIT `+r.dialect.Placeholder(len(args)), args...,
	)
	if err != nil {
		return nil, Wrap(ErrCodeInternal, "list claimable outbox events", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, Wrap(ErrCodeInternal, "scan outbox id", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, Wrap(ErrCodeInternal, "iterate outbox ids", err)
	}

	var claimed []OutboxEvent
	for _, id := range ids {
		res, err := db.ExecContext(ctx,
			`UPDATE platform_outbox_events SET status = `+r.dialect.Placeholder(1)+` WHERE id = `+r.dialect.Placeholder(2)+` AND status = `+r.dialect.Placeholder(3),
			string(OutboxProcessing), id, string(OutboxPending),
		)
		if err != nil {
			return nil, Wrap(ErrCodeInternal, "claim outbox event", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue // another worker claimed it first
		}
		ev, err := r.getByID(ctx, db, id)
		if err != nil {
			continue
		}
		claimed = append(claimed, *ev)
	}
	return claimed, nil
}

func (r *OutboxRepository) getByID(ctx context.Context, db *sql.DB, id int64) (*OutboxEvent, error) {
	var ev OutboxEvent
	var payload string
	var completedAt sql.NullTime
	row := db.QueryRowContext(ctx,
		`SELECT id, topic, aggregate_id, payload, status, attempts, last_error, created_at, next_attempt_at, completed_at FROM platform_outbox_events WHERE id = `+r.dialect.Placeholder(1),
		id,
	)
	// payload is scanned into a plain string, not directly into
	// json.RawMessage ([]byte) — database/sql's default converter has no
	// automatic string->[]byte-based-named-type path and returns
	// "unsupported Scan" for a TEXT column scanned straight into
	// *json.RawMessage.
	if err := row.Scan(&ev.ID, &ev.Topic, &ev.AggregateID, &payload, &ev.Status, &ev.Attempts, &ev.LastError, &ev.CreatedAt, &ev.NextAttemptAt, &completedAt); err != nil {
		return nil, err
	}
	ev.Payload = json.RawMessage(payload)
	if completedAt.Valid {
		ev.CompletedAt = &completedAt.Time
	}
	return &ev, nil
}

// MarkDone records successful processing.
func (r *OutboxRepository) MarkDone(ctx context.Context, q Querier, id int64, now time.Time) error {
	_, err := q.ExecContext(ctx,
		`UPDATE platform_outbox_events SET status = `+r.dialect.Placeholder(1)+`, completed_at = `+r.dialect.Placeholder(2)+` WHERE id = `+r.dialect.Placeholder(3),
		string(OutboxDone), now, id,
	)
	return err
}

// MarkRetry records a failed attempt and reschedules with the caller-
// supplied backoff, or marks Failed (exhausted) if attempts >= maxAttempts.
// safeErr must already be redacted — never a raw provider/SQL error.
func (r *OutboxRepository) MarkRetry(ctx context.Context, db *sql.DB, id int64, attempts, maxAttempts int, safeErr string, nextAttemptAt, now time.Time) error {
	status := string(OutboxPending)
	if attempts >= maxAttempts {
		status = string(OutboxFailed)
	}
	_, err := db.ExecContext(ctx,
		`UPDATE platform_outbox_events SET status = `+r.dialect.Placeholder(1)+`, attempts = `+r.dialect.Placeholder(2)+`, last_error = `+r.dialect.Placeholder(3)+`, next_attempt_at = `+r.dialect.Placeholder(4)+` WHERE id = `+r.dialect.Placeholder(5),
		status, attempts, safeErr, nextAttemptAt, id,
	)
	return err
}
