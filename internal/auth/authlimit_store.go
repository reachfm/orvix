package auth

import (
	"context"
	"sync"
	"time"
)

// LimitStore is the fixed-window counter backing authentication throttling.
//
// Two implementations exist and MUST behave identically (same limits, windows,
// key dimensions and reset semantics): RedisLimitStore for multi-node
// deployments, MemoryLimitStore for single-node ones and as the degraded
// fallback when Redis is unreachable. The parity suite in
// authlimit_parity_test.go runs one behavioural table against both.
type LimitStore interface {
	// Incr increments key's counter, (re)arming its expiry to window on
	// first use, and returns the post-increment count.
	Incr(ctx context.Context, key string, window time.Duration) (int64, error)
	// Reset clears key entirely.
	Reset(ctx context.Context, key string) error
}

// ── In-memory store ─────────────────────────────────────────────────────────

// memoryCounter is one fixed window.
type memoryCounter struct {
	count     int64
	expiresAt time.Time
}

// MemoryLimitStore is a bounded, concurrency-safe, in-process fixed-window
// counter.
//
// Bounded is the operative word: an attacker rotating identifiers would
// otherwise grow the map without limit and turn the limiter into a memory
// exhaustion vector. Once maxEntries is reached the store sweeps expired
// windows, and if that frees nothing it refuses to track NEW keys rather than
// grow — existing counters keep working, so an attacker cannot evict a
// counter that is currently throttling them.
type MemoryLimitStore struct {
	mu         sync.Mutex
	entries    map[string]*memoryCounter
	maxEntries int
	now        func() time.Time
}

// DefaultMemoryLimitEntries bounds the in-memory store.
const DefaultMemoryLimitEntries = 50_000

// NewMemoryLimitStore builds an in-memory store with the default bound.
func NewMemoryLimitStore() *MemoryLimitStore {
	return &MemoryLimitStore{
		entries:    make(map[string]*memoryCounter),
		maxEntries: DefaultMemoryLimitEntries,
		now:        time.Now,
	}
}

// SetClock overrides the time source (tests use it to advance windows
// deterministically instead of sleeping).
func (s *MemoryLimitStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetMaxEntries overrides the bound. Values <= 0 are ignored.
func (s *MemoryLimitStore) SetMaxEntries(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxEntries = n
}

func (s *MemoryLimitStore) Incr(_ context.Context, key string, window time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	if e, ok := s.entries[key]; ok {
		if now.Before(e.expiresAt) {
			e.count++
			return e.count, nil
		}
		// Window elapsed: start a fresh one.
		e.count = 1
		e.expiresAt = now.Add(window)
		return 1, nil
	}

	if len(s.entries) >= s.maxEntries {
		s.sweepLocked(now)
		if len(s.entries) >= s.maxEntries {
			// Refuse to grow. Report the limit as already reached so the
			// caller throttles rather than failing open under pressure.
			return int64(s.maxEntries), nil
		}
	}
	s.entries[key] = &memoryCounter{count: 1, expiresAt: now.Add(window)}
	return 1, nil
}

func (s *MemoryLimitStore) Reset(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
	return nil
}

// sweepLocked removes elapsed windows. Caller holds the mutex.
func (s *MemoryLimitStore) sweepLocked(now time.Time) {
	for k, e := range s.entries {
		if !now.Before(e.expiresAt) {
			delete(s.entries, k)
		}
	}
}

// Len reports the number of tracked keys (test/observability helper).
func (s *MemoryLimitStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
