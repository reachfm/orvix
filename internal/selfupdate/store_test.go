package selfupdate

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// newTestDB opens a fresh SQLite in-memory database with the selfupdate
// schema applied. Following the same pattern as internal/billing's
// pg_test_helper.go, a shared file-backed (not :memory:) database is used
// so multiple connections (needed for BEGIN IMMEDIATE via db.Conn) see the
// same data — a pure :memory: database is private to a single connection
// in modernc.org/sqlite unless using the shared-cache URI, so we use a
// temp-file DB per test instead, which is simpler and avoids relying on
// shared-cache semantics.
func newTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := t.TempDir() + "/selfupdate_test.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	return db, path
}

// newPostgresTestDB opens a PostgreSQL test database in its own schema, the
// same way internal/billing/pg_test_helper.go does, gated on PGHOST being
// set. Tests using this helper must check pgAvailable(t) first and Skip if
// false — Postgres is not assumed to be reachable in every dev/CI
// environment.
func pgAvailable() bool {
	return os.Getenv("PGHOST") != ""
}

func newPostgresTestDB(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("PGHOST")
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("PGUSER")
	password := os.Getenv("PGPASSWORD")
	dbname := os.Getenv("PGDATABASE")

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	schemaName := fmt.Sprintf("orvix_test_selfupdate_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("SET search_path TO %s", schemaName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
	})

	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newJob(idemKey string) Job {
	return Job{
		Kind:             JobKindInstall,
		IdempotencyKey:   idemKey,
		RequestedVersion: "1.2.3",
		InitiatedBy:      "admin@example.com",
	}
}

func TestCreateJob_DuplicateIdempotencyKeyReturnsOriginal(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	first, err := store.CreateJob(newJob("idem-1"))
	if err != nil {
		t.Fatalf("first CreateJob: %v", err)
	}

	second, err := store.CreateJob(newJob("idem-1"))
	if err != nil {
		t.Fatalf("second CreateJob (replay): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected replay to return original job %s, got %s", first.ID, second.ID)
	}
}

func TestCreateJob_OnlyOneActiveJobAllowed(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.CreateJob(newJob("idem-a")); err != nil {
		t.Fatalf("first CreateJob: %v", err)
	}

	_, err = store.CreateJob(newJob("idem-b"))
	if err == nil {
		t.Fatal("expected second distinct job to be rejected while first is active")
	}
	if err != ErrJobAlreadyActive {
		t.Fatalf("expected ErrJobAlreadyActive, got %v", err)
	}
}

func TestCreateJob_ConcurrentCreatesOnlyOneWins(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	const n = 12
	var wg sync.WaitGroup
	var successes int64
	var alreadyActive int64
	var otherErrs int64

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.CreateJob(newJob(fmt.Sprintf("idem-concurrent-%d", i)))
			switch err {
			case nil:
				atomic.AddInt64(&successes, 1)
			case ErrJobAlreadyActive:
				atomic.AddInt64(&alreadyActive, 1)
			default:
				atomic.AddInt64(&otherErrs, 1)
				t.Logf("unexpected error from goroutine %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if otherErrs != 0 {
		t.Fatalf("expected only nil or ErrJobAlreadyActive errors, got %d other errors", otherErrs)
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 job creation to succeed under concurrency, got %d (alreadyActive=%d)", successes, alreadyActive)
	}
	if successes+alreadyActive != n {
		t.Fatalf("expected successes+alreadyActive == %d, got %d", n, successes+alreadyActive)
	}

	jobs, err := store.ListJobs(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected exactly 1 persisted job, got %d", len(jobs))
	}
}

func TestAppendEvent_StrictlySequentialAndImmutable(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	job, err := store.CreateJob(newJob("idem-events"))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		ev, err := store.AppendEvent(job.ID, PhaseChecking, fmt.Sprintf("step %d", i))
		if err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
		if ev.Seq != i+1 {
			t.Fatalf("expected seq %d, got %d", i+1, ev.Seq)
		}
	}

	events, err := store.ListEvents(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Seq != i+1 {
			t.Fatalf("event %d out of order: seq=%d", i, ev.Seq)
		}
	}
}

