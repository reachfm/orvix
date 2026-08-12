package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type fakeSender struct {
	sent []Alert
}

func (f *fakeSender) Send(ctx context.Context, alert Alert, rule Rule) error {
	f.sent = append(f.sent, alert)
	return nil
}

func newTestService(t *testing.T) (*sql.DB, *Service, *fakeSender) {
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
	sender := &fakeSender{}
	return db, NewService(repo, nil, nil, sender, nil), sender
}

func TestEvaluate_ConditionBelowDurationStaysPending(t *testing.T) {
	_, svc, sender := newTestService(t)
	ctx := context.Background()
	rule, err := svc.CreateRule(ctx, Rule{Name: "high-bounce", MetricName: "bounce_rate", Comparator: ComparatorGT, Threshold: 0.05, Duration: time.Minute})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if err := svc.Evaluate(ctx, []MetricSample{{MetricName: "bounce_rate", Scope: "global", Value: 0.10}}); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, _ := svc.ListAlerts(ctx, AlertPending, 0, 10)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 pending alert, got %d", len(alerts))
	}
	if len(sender.sent) != 0 {
		t.Fatal("expected no notification before Duration elapses")
	}
	_ = rule
}

func TestEvaluate_ConditionSustainedFiresAndNotifies(t *testing.T) {
	db, svc, sender := newTestService(t)
	ctx := context.Background()
	svc.CreateRule(ctx, Rule{Name: "high-bounce", MetricName: "bounce_rate", Comparator: ComparatorGT, Threshold: 0.05, Duration: time.Minute})
	svc.Evaluate(ctx, []MetricSample{{MetricName: "bounce_rate", Scope: "global", Value: 0.10}})

	// Simulate Duration having elapsed by aging first_observed_at.
	if _, err := db.Exec(`UPDATE platform_alerts SET first_observed_at=?`, time.Now().UTC().Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Evaluate(ctx, []MetricSample{{MetricName: "bounce_rate", Scope: "global", Value: 0.12}}); err != nil {
		t.Fatalf("evaluate 2: %v", err)
	}
	alerts, _ := svc.ListAlerts(ctx, AlertFiring, 0, 10)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 firing alert, got %d", len(alerts))
	}
	if len(sender.sent) != 1 {
		t.Fatalf("expected exactly 1 notification on transition to firing, got %d", len(sender.sent))
	}
}

func TestEvaluate_FiringDedupesWithinCooldown(t *testing.T) {
	db, svc, sender := newTestService(t)
	ctx := context.Background()
	svc.CreateRule(ctx, Rule{Name: "r", MetricName: "m", Comparator: ComparatorGT, Threshold: 0, Duration: 0, CooldownSeconds: 300})
	svc.Evaluate(ctx, []MetricSample{{MetricName: "m", Scope: "global", Value: 1}})
	// Duration=0 so it fires immediately on first evaluation past pending; but
	// GetOrCreateAlert's first call is "created", which always waits one tick.
	// Force straight to firing for a clean dedup test.
	db.Exec(`UPDATE platform_alerts SET state='firing', fired_at=?, last_notified_at=?`, time.Now().UTC(), time.Now().UTC())
	sender.sent = nil

	// Re-evaluate repeatedly within the cooldown window — must not renotify.
	for i := 0; i < 5; i++ {
		svc.Evaluate(ctx, []MetricSample{{MetricName: "m", Scope: "global", Value: 1}})
	}
	if len(sender.sent) != 0 {
		t.Fatalf("expected no renotification within cooldown, got %d", len(sender.sent))
	}
}

func TestEvaluate_ConditionClearedResolves(t *testing.T) {
	db, svc, _ := newTestService(t)
	ctx := context.Background()
	svc.CreateRule(ctx, Rule{Name: "r", MetricName: "m", Comparator: ComparatorGT, Threshold: 0.5, Duration: 0})
	svc.Evaluate(ctx, []MetricSample{{MetricName: "m", Scope: "global", Value: 0.9}})
	db.Exec(`UPDATE platform_alerts SET state='firing', fired_at=?`, time.Now().UTC())

	if err := svc.Evaluate(ctx, []MetricSample{{MetricName: "m", Scope: "global", Value: 0.1}}); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, _ := svc.ListAlerts(ctx, AlertResolved, 0, 10)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 resolved alert, got %d", len(alerts))
	}
	if alerts[0].ResolvedAt == nil {
		t.Fatal("expected resolved_at to be set")
	}
}

func TestAcknowledge_OnlyValidFromFiring(t *testing.T) {
	db, svc, _ := newTestService(t)
	ctx := context.Background()
	svc.CreateRule(ctx, Rule{Name: "r", MetricName: "m", Comparator: ComparatorGT, Threshold: 0, Duration: 0})
	svc.Evaluate(ctx, []MetricSample{{MetricName: "m", Scope: "global", Value: 1}})
	alerts, _ := svc.ListAlerts(ctx, "", 0, 10)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	id := alerts[0].ID

	// Still pending — acknowledge must be rejected.
	if err := svc.Acknowledge(ctx, id, 99); err != ErrNotFiring {
		t.Fatalf("expected ErrNotFiring while pending, got %v", err)
	}

	db.Exec(`UPDATE platform_alerts SET state='firing' WHERE id=?`, id)
	if err := svc.Acknowledge(ctx, id, 99); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	a, _ := svc.repo.GetAlert(ctx, id)
	if a.State != AlertAcknowledged || a.AcknowledgedBy != 99 {
		t.Fatalf("unexpected state after ack: %+v", a)
	}
}

