package queue_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func postgresQueueDB(t *testing.T) *sql.DB {
	t.Helper()
	if os.Getenv("PGHOST") == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres queue: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", os.Getenv("PGUSER"), os.Getenv("PGPASSWORD"), os.Getenv("PGHOST"), port, os.Getenv("PGDATABASE"))
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("queue_portability_%d", time.Now().UnixNano())
	if _, err := db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		db.Close()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec(`SET search_path TO "` + schema + `"`); err != nil {
		db.Close()
		t.Fatalf("set search path: %v", err)
	}
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		db.Close()
		t.Fatalf("wrap postgres connection with gorm: %v", err)
	}
	if err := models.MigrateAllPostgres(gormDB); err != nil {
		db.Close()
		t.Fatalf("migrate postgres: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
		db.Close()
	})
	return db
}

// TestPostgresQueueAndDeliveryHistory exercises the production queue and
// immutable-attempt repositories twice against the same PostgreSQL schema.
// It catches raw '?', LastInsertId, SQLite datetime(), and INTEGER-vs-BOOLEAN
// regressions in one end-to-end persistence contract.
func TestPostgresQueueAndDeliveryHistory(t *testing.T) {
	db := postgresQueueDB(t)
	ctx := context.Background()
	engine, err := queue.NewQueueEngineChecked(db)
	if err != nil {
		t.Fatalf("new queue engine: %v", err)
	}
	history, err := delivery.NewAttemptHistorySQLRepoChecked(db)
	if err != nil {
		t.Fatalf("new history repo: %v", err)
	}

	for run := 1; run <= 2; run++ {
		entry := &queue.QueueEntry{
			TenantID: 7, MessageID: fmt.Sprintf("pg-queue-%d-%d", time.Now().UnixNano(), run),
			FromAddress: "sender@example.test", ToAddress: "recipient@external.test",
			RecipientDomain: "external.test", Direction: queue.DirectionOutbound,
			DeliveryMode: queue.DeliveryRemoteSMTP,
		}
		if err := engine.Enqueue(ctx, entry); err != nil {
			t.Fatalf("run %d enqueue: %v", run, err)
		}
		if entry.ID == 0 {
			t.Fatalf("run %d enqueue returned zero id", run)
		}
		leased, err := engine.LeaseNext(ctx, fmt.Sprintf("worker-%d", run))
		if err != nil || leased == nil || leased.ID != entry.ID {
			t.Fatalf("run %d lease: entry=%+v err=%v", run, leased, err)
		}
		if err := engine.Repo.DeferWithDiagnostics(ctx, entry.ID, time.Now().UTC(), queue.DeliveryDiagnostics{
			LastError: "temporary", RemoteHost: "relay.example.test", TLSUsed: true,
		}, nil); err != nil {
			t.Fatalf("run %d defer diagnostics: %v", run, err)
		}
		got, err := engine.Repo.Get(ctx, entry.ID, nil)
		if err != nil || got == nil || !got.TLSUsed {
			t.Fatalf("run %d get boolean diagnostics: entry=%+v err=%v", run, got, err)
		}

		attempt := &delivery.DeliveryAttempt{
			QueueEntryID: entry.ID, AttemptNumber: 1, Status: "deferred",
			RemoteHost: "relay.example.test", TLSUsed: true, WorkerID: fmt.Sprintf("worker-%d", run),
		}
		if err := history.RecordAttempt(ctx, attempt, nil); err != nil {
			t.Fatalf("run %d record history: %v", run, err)
		}
		if attempt.ID == 0 {
			t.Fatalf("run %d history returned zero id", run)
		}
		listed, err := history.ListByEntry(ctx, entry.ID, nil)
		if err != nil || len(listed) != 1 || !listed[0].TLSUsed {
			t.Fatalf("run %d list history: attempts=%+v err=%v", run, listed, err)
		}

		// Drain this run's entry before the next iteration.
		//
		// Without this, run 2's LeaseNext returns run 1's entry rather than
		// its own: DeferWithDiagnostics set NextAttemptAt to now, so the
		// earlier entry is immediately due again and wins the queue's
		// oldest-due ordering. That is correct queue behaviour — the test's
		// re-run loop was the thing that assumed otherwise. The bug stayed
		// invisible while no workflow executed this test against PostgreSQL.
		//
		// Acknowledging through AckDeliveredForOwner rather than a bare
		// status update also exercises the LEASE-FENCED acknowledgement path
		// on real PostgreSQL: the ack is accepted only for the worker that
		// currently holds the lease.
		if err := engine.AckDeliveredForOwner(ctx, entry.ID, fmt.Sprintf("worker-%d", run)); err != nil {
			t.Fatalf("run %d ack delivered for lease owner: %v", run, err)
		}
		acked, err := engine.Repo.Get(ctx, entry.ID, nil)
		if err != nil || acked == nil {
			t.Fatalf("run %d reload after ack: entry=%+v err=%v", run, acked, err)
		}
		if acked.Status != queue.StatusDelivered {
			t.Fatalf("run %d entry must be delivered after a fenced ack, got %q", run, acked.Status)
		}

		// A stale worker that no longer holds the lease must not be able to
		// acknowledge the same entry again.
		if err := engine.AckDeliveredForOwner(ctx, entry.ID, "stale-worker"); err == nil {
			t.Fatalf("run %d: a worker without the lease must not acknowledge the entry", run)
		}
	}

	metrics, err := engine.Repo.Metrics(ctx, nil, nil)
	if err != nil || metrics.Total < 2 {
		t.Fatalf("queue metrics: %+v err=%v", metrics, err)
	}
}