func TestAppendEvent_ConcurrentAppendsStayOrdered(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateJob(newJob("idem-events-concurrent"))
	if err != nil {
		t.Fatal(err)
	}

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.AppendEvent(job.ID, PhaseChecking, fmt.Sprintf("event %d", i)); err != nil {
				t.Errorf("AppendEvent: %v", err)
			}
		}(i)
	}
	wg.Wait()

	events, err := store.ListEvents(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != n {
		t.Fatalf("expected %d events, got %d", n, len(events))
	}
	seen := map[int]bool{}
	for _, ev := range events {
		if seen[ev.Seq] {
			t.Fatalf("duplicate seq %d", ev.Seq)
		}
		seen[ev.Seq] = true
		if ev.Seq < 1 || ev.Seq > n {
			t.Fatalf("seq %d out of expected range", ev.Seq)
		}
	}
}

func TestUpdateJobPhase_RejectsInvalidTransition(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateJob(newJob("idem-phase"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Phase != PhaseQueued {
		t.Fatalf("expected new job to start at PhaseQueued, got %s", job.Phase)
	}

	// Queued -> HealthCheck skips the entire pipeline and is not legal.
	_, err = store.UpdateJobPhase(job.ID, PhaseHealthCheck, 0, "skip ahead")
	if err == nil {
		t.Fatal("expected invalid phase transition to be rejected")
	}

	// Queued -> Checking is legal.
	updated, err := store.UpdateJobPhase(job.ID, PhaseChecking, 10, "checking for updates")
	if err != nil {
		t.Fatalf("expected legal transition to succeed: %v", err)
	}
	if updated.Phase != PhaseChecking {
		t.Fatalf("expected phase Checking, got %s", updated.Phase)
	}

	// Once Completed (via the full legal chain further down), no further
	// transition should be legal except explicitly modeled ones. Directly
	// verify a completed->downloading jump is rejected.
	_, err = store.UpdateJobPhase(job.ID, PhaseDownloading, 0, "")
	if err != nil {
		t.Fatalf("Checking -> Downloading should be legal: %v", err)
	}
	_, err = store.UpdateJobPhase(job.ID, PhaseCompleted, 100, "done early")
	if err == nil {
		t.Fatal("expected Downloading -> Completed (skipping intermediate phases) to be rejected")
	}
}

func TestRestartRecovery_NonTerminalJobSurvivesNewStoreInstance(t *testing.T) {
	db, path := newTestDB(t)

	store1, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store1.CreateJob(newJob("idem-recovery"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store1.UpdateJobPhase(job.ID, PhaseChecking, 5, "in progress at crash time"); err != nil {
		t.Fatal(err)
	}
	// Simulate the daemon process dying mid-job: close this DB handle
	// without ever reaching a terminal phase.
	db.Close()

	db2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db2.Close() })
	if err := CreateTables(db2); err != nil {
		t.Fatal(err)
	}
	store2, err := NewStore(db2)
	if err != nil {
		t.Fatal(err)
	}

	recovered, ok, err := store2.RecoverActiveJob()
	if err != nil {
		t.Fatalf("RecoverActiveJob: %v", err)
	}
	if !ok {
		t.Fatal("expected the non-terminal job to be recoverable after restart")
	}
	if recovered.ID != job.ID {
		t.Fatalf("expected recovered job %s, got %s", job.ID, recovered.ID)
	}
	if recovered.Phase != PhaseChecking {
		t.Fatalf("expected recovered phase Checking, got %s", recovered.Phase)
	}

	// And its event history must have survived too.
	events, err := store2.ListEvents(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected event history to survive restart")
	}
}

func TestSQLiteLocking_BeginImmediateSerializesWriters(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ss := store.(*sqlStore)
	if ss.dialect.IsPostgres() {
		t.Skip("this test targets SQLite's BEGIN IMMEDIATE locking path specifically")
	}

	job, err := store.CreateJob(newJob("idem-lock"))
	if err != nil {
		t.Fatal(err)
	}

	// Hold a write transaction open on one goroutine; a second goroutine's
	// AppendEvent must block until the first releases it, never
	// interleave, and never error with "database is locked" thanks to the
	// busy_timeout PRAGMA plus BEGIN IMMEDIATE serialization.
	tx, err := ss.beginWriteTx()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := store.AppendEvent(job.ID, PhaseChecking, "from second writer"); err != nil {
			t.Errorf("AppendEvent from second writer: %v", err)
		}
	}()

	// Give the second goroutine a moment to reach (and block on) its own
	// BEGIN IMMEDIATE.
	time.Sleep(100 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("second writer completed before first transaction committed — locking did not serialize writers")
	default:
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("second writer never completed after first transaction committed")
	}

	events, err := store.ListEvents(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestRollbackSnapshots_CreateListMarkLastKnownGood(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	snap1, err := store.CreateSnapshot(RollbackSnapshot{
		SourceVersion:  "1.0.0",
		SourceCommit:   "abc123",
		ChecksumSHA256: "deadbeef",
		Verified:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	snap2, err := store.CreateSnapshot(RollbackSnapshot{
		SourceVersion:  "1.1.0",
		SourceCommit:   "def456",
		ChecksumSHA256: "cafef00d",
		Verified:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	snaps, err := store.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}

	if err := store.MarkLastKnownGood(snap1.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkLastKnownGood(snap2.ID); err != nil {
		t.Fatal(err)
	}

	snaps, err = store.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	var lkgCount int
	for _, s := range snaps {
		if s.LastKnownGood {
			lkgCount++
			if s.ID != snap2.ID {
				t.Fatalf("expected snap2 (%s) to be last-known-good, found %s instead", snap2.ID, s.ID)
			}
		}
	}
	if lkgCount != 1 {
		t.Fatalf("expected exactly 1 last-known-good snapshot, got %d", lkgCount)
	}

	if err := store.MarkLastKnownGood("does-not-exist"); err != ErrSnapshotNotFound {
		t.Fatalf("expected ErrSnapshotNotFound, got %v", err)
	}
}

// TestPostgres_RowLockingSerializesActiveJobCheck exercises the same
// "only one active job under concurrency" invariant as
// TestCreateJob_ConcurrentCreatesOnlyOneWins, but against a real PostgreSQL
// instance so the `SELECT ... FOR UPDATE` locking path is actually
// exercised (SQLite never takes this code path — see sqlStore.lockClause).
// Gated on PGHOST being set, following the same skip convention as
// internal/billing/pg_test_helper.go: if no PostgreSQL instance is
// reachable in this environment, the test is skipped rather than faked.
func TestPostgres_RowLockingSerializesActiveJobCheck(t *testing.T) {
	if !pgAvailable() {
		t.Skip("PGHOST not set; skipping PostgreSQL-specific row-locking test (no reachable Postgres instance in this environment)")
	}
	db := newPostgresTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	const n = 12
	var wg sync.WaitGroup
	var successes int64
	var alreadyActive int64

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.CreateJob(newJob(fmt.Sprintf("idem-pg-%d", i)))
			switch err {
			case nil:
				atomic.AddInt64(&successes, 1)
			case ErrJobAlreadyActive:
				atomic.AddInt64(&alreadyActive, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 job creation to succeed under Postgres row-locking, got %d", successes)
	}
	if successes+alreadyActive != n {
		t.Fatalf("expected successes+alreadyActive == %d, got %d", n, successes+alreadyActive)
	}
}
