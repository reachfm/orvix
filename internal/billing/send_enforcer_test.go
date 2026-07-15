package billing

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"
)

type sendEnforcerTestEnv struct {
	db       *sql.DB
	enforcer *SendEnforcer
	id       SendIdentity
}

func newSendEnforcerTestEnv(t *testing.T) *sendEnforcerTestEnv {
	t.Helper()
	db := newTestDB(t)
	db.SetMaxOpenConns(1)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
	const tenantID = uint(1)
	if _, err := svc.CreateSubscription(tenantID, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	return &sendEnforcerTestEnv{
		db:       db,
		enforcer: NewSendEnforcer(db, svc, NewQuotaService(db, svc)),
		id:       SendIdentity{TenantID: tenantID, MailboxID: 7},
	}
}

func TestSendEnforcerAllowSendUsesRequestedRecipientCount(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	if _, err := env.db.Exec("UPDATE subscriptions SET send_limit_day = 5 WHERE tenant_id = 1"); err != nil {
		t.Fatalf("shrink send limit: %v", err)
	}
	if err := env.enforcer.RecordSend(context.Background(), env.id, "evt-send-near-limit", 4); err != nil {
		t.Fatalf("record send: %v", err)
	}
	if got := env.enforcer.AllowSend(context.Background(), env.id, 1); !got.Allowed || got.Remaining != 1 {
		t.Fatalf("expected one more recipient allowed, got %+v", got)
	}
	if got := env.enforcer.AllowSend(context.Background(), env.id, 2); got.Allowed || got.Remaining != 1 {
		t.Fatalf("expected two-recipient send denied at remaining=1, got %+v", got)
	}
	if got := env.enforcer.AllowSend(context.Background(), env.id, 0); got.Allowed || got.Reason != "invalid recipient count" {
		t.Fatalf("expected invalid count denied, got %+v", got)
	}
}

func TestSendEnforcerRecordSendExactlyOnceAccounting(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	for i := 0; i < 2; i++ {
		if err := env.enforcer.RecordSend(context.Background(), env.id, "evt-send-once", 3); err != nil {
			t.Fatalf("record send attempt %d: %v", i+1, err)
		}
	}
	assertEventCount(t, env.db, "send", 1)
	assertEventRecipientTotal(t, env.db, "send", 3)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = 1", 3)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM usage_records WHERE tenant_id = 1", 3)
}

func TestSendEnforcerRecordBounceExactlyOnceAccounting(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	for i := 0; i < 2; i++ {
		if err := env.enforcer.RecordBounce(context.Background(), env.id, "evt-bounce-once"); err != nil {
			t.Fatalf("record bounce attempt %d: %v", i+1, err)
		}
	}
	assertEventCount(t, env.db, "bounce", 1)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(bounce_count), 0) FROM abuse_bounce_counts WHERE tenant_id = 1", 1)
}

func TestSendEnforcerConcurrentDuplicateSendExactlyOnce(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	runConcurrent(t, 24, func() error {
		return env.enforcer.RecordSend(context.Background(), env.id, "evt-send-concurrent", 2)
	})
	assertEventCount(t, env.db, "send", 1)
	assertEventRecipientTotal(t, env.db, "send", 2)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = 1", 2)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM usage_records WHERE tenant_id = 1", 2)
}

func TestSendEnforcerConcurrentDuplicateBounceExactlyOnce(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	runConcurrent(t, 24, func() error {
		return env.enforcer.RecordBounce(context.Background(), env.id, "evt-bounce-concurrent")
	})
	assertEventCount(t, env.db, "bounce", 1)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(bounce_count), 0) FROM abuse_bounce_counts WHERE tenant_id = 1", 1)
}

func TestSendEnforcerRecordSendRollbackOnUsageCounterFailure(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	if _, err := env.db.Exec("DROP TABLE usage_records"); err != nil {
		t.Fatalf("drop usage_records: %v", err)
	}
	if err := env.enforcer.RecordSend(context.Background(), env.id, "evt-send-rollback", 4); err == nil {
		t.Fatalf("expected RecordSend to fail when usage_records is unavailable")
	}
	assertEventCount(t, env.db, "send", 0)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = 1", 0)
}

