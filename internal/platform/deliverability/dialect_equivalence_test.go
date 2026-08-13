package deliverability

import (
	"context"
	"testing"
	"time"
)

// H-5/B-4: the deliverability aggregation queries must produce semantically
// identical results on SQLite and PostgreSQL. The defect was raw `?`
// placeholders that 500'd on PostgreSQL; these behavioral tables run the SAME
// operations and assert the SAME numbers on both dialects, so a future
// dialect-specific regression is caught by comparison, not just by a
// string-level placeholder check.

// deliverabilityEquivalenceTable records a fixed set of outcomes for tenantID
// and asserts per-dimension Metrics, tenant MetricsSummary, empty-window
// zeroing, and timezone-equivalent cutoffs. tag makes the dimension values and
// event keys unique so it is safe to run against a shared PostgreSQL database
// alongside the other suites.
func deliverabilityEquivalenceTable(t *testing.T, svc *Service, tenantID uint, tag string) {
	t.Helper()
	ctx := context.Background()
	sd := tag + ".send.example"
	rd := tag + ".rcpt.example"
	prov := tag + "-prov"

	// 5 events: 3 delivered, 1 bounce, 1 tempfail.
	events := []struct {
		key string
		typ SignalType
	}{
		{tag + "-e1", SignalDelivered},
		{tag + "-e2", SignalDelivered},
		{tag + "-e3", SignalDelivered},
		{tag + "-e4", SignalBounce},
		{tag + "-e5", SignalTempFail},
	}
	for _, e := range events {
		if err := svc.RecordDeliveryOutcome(ctx, e.key, tenantID, sd, rd, prov, e.typ, 15); err != nil {
			t.Fatalf("record %s: %v", e.key, err)
		}
	}

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)

	// Per-dimension aggregate (the Metrics/Aggregate path).
	m, err := svc.Metrics(ctx, DimensionSendingDomain, sd, start, end)
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if m.Volume != 5 || m.Delivered != 3 || m.Bounced != 1 || m.TempFail != 1 {
		t.Fatalf("per-dimension aggregate wrong: %+v", m)
	}
	if m.DeliveryRate < 0.599 || m.DeliveryRate > 0.601 {
		t.Fatalf("delivery rate wrong: %v", m.DeliveryRate)
	}
	if m.BounceRate < 0.199 || m.BounceRate > 0.201 {
		t.Fatalf("bounce rate wrong: %v", m.BounceRate)
	}

	// Tenant summary (the MetricsSummary/AggregateTenant path). Each event
	// fans out to 4 dimensions (tenant/sending/recipient/provider), so the
	// tenant-wide row count is 5*4=20.
	summary, err := svc.MetricsSummary(ctx, tenantID, start, end)
	if err != nil {
		t.Fatalf("MetricsSummary: %v", err)
	}
	if summary.Volume != 20 || summary.Delivered != 12 || summary.Bounced != 4 {
		t.Fatalf("tenant summary wrong: volume=%d delivered=%d bounced=%d", summary.Volume, summary.Delivered, summary.Bounced)
	}

	// Empty window: a window in the far past must return zeros, never NULLs
	// (COALESCE) and never an error.
	emptyStart := time.Now().UTC().Add(-100 * 24 * time.Hour)
	emptyEnd := time.Now().UTC().Add(-99 * 24 * time.Hour)
	em, err := svc.Metrics(ctx, DimensionSendingDomain, sd, emptyStart, emptyEnd)
	if err != nil {
		t.Fatalf("empty-window Metrics: %v", err)
	}
	if em.Volume != 0 || em.Delivered != 0 || em.DeliveryRate != 0 {
		t.Fatalf("empty window must be zeroed: %+v", em)
	}

	// Timezone-equivalent cutoffs: the SAME instant expressed in a different
	// zone must return identical results (the service normalizes to UTC).
	loc := time.FixedZone("UTC-5", -5*3600)
	mTz, err := svc.Metrics(ctx, DimensionSendingDomain, sd, start.In(loc), end.In(loc))
	if err != nil {
		t.Fatalf("tz-equivalent Metrics: %v", err)
	}
	if mTz.Volume != m.Volume || mTz.Delivered != m.Delivered || mTz.Bounced != m.Bounced {
		t.Fatalf("timezone-equivalent cutoffs must match: %+v vs %+v", mTz, m)
	}

	// Invalid windows rejected consistently.
	if _, err := svc.Metrics(ctx, DimensionSendingDomain, sd, end, start); err == nil {
		t.Fatal("inverted window must be rejected")
	}
	if _, err := svc.Metrics(ctx, DimensionSendingDomain, sd, start, start.Add(200*24*time.Hour)); err == nil {
		t.Fatal("excessively large window must be rejected")
	}
}

func TestDeliverabilityEquivalence_SQLite(t *testing.T) {
	_, svc := newTestService(t)
	deliverabilityEquivalenceTable(t, svc, 4242, "sqlite-eq")
}

func TestDeliverabilityEquivalence_PostgreSQL(t *testing.T) {
	_, svc := newPostgresDeliverabilityService(t) // skips without PGHOST
	deliverabilityEquivalenceTable(t, svc, 4242, "pg-eq")
}
