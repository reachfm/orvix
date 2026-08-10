package jobs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	repo := NewJobRepository(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return NewService(repo), ctx
}

func TestJob_FullLifecycle(t *testing.T) {
	svc, ctx := newTestService(t)
	j, err := svc.CreateJob(ctx, "test-job", []byte(`{"key":"value"}`), 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if j.Status != StatusQueued {
		t.Fatalf("expected queued, got %s", j.Status)
	}
	// Claim
	claimed, err := svc.Claim(ctx, "worker-1", 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed, got %d", len(claimed))
	}
	if claimed[0].Status != StatusRunning {
		t.Fatalf("expected running after claim, got %s", claimed[0].Status)
	}
	// Complete
	if err := svc.Complete(ctx, j.ID, []byte(`{"result":"ok"}`)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	got, _ := svc.Get(ctx, j.ID)
	if got.Status != StatusSucceeded {
		t.Fatalf("expected succeeded, got %s", got.Status)
	}
	if got.Progress != 100 {
		t.Fatalf("expected progress 100, got %d", got.Progress)
	}
}

func TestJob_ClaimIsExclusive(t *testing.T) {
	svc, ctx := newTestService(t)
	_, _ = svc.CreateJob(ctx, "job1", []byte(`{}`), 1)
	_, _ = svc.CreateJob(ctx, "job2", []byte(`{}`), 1)
	// Worker 1 claims
	c1, _ := svc.Claim(ctx, "worker-1", 10)
	if len(c1) != 2 {
		t.Fatalf("expected 2 claimed, got %d", len(c1))
	}
	// Worker 2 claims - should get nothing
	c2, _ := svc.Claim(ctx, "worker-2", 10)
	if len(c2) != 0 {
		t.Fatalf("expected 0 claimed by worker-2, got %d", len(c2))
	}
}

func TestJob_FailAndRetry(t *testing.T) {
	svc, ctx := newTestService(t)
	j, _ := svc.CreateJob(ctx, "job", []byte(`{}`), 1)
	_, _ = svc.Claim(ctx, "worker-1", 10)
	if err := svc.Fail(ctx, j.ID, "temporary error"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	got, _ := svc.Get(ctx, j.ID)
	if got.Status != StatusQueued {
		t.Fatalf("expected re-queued for retry, got %s", got.Status)
	}
	if got.Attempt != 2 {
		t.Fatalf("expected attempt 2, got %d", got.Attempt)
	}
}

func TestJob_StaleRecovery(t *testing.T) {
	svc, ctx := newTestService(t)
	_, _ = svc.CreateJob(ctx, "job", []byte(`{}`), 1)
	n, err := svc.RecoverStaleJobs(ctx, time.Millisecond, 10)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 recovered, got %d", n)
	}
}
