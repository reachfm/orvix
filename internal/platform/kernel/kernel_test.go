package kernel

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) (*sql.DB, *dbdialect.Info) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/kernel_test.db?_txlock=immediate", dir))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		t.Fatalf("detect dialect: %v", err)
	}
	return db, dialect
}

// --- errors.go ---

func TestErrorHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		code ErrorCode
		want int
	}{
		{ErrCodeNotFound, 404},
		{ErrCodeConflict, 409},
		{ErrCodeValidation, 400},
		{ErrCodeForbidden, 403},
		{ErrCodeQuotaExceeded, 409},
		{ErrCodePreconditionFail, 412},
		{ErrCodeInternal, 500},
	}
	for _, c := range cases {
		e := NewError(c.code, "x")
		if got := e.HTTPStatus(); got != c.want {
			t.Errorf("%s: got status %d, want %d", c.code, got, c.want)
		}
	}
}

func TestAsAPIError_NeverLeaksRawCause(t *testing.T) {
	raw := fmt.Errorf("dial tcp 10.0.0.5:5432: connection refused, dsn=postgres://user:hunter2@host/db")
	e := AsAPIError(raw)
	if e.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %s", e.Code)
	}
	if e.Message == raw.Error() {
		t.Fatal("AsAPIError must not surface the raw error string as the client-facing Message")
	}
	// The cause remains available server-side via Unwrap.
	if e.Unwrap() != raw {
		t.Fatal("expected Unwrap to return the original cause for server-side logging")
	}
}

func TestAsAPIError_PassesThroughAlreadyTypedError(t *testing.T) {
	original := NotFound("organization")
	got := AsAPIError(original)
	if got != original {
		t.Fatal("expected AsAPIError to pass through an already-typed *Error unchanged")
	}
}

// --- pagination.go ---

func TestPageRequestNormalize(t *testing.T) {
	p := PageRequest{Page: 0, PageSize: 0}.Normalize()
	if p.Page != 1 || p.PageSize != DefaultPageSize {
		t.Fatalf("got page=%d size=%d", p.Page, p.PageSize)
	}
	p2 := PageRequest{Page: -5, PageSize: 999999}.Normalize()
	if p2.Page != 1 || p2.PageSize != MaxPageSize {
		t.Fatalf("expected clamping, got page=%d size=%d", p2.Page, p2.PageSize)
	}
}

func TestPageResponseTotalPages(t *testing.T) {
	req := PageRequest{Page: 2, PageSize: 10}.Normalize()
	resp := NewPageResponse([]string{"a", "b"}, req, 25)
	if resp.TotalPages != 3 {
		t.Fatalf("expected 3 total pages for 25 items at size 10, got %d", resp.TotalPages)
	}
	if resp.Items == nil {
		t.Fatal("Items must never be nil in the JSON response — expect [] not null")
	}
}

func TestPageResponseNilItemsBecomesEmptySlice(t *testing.T) {
	resp := NewPageResponse[string](nil, PageRequest{Page: 1, PageSize: 10}, 0)
	if resp.Items == nil {
		t.Fatal("nil items must be normalized to an empty slice, never null in JSON")
	}
}

func TestAllowlistSortRejectsUnknownColumn(t *testing.T) {
	allowed := map[string]string{"name": "name", "created_at": "created_at"}
	got := AllowlistSort("id; DROP TABLE users;--", allowed, "created_at")
	if got != "created_at" {
		t.Fatalf("expected fallback for an unallowlisted sort column, got %q", got)
	}
	got2 := AllowlistSort("name", allowed, "created_at")
	if got2 != "name" {
		t.Fatalf("expected allowlisted column to pass through, got %q", got2)
	}
}

// --- clock.go ---

func TestFixedClockAdvance(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFixedClock(base)
	if !c.Now().Equal(base) {
		t.Fatal("expected fixed clock to start at base time")
	}
	c.Advance(time.Hour)
	if !c.Now().Equal(base.Add(time.Hour)) {
		t.Fatal("expected clock to advance by exactly one hour")
	}
}

func TestSystemClockIsUTC(t *testing.T) {
	if (SystemClock{}).Now().Location() != time.UTC {
		t.Fatal("SystemClock must always return UTC time")
	}
}

// --- id.go ---

func TestUUIDGeneratorProducesUniqueIDs(t *testing.T) {
	g := UUIDGenerator{}
	a, b := g.NewID(), g.NewID()
	if a == b {
		t.Fatal("expected two distinct UUIDs")
	}
	if len(a) != 36 {
		t.Fatalf("expected a canonical 36-char UUID string, got %q", a)
	}
}

// --- redact.go ---

func TestIsSecretField(t *testing.T) {
	for _, f := range []string{"password", "smtp_relay_password", "api_key", "jwt_secret", "Authorization"} {
		if !IsSecretField(f) {
			t.Errorf("expected %q to be flagged as a secret field", f)
		}
	}
	if IsSecretField("display_name") {
		t.Fatal("display_name must not be flagged as secret")
	}
}