func TestSilence_SuppressesNotificationUntilExpiry(t *testing.T) {
	db, svc, sender := newTestService(t)
	ctx := context.Background()
	svc.CreateRule(ctx, Rule{Name: "r", MetricName: "m", Comparator: ComparatorGT, Threshold: 0, Duration: 0, CooldownSeconds: 1})
	svc.Evaluate(ctx, []MetricSample{{MetricName: "m", Scope: "global", Value: 1}})
	alerts, _ := svc.ListAlerts(ctx, "", 0, 10)
	id := alerts[0].ID
	db.Exec(`UPDATE platform_alerts SET state='firing', fired_at=? WHERE id=?`, time.Now().UTC(), id)

	silenceUntil := time.Now().UTC().Add(time.Hour)
	if err := svc.Silence(ctx, id, silenceUntil, 99); err != nil {
		t.Fatalf("silence: %v", err)
	}
	sender.sent = nil
	svc.Evaluate(ctx, []MetricSample{{MetricName: "m", Scope: "global", Value: 1}})
	if len(sender.sent) != 0 {
		t.Fatal("expected no notification while silenced")
	}

	// Expire the silence and re-evaluate: must resume firing/notifying.
	db.Exec(`UPDATE platform_alerts SET silenced_until=? WHERE id=?`, time.Now().UTC().Add(-time.Minute), id)
	svc.Evaluate(ctx, []MetricSample{{MetricName: "m", Scope: "global", Value: 1}})
	a, _ := svc.repo.GetAlert(ctx, id)
	if a.State != AlertFiring {
		t.Fatalf("expected the alert to resume firing after silence expiry, got %s", a.State)
	}
}

func TestEvaluate_ScopedRuleOnlyMatchesItsScope(t *testing.T) {
	_, svc, _ := newTestService(t)
	ctx := context.Background()
	svc.CreateRule(ctx, Rule{Name: "r", MetricName: "m", Comparator: ComparatorGT, Threshold: 0, Duration: 0, Scope: "tenant:1"})
	svc.Evaluate(ctx, []MetricSample{
		{MetricName: "m", Scope: "tenant:1", Value: 1},
		{MetricName: "m", Scope: "tenant:2", Value: 1},
	})
	alerts, _ := svc.ListAlerts(ctx, "", 0, 10)
	if len(alerts) != 1 || alerts[0].Scope != "tenant:1" {
		t.Fatalf("expected exactly 1 alert scoped to tenant:1, got %+v", alerts)
	}
}

func TestEvaluate_UnknownMetricNeverMatchesAnyRule(t *testing.T) {
	_, svc, _ := newTestService(t)
	ctx := context.Background()
	svc.CreateRule(ctx, Rule{Name: "r", MetricName: "known_metric", Comparator: ComparatorGT, Threshold: 0, Duration: 0})
	svc.Evaluate(ctx, []MetricSample{{MetricName: "unbounded_user_supplied_label", Scope: "global", Value: 999}})
	alerts, _ := svc.ListAlerts(ctx, "", 0, 10)
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts for a metric name with no matching rule, got %d", len(alerts))
	}
}

// ── SLO burn rate ─────────────────────────────────────────────────

func TestComputeBurnRate_ExactlyAtTarget(t *testing.T) {
	slo := SLO{Name: "delivery", Target: 0.99, Window: 24 * time.Hour}
	br := ComputeBurnRate(slo, time.Now(), time.Now(), 1000, 990) // 99% success, target 99%
	if br.SuccessRatio != 0.99 {
		t.Fatalf("expected success_ratio=0.99, got %v", br.SuccessRatio)
	}
	if br.BurnRate < 0.99 || br.BurnRate > 1.01 {
		t.Fatalf("expected burn_rate ~= 1.0 at exactly-target performance, got %v", br.BurnRate)
	}
}

func TestComputeBurnRate_PerfectSuccessIsZeroBurn(t *testing.T) {
	slo := SLO{Name: "delivery", Target: 0.99, Window: 24 * time.Hour}
	br := ComputeBurnRate(slo, time.Now(), time.Now(), 1000, 1000)
	if br.BurnRate != 0 {
		t.Fatalf("expected 0 burn rate for perfect success, got %v", br.BurnRate)
	}
}

func TestComputeBurnRate_ZeroTotalIsSafe(t *testing.T) {
	slo := SLO{Name: "delivery", Target: 0.99}
	br := ComputeBurnRate(slo, time.Now(), time.Now(), 0, 0)
	if br.BurnRate != 0 || br.SuccessRatio != 0 {
		t.Fatalf("expected zero-value safe result for zero total, got %+v", br)
	}
}

func TestComputeBurnRate_FastBurnExceedsOne(t *testing.T) {
	slo := SLO{Name: "delivery", Target: 0.99}
	// 90% success against a 99% target burns the budget ~10x faster.
	br := ComputeBurnRate(slo, time.Now(), time.Now(), 1000, 900)
	if br.BurnRate <= 1.0 {
		t.Fatalf("expected burn_rate > 1 for well-below-target performance, got %v", br.BurnRate)
	}
}
