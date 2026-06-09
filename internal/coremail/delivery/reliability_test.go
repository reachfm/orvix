package delivery

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ── Retry Policy Tests ───────────────────────────────────────

func TestRetryPolicyScheduleAttempt1(t *testing.T) {
	p := FastRetryPolicy()
	d, err := p.RetrySchedule(1)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if d < 0 {
		t.Fatal("expected non-negative delay")
	}
}

func TestRetryPolicyScheduleExponentialGrowth(t *testing.T) {
	p := RetryPolicy{BaseDelay: 10 * time.Millisecond, Multiplier: 2.0, MaxDelay: 1 * time.Second, MaxAttempts: 5, JitterPercent: 0}
	d1, _ := p.RetrySchedule(1)
	d2, _ := p.RetrySchedule(2)
	d3, _ := p.RetrySchedule(3)
	if d2 <= d1 {
		t.Fatal("expected d2 > d1 (exponential growth)")
	}
	if d3 <= d2 {
		t.Fatal("expected d3 > d2 (exponential growth)")
	}
}

func TestRetryPolicyMaxDelayCap(t *testing.T) {
	p := RetryPolicy{BaseDelay: 1 * time.Second, Multiplier: 1000, MaxDelay: 5 * time.Second, MaxAttempts: 10, JitterPercent: 0}
	d, err := p.RetrySchedule(5)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if d > p.MaxDelay {
		t.Fatalf("expected delay <= %v, got %v", p.MaxDelay, d)
	}
}

func TestRetryPolicyExceedsMaxAttempts(t *testing.T) {
	p := FastRetryPolicy()
	_, err := p.RetrySchedule(p.MaxAttempts + 1)
	if err == nil {
		t.Fatal("expected error for exceeding max attempts")
	}
}

func TestRetryPolicyInvalidAttempt(t *testing.T) {
	p := FastRetryPolicy()
	_, err := p.RetrySchedule(0)
	if err == nil {
		t.Fatal("expected error for attempt 0")
	}
}

func TestRetryPolicyNextAttemptAt(t *testing.T) {
	p := FastRetryPolicy()
	now := time.Now()
	next, err := p.NextAttemptAt(1, now)
	if err != nil {
		t.Fatalf("next attempt: %v", err)
	}
	if next.Before(now) {
		t.Fatal("next attempt should be in the future")
	}
}

func TestRetryPolicyDefaultHasSaneValues(t *testing.T) {
	p := DefaultRetryPolicy()
	if p.BaseDelay <= 0 {
		t.Fatal("expected positive base delay")
	}
	if p.MaxAttempts <= 0 {
		t.Fatal("expected positive max attempts")
	}
	if p.MaxDelay < p.BaseDelay {
		t.Fatal("expected max delay >= base delay")
	}
}

// ── Retry Decision Tests ─────────────────────────────────────

func TestClassifyResultSuccess(t *testing.T) {
	p := FastRetryPolicy()
	res := &DeliveryResult{Success: true}
	decision, _, err := p.ClassifyResult(res, 1)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if decision != DecisionDelivered {
		t.Fatal("expected decision: delivered")
	}
}

func TestClassifyResultRetry(t *testing.T) {
	p := FastRetryPolicy()
	res := &DeliveryResult{Success: false, TempFail: true}
	decision, nextTime, err := p.ClassifyResult(res, 1)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if decision != DecisionRetry {
		t.Fatal("expected decision: retry")
	}
	if nextTime.IsZero() {
		t.Fatal("expected non-zero next attempt time")
	}
}

func TestClassifyResultDeadLetter(t *testing.T) {
	p := FastRetryPolicy()
	res := &DeliveryResult{Success: false, TempFail: false}
	decision, _, err := p.ClassifyResult(res, 1)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if decision != DecisionDeadLetter {
		t.Fatal("expected decision: dead_letter for permanent failure")
	}
}

func TestClassifyResultMaxAttemptsReached(t *testing.T) {
	p := FastRetryPolicy()
	res := &DeliveryResult{Success: false, TempFail: true}
	decision, _, err := p.ClassifyResult(res, p.MaxAttempts)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if decision != DecisionDeadLetter {
		t.Fatal("expected decision: dead_letter at max attempts")
	}
}

