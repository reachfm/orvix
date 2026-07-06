package queue

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use a per-test on-disk file rather than `:memory:` so the
	// modernc.org/sqlite connection pool actually shares one DB
	// across all pooled connections.
	//
	// Background: SQLite's `:memory:` namespace is per-connection.
	// The default `sql.DB` pool opens a new connection on demand,
	// so a query on connection 2 sees a different (empty) DB than
	// a query on connection 1. That is invisible in tests that
	// run everything on a single connection (the common case),
	// but trips every test that drives more than one connection
	// in parallel — most visibly
	// TestLeaseNextOnlyOneWorkerWins, where two goroutines both
	// race to LeaseNext() against a single enqueued row. With
	// separate per-connection `:memory:` DBs, both goroutines see
	// zero rows and the test reports "expected exactly 1 worker
	// to win, got 0". This manifested as a flaky failure under
	// `go test ./... -p 4` on the Linux CI runner (it passes in
	// isolation on any single platform; the race only surfaces
	// when the connection pool gets pressure from neighbouring
	// packages).
	//
	// The on-disk file lives in t.TempDir() so it is removed by
	// the test harness's automatic cleanup. Each test gets its
	// own file, so there is no cross-test leakage either.
	path := filepath.Join(t.TempDir(), "queue.db")
	db, err := sql.Open("sqlite", path+"?_busy_timeout=5000&_txlock=immediate&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	nowFn = func() time.Time { return time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC) }

	for _, stmt := range Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v\nSQL: %s", err, stmt)
		}
	}
	for _, stmt := range Indexes() {
		db.Exec(stmt)
	}
	return db
}

func testQE(t *testing.T) (*sql.DB, *QueueEngine) {
	t.Helper()
	db := testDB(t)
	return db, NewQueueEngine(db)
}

func makeEntry(to, domain string) *QueueEntry {
	return &QueueEntry{
		TenantID:        1,
		DomainID:        1,
		MessageID:       "msg-" + to,
		FromAddress:     "sender@example.com",
		ToAddress:       to,
		RecipientDomain: domain,
		Direction:       DirectionOutbound,
		DeliveryMode:    DeliveryRemoteSMTP,
		MaxAttempts:     5,
	}
}

// ── Enqueue Tests ────────────────────────────────────────────

func TestEnqueue(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("user@test.com", "test.com")
	if err := qe.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if entry.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if entry.Status != StatusPending {
		t.Fatalf("expected pending, got %s", entry.Status)
	}
}

func TestEnqueueSetsDefaults(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := &QueueEntry{
		TenantID:    1,
		DomainID:    1,
		MessageID:   "defaults",
		FromAddress: "from@example.com",
		ToAddress:   "to@example.org",
	}
	if err := qe.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if entry.DeliveryMode != DeliveryRemoteSMTP {
		t.Fatalf("expected remote_smtp, got %s", entry.DeliveryMode)
	}
	if entry.RecipientDomain != "example.org" {
		t.Fatalf("expected example.org, got %s", entry.RecipientDomain)
	}
	if entry.MaxAttempts != DefaultMaxAttempts {
		t.Fatalf("expected %d max attempts, got %d", DefaultMaxAttempts, entry.MaxAttempts)
	}
}

func TestEnqueueInbound(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := &QueueEntry{
		TenantID:     1,
		DomainID:     1,
		MailboxID:    uintPtr(100),
		MessageID:    "inbound-1",
		FromAddress:  "external@outside.com",
		ToAddress:    "local@ourdomain.com",
		Direction:    DirectionInbound,
		DeliveryMode: DeliveryLocal,
	}
	if err := qe.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue inbound: %v", err)
	}
	if entry.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
}

// ── Get/List Tests ───────────────────────────────────────────

func TestGet(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()
	entry := makeEntry("get@test.com", "test.com")
	qe.Enqueue(ctx, entry)

	got, err := qe.Repo.Get(ctx, entry.ID, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("entry not found")
	}
	if got.ToAddress != "get@test.com" {
		t.Fatalf("expected get@test.com, got %s", got.ToAddress)
	}
}

