package observability

import (
	"strings"
	"sync"
	"testing"
)

// ── Logger Tests ─────────────────────────────────────────────

func TestLoggerRecordsEvent(t *testing.T) {
	l := NewLogger(100)
	l.Event(EventSMTPAccepted, map[string]string{"remote_ip": "1.2.3.4"})

	events := l.RecentEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventSMTPAccepted {
		t.Fatalf("expected SMTP accepted, got %s", events[0].Type)
	}
}

func TestLoggerBoundedHistory(t *testing.T) {
	l := NewLogger(5)
	for i := 0; i < 20; i++ {
		l.Event(EventSMTPAccepted, nil)
	}
	events := l.RecentEvents()
	if len(events) > 5 {
		t.Fatalf("expected max 5 events, got %d", len(events))
	}
}

func TestLoggerEventDoesNotContainPassword(t *testing.T) {
	l := NewLogger(10)
	l.Event(EventSMTPAuthSuccess, map[string]string{
		"identity": "user@test.com",
		"method":   "LOGIN",
	})

	for _, e := range l.RecentEvents() {
		if err := ValidateEventSafety(e); err != nil {
			t.Fatalf("safety violation: %v", err)
		}
	}
}

func TestLoggerEventDoesNotContainPrivateKey(t *testing.T) {
	l := NewLogger(10)
	l.Event(EventDKIMSignSuccess, map[string]string{
		"domain":   "test.com",
		"selector": "s1",
	})

	for _, e := range l.RecentEvents() {
		if err := ValidateEventSafety(e); err != nil {
			t.Fatalf("safety violation: %v", err)
		}
	}
}

func TestLoggerValidatesSensitiveFields(t *testing.T) {
	err := ValidateEventSafety(LogEvent{
		Type: EventSMTPAuthSuccess,
		Fields: map[string]string{"password": "secret123"},
	})
	if err == nil {
		t.Fatal("expected validation error for password field")
	}

	err = ValidateEventSafety(LogEvent{
		Type: EventDKIMSignSuccess,
		Fields: map[string]string{"private_key": "pempem"},
	})
	if err == nil {
		t.Fatal("expected validation error for private_key field")
	}
}

func TestFormatEvent(t *testing.T) {
	e := LogEvent{
		Type: EventSMTPAccepted,
		Fields: map[string]string{
			"remote_ip": "1.2.3.4",
			"domain":    "test.com",
		},
	}
	line := FormatEvent(e)
	if !strings.Contains(line, "evt=smtp.accepted") {
		t.Fatal("expected event type in output")
	}
	if !strings.Contains(line, "remote_ip=1.2.3.4") {
		t.Fatal("expected remote_ip in output")
	}
}

func TestFormatEventSensitiveFieldSkipped(t *testing.T) {
	e := LogEvent{
		Type: EventDKIMSignSuccess,
		Fields: map[string]string{
			"domain":     "test.com",
			"private_key": "should-not-appear",
		},
	}
	line := FormatEvent(e)
	if strings.Contains(line, "private_key") {
		t.Fatal("private_key should not appear in formatted output")
	}
	if !strings.Contains(line, "domain=test.com") {
		t.Fatal("safe fields should still appear")
	}
}

// ── Metrics Tests ────────────────────────────────────────────

func TestMetricsConcurrentSafe(t *testing.T) {
	m := NewMetricsCollector()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.IncSMTPAccepted()
			m.IncSMTPRejected()
			m.IncAuthSuccess()
			m.IncAuthFailure()
			m.IncSPFPass()
			m.IncSPFFail()
			m.IncSPFNone()
			m.IncDKIMSigned()
			m.IncDKIMFailed()
			m.IncDKIMSkipped()
			m.IncDMARCPass()
			m.IncDMARCFail()
			m.IncDMARCNone()
			m.IncSpamAccepted()
			m.IncSpamSuspicious()
			m.IncSpamRejected()
			m.IncQueueDelivered()
			m.IncQueueDeferred()
			m.IncQueueBounced()
			m.IncQueueDeadLetter()
			m.AddDeliveryLatency(100)
		}()
	}
	wg.Wait()

	snap := m.Snapshot()
	if snap.SMTPAccepted != 100 {
		t.Fatalf("expected 100 accepted, got %d", snap.SMTPAccepted)
	}
	if snap.DeliveryCount != 100 {
		t.Fatalf("expected 100 delivery count, got %d", snap.DeliveryCount)
	}
	if snap.DeliveryLatencyTotal != 10000 {
		t.Fatalf("expected 10000 latency total, got %d", snap.DeliveryLatencyTotal)
	}
}

func TestSMTPAcceptedMetricIncrements(t *testing.T) {
	m := NewMetricsCollector()
	m.IncSMTPAccepted()
	snap := m.Snapshot()
	if snap.SMTPAccepted != 1 {
		t.Fatalf("expected 1, got %d", snap.SMTPAccepted)
	}
}

func TestAuthFailureMetricIncrements(t *testing.T) {
	m := NewMetricsCollector()
	m.IncAuthFailure()
	m.IncAuthFailure()
	snap := m.Snapshot()
	if snap.AuthFailure != 2 {
		t.Fatalf("expected 2, got %d", snap.AuthFailure)
	}
}

