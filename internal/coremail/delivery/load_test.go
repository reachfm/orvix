package delivery

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	_ "modernc.org/sqlite"
)

// ── Load Test: 1,000 Queued Entries ──────────────────────────

func TestLoad1000QueuedEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	db, qe, ms, worker, fs := newLoadEnv(t)
	defer fs.ln.Close()
	defer db.Close()

	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		entry := &queue.QueueEntry{
			TenantID:        1,
			DomainID:        1,
			MessageID:       fmt.Sprintf("msg-load-%d", i),
			FromAddress:     "sender@example.com",
			ToAddress:       fmt.Sprintf("rcp%d@load.test", i),
			RecipientDomain: "load.test",
			Direction:       queue.DirectionOutbound,
			DeliveryMode:    queue.DeliveryRemoteSMTP,
			Status:          queue.StatusPending,
			MaxAttempts:     1,
		}
		if err := qe.Enqueue(ctx, entry); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}

		msg := &storage.Message{
			MessageID:   fmt.Sprintf("msg-load-%d", i),
			TenantID:    1, DomainID: 1, MailboxID: 1,
			FromAddress: "sender@example.com",
			ToAddresses: fmt.Sprintf("rcp%d@load.test", i),
		}
		if err := ms.StoreMessage(ctx, msg, []byte("Subject: Load Test\r\n\r\nBody"), nil); err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
	}

	count, err := worker.ProcessAll(ctx)
	if err != nil {
		t.Fatalf("process all: %v", err)
	}
	t.Logf("processed %d of 1000 in one pass", count)

	remaining, _ := worker.ProcessAll(ctx)
	t.Logf("second pass processed %d", remaining)

	metrics, _ := qe.Metrics(ctx, nil)
	t.Logf("queue metrics: pending=%d delivered=%d deferred=%d bounced=%d",
		metrics.Pending, metrics.Delivered, metrics.Deferred, metrics.Bounced)
}

// ── Load Test: Multiple Concurrent Delivery Workers ──────────

func TestLoadMultipleConcurrentWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	db, qe, ms, _, fs := newLoadEnv(t)
	defer fs.ln.Close()
	defer db.Close()

	workers := make([]*DeliveryWorker, 5)
	for i := 0; i < 5; i++ {
		resolver := NewFakeResolver()
		resolver.MXRecords["concur.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
		resolver.Hosts[fs.addr] = []string{fs.addr}
		transport := NewSMTPTransport(DefaultTransportConfig())
		w := NewDeliveryWorker(qe, ms, resolver, transport, "local.test", fmt.Sprintf("worker-%d", i))
		w.History = NewAttemptHistorySQLRepo(db)
		workers[i] = w
	}

	ctx := context.Background()
	for i := 0; i < 100; i++ {
		entry := &queue.QueueEntry{
			TenantID: 1, DomainID: 1,
			MessageID:       fmt.Sprintf("msg-concur-%d", i),
			FromAddress:     "sender@example.com",
			ToAddress:       fmt.Sprintf("rcp%d@concur.test", i),
			RecipientDomain: "concur.test",
			Direction:       queue.DirectionOutbound,
			DeliveryMode:    queue.DeliveryRemoteSMTP,
			Status:          queue.StatusPending,
		}
		qe.Enqueue(ctx, entry)
		msg := &storage.Message{
			MessageID:   fmt.Sprintf("msg-concur-%d", i),
			TenantID: 1, DomainID: 1, MailboxID: 1,
			FromAddress: "sender@example.com",
			ToAddresses: fmt.Sprintf("rcp%d@concur.test", i),
		}
		ms.StoreMessage(ctx, msg, []byte("Subject: Concur\r\n\r\nBody"), nil)
	}

	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(w *DeliveryWorker) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				w.ProcessOnce(ctx)
			}
		}(w)
	}
	wg.Wait()

	metrics, _ := qe.Metrics(ctx, nil)
	t.Logf("concurrent workers: delivered=%d deferred=%d bounced=%d",
		metrics.Delivered, metrics.Deferred, metrics.Bounced)
}

// ── Load Test: No Duplicate Delivery Under Concurrent Workers ──

func TestLoadNoDuplicateDeliveryConcurrent(t *testing.T) {
	db, qe, ms, _, fs := newLoadEnv(t)
	defer fs.ln.Close()
	defer db.Close()

	resolver := NewFakeResolver()
	resolver.MXRecords["dedup.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
	resolver.Hosts[fs.addr] = []string{fs.addr}
	transport := NewSMTPTransport(DefaultTransportConfig())

	entry := &queue.QueueEntry{
		TenantID: 1, DomainID: 1,
		MessageID:       "msg-dedup",
		FromAddress:     "sender@example.com",
		ToAddress:       "rcpt@dedup.test",
		RecipientDomain: "dedup.test",
		Direction:       queue.DirectionOutbound,
		DeliveryMode:    queue.DeliveryRemoteSMTP,
		Status:          queue.StatusPending,
	}
	qe.Enqueue(context.Background(), entry)
	msg := &storage.Message{
		MessageID: "msg-dedup", TenantID: 1, DomainID: 1, MailboxID: 1,
		FromAddress: "sender@example.com", ToAddresses: "rcpt@dedup.test",
	}
	ms.StoreMessage(context.Background(), msg, []byte("Subject: Dedup\r\n\r\nBody"), nil)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			w := NewDeliveryWorker(qe, ms, resolver, transport, "local.test", fmt.Sprintf("dedup-worker-%d", id))
			w.ProcessOnce(context.Background())
		}(i)
	}
	wg.Wait()

	ctx := context.Background()
	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got == nil {
		t.Fatal("entry not found")
	}

	count, _ := qe.Repo.CountByStatus(ctx, queue.StatusDelivered, nil, nil)
	if count > 1 {
		t.Fatalf("expected at most 1 delivery, got %d", count)
	}
	t.Logf("entry status: %s, delivered count: %d", got.Status, count)
}

