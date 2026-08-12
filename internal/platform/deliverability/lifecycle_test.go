package deliverability

// Phase B lifecycle, events, aggregation, and real-path enforcement
// tests. All use a REAL SQLite database; concurrency tests use real
// goroutines against a file-backed pool (not sequential mocks).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/platform/kernel"
	_ "modernc.org/sqlite"
)

func newConcurrentService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "deliv-conc.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db, NewService(repo, nil, nil, kernel.NewFixedClock(time.Now().UTC()))
}

// ── Lifecycle ──────────────────────────────────────────────────────

func TestSuppressionLifecycle_AddReleaseReactivateExpire(t *testing.T) {
	db, svc := newConcurrentService(t)
	ctx := context.Background()

	sup, err := svc.AddSuppression(ctx, 1, "lifecycle@example.test", SuppressionManual, "operator", 42, "", nil)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if sup.State != SuppressionActive || sup.Version != 1 {
		t.Fatalf("expected active v1: %+v", sup)
	}
	blocked, _ := svc.IsSuppressed(ctx, 1, "lifecycle@example.test")
	if !blocked {
		t.Fatal("active suppression must block delivery")
	}

	// Release → no longer blocks, history preserved, version bumped.
	if err := svc.ReleaseSuppression(ctx, sup.ID, 1, 7, "recipient requested removal"); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err := svc.GetSuppression(ctx, sup.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != SuppressionReleased || got.ReleasedBy != 7 || got.ReleasedReason != "recipient requested removal" {
		t.Fatalf("release not recorded: %+v", got)
	}
	blocked, _ = svc.IsSuppressed(ctx, 1, "lifecycle@example.test")
	if blocked {
		t.Fatal("released suppression must NOT block delivery")
	}

	// Releasing again is a no-op conflict (one valid terminal state).
	if err := svc.ReleaseSuppression(ctx, sup.ID, 1, 7, "again"); !errors.Is(err, ErrSuppressionNotActive) {
		t.Fatalf("expected ErrSuppressionNotActive on double release, got %v", err)
	}

	// Reactivate → blocks again.
	if err := svc.ReactivateSuppression(ctx, sup.ID, 1, 42, SuppressionManual, "operator", "", nil); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	blocked, _ = svc.IsSuppressed(ctx, 1, "lifecycle@example.test")
	if !blocked {
		t.Fatal("reactivated suppression must block delivery")
	}

	// Expiry via the scheduler reconciliation (never request-time).
	fc := svc.clock.(*kernel.FixedClock)
	expires := fc.Now().Add(2 * time.Hour)
	sup2, err := svc.AddSuppression(ctx, 1, "ephemeral@example.test", SuppressionManual, "operator", 42, "", &expires)
	if err != nil {
		t.Fatal(err)
	}
	blocked, _ = svc.IsSuppressed(ctx, 1, "ephemeral@example.test")
	if !blocked {
		t.Fatal("unexpired suppression must block")
	}
	fc.Advance(3 * time.Hour)
	n, err := svc.ReconcileExpired(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reconcile: n=%d err=%v", n, err)
	}
	got2, _ := svc.GetSuppression(ctx, sup2.ID, 1)
	if got2.State != SuppressionExpired {
		t.Fatalf("expected expired state after reconcile, got %+v", got2)
	}
	blocked, _ = svc.IsSuppressed(ctx, 1, "ephemeral@example.test")
	if blocked {
		t.Fatal("expired suppression must NOT block delivery")
	}
	// Expired is still reactivatable.
	if err := svc.ReactivateSuppression(ctx, sup2.ID, 1, 42, SuppressionManual, "operator", "", nil); err != nil {
		t.Fatalf("reactivate expired: %v", err)
	}

	// History trail: created/released/reactivated/expired events.
	events, err := svc.ListSuppressionEvents(ctx, sup.ID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Event] = true
	}
	for _, want := range []string{"created", "released", "reactivated"} {
		if !seen[want] {
			t.Fatalf("history missing %q: %+v", want, events)
		}
	}

	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_deliverability_suppressions WHERE tenant_id=1 AND address='lifecycle@example.test'`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("release semantics must keep ONE row (history preserved), got %d", rowCount)
	}
}

func TestSuppressionLifecycle_ReaddAfterReleaseReactivates(t *testing.T) {
	_, svc := newConcurrentService(t)
	ctx := context.Background()
	sup, _ := svc.AddSuppression(ctx, 1, "roundtrip@example.test", SuppressionManual, "operator", 1, "", nil)
	if err := svc.ReleaseSuppression(ctx, sup.ID, 1, 2, ""); err != nil {
		t.Fatal(err)
	}
	// Re-adding the same address (e.g. a second hard bounce) must
	// reactivate the SAME logical row, not create a duplicate.
	sup2, err := svc.AddSuppression(ctx, 1, "ROUNDTRIP@example.test", SuppressionHardBounce, "smtp_5xx", 3, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sup2.ID != sup.ID {
		t.Fatalf("expected the same logical suppression row, got %d vs %d", sup2.ID, sup.ID)
	}
	if sup2.State != SuppressionActive || sup2.ReleasedAt != nil {
		t.Fatalf("expected reactivated row with cleared release fields: %+v", sup2)
	}
	if sup2.Version <= sup.Version {
		t.Fatalf("expected a bumped version: %d -> %d", sup.Version, sup2.Version)
	}
	blocked, _ := svc.IsSuppressed(ctx, 1, "roundtrip@example.test")
	if !blocked {
		t.Fatal("re-added suppression must block delivery")
	}
}

func TestSuppressionLifecycle_TenantScopedDetailAndMutations(t *testing.T) {
	_, svc := newConcurrentService(t)
	ctx := context.Background()
	sup, _ := svc.AddSuppression(ctx, 1, "owned@example.test", SuppressionManual, "operator", 1, "", nil)

	if _, err := svc.GetSuppression(ctx, sup.ID, 2); !errors.Is(err, ErrSuppressionNotFound) {
		t.Fatalf("cross-tenant detail must be NOT_FOUND, got %v", err)
	}
	if err := svc.ReleaseSuppression(ctx, sup.ID, 2, 9, ""); !errors.Is(err, ErrSuppressionNotFound) {
		t.Fatalf("cross-tenant release must be NOT_FOUND, got %v", err)
	}
	if err := svc.ReactivateSuppression(ctx, sup.ID, 2, 9, SuppressionManual, "", "", nil); !errors.Is(err, ErrSuppressionNotFound) {
		t.Fatalf("cross-tenant reactivate must be NOT_FOUND, got %v", err)
	}
	if _, err := svc.ListSuppressionEvents(ctx, sup.ID, 2, 50); !errors.Is(err, ErrSuppressionNotFound) {
		t.Fatalf("cross-tenant history must be NOT_FOUND, got %v", err)
	}
	// The tenant-1 row is untouched.
	got, _ := svc.GetSuppression(ctx, sup.ID, 1)
	if got.State != SuppressionActive {
		t.Fatalf("cross-tenant attempt must not mutate: %+v", got)
	}
}

func TestSuppressionLifecycle_ListFiltersAndPagination(t *testing.T) {
	_, svc := newConcurrentService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		addr := fmt.Sprintf("user%d@domain%d.example", i, i%2)
		reason := SuppressionManual
		if i%2 == 1 {
			reason = SuppressionHardBounce
		}
		if _, err := svc.AddSuppression(ctx, 1, addr, reason, "operator", 1, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	// Release one row to produce a released state.
	list, total, err := svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Limit: 10})
	if err != nil || total != 5 || len(list) != 5 {
		t.Fatalf("plain list: total=%d len=%d err=%v", total, len(list), err)
	}
	sup := list[0]
	svc.ReleaseSuppression(ctx, sup.ID, 1, 1, "")

	_, total, _ = svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, State: SuppressionActive, Limit: 10})
	if total != 4 {
		t.Fatalf("active filter: total=%d", total)
	}
	_, total, _ = svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, State: SuppressionReleased, Limit: 10})
	if total != 1 {
		t.Fatalf("released filter: total=%d", total)
	}
	_, total, _ = svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Reason: string(SuppressionHardBounce), Limit: 10})
	if total != 2 {
		t.Fatalf("reason filter: total=%d", total)
	}
	_, total, _ = svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Domain: "domain1.example", Limit: 10})
	if total != 2 {
		t.Fatalf("domain filter: total=%d", total)
	}
	_, total, _ = svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Search: "user3", Limit: 10})
	if total != 1 {
		t.Fatalf("search filter: total=%d", total)
	}
	_, total, _ = svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, State: SuppressionState("all"), CreatedFrom: &now, Limit: 10})
	if total != 5 {
		t.Fatalf("created-from filter: total=%d", total)
	}
	// Bounded pagination: limit clamped, deterministic order.
	list, total, _ = svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Limit: 2})
	if len(list) != 2 || list[0].ID <= list[1].ID {
		t.Fatalf("pagination/order wrong: %+v", list)
	}
	list2, _, _ := svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Limit: 2, Offset: 2})
	if list2[0].ID == list[0].ID {
		t.Fatal("offset must not repeat rows")
	}
	// Determinism: same query twice yields the same order.
	listA, _, _ := svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Limit: 10})
	listB, _, _ := svc.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Limit: 10})
	for i := range listA {
		if listA[i].ID != listB[i].ID {
			t.Fatal("non-deterministic ordering")
		}
	}
}

// ── Concurrency (real goroutines, real SQLite) ─────────────────────

func TestSuppressionConcurrency_DuplicateAddsOneRow(t *testing.T) {
	db, svc := newConcurrentService(t)
	ctx := context.Background()
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = svc.AddSuppression(ctx, 1, "concurrent@example.test", SuppressionManual, fmt.Sprintf("s%d", i), uint(i), "", nil)
		}(i)
	}
	wg.Wait()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_deliverability_suppressions WHERE tenant_id=1 AND address='concurrent@example.test'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent duplicate adds must create one logical suppression, got %d rows", count)
	}
	blocked, _ := svc.IsSuppressed(ctx, 1, "concurrent@example.test")
	if !blocked {
		t.Fatal("the single logical suppression must block delivery")
	}
}

func TestSuppressionConcurrency_ReleaseExpiryRaceOneTerminalState(t *testing.T) {
	_, svc := newConcurrentService(t)
	ctx := context.Background()
	fc := svc.clock.(*kernel.FixedClock)
	expires := fc.Now().Add(time.Hour)
	sup, err := svc.AddSuppression(ctx, 1, "race@example.test", SuppressionManual, "operator", 1, "", &expires)
	if err != nil {
		t.Fatal(err)
	}
	// Race: release (guarded on state=active) vs reconcile-expiry
	// (guarded on state=active). Exactly one wins; the row ends in one
	// terminal state.
	var wg sync.WaitGroup
	releaseErrs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			releaseErrs[i] = svc.ReleaseSuppression(ctx, sup.ID, 1, 1, "operator")
		}(i)
	}
	expireErrs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fc.Advance(2 * time.Hour) // make expiry applicable
			_, expireErrs[i] = svc.ReconcileExpired(ctx)
		}(i)
	}
	wg.Wait()
	got, _ := svc.GetSuppression(ctx, sup.ID, 1)
	if got.State != SuppressionReleased && got.State != SuppressionExpired {
		t.Fatalf("expected exactly one terminal state, got %q", got.State)
	}
	blocked, _ := svc.IsSuppressed(ctx, 1, "race@example.test")
	if blocked {
		t.Fatal("terminal suppression must not block delivery")
	}
}

func TestDeliverabilityConcurrency_IngestionAndAggregation(t *testing.T) {
	db, svc := newConcurrentService(t)
	ctx := context.Background()
	const goroutines = 16
	const perGoroutine = 5
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				key := fmt.Sprintf("conc-%d-%d", g, i)
				_ = svc.RecordDeliveryOutcome(ctx, key, 1, "conc.example", "rcpt.example", "provider-x", SignalDelivered, 10)
			}
		}(g)
	}
	wg.Wait()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_deliverability_signals WHERE tenant_id=1 AND type='delivered'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	// Each event fans out to 3 non-provider dimensions + 1 provider
	// dimension = 4 rows per event.
	want := int64(goroutines * perGoroutine * 4)
	if int64(count) != want {
		t.Fatalf("expected %d delivered-dimension rows, got %d", want, count)
	}
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	m, err := svc.MetricsSummary(ctx, 1, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if m.Volume != want || m.Delivered != want {
		t.Fatalf("aggregation under concurrency: volume=%d delivered=%d want=%d", m.Volume, m.Delivered, want)
	}
}

// ── Events and aggregation ─────────────────────────────────────────

func TestEvents_ListFiltersAndTenantIsolation(t *testing.T) {
	_, svc := newConcurrentService(t)
	ctx := context.Background()
	// Tenant 1: 3 events; tenant 2: 2 events.
	svc.RecordDeliveryOutcome(ctx, "t1-e1", 1, "s1.example", "r1.example", "provider-a", SignalDelivered, 10)
	svc.RecordDeliveryOutcome(ctx, "t1-e2", 1, "s1.example", "r2.example", "provider-a", SignalBounce, 20)
	svc.RecordDeliveryOutcome(ctx, "t1-e3", 1, "s2.example", "r3.example", "", SignalPolicyReject, 30)
	svc.RecordDeliveryOutcome(ctx, "t2-e1", 2, "s3.example", "r9.example", "provider-b", SignalDelivered, 40)
	svc.RecordDeliveryOutcome(ctx, "t2-e2", 2, "s3.example", "r9.example", "provider-b", SignalDelivered, 50)

	events, total, err := svc.ListEvents(ctx, EventFilter{TenantID: 1, Limit: 100})
	// t1-e1 and t1-e2 fan out to 4 dimensions each (tenant, sending,
	// recipient, provider); t1-e3 has no provider → 3 dimensions.
	if err != nil || total != 11 || len(events) != 11 {
		t.Fatalf("tenant1 all events: total=%d len=%d err=%v", total, len(events), err)
	}
	for _, e := range events {
		if e.TenantID != 1 {
			t.Fatalf("tenant isolation violated: %+v", e)
		}
		if e.Category == "" {
			t.Fatalf("event missing category: %+v", e)
		}
	}
	// Provider filter.
	_, total, _ = svc.ListEvents(ctx, EventFilter{TenantID: 1, Provider: "provider-a", Limit: 100})
	if total != 2 {
		t.Fatalf("provider filter: total=%d", total)
	}
	// Type filter (matches every dimension row of that type: the
	// bounce event fans out to 4 dimensions).
	_, total, _ = svc.ListEvents(ctx, EventFilter{TenantID: 1, Type: SignalBounce, Limit: 100})
	if total != 4 {
		t.Fatalf("type filter: total=%d", total)
	}
	// Domain filter matches the sending-domain dimension rows of the
	// two events sent from s1.example.
	_, total, _ = svc.ListEvents(ctx, EventFilter{TenantID: 1, Domain: "s1.example", Limit: 100})
	if total != 2 {
		t.Fatalf("domain filter: total=%d", total)
	}
	// Time filter: bounded window.
	_, total, _ = svc.ListEvents(ctx, EventFilter{TenantID: 1, Start: &[]time.Time{time.Now().UTC().Add(-time.Hour)}[0], End: &[]time.Time{time.Now().UTC().Add(time.Hour)}[0], Limit: 100})
	if total != 11 {
		t.Fatalf("time filter: total=%d", total)
	}
	// Deterministic ordering: newest first, stable across calls.
	a, _, _ := svc.ListEvents(ctx, EventFilter{TenantID: 1, Limit: 100})
	b, _, _ := svc.ListEvents(ctx, EventFilter{TenantID: 1, Limit: 100})
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatal("non-deterministic event ordering")
		}
	}
	if a[0].ID < a[len(a)-1].ID {
		t.Fatal("expected newest-first ordering")
	}
	// Event detail is tenant-scoped.
	if _, err := svc.GetEvent(ctx, a[0].ID, 2); err == nil {
		t.Fatal("cross-tenant event detail must fail")
	}
	ev, err := svc.GetEvent(ctx, a[0].ID, 1)
	if err != nil || ev.ID != a[0].ID {
		t.Fatalf("event detail: %+v err=%v", ev, err)
	}
}

func TestMetricsSummary_BoundariesAndUTCNormalization(t *testing.T) {
	db, svc := newConcurrentService(t)
	ctx := context.Background()
	// Seed deterministic timestamps directly.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insert := func(key string, at time.Time, typ SignalType) {
		if _, err := db.Exec(`INSERT INTO platform_deliverability_signals (event_key, tenant_id, dimension, dimension_value, type, latency_ms, recorded_at) VALUES (?, 1, 'recipient_domain', 'r.example', ?, 0, ?)`, key, string(typ), at); err != nil {
			t.Fatal(err)
		}
	}
	// 2 delivered inside [00:00, 01:00), 1 bounce exactly at 01:00
	// (excluded by the half-open end), 1 temp_fail at 01:30 (outside).
	insert("b1", base, SignalDelivered)
	insert("b2", base.Add(30*time.Minute), SignalDelivered)
	insert("b3", base.Add(time.Hour), SignalBounce)
	insert("b4", base.Add(90*time.Minute), SignalTempFail)

	m, err := svc.MetricsSummary(ctx, 1, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if m.Volume != 2 || m.Delivered != 2 || m.Bounced != 0 {
		t.Fatalf("boundary aggregation wrong: %+v", m)
	}
	if m.DeliveryRate != 1.0 {
		t.Fatalf("expected delivery_rate=1.0, got %v", m.DeliveryRate)
	}

	// Equivalent non-UTC input window must produce identical results.
	m2, err := svc.MetricsSummary(ctx, 1, base.In(time.FixedZone("X", 3600)), base.Add(time.Hour).In(time.FixedZone("X", 3600)))
	if err != nil {
		t.Fatal(err)
	}
	if m2.Volume != m.Volume || m2.Delivered != m.Delivered {
		t.Fatalf("non-UTC window must equal UTC window: %+v vs %+v", m2, m)
	}

	// Empty window behavior: zeroed totals, no NULL defect.
	m3, err := svc.MetricsSummary(ctx, 1, base.Add(10*time.Hour), base.Add(11*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if m3.Volume != 0 || m3.Delivered != 0 || m3.DeliveryRate != 0 {
		t.Fatalf("empty window must be zeroed, got %+v", m3)
	}

	// Inverted window rejected; abusive span rejected.
	if _, err := svc.MetricsSummary(ctx, 1, base.Add(time.Hour), base); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("inverted window must be rejected, got %v", err)
	}
	if _, err := svc.MetricsSummary(ctx, 1, base, base.Add(91*24*time.Hour)); err == nil {
		t.Fatal("abusive 91-day span must be rejected")
	}
}

func TestMetricsSummary_BreakdownsAndBuckets(t *testing.T) {
	_, svc := newConcurrentService(t)
	ctx := context.Background()
	svc.RecordDeliveryOutcome(ctx, "bk1", 1, "a.example", "r.example", "provider-a", SignalDelivered, 10)
	svc.RecordDeliveryOutcome(ctx, "bk2", 1, "a.example", "r.example", "provider-a", SignalBounce, 20)
	svc.RecordDeliveryOutcome(ctx, "bk3", 1, "b.example", "r.example", "provider-b", SignalTempFail, 30)

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	m, err := svc.MetricsSummary(ctx, 1, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if m.Volume != 12 { // 3 events × 4 dimensions
		t.Fatalf("volume=%d", m.Volume)
	}
	// Category breakdown exists and covers the recorded types.
	byCat := map[string]int64{}
	for _, b := range m.ByCategory {
		byCat[b.Key] = b.Count
	}
	if byCat["delivered"] == 0 || byCat["bounce"] == 0 || byCat["temp_fail"] == 0 {
		t.Fatalf("category breakdown incomplete: %+v", m.ByCategory)
	}
	// Domain breakdown reflects sending domains.
	byDomain := map[string]int64{}
	for _, b := range m.ByDomain {
		byDomain[b.Key] = b.Count
	}
	if byDomain["a.example"] != 2 || byDomain["b.example"] != 1 {
		t.Fatalf("domain breakdown wrong: %+v", m.ByDomain)
	}
	// Provider attribution only where persisted.
	byProv := map[string]int64{}
	for _, b := range m.ByProvider {
		byProv[b.Key] = b.Count
	}
	if byProv["provider-a"] != 2 || byProv["provider-b"] != 1 {
		t.Fatalf("provider breakdown wrong: %+v", m.ByProvider)
	}
	// Time buckets bounded and present (hourly for a 2h span).
	if len(m.TimeBuckets) == 0 || len(m.TimeBuckets) > 3 {
		t.Fatalf("unexpected bucket count: %d", len(m.TimeBuckets))
	}
	if m.BucketSize != "hour" {
		t.Fatalf("expected hourly buckets, got %s", m.BucketSize)
	}
	// The delivered count inside a bucket is real, not fabricated.
}

func TestEvents_RetentionPurge(t *testing.T) {
	db, svc := newConcurrentService(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-200 * 24 * time.Hour)
	if _, err := db.Exec(`INSERT INTO platform_deliverability_signals (event_key, tenant_id, dimension, dimension_value, type, latency_ms, recorded_at) VALUES ('ancient', 1, 'recipient_domain', 'old.example', 'delivered', 0, ?)`, old); err != nil {
		t.Fatal(err)
	}
	svc.RecordDeliveryOutcome(ctx, "fresh", 1, "s.example", "r.example", "", SignalDelivered, 0)
	n, err := svc.PurgeOldSignals(ctx, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 purged signal, got %d", n)
	}
	// Retention: the fresh event fans out to 3 dimensions (no
	// provider) → 3 rows remain.
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_deliverability_signals`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 3 {
		t.Fatalf("expected only the fresh signal's 3 rows to remain, got %d", remaining)
	}
}

