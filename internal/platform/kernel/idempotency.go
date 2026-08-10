package kernel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// IdempotencyStore persists the outcome of a write request keyed by a
// client-supplied idempotency key, so a retried request (network timeout,
// duplicate client submission) replays the original result instead of
// re-executing the side effect. Every platform write endpoint that would
// be harmful to execute twice (organization creation, mailbox creation,
// bulk import commit, billing charge) must go through this.
type IdempotencyStore struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewIdempotencyStore(db *sql.DB, dialect *dbdialect.Info) *IdempotencyStore {
	return &IdempotencyStore{db: db, dialect: dialect}
}

func (s *IdempotencyStore) EnsureSchema(ctx context.Context) error {
	ts := s.dialect.TimestampType()
	autoInc := s.dialect.AutoIncrement()
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_idempotency_keys (
		id `+autoInc+`,
		scope TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		request_hash TEXT NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		response_body TEXT NOT NULL DEFAULT '',
		created_at `+ts+` NOT NULL,
		completed_at `+ts+`,
		UNIQUE(scope, idempotency_key)
	)`)
	return err
}

// RequestHash computes a stable hash of a request body for
// IdempotencyStore.Begin's "same key, different body" detection. Callers
// pass the exact bytes they would otherwise unmarshal.
func RequestHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ErrIdempotencyInFlight is returned by Begin when a request with the same
// key is currently being processed by another concurrent request — the
// caller should return 409/425 rather than double-execute.
var ErrIdempotencyInFlight = errors.New("kernel: idempotency key is currently in flight")

// Begin registers an attempt to process (scope, key). Exactly one of three
// outcomes happens:
//   - the key is new: a row is inserted with status_code=0 (in-flight),
//     and (nil, false, nil) is returned so the caller proceeds.
//   - the key exists and is complete with the SAME request hash: the
//     original (statusCode, responseBody) is returned via a *StoredResult
//     with replay=true, so the caller replays it without re-executing.
//   - the key exists with a DIFFERENT request hash: ErrCodeIdempotencyReuse
//     is returned — the client reused a key for a different request body,
//     which is a client bug, not something to silently allow.
//
// If the row exists but is still in-flight (another concurrent request
// with the same key hasn't finished), ErrIdempotencyInFlight is returned.
func (s *IdempotencyStore) Begin(ctx context.Context, scope, key, requestHash string, now time.Time) (*StoredResult, bool, error) {
	var (
		existingHash string
		statusCode   int
		responseBody string
		completedAt  sql.NullTime
	)
	row := s.db.QueryRowContext(ctx,
		`SELECT request_hash, status_code, response_body, completed_at FROM platform_idempotency_keys WHERE scope = `+s.dialect.Placeholder(1)+` AND idempotency_key = `+s.dialect.Placeholder(2),
		scope, key,
	)
	err := row.Scan(&existingHash, &statusCode, &responseBody, &completedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, insertErr := s.db.ExecContext(ctx,
			`INSERT INTO platform_idempotency_keys (scope, idempotency_key, request_hash, status_code, response_body, created_at) VALUES (`+s.dialect.Placeholders(6)+`)`,
			scope, key, requestHash, 0, "", now,
		); insertErr != nil {
			// A concurrent insert can race here (UNIQUE violation) —
			// that's not a real error, it means another request just
			// claimed the key first; recurse once to read its state.
			return s.Begin(ctx, scope, key, requestHash, now)
		}
		return nil, false, nil
	case err != nil:
		return nil, false, Wrap(ErrCodeInternal, "idempotency lookup failed", err)
	}

	if existingHash != requestHash {
		return nil, false, NewError(ErrCodeIdempotencyReuse, "idempotency key was already used for a request with a different body")
	}
	if !completedAt.Valid {
		return nil, false, ErrIdempotencyInFlight
	}
	return &StoredResult{StatusCode: statusCode, ResponseBody: responseBody}, true, nil
}

// Complete records the outcome of a request that Begin allowed to proceed
// (replay=false). statusCode/responseBody are what a retried request with
// the same key will be replayed. responseBody must already be
// redacted/safe — it is stored verbatim and returned verbatim on replay.
func (s *IdempotencyStore) Complete(ctx context.Context, scope, key string, statusCode int, response any, now time.Time) error {
	body, err := json.Marshal(response)
	if err != nil {
		return Wrap(ErrCodeInternal, "encode idempotent response", err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE platform_idempotency_keys SET status_code = `+s.dialect.Placeholder(1)+`, response_body = `+s.dialect.Placeholder(2)+`, completed_at = `+s.dialect.Placeholder(3)+` WHERE scope = `+s.dialect.Placeholder(4)+` AND idempotency_key = `+s.dialect.Placeholder(5),
		statusCode, string(body), now, scope, key,
	)
	if err != nil {
		return Wrap(ErrCodeInternal, "complete idempotency record", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return NewError(ErrCodeInternal, fmt.Sprintf("no idempotency row for scope=%s key=%s to complete", scope, key))
	}
	return nil
}

// Abandon removes an in-flight (never-completed) idempotency row — used
// when the handler's own processing fails before Complete, so a client
// retry with the same key is treated as a fresh attempt instead of being
// stuck reporting ErrIdempotencyInFlight forever.
func (s *IdempotencyStore) Abandon(ctx context.Context, scope, key string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM platform_idempotency_keys WHERE scope = `+s.dialect.Placeholder(1)+` AND idempotency_key = `+s.dialect.Placeholder(2)+` AND completed_at IS NULL`,
		scope, key,
	)
	if err != nil {
		return Wrap(ErrCodeInternal, "abandon idempotency record", err)
	}
	return nil
}

// PurgeBefore removes completed replay records and abandoned in-flight claims
// older than the retention cutoff. Callers choose and document the retention
// policy; this store never silently grows forever.
func (s *IdempotencyStore) PurgeBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM platform_idempotency_keys WHERE created_at < `+s.dialect.Placeholder(1), cutoff)
	if err != nil {
		return 0, Wrap(ErrCodeInternal, "purge idempotency records", err)
	}
	return res.RowsAffected()
}

type StoredResult struct {
	StatusCode   int
	ResponseBody string
}
