package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
	_ "modernc.org/sqlite"
)

func newTestService(t *testing.T) (*Service, *Repository, *Registry, *kernel.FixedClock, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db")+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewJobRepository(db)
	if err = repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err = registry.Register(Definition{Type: "tenant.domain.verify", Scope: ScopeTenant, PayloadVersion: 1, Timeout: time.Minute, Validate: func(payload json.RawMessage) error {
		var body struct {
			DomainID uint `json:"domain_id"`
		}
		if json.Unmarshal(payload, &body) != nil || body.DomainID == 0 {
			return errors.New("domain_id required")
		}
		return nil
	}, Handle: func(context.Context, Execution, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"verified":true}`), nil
	}}); err != nil {
		t.Fatal(err)
	}
	clock := kernel.NewFixedClock(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	return NewServiceWithRegistry(repo, registry, clock), repo, registry, clock, db
}

func validSubmission(key string) Submission {
	return Submission{TenantID: 7, Scope: ScopeTenant, Actor: "user:42", Type: "tenant.domain.verify", Payload: json.RawMessage(`{"domain_id":9}`), IdempotencyKey: key, CorrelationID: "req-1"}
}

func TestStatusTransitionTableIsClosed(t *testing.T) {
	valid := map[Status][]Status{
		StatusQueued:    {StatusRunning, StatusCancelled},
		StatusRunning:   {StatusQueued, StatusSucceeded, StatusFailed, StatusCancelled},
		StatusFailed:    {StatusQueued},
		StatusSucceeded: {}, StatusCancelled: {},
	}
	all := []Status{StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, "invented"}
	for _, from := range all {
		for _, to := range all {
			want := false
			for _, allowed := range valid[from] {
				want = want || allowed == to
			}
			if got := from.CanTransition(to); got != want {
				t.Fatalf("transition %s -> %s got=%v want=%v", from, to, got, want)
			}
		}
	}
}

func TestSubmitIdempotencyAndPayloadNormalization(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)
	first := validSubmission("idem-1")
	first.Payload = json.RawMessage(`{ "domain_id" : 9 }`)
	job, replay, err := svc.Submit(context.Background(), first)
	if err != nil || replay {
		t.Fatalf("first submit replay=%v err=%v", replay, err)
	}
	second, replay, err := svc.Submit(context.Background(), validSubmission("idem-1"))
	if err != nil || !replay || second.ID != job.ID {
		t.Fatalf("replay job=%+v replay=%v err=%v", second, replay, err)
	}
	changed := validSubmission("idem-1")
	changed.Payload = json.RawMessage(`{"domain_id":10}`)
	if _, _, err = svc.Submit(context.Background(), changed); !errors.Is(err, ErrIdempotencyReuse) {
		t.Fatalf("changed payload err=%v", err)
	}
}

func TestConcurrentIdenticalSubmissionPersistsOnce(t *testing.T) {
	svc, _, _, _, db := newTestService(t)
	const workers = 12
	ids := make(chan uint, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, _, err := svc.Submit(context.Background(), validSubmission("same-key"))
			if err == nil {
				ids <- job.ID
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	var id uint
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for got := range ids {
		if id == 0 {
			id = got
		} else if got != id {
			t.Fatalf("multiple job ids: %d and %d", id, got)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestSubmissionRejectsUnknownTypeScopeAndSensitivePayload(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)
	unknown := validSubmission("unknown")
	unknown.Type = "shell.exec"
	if _, _, err := svc.Submit(context.Background(), unknown); !errors.Is(err, ErrUnknownJobType) {
		t.Fatalf("unknown type err=%v", err)
	}
	wrongScope := validSubmission("scope")
	wrongScope.Scope = ScopePlatform
	wrongScope.TenantID = 0
	if _, _, err := svc.Submit(context.Background(), wrongScope); err == nil {
		t.Fatal("wrong scope accepted")
	}
	sensitive := validSubmission("secret")
	sensitive.Payload = json.RawMessage(`{"domain_id":9,"token":"do-not-store"}`)
	if _, _, err := svc.Submit(context.Background(), sensitive); err == nil {
		t.Fatal("sensitive payload accepted")
	}
}

func TestTenantIsolationAndPagination(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)
	job, _, err := svc.Submit(context.Background(), validSubmission("tenant-a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(context.Background(), job.ID, 8, ScopeTenant); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get err=%v", err)
	}
	page, err := svc.List(context.Background(), ListFilter{TenantID: 8, Scope: ScopeTenant, Page: kernel.PageRequest{Page: 1, PageSize: 10}})
	if err != nil || page.TotalCount != 0 {
		t.Fatalf("cross-tenant list=%+v err=%v", page, err)
	}
}

func TestEnsureSchemaMigratesLegacyJobsAdditively(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE platform_jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, status TEXT NOT NULL,
		progress INTEGER NOT NULL DEFAULT 0, result TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL DEFAULT '', worker_leased_to TEXT NOT NULL DEFAULT '',
		attempt_count INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 3,
		payload TEXT NOT NULL DEFAULT '{}', tenant_id INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, started_at DATETIME,
		completed_at DATETIME, next_run_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = db.Exec(`INSERT INTO platform_jobs(type,status,payload,created_at,updated_at,next_run_at) VALUES('legacy','queued','{}',?,?,?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewJobRepository(db)
	if err = repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := repo.Get(context.Background(), 1)
	if err != nil || job.Type != "legacy" || job.RunAfter.IsZero() {
		t.Fatalf("legacy row not preserved: job=%+v err=%v", job, err)
	}
}

func TestPostgresQueriesUseDialectPlaceholders(t *testing.T) {
	repo := &Repository{dialect: dbdialect.FromDriver("postgres")}
	got := repo.q(`UPDATE platform_jobs SET status=? WHERE id=? AND lease_token=?`)
	if got != `UPDATE platform_jobs SET status=$1 WHERE id=$2 AND lease_token=$3` {
		t.Fatalf("postgres rewrite=%q", got)
	}
}