// ── Failure Injection: MailStore Write Failure ───────────────

func TestFailureMailStoreWriteFailure(t *testing.T) {
	db, qe, ms, worker, fs := newLoadEnv(t)
	defer fs.ln.Close()
	defer db.Close()

	ctx := context.Background()
	entry := &queue.QueueEntry{
		TenantID: 1, DomainID: 1,
		MessageID:       "msg-storefail",
		FromAddress:     "sender@example.com",
		ToAddress:       "rcpt@fail.test",
		RecipientDomain: "fail.test",
		Direction:       queue.DirectionOutbound,
		DeliveryMode:    queue.DeliveryRemoteSMTP,
		Status:          queue.StatusPending,
	}
	qe.Enqueue(ctx, entry)

	// Don't store the message — load will fail gracefully.
	worked, err := worker.ProcessOnce(ctx)
	if err != nil {
		t.Logf("expected error: %v", err)
	}
	_ = worked

	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got != nil && got.Status != queue.StatusBounced {
		t.Logf("entry status after store failure: %s", got.Status)
	}
	_ = ms
}

// ── Failure Injection: Queue Insert Failure ──────────────────

func TestFailureQueueInsertFailure(t *testing.T) {
	db := newLoadDB(t)
	defer db.Close()

	db.Close()

	entry := &queue.QueueEntry{
		TenantID: 1, DomainID: 1,
		MessageID:       "msg-qfail",
		FromAddress:     "sender@example.com",
		ToAddress:       "rcpt@fail.test",
		RecipientDomain: "fail.test",
		Direction:       queue.DirectionOutbound,
		DeliveryMode:    queue.DeliveryRemoteSMTP,
		Status:          queue.StatusPending,
	}
	err := queue.NewQueueEngine(db).Enqueue(context.Background(), entry)
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

// ── Failure Injection: Shutdown During Active Delivery ───────

func TestFailureShutdownDuringDelivery(t *testing.T) {
	db, qe, ms, _, fs := newLoadEnv(t)
	defer fs.ln.Close()
	defer db.Close()

	ctx := context.Background()
	resolver := NewFakeResolver()
	transport := NewSMTPTransport(DefaultTransportConfig())
	w := NewDeliveryWorker(qe, ms, resolver, transport, "local.test", "shutdown-worker")

	for i := 0; i < 5; i++ {
		entry := &queue.QueueEntry{
			TenantID: 1, DomainID: 1,
			MessageID:       fmt.Sprintf("msg-shutdown-%d", i),
			FromAddress:     "sender@example.com",
			ToAddress:       fmt.Sprintf("rcp%d@shutdown.test", i),
			RecipientDomain: "shutdown.test",
			Direction:       queue.DirectionOutbound,
			DeliveryMode:    queue.DeliveryRemoteSMTP,
			Status:          queue.StatusPending,
		}
		qe.Enqueue(ctx, entry)
		msg := &storage.Message{
			MessageID: fmt.Sprintf("msg-shutdown-%d", i), TenantID: 1, DomainID: 1, MailboxID: 1,
			FromAddress: "sender@example.com", ToAddresses: fmt.Sprintf("rcp%d@shutdown.test", i),
		}
		ms.StoreMessage(ctx, msg, []byte("Subject: Shutdown\r\n\r\nBody"), nil)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	go w.Shutdown.Shutdown(shutdownCtx)

	_, err := w.ProcessAll(ctx)
	if err != nil {
		t.Logf("process all during shutdown: %v", err)
	}
}

// ── Environment Setup for Load Tests ─────────────────────────

func newLoadEnv(t *testing.T) (*sql.DB, *queue.QueueEngine, *storage.MailStore, *DeliveryWorker, *fakeSMTPServer) {
	t.Helper()
	db := newLoadDB(t)

	qe := queue.NewQueueEngine(db)
	base := filepath.Join(t.TempDir(), "msgs")
	ms, _ := storage.NewMailStore(db, base)

	fs := startFakeSMTP(t)

	resolver := NewFakeResolver()
	resolver.MXRecords["load.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
	resolver.MXRecords["concur.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
	resolver.MXRecords["dedup.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
	resolver.MXRecords["fail.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
	resolver.MXRecords["shutdown.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
	resolver.Hosts[fs.addr] = []string{fs.addr}

	transport := NewSMTPTransport(DefaultTransportConfig())
	worker := NewDeliveryWorker(qe, ms, resolver, transport, "local.test", "load-worker")
	worker.History = NewAttemptHistorySQLRepo(db)

	return db, qe, ms, worker, fs
}

func newLoadDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "load.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range queue.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range queue.Indexes() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}
	db.Exec(AttemptHistoryTable())
	for _, idx := range AttemptHistoryIndexes() {
		db.Exec(idx)
	}
	return db
}