func TestListByStatus(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		entry := makeEntry(fmt.Sprintf("u%d@test.com", i), "test.com")
		qe.Enqueue(ctx, entry)
	}

	pending := StatusPending
	entries, total, err := qe.Repo.List(ctx, QueueFilter{Status: &pending}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected 5 pending, got %d", total)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 results, got %d", len(entries))
	}
}

func TestListPagination(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		entry := makeEntry(fmt.Sprintf("p%d@test.com", i), "test.com")
		qe.Enqueue(ctx, entry)
	}

	page1, total, err := qe.Repo.List(ctx, QueueFilter{Limit: 3, Offset: 0}, nil)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total != 10 {
		t.Fatalf("expected total 10, got %d", total)
	}
	if len(page1) != 3 {
		t.Fatalf("expected 3 on page 1, got %d", len(page1))
	}

	page2, _, _ := qe.Repo.List(ctx, QueueFilter{Limit: 3, Offset: 3}, nil)
	if len(page2) != 3 {
		t.Fatalf("expected 3 on page 2, got %d", len(page2))
	}
}

func TestListTenantIsolation(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		entry := makeEntry(fmt.Sprintf("t1-%d@a.com", i), "a.com")
		entry.TenantID = 1
		qe.Enqueue(ctx, entry)
	}
	for i := 0; i < 2; i++ {
		entry := makeEntry(fmt.Sprintf("t2-%d@b.com", i), "b.com")
		entry.TenantID = 2
		qe.Enqueue(ctx, entry)
	}

	t1 := uint(1)
	entries, total, _ := qe.Repo.List(ctx, QueueFilter{TenantID: &t1}, nil)
	if total != 3 {
		t.Fatalf("expected 3 for tenant 1, got %d", total)
	}
	_ = entries
}

// ── Leasing Tests ────────────────────────────────────────────

func TestLeaseNext(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("lease@test.com", "test.com")
	qe.Enqueue(ctx, entry)

	leased, err := qe.LeaseNext(ctx, "worker-1")
	if err != nil {
		t.Fatalf("lease next: %v", err)
	}
	if leased == nil {
		t.Fatal("expected leased entry")
	}
	if leased.ID != entry.ID {
		t.Fatalf("expected entry ID %d, got %d", entry.ID, leased.ID)
	}
	if leased.Status != StatusLeased {
		t.Fatalf("expected leased status, got %s", leased.Status)
	}
	if leased.LeaseOwner != "worker-1" {
		t.Fatalf("expected worker-1 owner, got %s", leased.LeaseOwner)
	}
	if leased.AttemptCount != 1 {
		t.Fatalf("expected attempt count 1, got %d", leased.AttemptCount)
	}
}