// ── Delivery Attempt History Tests ───────────────────────────

func historyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(AttemptHistoryTable()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, idx := range AttemptHistoryIndexes() {
		db.Exec(idx)
	}
	return db
}

func TestAttemptHistoryRecordAndList(t *testing.T) {
	db := historyDB(t)
	repo := NewAttemptHistorySQLRepo(db)
	ctx := context.Background()

	a := &DeliveryAttempt{
		QueueEntryID:  1,
		AttemptNumber: 1,
		Status:        "deferred",
		RemoteHost:    "mx.example.com",
		RemoteIP:      "192.0.2.1",
		StatusCode:    450,
		StatusMsg:     "4.2.1 Mailbox busy",
		EnhancedCode:  "4.2.1",
		DurationMs:    1234,
		TLSUsed:       false,
		WorkerID:      "worker-1",
	}
	if err := repo.RecordAttempt(ctx, a, nil); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	attempts, err := repo.ListByEntry(ctx, 1, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(attempts))
	}
	if attempts[0].Status != "deferred" {
		t.Fatalf("expected deferred, got %s", attempts[0].Status)
	}
	if attempts[0].EnhancedCode != "4.2.1" {
		t.Fatalf("expected enhanced code 4.2.1, got %s", attempts[0].EnhancedCode)
	}
}

func TestAttemptHistoryMultipleAttempts(t *testing.T) {
	db := historyDB(t)
	repo := NewAttemptHistorySQLRepo(db)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		repo.RecordAttempt(ctx, &DeliveryAttempt{
			QueueEntryID: 1, AttemptNumber: i, Status: "deferred",
			StatusCode: 450, WorkerID: "w1",
		}, nil)
	}

	count, err := repo.CountByEntry(ctx, 1, nil)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3, got %d", count)
	}

	last, err := repo.LastAttempt(ctx, 1, nil)
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	if last == nil || last.AttemptNumber != 3 {
		t.Fatalf("expected attempt 3 as last, got %d", last.AttemptNumber)
	}
}

func TestAttemptHistoryLastNonexistent(t *testing.T) {
	db := historyDB(t)
	repo := NewAttemptHistorySQLRepo(db)
	ctx := context.Background()

	last, err := repo.LastAttempt(ctx, 999, nil)
	if err != nil {
		t.Fatalf("last nonexistent: %v", err)
	}
	if last != nil {
		t.Fatal("expected nil for nonexistent entry")
	}
}

func TestAttemptHistoryListEmpty(t *testing.T) {
	db := historyDB(t)
	repo := NewAttemptHistorySQLRepo(db)
	ctx := context.Background()

	attempts, err := repo.ListByEntry(ctx, 999, nil)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(attempts) != 0 {
		t.Fatalf("expected 0, got %d", len(attempts))
	}
}

// ── Shutdown Manager Tests ───────────────────────────────────

func TestShutdownManagerBeginEndJob(t *testing.T) {
	sm := NewShutdownManager()
	if ok := sm.BeginJob(); !ok {
		t.Fatal("expected BeginJob to succeed before shutdown")
	}
	if sm.ActiveJobs() != 1 {
		t.Fatalf("expected 1 active job, got %d", sm.ActiveJobs())
	}
	sm.EndJob()
	if sm.ActiveJobs() != 0 {
		t.Fatalf("expected 0 active jobs, got %d", sm.ActiveJobs())
	}
}

func TestShutdownManagerBlocksNewJobs(t *testing.T) {
	sm := NewShutdownManager()
	ctx := context.Background()
	<-sm.Shutdown(ctx)

	if ok := sm.BeginJob(); ok {
		t.Fatal("expected BeginJob to fail after shutdown")
	}
}

func TestShutdownManagerIsShutdown(t *testing.T) {
	sm := NewShutdownManager()
	if sm.IsShutdown() {
		t.Fatal("expected not shutdown initially")
	}
	<-sm.Shutdown(context.Background())
	if !sm.IsShutdown() {
		t.Fatal("expected shutdown after Shutdown()")
	}
}

