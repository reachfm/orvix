package billing

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

func newUsageCounterStore(t *testing.T) *UsageCounterStore {
	t.Helper()
	db := setupTestDB(t)
	s := NewUsageCounterStore(db)
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUsageCounter_ReserveWithinLimitSucceeds(t *testing.T) {
	s := newUsageCounterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.EnsureCounter(ctx, 1, UsageMailboxes, 10, 0, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve(ctx, 1, UsageMailboxes, 1, now); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	c, err := s.Get(ctx, 1, UsageMailboxes)
	if err != nil {
		t.Fatal(err)
	}
	if c.Used != 1 {
		t.Fatalf("expected used=1, got %d", c.Used)
	}
}

func TestUsageCounter_ReserveOverLimitFailsWithQuotaExceeded(t *testing.T) {
	s := newUsageCounterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.EnsureCounter(ctx, 1, UsageMailboxes, 2, 2, now); err != nil {
		t.Fatal(err)
	}
	err := s.Reserve(ctx, 1, UsageMailboxes, 1, now)
	apiErr, ok := err.(*kernel.Error)
	if !ok || apiErr.Code != kernel.ErrCodeQuotaExceeded {
		t.Fatalf("expected ErrCodeQuotaExceeded, got %v", err)
	}
	c, _ := s.Get(ctx, 1, UsageMailboxes)
	if c.Used != 2 {
		t.Fatal("a failed reservation must not partially increment the counter")
	}
}

func TestUsageCounter_ReserveUnknownCounterIsNotFound(t *testing.T) {
	s := newUsageCounterStore(t)
	err := s.Reserve(context.Background(), 999, UsageMailboxes, 1, time.Now().UTC())
	apiErr, ok := err.(*kernel.Error)
	if !ok || apiErr.Code != kernel.ErrCodeNotFound {
		t.Fatalf("expected ErrCodeNotFound, got %v", err)
	}
}

func TestUsageCounter_ReleaseNeverGoesNegative(t *testing.T) {
	s := newUsageCounterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.EnsureCounter(ctx, 1, UsageMailboxes, 10, 0, now); err != nil {
		t.Fatal(err)
	}
	err := s.Release(ctx, 1, UsageMailboxes, 5, now) // releasing more than reserved
	apiErr, ok := err.(*kernel.Error)
	if !ok || apiErr.Code != kernel.ErrCodeQuotaExceeded {
		t.Fatalf("expected the over-release to be rejected, got %v", err)
	}
	c, _ := s.Get(ctx, 1, UsageMailboxes)
	if c.Used < 0 {
		t.Fatal("counter must never go negative")
	}
}

func TestUsageCounter_ReserveThenReleaseRoundTrips(t *testing.T) {
	s := newUsageCounterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.EnsureCounter(ctx, 1, UsageMailboxes, 10, 0, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve(ctx, 1, UsageMailboxes, 3, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, 1, UsageMailboxes, 3, now); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Get(ctx, 1, UsageMailboxes)
	if c.Used != 0 {
		t.Fatalf("expected used=0 after reserve+release round trip, got %d", c.Used)
	}
}

// TestUsageCounter_ConcurrentReservesNeverExceedLimit is the real
// concurrency-safety proof: 20 goroutines race to reserve 1 unit each
// against a counter with capacity for only 5. Exactly 5 must succeed and
// 15 must fail with QUOTA_EXCEEDED — never more than 5 succeeding (which
// would mean the atomic UPDATE's WHERE-guard has a race window) and
// never fewer (which would mean a correct reservation was wrongly denied).
func TestUsageCounter_ConcurrentReservesNeverExceedLimit(t *testing.T) {
	s := newUsageCounterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const limit = 5
	const attempts = 20
	if err := s.EnsureCounter(ctx, 1, UsageMailboxes, limit, 0, now); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = s.Reserve(ctx, 1, UsageMailboxes, 1, now)
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != limit {
		t.Fatalf("expected exactly %d successful reservations out of %d concurrent attempts, got %d", limit, attempts, succeeded)
	}
	c, err := s.Get(ctx, 1, UsageMailboxes)
	if err != nil {
		t.Fatal(err)
	}
	if c.Used != limit {
		t.Fatalf("expected final counter to equal the limit (%d), got %d — over-reservation occurred", limit, c.Used)
	}
}

func TestUsageCounter_Reconcile(t *testing.T) {
	s := newUsageCounterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.EnsureCounter(ctx, 1, UsageMailboxes, 10, 3, now); err != nil {
		t.Fatal(err)
	}
	// Drift: real data says there are actually only 2, not the recorded 3
	// (e.g. an out-of-band deletion bypassed Release).
	if err := s.Reconcile(ctx, 1, UsageMailboxes, 2, now); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get(ctx, 1, UsageMailboxes)
	if err != nil {
		t.Fatal(err)
	}
	if c.Used != 2 {
		t.Fatalf("expected reconcile to correct used to 2, got %d", c.Used)
	}
}

func TestUsageCounter_ReconcileUnknownCounterIsNotFound(t *testing.T) {
	s := newUsageCounterStore(t)
	err := s.Reconcile(context.Background(), 999, UsageMailboxes, 5, time.Now().UTC())
	apiErr, ok := err.(*kernel.Error)
	if !ok || apiErr.Code != kernel.ErrCodeNotFound {
		t.Fatalf("expected ErrCodeNotFound, got %v", err)
	}
}

func TestUsageCounter_EnsureCounterIsIdempotentAndNeverResetsUsed(t *testing.T) {
	s := newUsageCounterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.EnsureCounter(ctx, 1, UsageMailboxes, 10, 0, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve(ctx, 1, UsageMailboxes, 4, now); err != nil {
		t.Fatal(err)
	}
	// Calling EnsureCounter again (e.g. plan changed, limit updated) must
	// not wipe out the 4 already reserved.
	if err := s.EnsureCounter(ctx, 1, UsageMailboxes, 20, 0, now); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Get(ctx, 1, UsageMailboxes)
	if c.Used != 4 {
		t.Fatalf("expected used to remain 4 after re-calling EnsureCounter, got %d", c.Used)
	}
	if c.Limit != 20 {
		t.Fatalf("expected limit to update to 20, got %d", c.Limit)
	}
}
