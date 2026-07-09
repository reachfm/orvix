// Package loadtest provides a scaffold for future database load-test
// benchmarks.  THIS PACKAGE DOES NOT YET CONNECT TO A REAL DATABASE.
//
// All tests in this package are in-memory or simulation-only; they
// prove that the benchmarking primitives (timers, counters, goroutine
// pools, cursor-pagination simulation) compile and behave correctly.
// The actual PostgreSQL/SQLite-backed heavy benchmark will be wired in
// a follow-up DB-2 PR once database opener integration is ready.
//
// DEFAULT (CI): `go test ./internal/storage/loadtest/` runs only the
// scaffold-config test. Always fast, always safe.
//
// HEAVY (future): Set ORVIX_RUN_DB_LOADTEST=1 to run the scaffold
// self-tests that exercise the benchmarking harness. They do NOT yet
// insert real rows into any database; they simulate concurrency and
// pagination timing patterns so the harness is proven before DB wiring.
//
// Usage:
//
//	go test ./internal/storage/loadtest/                                              # CI: config-only
//	ORVIX_RUN_DB_LOADTEST=1 go test -v -timeout 5m ./internal/storage/loadtest/ -run Scaffold  # harness self-test
package loadtest

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runHeavy reports whether the caller should run the harness self-tests.
// Only true when ORVIX_RUN_DB_LOADTEST=1 is set.
func runHeavy() bool {
	return strings.TrimSpace(os.Getenv("ORVIX_RUN_DB_LOADTEST")) == "1"
}

// Placeholder thresholds for future real-DB benchmarks.  These values
// are intentionally generous and will be tightened after measuring on
// real staging hardware (SSD-backed PostgreSQL).  They are NOT current
// performance claims — they are scaffolding constants.
const (
	targetRows        = 3_000_000 // future target row count
	minInsertRate     = 500       // rows/sec placeholder — measure on staging
	maxListLatencyMS  = 500       // placeholder — measure on staging
	maxFlagUpdateMS   = 100       // placeholder — measure on staging
	maxLeaseClaimMS   = 100       // placeholder — measure on staging
	maxSearchMS       = 2000      // placeholder — measure on staging
)

// TestScaffoldConfig verifies the load-test scaffold compiles and the
// threshold constants are sensible.  Runs in every CI pass.
func TestScaffoldConfig(t *testing.T) {
	t.Run("thresholds_are_positive", func(t *testing.T) {
		if minInsertRate <= 0 {
			t.Error("minInsertRate must be positive")
		}
		if maxListLatencyMS <= 0 {
			t.Error("maxListLatencyMS must be positive")
		}
		if maxFlagUpdateMS <= 0 {
			t.Error("maxFlagUpdateMS must be positive")
		}
		if maxLeaseClaimMS <= 0 {
			t.Error("maxLeaseClaimMS must be positive")
		}
		if maxSearchMS <= 0 {
			t.Error("maxSearchMS must be positive")
		}
	})

	t.Run("env_guard_works", func(t *testing.T) {
		if runHeavy() {
			t.Log("ORVIX_RUN_DB_LOADTEST=1 — scaffold self-tests will run")
		} else {
			t.Log("ORVIX_RUN_DB_LOADTEST not set — scaffold self-tests skipped")
		}
	})

	t.Run("targetRows_is_3M", func(t *testing.T) {
		if targetRows != 3_000_000 {
			t.Errorf("expected targetRows=3000000, got %d", targetRows)
		}
	})
}

// TestScaffoldSelfTests exercises the benchmarking harness primitives
// without connecting to any database.  These are in-memory concurrency
// and timing simulations that prove the scaffold compiles and runs.
//
// Requires ORVIX_RUN_DB_LOADTEST=1.  Skipped in normal CI.
//
// THIS TEST DOES NOT INSERT REAL DATABASE ROWS.  It simulates the
// timing/concurrency patterns the future real benchmark will use.
// Real DB wiring is deferred to DB-2.
func TestScaffoldSelfTests(t *testing.T) {
	if !runHeavy() {
		t.Skip("set ORVIX_RUN_DB_LOADTEST=1 to run scaffold self-tests")
	}

	t.Log("scaffold self-tests: verifying harness primitives (in-memory, no real DB)")

	t.Run("concurrency_primitives", func(t *testing.T) {
		var (
			total   int64
			workers = 4
			items   = 1000
			wg      sync.WaitGroup
		)
		start := time.Now()
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < items; i++ {
					buf := make([]byte, 256)
					_, _ = rand.Read(buf)
					_ = fmt.Sprintf("%x", buf)
					atomic.AddInt64(&total, 1)
				}
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)
		rate := float64(total) / elapsed.Seconds()
		t.Logf("concurrency primitives: %d items in %v (%.0f items/sec)", total, elapsed, rate)
		if total != int64(workers*items) {
			t.Errorf("expected %d items, got %d", workers*items, total)
		}
	})

	t.Run("cursor_pagination_timing", func(t *testing.T) {
		const (
			pageSize  = 50
			numPages  = 1000
			maxPerOp  = 50 * time.Millisecond
		)
		var totalPages int64
		start := time.Now()
		for p := 0; p < numPages; p++ {
			// In the real benchmark this will be a DB query with
			// WHERE id < cursor ORDER BY id DESC LIMIT 50.
			time.Sleep(1 * time.Microsecond)
			atomic.AddInt64(&totalPages, 1)
		}
		elapsed := time.Since(start)
		avgPerPage := elapsed / numPages
		t.Logf("cursor pagination timing: %d pages in %v (%.3v avg per page)", numPages, elapsed, avgPerPage)
		if avgPerPage > maxPerOp {
			t.Logf("TODO: tighten maxPerOp after real DB measurement (%v > %v)", avgPerPage, maxPerOp)
		}
	})

	t.Run("concurrent_update_timing", func(t *testing.T) {
		const (
			concurrency = 8
			batches     = 500
		)
		var total int64
		var wg sync.WaitGroup
		start := time.Now()
		for g := 0; g < concurrency; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < batches; i++ {
					time.Sleep(500 * time.Microsecond)
					atomic.AddInt64(&total, 1)
				}
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)
		avgPerUpdate := elapsed / time.Duration(total)
		t.Logf("concurrent update timing: %d batches in %v (%.3v avg per batch)", total, elapsed, avgPerUpdate)
	})

	t.Log("scaffold self-tests complete — all primitives work. Real DB wiring is DB-2.")
}

// --- Forward-looking helpers (used when real DB wiring exists) ---

type benchTimer struct {
	start time.Time
}

func startTimer() benchTimer {
	return benchTimer{start: time.Now()}
}

func (b benchTimer) elapsed() time.Duration {
	return time.Since(b.start)
}

func (b benchTimer) rate(count int64) float64 {
	return float64(count) / b.elapsed().Seconds()
}

// synthSubject returns a deterministic subject line for synthetic
// message rows.  Used by the future real-DB insert benchmark.
func synthSubject(seq int) string {
	topics := []string{
		"Q%d Budget Review",
		"Re: Meeting Notes %d",
		"Fwd: Invoice #%d",
		"Newsletter Edition %d",
		"Security Alert %d",
		"Welcome to Orvix %d",
		"Your order #%d has shipped",
		"Password reset request %d",
		"[spam] Special offer %d",
		"Build #%d succeeded",
	}
	return fmt.Sprintf(topics[seq%len(topics)], seq)
}