func TestSendEnforcerRecordBounceRollbackOnCounterFailure(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	if _, err := env.db.Exec("DROP TABLE abuse_bounce_counts"); err != nil {
		t.Fatalf("drop abuse_bounce_counts: %v", err)
	}
	if err := env.enforcer.RecordBounce(context.Background(), env.id, "evt-bounce-rollback"); err == nil {
		t.Fatalf("expected RecordBounce to fail when abuse_bounce_counts is unavailable")
	}
	assertEventCount(t, env.db, "bounce", 0)
}

func TestSendEnforcerIgnoresInvalidIdempotencyInputs(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	if err := env.enforcer.RecordSend(context.Background(), env.id, "", 3); err != nil {
		t.Fatalf("empty send event should be ignored: %v", err)
	}
	if err := env.enforcer.RecordSend(context.Background(), env.id, "evt-invalid-count", 0); err != nil {
		t.Fatalf("zero recipient send should be ignored: %v", err)
	}
	if err := env.enforcer.RecordBounce(context.Background(), env.id, ""); err != nil {
		t.Fatalf("empty bounce event should be ignored: %v", err)
	}
	assertScalarInt64(t, env.db, "SELECT COUNT(*) FROM send_events", 0)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = 1", 0)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(bounce_count), 0) FROM abuse_bounce_counts WHERE tenant_id = 1", 0)
}

func TestSendEnforcerReserveConcurrentOneRemainingQuota(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	mustSetSendLimit(t, env.db, 1, 1)

	var mu sync.Mutex
	allowed := 0
	var seq int64
	runConcurrent(t, 2, func() error {
		eventID := fmt.Sprintf("evt-reserve-one-%d", atomic.AddInt64(&seq, 1))
		res, err := env.enforcer.ReserveSend(context.Background(), env.id, eventID, 1)
		if err != nil {
			return err
		}
		if res.Allowed {
			mu.Lock()
			allowed++
			mu.Unlock()
		}
		return nil
	})

	if allowed != 1 {
		t.Fatalf("exactly one concurrent one-recipient reservation should win, got %d", allowed)
	}
	assertEventCount(t, env.db, "reservation", 1)
	assertEventRecipientTotal(t, env.db, "reservation", 1)
}

func TestSendEnforcerReserveConcurrentMultiRecipientCannotExceedQuota(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	mustSetSendLimit(t, env.db, 1, 3)

	var mu sync.Mutex
	allowed := 0
	var seq int64
	runConcurrent(t, 2, func() error {
		eventID := fmt.Sprintf("evt-reserve-multi-%d", atomic.AddInt64(&seq, 1))
		res, err := env.enforcer.ReserveSend(context.Background(), env.id, eventID, 2)
		if err != nil {
			return err
		}
		if res.Allowed {
			mu.Lock()
			allowed++
			mu.Unlock()
		}
		return nil
	})

	if allowed != 1 {
		t.Fatalf("only one two-recipient reservation should fit limit=3, got %d", allowed)
	}
	assertEventCount(t, env.db, "reservation", 1)
	assertEventRecipientTotal(t, env.db, "reservation", 2)
}

func TestSendEnforcerCancelReservationReleasesQuota(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	mustSetSendLimit(t, env.db, 1, 2)

	res, err := env.enforcer.ReserveSend(context.Background(), env.id, "evt-release", 2)
	if err != nil || !res.Allowed {
		t.Fatalf("reserve: %+v err=%v", res, err)
	}
	if err := env.enforcer.CancelSendReservation(context.Background(), env.id, "evt-release"); err != nil {
		t.Fatalf("cancel reservation: %v", err)
	}
	assertScalarInt64(t, env.db, "SELECT COUNT(*) FROM send_events", 0)

	res, err = env.enforcer.ReserveSend(context.Background(), env.id, "evt-after-release", 2)
	if err != nil || !res.Allowed {
		t.Fatalf("reserve after release should fit full quota: %+v err=%v", res, err)
	}
}

