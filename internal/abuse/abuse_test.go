package abuse

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB { return newTestDB(t) }

func TestRecordSend(t *testing.T) {
	db := setupTestDB(t)
	svc := NewRateLimitService(db)
	if err := svc.RecordSend(context.Background(), 1, 5); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordSend(context.Background(), 1, 3); err != nil {
		t.Fatal(err)
	}
	var total int
	db.QueryRow("SELECT COALESCE(emails_sent,0) FROM abuse_send_counts WHERE tenant_id=1").Scan(&total)
	if total != 8 {
		t.Fatalf("expected 8, got %d", total)
	}
}

func TestRecordBounce(t *testing.T) {
	db := setupTestDB(t)
	svc := NewRateLimitService(db)
	for i := 0; i < 3; i++ {
		if err := svc.RecordBounce(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	var total int
	db.QueryRow("SELECT COALESCE(bounce_count,0) FROM abuse_bounce_counts WHERE tenant_id=1").Scan(&total)
	if total != 3 {
		t.Fatalf("expected 3 bounces, got %d", total)
	}
}

func TestSendLimit(t *testing.T) {
	db := setupTestDB(t)
	db.Exec("INSERT INTO subscriptions (tenant_id, send_limit_day, status) VALUES (1, 100, 'active')")
	svc := NewRateLimitService(db)

	// Record sends
	for i := 0; i < 50; i++ {
		svc.RecordSend(context.Background(), 1, 1)
	}
	bucket, err := svc.CheckSendLimit(context.Background(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bucket.Remaining != 50 {
		t.Fatalf("expected 50 remaining, got %d", bucket.Remaining)
	}
	if bucket.Limit != 100 {
		t.Fatalf("expected limit 100, got %d", bucket.Limit)
	}
}

func TestBounceRate(t *testing.T) {
	db := setupTestDB(t)
	db.Exec("INSERT INTO subscriptions (tenant_id, send_limit_day, status) VALUES (1, 1000, 'active')")
	svc := NewRateLimitService(db)

	for i := 0; i < 100; i++ {
		svc.RecordSend(context.Background(), 1, 1)
	}
	for i := 0; i < 10; i++ {
		svc.RecordBounce(context.Background(), 1)
	}
	rate, err := svc.CheckBounceRate(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.1 {
		t.Fatalf("expected 0.1 bounce rate, got %f", rate)
	}
}

func TestSignalCreation(t *testing.T) {
	db := setupTestDB(t)
	svc := NewSignalService(db, NewRateLimitService(db))
	err := svc.RecordSignal(context.Background(), &AbuseSignal{
		TenantID:    1,
		SignalType:  SignalHighBounceRate,
		Severity:    SeverityWarning,
		Description: "test signal",
		DetectedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := svc.ListActiveSignals(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}
	if signals[0].SignalType != SignalHighBounceRate {
		t.Fatalf("expected high_bounce_rate, got %s", signals[0].SignalType)
	}
}

func TestSignalAcknowledgeResolve(t *testing.T) {
	db := setupTestDB(t)
	svc := NewSignalService(db, NewRateLimitService(db))
	svc.RecordSignal(context.Background(), &AbuseSignal{
		TenantID:    1,
		SignalType:  SignalHighBounceRate,
		Severity:    SeverityWarning,
		Description: "test",
		DetectedAt:  time.Now().UTC(),
	})
	signals, _ := svc.ListActiveSignals(context.Background(), 1)
	if len(signals) != 1 {
		t.Fatal("expected signal")
	}
	if err := svc.AcknowledgeSignal(context.Background(), signals[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveSignal(context.Background(), signals[0].ID, 99); err != nil {
		t.Fatal(err)
	}
	signals, _ = svc.ListActiveSignals(context.Background(), 1)
	if len(signals) != 0 {
		t.Fatal("expected no active signals after resolution")
	}
}

func TestSignalCheckAndAlert(t *testing.T) {
	db := setupTestDB(t)
	db.Exec("INSERT INTO subscriptions (tenant_id, send_limit_day, status) VALUES (1, 1000, 'active')")
	svc := NewRateLimitService(db)
	sigSvc := NewSignalService(db, svc)

	for i := 0; i < 100; i++ {
		svc.RecordSend(context.Background(), 1, 1)
	}
	for i := 0; i < 20; i++ {
		svc.RecordBounce(context.Background(), 1)
	}

	if err := sigSvc.CheckAndAlert(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	signals, _ := sigSvc.ListActiveSignals(context.Background(), 1)
	if len(signals) == 0 {
		t.Fatal("expected at least one signal due to high bounce rate")
	}
}

func TestTenantIsolationSignal(t *testing.T) {
	db := setupTestDB(t)
	svc := NewSignalService(db, NewRateLimitService(db))
	svc.RecordSignal(context.Background(), &AbuseSignal{TenantID: 1, SignalType: SignalHighBounceRate, Severity: SeverityWarning, Description: "tenant1", DetectedAt: time.Now().UTC()})
	svc.RecordSignal(context.Background(), &AbuseSignal{TenantID: 2, SignalType: SignalHighBounceRate, Severity: SeverityWarning, Description: "tenant2", DetectedAt: time.Now().UTC()})

	signals1, _ := svc.ListActiveSignals(context.Background(), 1)
	signals2, _ := svc.ListActiveSignals(context.Background(), 2)
	if len(signals1) != 1 || len(signals2) != 1 {
		t.Fatal("signals should be isolated per tenant")
	}
	if signals1[0].TenantID != 1 {
		t.Fatal("tenant 1 got tenant 2 signal")
	}
}

func TestRateLimitTenantIsolation(t *testing.T) {
	db := setupTestDB(t)
	db.Exec("INSERT INTO subscriptions (tenant_id, send_limit_day, status) VALUES (1, 100, 'active')")
	db.Exec("INSERT INTO subscriptions (tenant_id, send_limit_day, status) VALUES (2, 100, 'active')")
	svc := NewRateLimitService(db)

	svc.RecordSend(context.Background(), 1, 50)
	svc.RecordSend(context.Background(), 2, 10)

	var t1sent, t2sent int
	db.QueryRow("SELECT COALESCE(emails_sent,0) FROM abuse_send_counts WHERE tenant_id=1").Scan(&t1sent)
	db.QueryRow("SELECT COALESCE(emails_sent,0) FROM abuse_send_counts WHERE tenant_id=2").Scan(&t2sent)
	if t1sent != 50 || t2sent != 10 {
		t.Fatalf("tenant isolation broken: t1=%d t2=%d", t1sent, t2sent)
	}
}
