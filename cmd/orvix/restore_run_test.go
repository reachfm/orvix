package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/backup"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/restorecoord"
	"go.uber.org/zap"

	_ "modernc.org/sqlite"
)

// buildRealRestoreService constructs a genuine backup.Service backed by a temp
// SQLite registry and a real created backup, so the coordinator tests exercise
// the actual activate/restart/health/rollback ordering and durable state
// transitions rather than a mock.
func buildRealRestoreService(t *testing.T) (*backup.Service, string) {
	t.Helper()
	base := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(base, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS backup_registry (
		id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'pending',
		size_bytes INTEGER NOT NULL DEFAULT 0, sha256 TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL, completed_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	mailDir := filepath.Join(base, "mailstore")
	os.MkdirAll(mailDir, 0o750)
	os.WriteFile(filepath.Join(mailDir, "test.eml"), []byte("Subject: t\r\n\r\nbody"), 0o640)
	attachDir := filepath.Join(base, "attachments")
	os.MkdirAll(attachDir, 0o750)

	svc := backup.NewService(filepath.Join(base, "backups"), db, db, mailDir, attachDir)
	svc.SetStagingRoot(filepath.Join(base, "restore-staging"))
	svc.SetDatabasePath(filepath.Join(base, "restored.db"))

	b, err := svc.CreateBackup(context.Background(), "coordinator-test")
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	return svc, b.ID
}

func newTestCoordWithJob(t *testing.T, backupID string) (*restorecoord.Coordinator, string) {
	t.Helper()
	c := restorecoord.New(t.TempDir())
	if err := c.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	job, err := c.Submit(backupID, "user:test")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return c, job.ID
}

// Success path: restart then health both succeed -> durable succeeded, and the
// terminal success is recorded only after the health callback ran.
func TestRunOneRestoreJob_Success_OrderedDurable(t *testing.T) {
	svc, backupID := buildRealRestoreService(t)
	coord, jobID := newTestCoordWithJob(t, backupID)

	var order []string
	restart := func(context.Context) error {
		// At the moment restart runs, the durable status must be "restarting"
		// and must NOT yet be a success.
		r, _ := coord.GetResult(jobID)
		if r.Status != restorecoord.StatusRestarting {
			t.Errorf("status during restart = %s, want restarting", r.Status)
		}
		order = append(order, "restart")
		return nil
	}
	health := func(context.Context) error {
		r, _ := coord.GetResult(jobID)
		if r.Status != restorecoord.StatusVerifying {
			t.Errorf("status during health = %s, want verifying", r.Status)
		}
		if r.Status == restorecoord.StatusSucceeded {
			t.Error("success recorded before health completed")
		}
		order = append(order, "health")
		return nil
	}

	if err := runOneRestoreJob(context.Background(), coord, svc, jobID, restart, health, zap.NewNop()); err != nil {
		t.Fatalf("runOneRestoreJob: %v", err)
	}
	res, _ := coord.GetResult(jobID)
	if res.Status != restorecoord.StatusSucceeded {
		t.Fatalf("final status = %s, want succeeded (msg=%s err=%s)", res.Status, res.Message, res.Error)
	}
	if len(order) != 2 || order[0] != "restart" || order[1] != "health" {
		t.Fatalf("callback order = %v, want [restart health]", order)
	}
}

// Restart failure -> rollback -> durable failed with rolled_back recorded.
func TestRunOneRestoreJob_RestartFailure_RollsBack(t *testing.T) {
	svc, backupID := buildRealRestoreService(t)
	coord, jobID := newTestCoordWithJob(t, backupID)

	calls := 0
	restart := func(context.Context) error {
		calls++
		if calls == 1 {
			return fmt.Errorf("systemctl restart failed")
		}
		return nil // rollback restart recovers
	}
	healthCalled := false
	health := func(context.Context) error { healthCalled = true; return nil }

	err := runOneRestoreJob(context.Background(), coord, svc, jobID, restart, health, zap.NewNop())
	if err == nil {
		t.Fatal("expected job failure")
	}
	res, _ := coord.GetResult(jobID)
	if res.Status != restorecoord.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if !res.RolledBack {
		t.Fatal("expected rolled_back=true")
	}
	if res.Error == "" {
		t.Fatal("expected preserved error chain")
	}
	if healthCalled {
		t.Fatal("health must not run after a failed restart")
	}
}

// Health failure -> rollback -> durable failed.
func TestRunOneRestoreJob_HealthFailure_RollsBack(t *testing.T) {
	svc, backupID := buildRealRestoreService(t)
	coord, jobID := newTestCoordWithJob(t, backupID)

	restart := func(context.Context) error { return nil }
	health := func(context.Context) error { return fmt.Errorf("service unhealthy after restart") }

	err := runOneRestoreJob(context.Background(), coord, svc, jobID, restart, health, zap.NewNop())
	if err == nil {
		t.Fatal("expected job failure")
	}
	res, _ := coord.GetResult(jobID)
	if res.Status != restorecoord.StatusFailed || !res.RolledBack {
		t.Fatalf("expected failed+rolled_back, got status=%s rolled_back=%v", res.Status, res.RolledBack)
	}
	if res.Status == restorecoord.StatusSucceeded {
		t.Fatal("must never record success on health failure")
	}
}

// The test-only forced health failure must be honored in non-production and
// ignored in production so it can never be activated accidentally there.
func TestProbeOrvixHealth_ForcedFailureIsNonProductionOnly(t *testing.T) {
	t.Setenv("ORVIX_RESTORE_FORCE_HEALTH_FAILURE", "1")

	nonProd := &config.Config{}
	nonProd.Server.AdminPort = 59999 // nothing is listening
	if err := probeOrvixHealth(context.Background(), nonProd); err == nil || !strings.Contains(err.Error(), "test-only injection") {
		t.Fatalf("non-production must honor forced failure, got %v", err)
	}

	prod := &config.Config{}
	prod.Server.AdminPort = 59999
	prod.Database.DeploymentMode = "production"
	// In production the injection is ignored; the probe instead really tries
	// HTTP and fails against the bounded context (nothing is listening).
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := probeOrvixHealth(ctx, prod)
	if err == nil {
		t.Fatal("expected a health error in production against a dead port")
	}
	if strings.Contains(err.Error(), "test-only injection") {
		t.Fatal("production must IGNORE the forced-failure injection")
	}
}

// A terminal job is never reprocessed.
func TestRunOneRestoreJob_SkipsTerminal(t *testing.T) {
	svc, backupID := buildRealRestoreService(t)
	coord, jobID := newTestCoordWithJob(t, backupID)
	res, _ := coord.GetResult(jobID)
	res.Status = restorecoord.StatusSucceeded
	_ = coord.WriteResult(res)

	called := false
	restart := func(context.Context) error { called = true; return nil }
	if err := runOneRestoreJob(context.Background(), coord, svc, jobID, restart, restart, zap.NewNop()); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if called {
		t.Fatal("terminal job must not be reprocessed")
	}
}