func TestSPFDKIMDMARCMetrics(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordSPFResult("pass")
	m.RecordSPFResult("fail")
	m.RecordSPFResult("none")
	m.RecordDMARCResult("pass")
	m.RecordDMARCResult("fail")
	m.RecordDMARCResult("none")

	snap := m.Snapshot()
	if snap.SPFPass != 1 || snap.SPFFail != 1 || snap.SPFNone != 1 {
		t.Fatal("SPF metrics incorrect")
	}
	if snap.DMARCPass != 1 || snap.DMARCFail != 1 || snap.DMARCNone != 1 {
		t.Fatal("DMARC metrics incorrect")
	}
}

func TestSpamVerdictMetricIncrements(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordSpamVerdict("reject")
	m.RecordSpamVerdict("suspicious")
	m.RecordSpamVerdict("accept")

	snap := m.Snapshot()
	if snap.SpamRejected != 1 || snap.SpamSuspicious != 1 || snap.SpamAccepted != 1 {
		t.Fatal("spam verdict metrics incorrect")
	}
}

func TestQueueDeliveryMetrics(t *testing.T) {
	m := NewMetricsCollector()
	m.IncQueueDelivered()
	m.IncQueueDeferred()
	m.IncQueueBounced()
	m.IncQueueDeadLetter()

	snap := m.Snapshot()
	if snap.QueueDelivered != 1 || snap.QueueDeferred != 1 || snap.QueueBounced != 1 || snap.QueueDeadLetter != 1 {
		t.Fatal("queue delivery metrics incorrect")
	}
}

// ── Health Tests ─────────────────────────────────────────────

func TestHealthReportAllReady(t *testing.T) {
	h := NewHealthChecker()
	h.Ready(HealthCheckSMTPReceive)
	h.Ready(HealthCheckQueue)
	h.Ready(HealthCheckMailStore)

	r := h.Report()
	if r.Overall != HealthReady {
		t.Fatalf("expected ready, got %s", r.Overall)
	}
}

func TestHealthReportNotReady(t *testing.T) {
	h := NewHealthChecker()
	h.Ready(HealthCheckSMTPReceive)
	h.NotReady(HealthCheckDatabase, "connection failed")

	r := h.Report()
	if r.Overall != HealthNotReady {
		t.Fatalf("expected not_ready, got %s", r.Overall)
	}
	dbc, ok := r.Checks[HealthCheckDatabase]
	if !ok || dbc.Status != HealthNotReady {
		t.Fatal("expected database check to be not_ready")
	}
}

func TestHealthReportDegraded(t *testing.T) {
	h := NewHealthChecker()
	h.Ready(HealthCheckSMTPReceive)
	h.Degraded(HealthCheckDNSResolver, "high latency")

	r := h.Report()
	if r.Overall != HealthDegraded {
		t.Fatalf("expected degraded, got %s", r.Overall)
	}
}

// ── Diagnostic Snapshot Tests ────────────────────────────────

func TestSnapshotBoundedHistory(t *testing.T) {
	metrics := NewMetricsCollector()
	logger := NewLogger(1000)
	events := NewEventHistory(10)

	for i := 0; i < 25; i++ {
		events.Record(EventSMTPAccepted, map[string]string{"idx": string(rune('0' + i))})
	}

	collector := NewSnapshotCollector(metrics, logger, NewHealthChecker(), events)
	snap := collector.Snapshot()
	if len(snap.RecentEvents) > 10 {
		t.Fatalf("expected max 10 events, got %d", len(snap.RecentEvents))
	}
}

func TestSnapshotRecentFailures(t *testing.T) {
	events := NewEventHistory(50)
	events.Record(EventQueueBounced, map[string]string{"reason": "policy"})
	events.Record(EventSMTPAuthFailure, map[string]string{"ip": "1.2.3.4"})
	events.Record(EventSpamRejected, map[string]string{"score": "8.0"})
	events.Record(EventQueueDelivered, map[string]string{"to": "user@test.com"})
	events.Record(EventSMTPAccepted, map[string]string{"ip": "1.2.3.4"})

	collector := NewSnapshotCollector(nil, nil, nil, events)
	snap := collector.Snapshot()

	if len(snap.RecentFailures) != 1 {
		t.Fatalf("expected 1 bounced, got %d", len(snap.RecentFailures))
	}
	if len(snap.RecentAuthFailures) != 1 {
		t.Fatalf("expected 1 auth failure, got %d", len(snap.RecentAuthFailures))
	}
	if len(snap.RecentSpamRejects) != 1 {
		t.Fatalf("expected 1 spam reject, got %d", len(snap.RecentSpamRejects))
	}
}

func TestSnapshotContainsMetrics(t *testing.T) {
	metrics := NewMetricsCollector()
	metrics.IncSMTPAccepted()
	metrics.IncAuthFailure()

	collector := NewSnapshotCollector(metrics, nil, NewHealthChecker(), nil)
	snap := collector.Snapshot()

	if snap.Metrics.SMTPAccepted != 1 {
		t.Fatalf("expected 1 accepted, got %d", snap.Metrics.SMTPAccepted)
	}
	if snap.Metrics.AuthFailure != 1 {
		t.Fatalf("expected 1 auth failure, got %d", snap.Metrics.AuthFailure)
	}
}