func TestLeaseNextRespectsAllowedStatuses(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("bounced@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.Repo.Bounce(ctx, entry.ID, "bounced", nil)

	// Should not find bounced entries with default allowed statuses.
	leased, err := qe.LeaseNext(ctx, "worker-1")
	if err != nil {
		t.Fatalf("lease next: %v", err)
	}
	if leased != nil {
		t.Fatal("expected no leased entry for non-matching status")
	}
}

func TestLeaseNextOnlyOneWorkerWins(t *testing.T) {
	db, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("race@test.com", "test.com")
	qe.Enqueue(ctx, entry)

	// Two workers attempt to lease simultaneously.
	db.SetMaxOpenConns(2)

	var wg sync.WaitGroup
	results := make(chan *QueueEntry, 2)
	errs := make(chan error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			leased, err := qe.LeaseNext(ctx, fmt.Sprintf("worker-%d", id))
			if err != nil {
				errs <- err
				return
			}
			if leased != nil {
				results <- leased
			}
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)

	var errList []error
	for e := range errs {
		errList = append(errList, e)
	}
	count := 0
	for range results {
		count++
	}
	if count != 1 {
		errStr := ""
		for _, e := range errList {
			errStr += e.Error() + "; "
		}
		t.Fatalf("expected exactly 1 worker to win, got %d (errors: %s)", count, errStr)
	}
}

func TestLeaseNextAttemptIncrement(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("attempts@test.com", "test.com")
	qe.Enqueue(ctx, entry)

	leased1, _ := qe.LeaseNext(ctx, "w1")
	if leased1.AttemptCount != 1 {
		t.Fatalf("expected attempt 1, got %d", leased1.AttemptCount)
	}

	// Release lease and re-lease
	qe.Repo.UpdateStatus(ctx, leased1.ID, StatusPending, "", nil)
	leased2, _ := qe.LeaseNext(ctx, "w2")
	if leased2.AttemptCount != 2 {
		t.Fatalf("expected attempt 2, got %d", leased2.AttemptCount)
	}
}

// ── Ack/Defer/Bounce/DeadLetter Tests ────────────────────────

func TestAckDelivered(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("ack@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.LeaseNext(ctx, "worker")

	if err := qe.AckDelivered(ctx, entry.ID); err != nil {
		t.Fatalf("ack: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusDelivered {
		t.Fatalf("expected delivered, got %s", got.Status)
	}
	if got.CompletedAt == nil {
		t.Fatal("expected completed_at set")
	}
}

func TestDeferAndRetry(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("defer@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.LeaseNext(ctx, "worker")

	nextAttempt := nowFn().Add(5 * time.Minute)
	if err := qe.Defer(ctx, entry.ID, nextAttempt, "temporary failure"); err != nil {
		t.Fatalf("defer: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusDeferred {
		t.Fatalf("expected deferred, got %s", got.Status)
	}
	if got.LastError != "temporary failure" {
		t.Fatalf("expected error msg, got %s", got.LastError)
	}
}

func TestBounce(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("bounce@test.com", "test.com")
	qe.Enqueue(ctx, entry)

	if err := qe.Bounce(ctx, entry.ID, "user unknown"); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusBounced {
		t.Fatalf("expected bounced, got %s", got.Status)
	}
}

func TestDeadLetter(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("dl@test.com", "test.com")
	qe.Enqueue(ctx, entry)

	if err := qe.DeadLetter(ctx, entry.ID, "max attempts exceeded"); err != nil {
		t.Fatalf("dead letter: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusDeadLetter {
		t.Fatalf("expected dead_letter, got %s", got.Status)
	}
}

func TestCancel(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("cancel@test.com", "test.com")
	qe.Enqueue(ctx, entry)

	if err := qe.Cancel(ctx, entry.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusCancelled {
		t.Fatalf("expected cancelled, got %s", got.Status)
	}
}

func TestRetryNow(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("retry@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.DeadLetter(ctx, entry.ID, "failed")

	if err := qe.RetryNow(ctx, entry.ID); err != nil {
		t.Fatalf("retry now: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusPending {
		t.Fatalf("expected pending, got %s", got.Status)
	}
	if got.AttemptCount != 0 {
		t.Fatalf("expected attempt count 0, got %d", got.AttemptCount)
	}
}

// ── Expired Lease Tests ──────────────────────────────────────

func TestAdminQueueTransitionsRejectLeasedEntries(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		action func(uint) error
	}{
		{"retry", func(id uint) error { return qe.Repo.AdminRetryNow(ctx, id, nil) }},
		{"cancel", func(id uint) error { return qe.Repo.AdminCancel(ctx, id, nil) }},
		{"dead_letter", func(id uint) error { return qe.Repo.AdminDeadLetter(ctx, id, "operator action", nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := makeEntry(tc.name+"-leased@test.com", "test.com")
			if err := qe.Enqueue(ctx, entry); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			leased, err := qe.LeaseNext(ctx, "worker-"+tc.name)
			if err != nil {
				t.Fatalf("lease: %v", err)
			}
			if leased == nil || leased.Status != StatusLeased {
				t.Fatalf("expected leased entry, got %#v", leased)
			}
			if err := tc.action(entry.ID); err == nil {
				t.Fatalf("%s unexpectedly allowed leased queue entry", tc.name)
			}
			got, err := qe.Repo.Get(ctx, entry.ID, nil)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Status != StatusLeased {
				t.Fatalf("status changed after rejected %s: got %s", tc.name, got.Status)
			}
		})
	}
}

func TestAdminQueueTransitionRejectsChangedStatus(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("changed-status@test.com", "test.com")
	if err := qe.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := qe.DeadLetter(ctx, entry.ID, "first failure"); err != nil {
		t.Fatalf("dead letter: %v", err)
	}
	if _, err := qe.DB.ExecContext(ctx, `UPDATE coremail_queue SET status=? WHERE id=?`, string(StatusLeased), entry.ID); err != nil {
		t.Fatalf("force changed status: %v", err)
	}
	if err := qe.Repo.AdminRetryNow(ctx, entry.ID, nil); err == nil {
		t.Fatal("admin retry unexpectedly allowed row after status changed to leased")
	}
	got, err := qe.Repo.Get(ctx, entry.ID, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusLeased {
		t.Fatalf("status changed after rejected retry: got %s", got.Status)
	}
}

func TestReleaseExpiredLeases(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("expired@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.LeaseNext(ctx, "worker")

	// Move clock forward past lease expiration.
	future := nowFn().Add(2 * time.Hour)
	nowFn = func() time.Time { return future }

	released, err := qe.ReleaseExpiredLeases(ctx)
	if err != nil {
		t.Fatalf("release expired: %v", err)
	}
	if released != 1 {
		t.Fatalf("expected 1 released, got %d", released)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusPending {
		t.Fatalf("expected pending after release, got %s", got.Status)
	}
}

func TestReleaseExpiredLeasesNotLeased(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("notleased@test.com", "test.com")
	qe.Enqueue(ctx, entry)

	future := nowFn().Add(2 * time.Hour)
	nowFn = func() time.Time { return future }

	released, err := qe.ReleaseExpiredLeases(ctx)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released != 0 {
		t.Fatalf("expected 0 released for non-leased entries, got %d", released)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusPending {
		t.Fatalf("expected pending, got %s", got.Status)
	}
}

// ── Retry Scheduling Tests ───────────────────────────────────

func TestHandleDeliveryResultSuccess(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("success@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.LeaseNext(ctx, "worker")

	if err := qe.HandleDeliveryResult(ctx, entry, true, ""); err != nil {
		t.Fatalf("handle success: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusDelivered {
		t.Fatalf("expected delivered, got %s", got.Status)
	}
}

func TestHandleDeliveryResultDefer(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("defer-handle@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.LeaseNext(ctx, "worker")

	if err := qe.HandleDeliveryResult(ctx, entry, false, "temp failure"); err != nil {
		t.Fatalf("handle defer: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusDeferred {
		t.Fatalf("expected deferred, got %s", got.Status)
	}
}

func TestHandleDeliveryResultDeadLetter(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("dl-handle@test.com", "test.com")
	entry.MaxAttempts = 1
	qe.Enqueue(ctx, entry)
	leased, _ := qe.LeaseNext(ctx, "worker")

	// Use the leased entry which has the incremented attempt count.
	if err := qe.HandleDeliveryResult(ctx, leased, false, "exhausted"); err != nil {
		t.Fatalf("handle dl: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusDeadLetter {
		t.Fatalf("expected dead_letter, got %s", got.Status)
	}
}

func TestComputeNextAttempt(t *testing.T) {
	tests := []struct {
		attempt  int
		minDelay int // minutes
		maxDelay int // minutes
	}{
		{0, 0, 0},
		{1, 0, 3},       // ~1 minute + jitter
		{2, 4, 8},       // ~5 minutes + jitter
		{3, 13, 20},     // ~15 minutes + jitter
		{4, 55, 80},     // ~1 hour + jitter
		{5, 230, 280},   // ~4 hours + jitter
		{6, 1380, 1600}, // ~24 hours + jitter
	}

	for _, tc := range tests {
		result := computeNextAttempt(tc.attempt)
		delay := int(result.Sub(nowFn()).Minutes())
		if delay < tc.minDelay || delay > tc.maxDelay {
			t.Errorf("attempt %d: expected delay between %d-%d min, got %d", tc.attempt, tc.minDelay, tc.maxDelay, delay)
		}
	}
}

// ── Purge Tests ──────────────────────────────────────────────

func TestPurgeCompleted(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("purge@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.LeaseNext(ctx, "worker")
	qe.AckDelivered(ctx, entry.ID)

	// Purge everything completed more than 1 hour AGO.
	// Since entry was completed at nowFn() which is the fixed test time,
	// and nowFn() is NOT older than 1 hour ago, expect 0 purged.
	purged, err := qe.PurgeCompleted(ctx, nowFn().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged (completed before future cutoff), got %d", purged)
	}
}

func TestPurgeDeadLetters(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("purge-dl@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.DeadLetter(ctx, entry.ID, "failed")

	// All entries with dead_letter_at < cutoff. Since dead_letter_at = nowFn(),
	// and cutoff = nowFn().Add(1h), the condition is nowFn() < nowFn()+1h which
	// is TRUE, so the entry is purged.
	purged, err := qe.PurgeDeadLetters(ctx, nowFn().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("purge dl: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged, got %d", purged)
	}
}

// ── Dead Letter Operations Tests ─────────────────────────────

func TestListDeadLetters(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		entry := makeEntry(fmt.Sprintf("dl%d@test.com", i), "test.com")
		qe.Enqueue(ctx, entry)
		qe.DeadLetter(ctx, entry.ID, "reason")
	}

	entries, total, err := qe.Repo.ListDeadLetters(ctx, QueueFilter{}, nil)
	if err != nil {
		t.Fatalf("list dl: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 dead letters, got %d", total)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 results, got %d", len(entries))
	}
}

func TestRestoreDeadLetter(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry := makeEntry("restore@test.com", "test.com")
	qe.Enqueue(ctx, entry)
	qe.DeadLetter(ctx, entry.ID, "failed 5 times")

	if err := qe.Repo.RestoreDeadLetter(ctx, entry.ID, 10, nil); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got.Status != StatusPending {
		t.Fatalf("expected pending after restore, got %s", got.Status)
	}
	if got.MaxAttempts != 10 {
		t.Fatalf("expected max_attempts 10, got %d", got.MaxAttempts)
	}
}

// ── Metrics Tests ────────────────────────────────────────────

func TestMetrics(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		qe.Enqueue(ctx, makeEntry(fmt.Sprintf("m%d@a.com", i), "a.com"))
	}
	entry, _ := qe.LeaseNext(ctx, "w1")
	qe.AckDelivered(ctx, entry.ID)
	qe.Enqueue(ctx, makeEntry("bounce@b.com", "b.com"))
	entry2, _ := qe.LeaseNext(ctx, "w1")
	qe.Bounce(ctx, entry2.ID, "bad")

	metrics, err := qe.Metrics(ctx, nil)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if metrics.Pending != 4 {
		t.Fatalf("expected 4 pending (5 enqueued - 1 leased - 1 bounced), got %d", metrics.Pending)
	}
	if metrics.Delivered != 1 {
		t.Fatalf("expected 1 delivered, got %d", metrics.Delivered)
	}
	if metrics.Bounced != 1 {
		t.Fatalf("expected 1 bounced, got %d", metrics.Bounced)
	}
	if metrics.Total != 6 {
		t.Fatalf("expected total 6 (4 pending + 1 bounced + 1 delivered), got %d", metrics.Total)
	}
}

func TestMetricsPerTenant(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		e := makeEntry(fmt.Sprintf("t1-%d@a.com", i), "a.com")
		e.TenantID = 1
		qe.Enqueue(ctx, e)
	}
	for i := 0; i < 2; i++ {
		e := makeEntry(fmt.Sprintf("t2-%d@b.com", i), "b.com")
		e.TenantID = 2
		qe.Enqueue(ctx, e)
	}

	t1 := uint(1)
	m1, err := qe.Metrics(ctx, &t1)
	if err != nil {
		t.Fatalf("metrics t1: %v", err)
	}
	if m1.Pending != 3 {
		t.Fatalf("expected 3 pending for tenant 1, got %d", m1.Pending)
	}

	t2 := uint(2)
	m2, err := qe.Metrics(ctx, &t2)
	if err != nil {
		t.Fatalf("metrics t2: %v", err)
	}
	if m2.Pending != 2 {
		t.Fatalf("expected 2 pending for tenant 2, got %d", m2.Pending)
	}
}

// ── Retry Scheduler Tests ────────────────────────────────────

func TestRetrySchedulerProcessRetryQueue(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	// Enqueue 3 entries.
	var ids []uint
	for i := 0; i < 3; i++ {
		entry := makeEntry(fmt.Sprintf("rs-%d@test.com", i), "test.com")
		qe.Enqueue(ctx, entry)
		ids = append(ids, entry.ID)
	}

	// Manually set them to deferred with past next_attempt_at.
	past := nowFn().Add(-10 * time.Minute)
	for _, id := range ids {
		_, err := qe.DB.ExecContext(ctx, "UPDATE coremail_queue SET status=?, next_attempt_at=? WHERE id=?",
			string(StatusDeferred), past, id)
		if err != nil {
			t.Fatalf("set deferred: %v", err)
		}
	}

	// Verify all 3 are deferred.
	var deferredCount int64
	qe.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_queue WHERE status=?", string(StatusDeferred)).Scan(&deferredCount)
	if deferredCount != 3 {
		t.Fatalf("expected 3 deferred, got %d", deferredCount)
	}

	// Lease them back one by one (filtering ONLY deferred, not pending).
	leased := 0
	for {
		entry, err := qe.Repo.LeaseNext(ctx, "retry", DefaultLeaseSeconds, []QueueStatus{StatusDeferred}, nil)
		if err != nil {
			t.Fatalf("lease: %v", err)
		}
		if entry == nil {
			break
		}
		leased++
	}
	if leased != 3 {
		t.Fatalf("expected 3 leased from deferred, got %d", leased)
	}
}

func TestRetrySchedulerRetryAllDeadLetters(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()
	rs := NewRetryScheduler(qe)

	for i := 0; i < 2; i++ {
		entry := makeEntry(fmt.Sprintf("dlr-%d@test.com", i), "test.com")
		qe.Enqueue(ctx, entry)
		qe.DeadLetter(ctx, entry.ID, "failed")
	}

	count, err := rs.RetryAllDeadLetters(ctx, 10)
	if err != nil {
		t.Fatalf("retry all dl: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 retried, got %d", count)
	}
}

// ── Transaction Tests ────────────────────────────────────────

func TestTransactionalEnqueueAndLease(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	// Enqueue within a transaction.
	err := qe.WithTx(ctx, func(tx *sql.Tx) error {
		entry := &QueueEntry{
			TenantID:    1,
			DomainID:    1,
			MessageID:   "tx-test",
			FromAddress: "from@example.com",
			ToAddress:   "to@example.com",
			Direction:   DirectionOutbound,
		}
		if err := qe.Repo.Enqueue(ctx, entry, tx); err != nil {
			return err
		}
		// Lease within same transaction.
		leased, err := qe.Repo.LeaseNext(ctx, "tx-worker", 300, []QueueStatus{StatusPending}, tx)
		if err != nil {
			return err
		}
		if leased == nil {
			return fmt.Errorf("expected to lease within tx")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}

func TestTransactionRollback(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	// Intentionally fail a transaction after enqueue.
	err := qe.WithTx(ctx, func(tx *sql.Tx) error {
		entry := &QueueEntry{
			TenantID:  1,
			DomainID:  1,
			MessageID: "rollback-test",
			ToAddress: "to@example.com",
			Direction: DirectionOutbound,
		}
		if err := qe.Repo.Enqueue(ctx, entry, tx); err != nil {
			return err
		}
		// Force rollback by returning an error.
		return fmt.Errorf("intentional rollback")
	})
	if err == nil {
		t.Fatal("expected error from intentional rollback")
	}

	count, _ := qe.Repo.CountByStatus(ctx, StatusPending, nil, nil)
	if count != 0 {
		t.Fatalf("expected 0 pending after rollback, got %d", count)
	}
}

// ── Edge Cases ───────────────────────────────────────────────

func TestGetNonexistent(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()
	entry, err := qe.Repo.Get(ctx, 99999, nil)
	if err != nil {
		t.Fatalf("get nonexistent: %v", err)
	}
	if entry != nil {
		t.Fatal("expected nil for nonexistent entry")
	}
}

func TestLeaseNextEmptyQueue(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	entry, err := qe.LeaseNext(ctx, "worker")
	if err != nil {
		t.Fatalf("lease empty: %v", err)
	}
	if entry != nil {
		t.Fatal("expected nil for empty queue")
	}
}

func TestMultipleStatusesList(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	e1 := makeEntry("a@test.com", "test.com")
	qe.Enqueue(ctx, e1)
	e2 := makeEntry("b@test.com", "test.com")
	qe.Enqueue(ctx, e2)
	qe.AckDelivered(ctx, e1.ID)

	entries, total, _ := qe.Repo.List(ctx, QueueFilter{Statuses: []QueueStatus{StatusPending, StatusDelivered}}, nil)
	if total != 2 {
		t.Fatalf("expected 2 entries across statuses, got %d", total)
	}
	_ = entries
}

func TestDirectionFilter(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	in := DirectionInbound
	e := makeEntry("in@test.com", "test.com")
	e.Direction = DirectionInbound
	e.DeliveryMode = DeliveryLocal
	qe.Enqueue(ctx, e)

	entries, total, _ := qe.Repo.List(ctx, QueueFilter{Direction: &in}, nil)
	if total != 1 {
		t.Fatalf("expected 1 inbound, got %d", total)
	}
	_ = entries
}

func TestDeliveryModeClassification(t *testing.T) {
	entry := makeEntry("mode@test.com", "test.com")
	entry.DeliveryMode = DeliveryLocal
	if entry.DeliveryMode != DeliveryLocal {
		t.Fatal("delivery mode not preserved")
	}
}

func TestEnqueueMultipleMessages(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()

	for i := 0; i < 20; i++ {
		entry := makeEntry(fmt.Sprintf("multi-%d@test.com", i), "test.com")
		if err := qe.Enqueue(ctx, entry); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	count, _ := qe.Repo.CountByStatus(ctx, StatusPending, nil, nil)
	if count != 20 {
		t.Fatalf("expected 20 pending, got %d", count)
	}
}

func TestEntryZeroValues(t *testing.T) {
	entry := &QueueEntry{}
	if entry.AttemptCount != 0 {
		t.Fatal("expected zero attempt count")
	}
	if entry.LastError != "" {
		t.Fatal("expected empty last error")
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusPending != "pending" {
		t.Fatal("status mismatch")
	}
	if StatusLeased != "leased" {
		t.Fatal("status mismatch")
	}
	if StatusDelivered != "delivered" {
		t.Fatal("status mismatch")
	}
	if StatusBounced != "bounced" {
		t.Fatal("status mismatch")
	}
	if StatusDeadLetter != "dead_letter" {
		t.Fatal("status mismatch")
	}
	if StatusCancelled != "cancelled" {
		t.Fatal("status mismatch")
	}
}

func TestDirectionConstants(t *testing.T) {
	if DirectionInbound != "inbound" {
		t.Fatal("direction mismatch")
	}
	if DirectionOutbound != "outbound" {
		t.Fatal("direction mismatch")
	}
	if DirectionInternal != "internal" {
		t.Fatal("direction mismatch")
	}
}

// ── Helper ───────────────────────────────────────────────────

func uintPtr(u uint) *uint {
	return &u
}