func TestShutdownManagerChannelClosed(t *testing.T) {
	sm := NewShutdownManager()
	select {
	case <-sm.ShutdownRequested():
		t.Fatal("channel should not be closed before shutdown")
	default:
	}
	<-sm.Shutdown(context.Background())
	select {
	case <-sm.ShutdownRequested():
		// Expected — channel is closed.
	default:
		t.Fatal("channel should be closed after shutdown")
	}
}

func TestShutdownManagerConcurrentJobs(t *testing.T) {
	sm := NewShutdownManager()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sm.BeginJob() {
				time.Sleep(time.Millisecond)
				sm.EndJob()
			}
		}()
	}
	wg.Wait()
	if sm.ActiveJobs() != 0 {
		t.Fatalf("expected 0 active, got %d", sm.ActiveJobs())
	}
}

// ── Worker Crash Recovery Tests ──────────────────────────────

func TestWorkerCrashRecoveryRecoverCount(t *testing.T) {
	r := NewWorkerCrashRecovery(nil, "worker-1", 300)
	if r.RecoveredCount() != 0 {
		t.Fatal("expected 0 recovered initially")
	}
}

func TestWorkerCrashRecoveryNilQueue(t *testing.T) {
	r := NewWorkerCrashRecovery(nil, "worker-1", 300)
	_, err := r.RecoverAbandonedLeases(context.Background())
	if err == nil {
		t.Fatal("expected error with nil queue")
	}
}

// ── Reliability Metrics Tests ────────────────────────────────

func TestReliabilityMetricsRecordDelivery(t *testing.T) {
	m := &ReliabilityMetrics{}
	m.RecordDelivery(100)
	m.RecordDelivery(200)
	snap := m.Snapshot()
	if snap.TotalDeliveries != 2 {
		t.Fatalf("expected 2 deliveries, got %d", snap.TotalDeliveries)
	}
	if snap.TotalDeliveryDurMs != 300 {
		t.Fatalf("expected 300ms total, got %d", snap.TotalDeliveryDurMs)
	}
}

func TestReliabilityMetricsRecordDeferral(t *testing.T) {
	m := &ReliabilityMetrics{}
	m.RecordDeferral()
	m.RecordDeferral()
	if snap := m.Snapshot(); snap.TotalDeferrals != 2 {
		t.Fatalf("expected 2 deferrals, got %d", snap.TotalDeferrals)
	}
}

func TestReliabilityMetricsRecordBounce(t *testing.T) {
	m := &ReliabilityMetrics{}
	m.RecordBounce()
	if snap := m.Snapshot(); snap.TotalBounces != 1 {
		t.Fatalf("expected 1 bounce, got %d", snap.TotalBounces)
	}
}

func TestReliabilityMetricsRecordDeadLetter(t *testing.T) {
	m := &ReliabilityMetrics{}
	m.RecordDeadLetter()
	if snap := m.Snapshot(); snap.TotalDeadLetters != 1 {
		t.Fatalf("expected 1 dead letter, got %d", snap.TotalDeadLetters)
	}
}

func TestReliabilityMetricsRecordRetry(t *testing.T) {
	m := &ReliabilityMetrics{}
	m.RecordRetry(3)
	m.RecordRetry(5)
	snap := m.Snapshot()
	if snap.TotalRetryCount != 2 {
		t.Fatalf("expected 2 retries, got %d", snap.TotalRetryCount)
	}
	if snap.MaxRetryObserved != 5 {
		t.Fatalf("expected max retry 5, got %d", snap.MaxRetryObserved)
	}
}

func TestReliabilityMetricsDurationBounds(t *testing.T) {
	m := &ReliabilityMetrics{}
	m.RecordDelivery(500)
	m.RecordDelivery(100)
	m.RecordDelivery(1000)
	snap := m.Snapshot()
	if snap.MinDeliveryDurMs != 100 {
		t.Fatalf("expected min 100, got %d", snap.MinDeliveryDurMs)
	}
	if snap.MaxDeliveryDurMs != 1000 {
		t.Fatalf("expected max 1000, got %d", snap.MaxDeliveryDurMs)
	}
}

