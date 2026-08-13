package queue

// F6 lease-fencing tests: a worker whose lease expired and was
// re-claimed must not be able to complete the message; recovery
// releases an expired lease exactly once and only one worker can hold
// it afterwards.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// isBusy reports the SQLite transient-lock error text.
func isBusy(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "database is locked")
}

// TestF6_StaleWorkerCannotAckReclaimedMessage proves the fenced ack
// affects zero rows once the lease belongs to another worker.
func TestF6_StaleWorkerCannotAckReclaimedMessage(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()
	entry := makeEntry("stale@test.com", "test.com")
	if err := qe.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := qe.LeaseNext(ctx, "worker-a"); err != nil {
		t.Fatalf("lease by worker-a: %v", err)
	}
	// worker-a loses its lease (expiry simulated by time travel) and
	// worker-b reclaims the message.
	released, err := qe.ReleaseExpiredLeases(ctx)
	_ = released
	_ = err
	now := nowFn()
	future := now.Add(2 * time.Hour)
	nowFn = func() time.Time { return future }
	if _, err := qe.ReleaseExpiredLeases(ctx); err != nil {
		t.Fatalf("release expired: %v", err)
	}
	if _, err := qe.LeaseNext(ctx, "worker-b"); err != nil {
		t.Fatalf("lease by worker-b: %v", err)
	}

	// The stale worker must NOT complete the message.
	err = qe.AckDeliveredForOwner(ctx, entry.ID, "worker-a")
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker ack must fail with ErrLeaseLost, got %v", err)
	}

	// The current owner can still complete it.
	if err := qe.AckDeliveredForOwner(ctx, entry.ID, "worker-b"); err != nil {
		t.Fatalf("current owner ack must succeed, got %v", err)
	}
	got, err := qe.Repo.Get(ctx, entry.ID, nil)
	if err != nil || got.Status != StatusDelivered {
		t.Fatalf("expected delivered status, got %v err=%v", got, err)
	}
}

// TestF6_ConcurrentRecoverySingleWinner proves ReleaseExpiredLeases is
// safe under concurrent recovery calls: each expired lease is released
// exactly once (guarded update, RowsAffected), and only one worker can
// reclaim each message afterwards.
func TestF6_ConcurrentRecoverySingleWinner(t *testing.T) {
	_, qe := testQE(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		e := makeEntry("rec-"+string(rune('a'+i))+"@test.com", "test.com")
		if err := qe.Enqueue(ctx, e); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if _, err := qe.LeaseNext(ctx, "old-worker"); err != nil {
			t.Fatalf("lease: %v", err)
		}
	}
	future := nowFn().Add(2 * time.Hour)
	nowFn = func() time.Time { return future }

	var wg sync.WaitGroup
	var total int64
	var mu sync.Mutex
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// ReleaseExpiredLeases is a single guarded UPDATE; transient
			// SQLITE_BUSY from concurrent writers is retried (the DSN's
			// busy_timeout covers most of it; a bounded retry covers the
			// remainder without masking correctness — the count must still
			// be exactly 10, proving each lease released once).
			var n int64
			var err error
			for attempt := 0; attempt < 10; attempt++ {
				n, err = qe.ReleaseExpiredLeases(ctx)
				if err == nil || !isBusy(err) {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if err != nil {
				t.Errorf("recover: %v", err)
				return
			}
			mu.Lock()
			total += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 10 {
		t.Fatalf("exactly 10 expired leases must be released across concurrent recovery, got %d", total)
	}

	// After recovery, exactly one worker can claim each message; no
	// message is claimed twice.
	claimed := map[uint]int{}
	for i := 0; i < 10; i++ {
		entry, err := qe.LeaseNext(ctx, "new-worker")
		if err != nil {
			t.Fatalf("reclaim: %v", err)
		}
		if entry == nil {
			t.Fatal("expected a reclaimed message")
		}
		claimed[entry.ID]++
	}
	for id, n := range claimed {
		if n != 1 {
			t.Fatalf("message %d claimed %d times", id, n)
		}
	}
}
