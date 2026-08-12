package deliverability

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/delivery"
	_ "modernc.org/sqlite"
)

func newTestService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db, NewService(repo, nil, nil, nil)
}

func TestRecordDeliveryOutcome_FansOutAcrossDimensions(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	if err := svc.RecordDeliveryOutcome(ctx, "evt-1", 1, "sender.test", "recipient.test", "provider-a", SignalDelivered, 120); err != nil {
		t.Fatalf("record: %v", err)
	}

	now := time.Now().UTC()
	window := now.Add(time.Hour)
	for _, tc := range []struct {
		dim Dimension
		val string
	}{
		{DimensionTenant, "1"},
		{DimensionSendingDomain, "sender.test"},
		{DimensionRecipientDomain, "recipient.test"},
		{DimensionRelayProvider, "provider-a"},
	} {
		m, err := svc.Metrics(ctx, tc.dim, tc.val, now.Add(-time.Hour), window)
		if err != nil {
			t.Fatalf("metrics for %s: %v", tc.dim, err)
		}
		if m.Volume != 1 || m.Delivered != 1 {
			t.Fatalf("dimension %s=%s: expected volume=1 delivered=1, got %+v", tc.dim, tc.val, m)
		}
	}
}

func TestRecordDeliveryOutcome_IdempotentOnSameEventKey(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := svc.RecordDeliveryOutcome(ctx, "evt-dup", 1, "sender.test", "recipient.test", "", SignalDelivered, 0); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	m, err := svc.Metrics(ctx, DimensionTenant, "1", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if m.Volume != 1 {
		t.Fatalf("expected exactly 1 recorded signal despite 3 identical calls, got volume=%d", m.Volume)
	}
}

func TestAggregate_ComputesRatesCorrectly(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	events := []struct {
		key string
		typ SignalType
	}{
		{"e1", SignalDelivered}, {"e2", SignalDelivered}, {"e3", SignalDelivered},
		{"e4", SignalBounce}, {"e5", SignalTempFail},
	}
	for _, e := range events {
		if err := svc.RecordDeliveryOutcome(ctx, e.key, 1, "s.test", "r.test", "", e.typ, 0); err != nil {
			t.Fatalf("record %s: %v", e.key, err)
		}
	}
	m, err := svc.Metrics(ctx, DimensionRecipientDomain, "r.test", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if m.Volume != 5 || m.Delivered != 3 || m.Bounced != 1 || m.TempFail != 1 {
		t.Fatalf("unexpected counts: %+v", m)
	}
	if m.DeliveryRate != 0.6 {
		t.Fatalf("expected delivery_rate=0.6, got %v", m.DeliveryRate)
	}
	if m.BounceRate != 0.2 {
		t.Fatalf("expected bounce_rate=0.2, got %v", m.BounceRate)
	}
}

func TestAggregate_WindowExcludesOutOfRangeSignals(t *testing.T) {
	db, svc := newTestService(t)
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour)
	// Insert directly to control the timestamp precisely.
	if _, err := db.Exec(`INSERT INTO platform_deliverability_signals (event_key, tenant_id, dimension, dimension_value, type, latency_ms, recorded_at) VALUES (?, 1, 'recipient_domain', 'r.test', 'delivered', 0, ?)`, "old-evt", old); err != nil {
		t.Fatalf("seed old signal: %v", err)
	}
	if err := svc.RecordDeliveryOutcome(ctx, "recent-evt", 1, "s.test", "r.test", "", SignalDelivered, 0); err != nil {
		t.Fatalf("record recent: %v", err)
	}
	m, err := svc.Metrics(ctx, DimensionRecipientDomain, "r.test", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if m.Volume != 1 {
		t.Fatalf("expected the 48h-old signal excluded from a 1h window, got volume=%d", m.Volume)
	}
}

func TestSuppression_AddCheckRemove(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()

	suppressed, err := svc.IsSuppressed(ctx, 1, "user@example.test")
	if err != nil || suppressed {
		t.Fatalf("expected not suppressed initially, err=%v suppressed=%v", err, suppressed)
	}

	if _, err := svc.AddSuppression(ctx, 1, "USER@Example.test", SuppressionManual, "operator", 42, "spam complaints", nil); err != nil {
		t.Fatalf("add suppression: %v", err)
	}
	suppressed, err = svc.IsSuppressed(ctx, 1, "user@example.test")
	if err != nil || !suppressed {
		t.Fatalf("expected suppressed (case-insensitive), err=%v suppressed=%v", err, suppressed)
	}

	if err := svc.RemoveSuppression(ctx, 1, "user@example.test", 42); err != nil {
		t.Fatalf("remove suppression: %v", err)
	}
	suppressed, _ = svc.IsSuppressed(ctx, 1, "user@example.test")
	if suppressed {
		t.Fatal("expected not suppressed after removal")
	}
}

func TestSuppression_ExpiresAutomatically(t *testing.T) {
	db, svc := newTestService(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Minute)
	if _, err := db.Exec(`INSERT INTO platform_deliverability_suppressions (tenant_id, address, reason, source, created_at, expires_at) VALUES (1, 'expired@test.com', 'manual', 'operator', ?, ?)`, time.Now(), past); err != nil {
		t.Fatalf("seed: %v", err)
	}
	suppressed, err := svc.IsSuppressed(ctx, 1, "expired@test.com")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if suppressed {
		t.Fatal("expected an already-expired suppression to be treated as not-suppressed")
	}
}

func TestSuppression_TenantIsolation(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.AddSuppression(ctx, 1, "shared@test.com", SuppressionManual, "operator", 0, "", nil)

	t1, _ := svc.IsSuppressed(ctx, 1, "shared@test.com")
	t2, _ := svc.IsSuppressed(ctx, 2, "shared@test.com")
	if !t1 {
		t.Fatal("expected tenant 1 to see its own suppression")
	}
	if t2 {
		t.Fatal("expected tenant 2 to be unaffected by tenant 1's suppression")
	}
}

func TestSuppression_RemoveNonexistentReturnsTypedError(t *testing.T) {
	_, svc := newTestService(t)
	if err := svc.RemoveSuppression(context.Background(), 1, "nobody@test.com", 0); err != ErrSuppressionNotFound {
		t.Fatalf("expected ErrSuppressionNotFound, got %v", err)
	}
}

func TestRecordFromBounce_UserUnknownTriggersHardBounceSuppression(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	result := &delivery.DeliveryResult{Success: false, TempFail: false, StatusCode: 550, StatusMsg: "user unknown"}
	if err := svc.RecordFromBounce(ctx, "evt-hb", 1, "sender.test", "gone@recipient.test", "", result, 1); err != nil {
		t.Fatalf("record from bounce: %v", err)
	}
	suppressed, err := svc.IsSuppressed(ctx, 1, "gone@recipient.test")
	if err != nil || !suppressed {
		t.Fatalf("expected the user-unknown bounce to trigger suppression, err=%v suppressed=%v", err, suppressed)
	}
}

func TestRecordFromBounce_TempFailDoesNotSuppress(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	result := &delivery.DeliveryResult{Success: false, TempFail: true, StatusCode: 450, StatusMsg: "mailbox busy"}
	if err := svc.RecordFromBounce(ctx, "evt-temp", 1, "sender.test", "busy@recipient.test", "", result, 1); err != nil {
		t.Fatalf("record from bounce: %v", err)
	}
	suppressed, _ := svc.IsSuppressed(ctx, 1, "busy@recipient.test")
	if suppressed {
		t.Fatal("a temporary failure must never trigger suppression")
	}
}

// ── Feedback ingestion ────────────────────────────────────────────

func TestIngestFeedback_IdempotentReplay(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	ev := FeedbackEvent{ProviderEventID: "provider-evt-1", TenantID: 1, Address: "complainer@test.com", Type: SignalComplaint, RawSource: "test-provider"}

	processed1, err := svc.IngestFeedback(ctx, ev)
	if err != nil || !processed1 {
		t.Fatalf("first ingest: processed=%v err=%v", processed1, err)
	}
	processed2, err := svc.IngestFeedback(ctx, ev)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if processed2 {
		t.Fatal("expected the duplicate event to be reported as already-processed, not processed again")
	}

	m, _ := svc.Metrics(ctx, DimensionRecipientDomain, "test.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if m.Complaints != 1 {
		t.Fatalf("expected exactly 1 complaint recorded despite 2 ingest calls, got %d", m.Complaints)
	}
}

func TestIngestFeedback_ComplaintTriggersSuppression(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	ev := FeedbackEvent{ProviderEventID: "provider-evt-2", TenantID: 1, Address: "unhappy@test.com", Type: SignalComplaint, RawSource: "test-provider"}
	if _, err := svc.IngestFeedback(ctx, ev); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	suppressed, err := svc.IsSuppressed(ctx, 1, "unhappy@test.com")
	if err != nil || !suppressed {
		t.Fatalf("expected a complaint to trigger suppression, err=%v suppressed=%v", err, suppressed)
	}
}

func TestIngestFeedback_RejectsMalformedEvent(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	_, err := svc.IngestFeedback(ctx, FeedbackEvent{ProviderEventID: "", Address: "x@test.com", Type: SignalComplaint})
	if err == nil {
		t.Fatal("expected a missing provider event id to be rejected")
	}
	_, err = svc.IngestFeedback(ctx, FeedbackEvent{ProviderEventID: "evt", Address: "", Type: SignalComplaint})
	if err == nil {
		t.Fatal("expected a missing address to be rejected")
	}
	_, err = svc.IngestFeedback(ctx, FeedbackEvent{ProviderEventID: "evt", Address: "x@test.com", Type: "bogus"})
	if err == nil {
		t.Fatal("expected an invalid signal type to be rejected")
	}
}

// ── Concurrency ───────────────────────────────────────────────────

func TestRecordDeliveryOutcome_ConcurrentSameEventKeyRecordsOnce(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	const goroutines = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.RecordDeliveryOutcome(ctx, "race-evt", 1, "s.test", "r.test", "", SignalDelivered, 0)
		}()
	}
	wg.Wait()
	m, err := svc.Metrics(ctx, DimensionRecipientDomain, "r.test", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if m.Volume != 1 {
		t.Fatalf("expected exactly 1 signal despite %d concurrent identical calls, got %d", goroutines, m.Volume)
	}
}

func TestAddSuppression_ConcurrentUpsertsNeverDuplicate(t *testing.T) {
	db, svc := newTestService(t)
	ctx := context.Background()
	const goroutines = 15
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc.AddSuppression(ctx, 1, "race@test.com", SuppressionManual, fmt.Sprintf("source-%d", i), 0, "", nil)
		}(i)
	}
	wg.Wait()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_deliverability_suppressions WHERE tenant_id=1 AND address='race@test.com'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 suppression row despite %d concurrent adds, got %d", goroutines, count)
	}
}
