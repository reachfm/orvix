package messagetrace

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/orvix/orvix/internal/coremail/queue"
	_ "modernc.org/sqlite"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/trace_test.db", t.TempDir()))
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

	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_delivery_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT, queue_entry_id INTEGER NOT NULL,
		attempt_number INTEGER NOT NULL, status TEXT NOT NULL,
		remote_host TEXT NOT NULL DEFAULT '', remote_ip TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0, status_msg TEXT NOT NULL DEFAULT '',
		enhanced_code TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
		tls_used INTEGER NOT NULL DEFAULT 0, worker_id TEXT NOT NULL DEFAULT '',
		attempted_at DATETIME NOT NULL
	)`)

	qe := queue.NewQueueEngine(db)
	return NewService(qe, db)
}

func enqueue(t *testing.T, svc *Service, msgID, from, to, domain string, status queue.QueueStatus) uint {
	t.Helper()
	entry := &queue.QueueEntry{
		MessageID: msgID, FromAddress: from, ToAddress: to,
		RecipientDomain: domain, Direction: queue.DirectionOutbound,
		DeliveryMode: queue.DeliveryRemoteSMTP, Status: status, MaxAttempts: 3,
	}
	svc.queueRepo.Enqueue(context.Background(), entry, nil)
	return entry.ID
}

func TestSearchByMessageID(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	enqueue(t, svc, "msg-001", "a@x.com", "b@y.com", "y.com", queue.StatusPending)

	resp, err := svc.Search(ctx, "msg-001", "", "", "", 100, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 result, got %d", resp.Total)
	}
}

func TestSearchBySender(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	enqueue(t, svc, "m1", "sender@x.com", "r@y.com", "y.com", queue.StatusPending)
	enqueue(t, svc, "m2", "other@z.com", "r@y.com", "y.com", queue.StatusPending)

	resp, err := svc.Search(ctx, "", "sender@x.com", "", "", 100, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 result, got %d", resp.Total)
	}
}

func TestSearchByRecipient(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	enqueue(t, svc, "m1", "a@x.com", "target@y.com", "y.com", queue.StatusPending)
	enqueue(t, svc, "m2", "a@x.com", "other@z.com", "z.com", queue.StatusPending)

	resp, err := svc.Search(ctx, "", "", "target@y.com", "", 100, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 result, got %d", resp.Total)
	}
}

func TestSearchByDomain(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	enqueue(t, svc, "m1", "a@x.com", "r@y.com", "y.com", queue.StatusPending)
	enqueue(t, svc, "m2", "a@x.com", "r@z.com", "z.com", queue.StatusPending)

	resp, err := svc.Search(ctx, "", "", "", "y.com", 100, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 result, got %d", resp.Total)
	}
}

func TestGetTrace(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	id := enqueue(t, svc, "trace-msg", "a@x.com", "b@y.com", "y.com", queue.StatusDelivered)

	detail, err := svc.GetTrace(ctx, id)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if detail == nil {
		t.Fatal("expected non-nil detail")
	}
	if detail.Entry.MessageID != "trace-msg" {
		t.Fatalf("expected trace-msg, got %s", detail.Entry.MessageID)
	}
}

func TestTraceTimeline(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	id := enqueue(t, svc, "timeline-msg", "a@x.com", "b@y.com", "y.com", queue.StatusDelivered)

	detail, err := svc.GetTrace(ctx, id)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(detail.Timeline) < 2 {
		t.Fatalf("expected at least 2 timeline events (received, queued), got %d", len(detail.Timeline))
	}
}

func TestTraceWithAttempts(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	// Create entry and insert attempt manually.
	id := enqueue(t, svc, "attempt-msg", "a@x.com", "b@y.com", "y.com", queue.StatusDeferred)

	svc.attemptsDB.ExecContext(ctx,
		`INSERT INTO coremail_delivery_attempts (queue_entry_id, attempt_number, status, status_msg, remote_host, remote_ip, attempted_at)
		 VALUES (?, 1, 'deferred', 'Connection refused', 'mail.y.com', '1.2.3.4', datetime('now'))`, id)

	detail, err := svc.GetTrace(ctx, id)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(detail.Attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(detail.Attempts))
	}
	if detail.Attempts[0].Status != "deferred" {
		t.Fatalf("expected deferred, got %s", detail.Attempts[0].Status)
	}
}

func TestTraceNotFound(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	detail, err := svc.GetTrace(ctx, 99999)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if detail != nil {
		t.Fatal("expected nil for non-existent entry")
	}
}