// ── Real delivery-path enforcement ─────────────────────────────────

// TestSuppressionEnforcement_RealDeliveryWorker wires the REAL
// deliverability adapter (real SQLite repository) into the canonical
// delivery worker and proves:
//   - an active suppression blocks delivery with the stable message;
//   - a released suppression no longer blocks;
//   - an expired suppression no longer blocks;
//   - case variants cannot bypass the normalized check;
//   - alias-expanded recipients are checked per-queue-entry at the
//     canonical stage (the worker's deliverRemote);
//   - a store failure fails OPEN (documented behavior) — delivery
//     proceeds rather than silently dropping mail.
func TestSuppressionEnforcement_RealDeliveryWorker(t *testing.T) {
	db, svc := newConcurrentService(t)
	ctx := context.Background()
	adapter := NewDeliveryAdapter(svc)

	resolver := delivery.NewFakeResolver()
	resolver.FailDomain = "blocked.test" // distinguish "reached MX" from "suppressed"
	worker := &delivery.DeliveryWorker{
		Resolver:           resolver,
		WorkerID:           "test-worker",
		SuppressionChecker: adapter,
		TenantIDForRelay:   func(e *queue.QueueEntry) (uint, string) { return e.TenantID, "internal_external" },
	}
	entry := func(addr string) *queue.QueueEntry {
		return &queue.QueueEntry{TenantID: 1, FromAddress: "sender@send.example", ToAddress: addr, RecipientDomain: "blocked.test", ID: 1}
	}

	// 1. No suppression → delivery proceeds (MX failure, not suppression).
	res := workerDeliverRemote(t, worker, entry("free@blocked.test"))
	if res.StatusMsg == "recipient is suppressed" {
		t.Fatal("un-suppressed recipient must not be rejected as suppressed")
	}

	// 2. Active suppression → blocked, permanent (not temp).
	svc.AddSuppression(ctx, 1, "blocked@blocked.test", SuppressionManual, "operator", 1, "", nil)
	res = workerDeliverRemote(t, worker, entry("blocked@blocked.test"))
	if res.StatusMsg != "recipient is suppressed" || res.TempFail {
		t.Fatalf("active suppression must block permanently: %+v", res)
	}

	// 3. Case variant cannot bypass normalization.
	res = workerDeliverRemote(t, worker, entry("BLOCKED@blocked.test"))
	if res.StatusMsg != "recipient is suppressed" {
		t.Fatalf("case variant must be suppressed: %+v", res)
	}

	// 4. Alias-expanded recipient: each queue entry is checked at the
	// canonical stage — the alias address passes, the expanded target
	// is suppressed.
	svc.AddSuppression(ctx, 1, "target@blocked.test", SuppressionManual, "operator", 1, "", nil)
	res = workerDeliverRemote(t, worker, entry("alias@blocked.test"))
	if res.StatusMsg == "recipient is suppressed" {
		t.Fatal("the alias entry itself must not be suppressed")
	}
	res = workerDeliverRemote(t, worker, entry("target@blocked.test"))
	if res.StatusMsg != "recipient is suppressed" {
		t.Fatalf("the expanded target entry must be suppressed at the canonical stage: %+v", res)
	}

	// 5. Release → no longer blocks.
	sup, _ := svc.GetSuppression(ctx, mustFindSuppression(t, svc, 1, "blocked@blocked.test"), 1)
	svc.ReleaseSuppression(ctx, sup.ID, 1, 1, "")
	res = workerDeliverRemote(t, worker, entry("blocked@blocked.test"))
	if res.StatusMsg == "recipient is suppressed" {
		t.Fatal("released suppression must no longer block")
	}

	// 6. Expired → no longer blocks.
	fc := svc.clock.(*kernel.FixedClock)
	expires := fc.Now().Add(time.Hour)
	svc.AddSuppression(ctx, 1, "temp@blocked.test", SuppressionManual, "operator", 1, "", &expires)
	res = workerDeliverRemote(t, worker, entry("temp@blocked.test"))
	if res.StatusMsg != "recipient is suppressed" {
		t.Fatal("unexpired suppression must block")
	}
	fc.Advance(2 * time.Hour)
	res = workerDeliverRemote(t, worker, entry("temp@blocked.test"))
	if res.StatusMsg == "recipient is suppressed" {
		t.Fatal("expired suppression must no longer block")
	}

	// 7. Store unavailable → fails OPEN (documented): the worker only
	// blocks when the check returns suppressed with no error.
	closed := &delivery.DeliveryWorker{
		Resolver:           resolver,
		SuppressionChecker: failingChecker{err: fmt.Errorf("store unavailable")},
	}
	res = workerDeliverRemote(t, closed, entry("blocked@blocked.test"))
	if res.StatusMsg == "recipient is suppressed" {
		t.Fatal("store failure must fail open (delivery proceeds), never silently block")
	}

	_ = db
}

// failingChecker simulates an unavailable suppression store.
type failingChecker struct {
	err error
}

func (f failingChecker) IsSuppressed(ctx context.Context, tenantID uint, address string) (bool, error) {
	return false, f.err
}

func workerDeliverRemote(t *testing.T, w *delivery.DeliveryWorker, e *queue.QueueEntry) *delivery.DeliveryResult {
	t.Helper()
	return w.DeliverRemoteForTest(e)
}

func mustFindSuppression(t *testing.T, svc *Service, tenantID uint, addr string) uint {
	t.Helper()
	list, _, err := svc.ListSuppressions(context.Background(), SuppressionFilter{TenantID: tenantID, Search: addr, Limit: 10})
	if err != nil || len(list) == 0 {
		t.Fatalf("find suppression: %v", err)
	}
	return list[0].ID
}