func TestRedactMap(t *testing.T) {
	in := map[string]any{"name": "acme", "password": "hunter2"}
	out := RedactMap(in)
	if out["password"] != "[REDACTED]" {
		t.Fatalf("expected password redacted, got %v", out["password"])
	}
	if out["name"] != "acme" {
		t.Fatal("non-secret fields must pass through unchanged")
	}
	if in["password"] != "hunter2" {
		t.Fatal("RedactMap must not mutate the caller's original map")
	}
}

func TestRedactEmailLocalPart(t *testing.T) {
	got := RedactEmailLocalPart("alice@example.com")
	if got != "a***@example.com" {
		t.Fatalf("got %q", got)
	}
}

// --- concurrency.go ---

func TestCheckVersionedUpdate_ZeroRowsIsPreconditionFailed(t *testing.T) {
	db, dialect := testDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t (id, v) VALUES (1, 1)`); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`UPDATE t SET v = 2 WHERE id = 1 AND v = 999`) // stale version
	if err != nil {
		t.Fatal(err)
	}
	kerr := CheckVersionedUpdate(res, "widget")
	apiErr, ok := kerr.(*Error)
	if !ok || apiErr.Code != ErrCodePreconditionFail {
		t.Fatalf("expected ErrCodePreconditionFail, got %v", kerr)
	}
	_ = dialect
}

func TestCheckExistenceUpdate_ZeroRowsIsNotFound(t *testing.T) {
	db, _ := testDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`UPDATE t SET status = 'active' WHERE id = 42`)
	if err != nil {
		t.Fatal(err)
	}
	kerr := CheckExistenceUpdate(res, "organization")
	apiErr, ok := kerr.(*Error)
	if !ok || apiErr.Code != ErrCodeNotFound {
		t.Fatalf("expected ErrCodeNotFound, got %v", kerr)
	}
}

// --- confirm.go ---

func TestRequireTypedConfirmation(t *testing.T) {
	if err := RequireTypedConfirmation("acme-corp", "acme-corp"); err != nil {
		t.Fatalf("exact match must pass, got %v", err)
	}
	if err := RequireTypedConfirmation("acme-corp", "Acme-Corp"); err == nil {
		t.Fatal("case-mismatched confirmation must be rejected, not silently normalized")
	}
	if err := RequireTypedConfirmation("acme-corp", ""); err == nil {
		t.Fatal("empty confirmation must be rejected")
	}
}

// --- idempotency.go ---

func newIdempotencyStore(t *testing.T) (*IdempotencyStore, *sql.DB) {
	t.Helper()
	db, dialect := testDB(t)
	s := NewIdempotencyStore(db, dialect)
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return s, db
}

func TestIdempotency_FirstAttemptProceeds(t *testing.T) {
	s, _ := newIdempotencyStore(t)
	now := time.Now().UTC()
	res, replay, err := s.Begin(context.Background(), "org.create", "key-1", "hash-a", now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if replay || res != nil {
		t.Fatal("expected a fresh key to proceed, not replay")
	}
}

func TestIdempotency_CompletedRequestReplays(t *testing.T) {
	s, _ := newIdempotencyStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, _, err := s.Begin(ctx, "org.create", "key-1", "hash-a", now); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.Complete(ctx, "org.create", "key-1", 201, map[string]string{"id": "org-1"}, now); err != nil {
		t.Fatalf("complete: %v", err)
	}
	res, replay, err := s.Begin(ctx, "org.create", "key-1", "hash-a", now)
	if err != nil {
		t.Fatalf("begin (retry): %v", err)
	}
	if !replay {
		t.Fatal("expected the retried request to replay the stored result")
	}
	if res.StatusCode != 201 {
		t.Fatalf("expected replayed status 201, got %d", res.StatusCode)
	}
}

func TestIdempotency_SameKeyDifferentBodyIsRejected(t *testing.T) {
	s, _ := newIdempotencyStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, _, err := s.Begin(ctx, "org.create", "key-1", "hash-a", now); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.Complete(ctx, "org.create", "key-1", 201, "ok", now); err != nil {
		t.Fatalf("complete: %v", err)
	}
	_, _, err := s.Begin(ctx, "org.create", "key-1", "hash-B-different", now)
	apiErr, ok := err.(*Error)
	if !ok || apiErr.Code != ErrCodeIdempotencyReuse {
		t.Fatalf("expected ErrCodeIdempotencyReuse, got %v", err)
	}
}

func TestIdempotency_InFlightRequestReportsInFlight(t *testing.T) {
	s, _ := newIdempotencyStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, _, err := s.Begin(ctx, "org.create", "key-1", "hash-a", now); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Never call Complete — simulate a still-processing concurrent request.
	_, _, err := s.Begin(ctx, "org.create", "key-1", "hash-a", now)
	if err != ErrIdempotencyInFlight {
		t.Fatalf("expected ErrIdempotencyInFlight, got %v", err)
	}
}

func TestIdempotency_AbandonAllowsFreshRetry(t *testing.T) {
	s, _ := newIdempotencyStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, _, err := s.Begin(ctx, "org.create", "key-1", "hash-a", now); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.Abandon(ctx, "org.create", "key-1"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	_, replay, err := s.Begin(ctx, "org.create", "key-1", "hash-a", now)
	if err != nil {
		t.Fatalf("begin after abandon: %v", err)
	}
	if replay {
		t.Fatal("expected a fresh proceed after Abandon, not a replay")
	}
}

func TestIdempotency_ConcurrentBeginsOnlyOneProceeds(t *testing.T) {
	s, _ := newIdempotencyStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const n = 8
	var wg sync.WaitGroup
	proceeded := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, replay, err := s.Begin(ctx, "org.create", "shared-key", "hash-a", now)
			if err == nil && !replay {
				proceeded[i] = true
			}
		}(i)
	}
	wg.Wait()
	count := 0
	for _, p := range proceeded {
		if p {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent Begin calls to proceed, got %d", n, count)
	}
}

// --- outbox.go ---

func newOutboxRepo(t *testing.T) (*OutboxRepository, *sql.DB) {
	t.Helper()
	db, dialect := testDB(t)
	r := NewOutboxRepository(dialect)
	if err := r.EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return r, db
}

func TestOutbox_EnqueueWithinTransactionCommitsAtomically(t *testing.T) {
	r, db := newOutboxRepo(t)
	if _, err := db.Exec(`CREATE TABLE domain_thing (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_thing (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := r.Enqueue(ctx, tx, "org.provisioning.dns_setup", "org-1", map[string]string{"domain": "example.com"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	events, err := r.ClaimBatch(ctx, db, 10, now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(events) != 1 || events[0].AggregateID != "org-1" {
		t.Fatalf("expected the enqueued event to be claimable, got %v", events)
	}
}

func TestOutbox_EnqueueRollsBackWithDomainMutation(t *testing.T) {
	r, db := newOutboxRepo(t)
	if _, err := db.Exec(`CREATE TABLE domain_thing (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_thing (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := r.Enqueue(ctx, tx, "org.provisioning.dns_setup", "org-1", "payload", now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var thingCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_thing`).Scan(&thingCount); err != nil {
		t.Fatal(err)
	}
	if thingCount != 0 {
		t.Fatal("expected the domain mutation to be rolled back")
	}
	events, err := r.ClaimBatch(ctx, db, 10, now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(events) != 0 {
		t.Fatal("expected the outbox event to be rolled back along with the domain mutation")
	}
}

func TestOutbox_ClaimBatchIsConcurrencySafe(t *testing.T) {
	r, db := newOutboxRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := r.Enqueue(ctx, db, "topic", "agg-1", "payload", now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	const workers = 5
	var wg sync.WaitGroup
	claimedCounts := make([]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			events, err := r.ClaimBatch(ctx, db, 10, now)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			claimedCounts[i] = len(events)
		}(i)
	}
	wg.Wait()
	total := 0
	for _, c := range claimedCounts {
		total += c
	}
	if total != 1 {
		t.Fatalf("expected exactly 1 total claim across %d concurrent workers for a single event, got %d", workers, total)
	}
}

func TestOutbox_MarkRetryExhaustsToFailed(t *testing.T) {
	r, db := newOutboxRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := r.Enqueue(ctx, db, "topic", "agg-1", "payload", now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	events, err := r.ClaimBatch(ctx, db, 10, now)
	if err != nil || len(events) != 1 {
		t.Fatalf("claim: %v %v", events, err)
	}
	if err := r.MarkRetry(ctx, db, events[0].ID, 3, 3, "redacted provider error", now.Add(time.Minute), now); err != nil {
		t.Fatalf("mark retry: %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM platform_outbox_events WHERE id = ?`, events[0].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(OutboxFailed) {
		t.Fatalf("expected status=failed after exhausting attempts, got %q", status)
	}
}

func TestOutbox_MarkDone(t *testing.T) {
	r, db := newOutboxRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := r.Enqueue(ctx, db, "topic", "agg-1", "payload", now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	events, err := r.ClaimBatch(ctx, db, 10, now)
	if err != nil || len(events) != 1 {
		t.Fatalf("claim: %v %v", events, err)
	}
	if err := r.MarkDone(ctx, db, events[0].ID, now); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM platform_outbox_events WHERE id = ?`, events[0].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(OutboxDone) {
		t.Fatalf("expected status=done, got %q", status)
	}
}
