package auth

// Behavioural parity suite: MemoryLimitStore and RedisLimitStore MUST
// implement the SAME fixed-window semantics — same limits, windows, key
// isolation and reset behaviour — so a deployment behaves identically with
// and without Redis. The suite runs one scenario table against every
// available store.
//
// Redis coverage: when ORVIX_REDIS_TEST_ADDR is set it is used directly;
// otherwise a disposable redis:7-alpine container is started with Docker
// when Docker is available. When neither is available the Redis half is
// skipped with an explicit message and the memory half still runs.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// ── Disposable Redis ────────────────────────────────────────────────────────

func startDisposableRedis(t testing.TB) string {
	t.Helper()
	if addr := os.Getenv("ORVIX_REDIS_TEST_ADDR"); addr != "" {
		return addr
	}

	// Pick a free port by binding :0 and releasing it. The race window is
	// acceptable for a test container.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ""
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	container := fmt.Sprintf("orvix-h6-redis-%d", time.Now().UnixNano())
	cmd := exec.Command("docker", "run", "-d", "--rm",
		"--name", container,
		"-p", fmt.Sprintf("127.0.0.1:%d:6379", port),
		"redis:7-alpine")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("docker run redis unavailable (%v): %s", err, strings.TrimSpace(string(out)))
		return ""
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: time.Second})
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := client.Ping(context.Background()).Err(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = exec.Command("docker", "rm", "-f", container).Run()
			t.Logf("redis:%d did not become ready; skipping Redis parity", port)
			return ""
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = client.Close()
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", container).Run()
	})
	return addr
}

// ── Parity runner ───────────────────────────────────────────────────────────

// listCounts performs the steps and returns the observed counts.
func listCounts(ctx context.Context, store LimitStore, window time.Duration, keys []string) []int64 {
	outs := make([]int64, 0, len(keys))
	for _, k := range keys {
		c, err := store.Incr(ctx, k, window)
		if err != nil {
			panic(err) // scenario bug, not a store failure
		}
		outs = append(outs, c)
	}
	return outs
}

// parityScenario asserts both stores agree on every step of one scenario.
// Expects a fresh store (new keys) per run.
func parityScenario(t *testing.T, name string, window time.Duration, scenario func(ctx context.Context, s LimitStore) error) {
	t.Helper()
	ctx := context.Background()

	run := func(label string, s LimitStore) {
		t.Helper()
		if err := scenario(ctx, s); err != nil {
			t.Errorf("%s [%s]: %v", name, label, err)
		}
	}

	run("memory", NewMemoryLimitStore())
	if addr := startDisposableRedis(t); addr != "" {
		client := redis.NewClient(&redis.Options{Addr: addr})
		defer client.Close()
		run("redis", &RedisLimitStore{client: client})
	}
}

func TestParityFixedWindowCounts(t *testing.T) {
	parityScenario(t, "fixed-window-counts", time.Hour, func(ctx context.Context, s LimitStore) error {
		seq := []string{"k1", "k1", "k1", "k2", "k1", "k2", "k2", "k2", "k2"}
		want := []int64{1, 2, 3, 1, 4, 2, 3, 4, 5}
		for i, k := range seq {
			got, err := s.Incr(ctx, k, time.Hour)
			if err != nil {
				return err
			}
			if got != want[i] {
				return fmt.Errorf("step %d key %s: got %d, want %d", i, k, got, want[i])
			}
		}
		return nil
	})
}

func TestParityFixedWindowNotSliding(t *testing.T) {
	// A TTL re-armed on every hit would slide the window and let a steady
	// attacker keep a counter alive forever. Both stores must use FIXED
	// windows: the expiry must be armed once, on first use.
	//
	// Redis has no injectable clock; a single short sleep is the honest way
	// to observe expiry on a real server. The window is the minimum go-redis
	// accepts (1s) and the sleep is a fixed 2s — no polling loops.
	window := time.Second
	parityScenario(t, "fixed-window-not-sliding", window, func(ctx context.Context, s LimitStore) error {
		if _, err := s.Incr(ctx, "k", window); err != nil {
			return err
		}
		if _, err := s.Incr(ctx, "k", window); err != nil {
			return err
		}
		time.Sleep(2 * time.Second)
		got, err := s.Incr(ctx, "k", window)
		if err != nil {
			return err
		}
		if got != 1 {
			return fmt.Errorf("window slid: after expiry got %d, want 1 (fixed window)", got)
		}
		return nil
	})
}

func TestParityReset(t *testing.T) {
	parityScenario(t, "reset", time.Hour, func(ctx context.Context, s LimitStore) error {
		for i := 0; i < 3; i++ {
			if _, err := s.Incr(ctx, "k", time.Hour); err != nil {
				return err
			}
		}
		if err := s.Reset(ctx, "k"); err != nil {
			return err
		}
		got, err := s.Incr(ctx, "k", time.Hour)
		if err != nil {
			return err
		}
		if got != 1 {
			return fmt.Errorf("after reset got %d, want 1", got)
		}
		return nil
	})
}

func TestParityKeyIsolation(t *testing.T) {
	parityScenario(t, "key-isolation", time.Hour, func(ctx context.Context, s LimitStore) error {
		want := []int64{1, 1, 1}
		got := listCounts(ctx, s, time.Hour, []string{"a", "b", "c"})
		for i := range want {
			if got[i] != want[i] {
				return fmt.Errorf("key %d: got %d, want %d (keys must be isolated)", i, got[i], want[i])
			}
		}
		return nil
	})
}

func TestParityConcurrentIncrements(t *testing.T) {
	// INCR (Redis) and the mutex (memory) must both land every increment:
	// 8 goroutines × 25 on one key = exactly 200, then the next read is 201.
	parityScenario(t, "concurrent-increments", time.Hour, func(ctx context.Context, s LimitStore) error {
		done := make(chan error, 8)
		for g := 0; g < 8; g++ {
			go func() {
				for i := 0; i < 25; i++ {
					if _, err := s.Incr(ctx, "hot", time.Hour); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
		}
		for g := 0; g < 8; g++ {
			if err := <-done; err != nil {
				return err
			}
		}
		got, err := s.Incr(ctx, "hot", time.Hour)
		if err != nil {
			return err
		}
		if got != 201 {
			return fmt.Errorf("concurrent count = %d, want 201 (increments lost)", got)
		}
		return nil
	})
}

// ── Guard: RedisLimitStore fails cleanly without a client ──────────────────

func TestRedisLimitStoreNoClientFails(t *testing.T) {
	s := &RedisLimitStore{}
	if _, err := s.Incr(context.Background(), "k", time.Minute); err == nil {
		t.Fatal("expected error with nil client")
	}
	if err := s.Reset(context.Background(), "k"); err == nil {
		t.Fatal("expected error with nil client")
	}
}
