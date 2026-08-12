package deliverability

// PostgreSQL portability suite for the Phase B suppression lifecycle
// and deliverability aggregation SQL. Gated on PGHOST +
// ORVIX_RUN_POSTGRES_DML_TEST=1 (repo convention, see
// internal/auth/pg_engine_test.go): local SQLite runs skip; the
// PostgreSQL Runtime DML workflow executes against a real server. The
// suite proves the aggregate queries, guarded transitions, UTC
// bucket expressions, and upserts are portable (no SQLite-only
// constructs, no LastInsertId).

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/orvix/orvix/internal/platform/kernel"
)

func newPostgresDeliverabilityService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	host := os.Getenv("PGHOST")
	if host == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres deliverability: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	dsn := "postgres://" + os.Getenv("PGUSER") + ":" + os.Getenv("PGPASSWORD") + "@" + host + ":" + port + "/" + os.Getenv("PGDATABASE") + "?sslmode=disable"
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db, NewService(repo, nil, nil, kernel.SystemClock{})
}

func TestPostgresSuppressionLifecycle(t *testing.T) {
	_, svc := newPostgresDeliverabilityService(t)
	ctx := context.Background()
	sup, err := svc.AddSuppression(ctx, 1, "pg@example.test", SuppressionManual, "operator", 42, "", nil)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if sup.ID == 0 || sup.State != SuppressionActive {
		t.Fatalf("unexpected suppression after postgres add: %+v", sup)
	}
	blocked, _ := svc.IsSuppressed(ctx, 1, "PG@example.test")
	if !blocked {
		t.Fatal("case-normalized suppression must block on postgres")
	}
	if err := svc.ReleaseSuppression(ctx, sup.ID, 1, 7, "pg test"); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, _ := svc.GetSuppression(ctx, sup.ID, 1)
	if got.State != SuppressionReleased {
		t.Fatalf("expected released on postgres: %+v", got)
	}
	blocked, _ = svc.IsSuppressed(ctx, 1, "pg@example.test")
	if blocked {
		t.Fatal("released suppression must not block on postgres")
	}
	events, err := svc.ListSuppressionEvents(ctx, sup.ID, 1, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("expected 2 lifecycle events on postgres, got %d err=%v", len(events), err)
	}
}

func TestPostgresSuppressionConcurrency_UpsertSingleRow(t *testing.T) {
	db, svc := newPostgresDeliverabilityService(t)
	ctx := context.Background()
	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			_, err := svc.AddSuppression(ctx, 1, "pg-race@example.test", SuppressionManual, "s", 0, "", nil)
			errs <- err
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent add: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_deliverability_suppressions WHERE tenant_id=1 AND address='pg-race@example.test'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one logical suppression on postgres, got %d", count)
	}
}

func TestPostgresMetricsAggregationAndBuckets(t *testing.T) {
	_, svc := newPostgresDeliverabilityService(t)
	ctx := context.Background()
	svc.RecordDeliveryOutcome(ctx, "pg-e1", 1, "pg.example", "rcpt.example", "provider-pg", SignalDelivered, 10)
	svc.RecordDeliveryOutcome(ctx, "pg-e2", 1, "pg.example", "rcpt.example", "provider-pg", SignalBounce, 20)
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	m, err := svc.MetricsSummary(ctx, 1, start, end)
	if err != nil {
		t.Fatalf("metrics on postgres: %v", err)
	}
	if m.Volume != 8 || m.Delivered != 4 || m.Bounced != 4 {
		t.Fatalf("aggregation wrong on postgres: %+v", m)
	}
	if len(m.TimeBuckets) == 0 {
		t.Fatal("expected UTC buckets on postgres")
	}
	for _, b := range m.TimeBuckets {
		if _, err := time.Parse(time.RFC3339, b.Start); err != nil {
			t.Fatalf("bucket key must be RFC3339 UTC on postgres, got %q: %v", b.Start, err)
		}
	}
	// Tenant isolation on postgres.
	m2, err := svc.MetricsSummary(ctx, 2, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Volume != 0 {
		t.Fatalf("tenant isolation violated on postgres: %+v", m2)
	}
}

func TestPostgresAggregate_NullFreeEmptyWindow(t *testing.T) {
	_, svc := newPostgresDeliverabilityService(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC()
	m, err := svc.MetricsSummary(ctx, 7, start, end)
	if err != nil {
		t.Fatalf("empty-window aggregate on postgres: %v", err)
	}
	if m.Volume != 0 || m.Delivered != 0 {
		t.Fatalf("empty window must be zeroed on postgres: %+v", m)
	}
}

var _ = errors.Is