func TestSendEnforcerFinalizeReservationPartialReconcilesCounters(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	mustSetSendLimit(t, env.db, 1, 5)

	res, err := env.enforcer.ReserveSend(context.Background(), env.id, "evt-partial", 5)
	if err != nil || !res.Allowed {
		t.Fatalf("reserve: %+v err=%v", res, err)
	}
	if err := env.enforcer.FinalizeSendReservation(context.Background(), env.id, "evt-partial", 2); err != nil {
		t.Fatalf("finalize partial: %v", err)
	}

	assertEventCount(t, env.db, "reservation", 0)
	assertEventCount(t, env.db, "send", 1)
	assertEventRecipientTotal(t, env.db, "send", 2)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = 1", 2)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM usage_records WHERE tenant_id = 1", 2)
}

func TestSendEnforcerDuplicateReservationAndFinalizeIdempotent(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	mustSetSendLimit(t, env.db, 1, 5)

	for i := 0; i < 2; i++ {
		res, err := env.enforcer.ReserveSend(context.Background(), env.id, "evt-retry", 3)
		if err != nil || !res.Allowed {
			t.Fatalf("reserve attempt %d: %+v err=%v", i+1, res, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := env.enforcer.FinalizeSendReservation(context.Background(), env.id, "evt-retry", 3); err != nil {
			t.Fatalf("finalize attempt %d: %v", i+1, err)
		}
	}

	assertEventCount(t, env.db, "send", 1)
	assertEventRecipientTotal(t, env.db, "send", 3)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = 1", 3)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(emails_sent), 0) FROM usage_records WHERE tenant_id = 1", 3)
}

func TestSendEnforcerReserveTenantCountersRemainIsolated(t *testing.T) {
	env := newSendEnforcerTestEnv(t)
	svc := NewService(env.db)
	if _, err := svc.CreateSubscription(2, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatalf("create second tenant subscription: %v", err)
	}
	mustSetSendLimit(t, env.db, 1, 1)
	mustSetSendLimit(t, env.db, 2, 1)

	res1, err := env.enforcer.ReserveSend(context.Background(), env.id, "evt-tenant-1", 1)
	if err != nil || !res1.Allowed {
		t.Fatalf("tenant 1 reserve: %+v err=%v", res1, err)
	}
	id2 := SendIdentity{TenantID: 2, MailboxID: 8}
	res2, err := env.enforcer.ReserveSend(context.Background(), id2, "evt-tenant-2", 1)
	if err != nil || !res2.Allowed {
		t.Fatalf("tenant 2 reserve: %+v err=%v", res2, err)
	}
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(recipient_count), 0) FROM send_events WHERE tenant_id = 1", 1)
	assertScalarInt64(t, env.db, "SELECT COALESCE(SUM(recipient_count), 0) FROM send_events WHERE tenant_id = 2", 1)
}

func runConcurrent(t *testing.T, n int, fn func() error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- fn()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent operation failed: %v", err)
		}
	}
}

func mustSetSendLimit(t *testing.T, db *sql.DB, tenantID uint, limit int) {
	t.Helper()
	dial, err := dbdialect.Detect(db)
	if err != nil {
		dial = dbdialect.FromDriver("sqlite")
	}
	if _, err := db.Exec("UPDATE subscriptions SET send_limit_day = "+dial.Placeholder(1)+" WHERE tenant_id = "+dial.Placeholder(2), limit, tenantID); err != nil {
		t.Fatalf("set send limit: %v", err)
	}
}

func assertEventCount(t *testing.T, db *sql.DB, eventType string, want int64) {
	t.Helper()
	assertScalarInt64(t, db, fmt.Sprintf("SELECT COUNT(*) FROM send_events WHERE event_type = '%s'", sqlLiteral(eventType)), want)
}

func assertEventRecipientTotal(t *testing.T, db *sql.DB, eventType string, want int64) {
	t.Helper()
	assertScalarInt64(t, db, fmt.Sprintf("SELECT COALESCE(SUM(recipient_count), 0) FROM send_events WHERE event_type = '%s'", sqlLiteral(eventType)), want)
}

func assertScalarInt64(t *testing.T, db *sql.DB, query string, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("query %q: got %d want %d", query, got, want)
	}
}

func sqlLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
