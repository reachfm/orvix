package queuemgmt

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/queue"
	_ "modernc.org/sqlite"
)

type mockAttemptRepo struct{}

func (m *mockAttemptRepo) ListByEntry(ctx context.Context, queueEntryID uint, tx interface{}) ([]interface{}, error) {
	return nil, nil
}

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/queue_test.db", t.TempDir()))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range queue.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	for _, stmt := range queue.Indexes() {
		db.Exec(stmt)
	}

	// Create delivery_attempts table
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_delivery_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		queue_entry_id INTEGER NOT NULL,
		attempt_number INTEGER NOT NULL,
		status TEXT NOT NULL,
		remote_host TEXT NOT NULL DEFAULT '',
		remote_ip TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		status_msg TEXT NOT NULL DEFAULT '',
		enhanced_code TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER NOT NULL DEFAULT 0,
		tls_used INTEGER NOT NULL DEFAULT 0,
		worker_id TEXT NOT NULL DEFAULT '',
		attempted_at DATETIME NOT NULL
	)`)

	qe := queue.NewQueueEngine(db)
	return NewService(qe, &mockAttemptRepo{})
}

func enqueueTestEntry(t *testing.T, svc *Service, status queue.QueueStatus) uint {
	t.Helper()
	entry := &queue.QueueEntry{
		MessageID:       fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		FromAddress:     "sender@test.com",
		ToAddress:       "rcpt@test.com",
		RecipientDomain: "test.com",
		Direction:       queue.DirectionOutbound,
		DeliveryMode:    queue.DeliveryRemoteSMTP,
		Status:          status,
		MaxAttempts:     3,
	}
	svc.queue.Enqueue(context.Background(), entry)
	return entry.ID
}

func TestQueueSummary(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	enqueueTestEntry(t, svc, queue.StatusPending)
	enqueueTestEntry(t, svc, queue.StatusDeferred)

	summary, err := svc.GetSummary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Total <= 0 {
		t.Fatal("expected total > 0")
	}
}

func TestListEntries(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	enqueueTestEntry(t, svc, queue.StatusPending)
	enqueueTestEntry(t, svc, queue.StatusPending)

	resp, err := svc.ListEntries(ctx, "", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("expected 2 entries, got %d", resp.Total)
	}
}

func TestListEntriesFilterByStatus(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	enqueueTestEntry(t, svc, queue.StatusPending)
	enqueueTestEntry(t, svc, queue.StatusDeferred)

	resp, err := svc.ListEntries(ctx, "deferred", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 deferred, got %d", resp.Total)
	}
}

func TestGetEntry(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	id := enqueueTestEntry(t, svc, queue.StatusPending)
	entry, err := svc.GetEntry(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if entry.ID != id {
		t.Fatalf("expected id %d, got %d", id, entry.ID)
	}
}

func TestListAttempts(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	id := enqueueTestEntry(t, svc, queue.StatusPending)
	attempts, err := svc.ListAttempts(ctx, id)
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	// Mock repo returns empty — no error means it works.
	_ = attempts
}

func TestRetryDeferred(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	id := enqueueTestEntry(t, svc, queue.StatusDeferred)
	if err := svc.RetryEntry(ctx, id); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestRetryDeadLetter(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	id := enqueueTestEntry(t, svc, queue.StatusDeadLetter)
	if err := svc.RetryEntry(ctx, id); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestRetryInvalidStatusRejected(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	id := enqueueTestEntry(t, svc, queue.StatusDelivered)
	if err := svc.RetryEntry(ctx, id); err == nil {
		t.Fatal("expected error for retry delivered entry")
	}
}

func TestCancelPending(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	id := enqueueTestEntry(t, svc, queue.StatusPending)
	if err := svc.CancelEntry(ctx, id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func TestCancelDeferred(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	id := enqueueTestEntry(t, svc, queue.StatusDeferred)
	if err := svc.CancelEntry(ctx, id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func TestCancelInvalidStatusRejected(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	id := enqueueTestEntry(t, svc, queue.StatusDelivered)
	if err := svc.CancelEntry(ctx, id); err == nil {
		t.Fatal("expected error for cancel delivered entry")
	}
}

func TestGetEntryNotFound(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	entry, err := svc.GetEntry(ctx, 99999)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if entry != nil {
		t.Fatal("expected nil for non-existent entry")
	}
}