func TestReliabilityMetricsSetActiveWorkers(t *testing.T) {
	m := &ReliabilityMetrics{}
	m.SetActiveWorkers(5)
	if snap := m.Snapshot(); snap.ActiveWorkers != 5 {
		t.Fatalf("expected 5 workers, got %d", snap.ActiveWorkers)
	}
}

func TestReliabilityMetricsConcurrentSafe(t *testing.T) {
	m := &ReliabilityMetrics{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.RecordDelivery(1)
			m.RecordDeferral()
			m.RecordRetry(1)
		}()
	}
	wg.Wait()
	snap := m.Snapshot()
	if snap.TotalDeliveries != 50 {
		t.Fatalf("expected 50 deliveries, got %d", snap.TotalDeliveries)
	}
}

// ── Concurrent Shutdown Tests ────────────────────────────────

func TestShutdownManagerConcurrentShutdown(t *testing.T) {
	sm := NewShutdownManager()
	var active int32

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sm.BeginJob() {
				atomic.AddInt32(&active, 1)
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&active, -1)
				sm.EndJob()
			}
		}()
	}

	time.Sleep(time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	<-sm.Shutdown(ctx)

	if sm.IsShutdown() != true {
		t.Fatal("expected shutdown")
	}
}

// ── Retry Classify Edge Cases ────────────────────────────────

func TestClassifyResultNilResult(t *testing.T) {
	p := FastRetryPolicy()
	res := &DeliveryResult{}
	decision, _, err := p.ClassifyResult(res, 1)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if decision != DecisionDeadLetter {
		t.Fatal("expected dead_letter for nil result")
	}
}

func TestClassifyResult4xxDeferred(t *testing.T) {
	p := FastRetryPolicy()
	res := &DeliveryResult{Success: false, StatusCode: 450, StatusMsg: "4.2.1 Busy", TempFail: true}
	decision, _, err := p.ClassifyResult(res, 1)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if decision != DecisionRetry {
		t.Fatal("expected retry for 4xx")
	}
}

func TestClassifyResult5xxBounce(t *testing.T) {
	p := FastRetryPolicy()
	res := &DeliveryResult{Success: false, StatusCode: 550, StatusMsg: "5.1.1 Unknown", TempFail: false}
	decision, _, err := p.ClassifyResult(res, 1)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if decision != DecisionDeadLetter {
		t.Fatal("expected dead_letter for 5xx")
	}
}

// ── Attempt History Persistence Tests ─────────────────────────

func TestAttemptHistoryTimestamps(t *testing.T) {
	db := historyDB(t)
	repo := NewAttemptHistorySQLRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	a := &DeliveryAttempt{
		QueueEntryID: 1, AttemptNumber: 1, Status: "delivered",
		AttemptedAt: now, WorkerID: "w1",
	}
	repo.RecordAttempt(ctx, a, nil)

	last, _ := repo.LastAttempt(ctx, 1, nil)
	if last.AttemptedAt.Unix() != now.Unix() {
		t.Fatalf("expected timestamp %s, got %s", now, last.AttemptedAt)
	}
}

func TestAttemptHistoryTLSUsed(t *testing.T) {
	db := historyDB(t)
	repo := NewAttemptHistorySQLRepo(db)
	ctx := context.Background()

	repo.RecordAttempt(ctx, &DeliveryAttempt{
		QueueEntryID: 1, AttemptNumber: 1, Status: "delivered",
		TLSUsed: true, WorkerID: "w1",
	}, nil)

	last, _ := repo.LastAttempt(ctx, 1, nil)
	if !last.TLSUsed {
		t.Fatal("expected TLS used")
	}
}

func TestAttemptHistoryDefaultTimestamps(t *testing.T) {
	db := historyDB(t)
	repo := NewAttemptHistorySQLRepo(db)
	ctx := context.Background()

	a := &DeliveryAttempt{
		QueueEntryID: 1, AttemptNumber: 1, Status: "deferred",
		WorkerID: "w1",
	}
	if err := repo.RecordAttempt(ctx, a, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if a.AttemptedAt.IsZero() {
		t.Fatal("expected timestamp to be auto-set")
	}
}
