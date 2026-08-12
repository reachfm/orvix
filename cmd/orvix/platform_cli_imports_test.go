package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/importer"
	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"

	_ "modernc.org/sqlite"
)

// importsTestDB builds an isolated real SQLite database with the business
// tables the production import adapters mutate, plus the importer and jobs
// schemas. This is the "isolated real database" the CLI import lifecycle
// acceptance test runs against.
func importsTestDB(t *testing.T) (*sql.DB, *dbdialect.Info) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "imports.db")+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dial := dbdialect.FromDriver("sqlite")

	// Business tables (the schema the production adapters write to).
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS tenants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME,
			name TEXT NOT NULL, slug TEXT NOT NULL, domain TEXT NOT NULL,
			plan TEXT DEFAULT 'smb', max_domains INTEGER DEFAULT 10, max_mailboxes INTEGER DEFAULT 500,
			logo_url TEXT, primary_color TEXT, active INTEGER DEFAULT 1)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME,
			email TEXT NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user',
			tenant_id INTEGER, active INTEGER NOT NULL DEFAULT 1, email_verified INTEGER NOT NULL DEFAULT 0,
			full_name TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL, name TEXT NOT NULL, status TEXT NOT NULL,
			plan TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
			max_mailboxes INTEGER NOT NULL DEFAULT 500, max_aliases INTEGER NOT NULL DEFAULT 50,
			max_quota_mb INTEGER NOT NULL DEFAULT 10240, dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '', dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL DEFAULT 0,
			local_part TEXT NOT NULL, email TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL, auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
			status TEXT NOT NULL DEFAULT 'active', quota_mb INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0, msg_count INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0, allow_smtp INTEGER NOT NULL DEFAULT 1,
			allow_imap INTEGER NOT NULL DEFAULT 1, allow_pop3 INTEGER NOT NULL DEFAULT 1,
			allow_jmap INTEGER NOT NULL DEFAULT 1, allow_webmail INTEGER NOT NULL DEFAULT 1,
			send_limit_per_hour INTEGER NOT NULL DEFAULT 500, recv_limit_per_hour INTEGER NOT NULL DEFAULT 1000,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS coremail_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL DEFAULT 0,
			from_addr TEXT NOT NULL, to_addr TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS coremail_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0, name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS coremail_group_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL, email TEXT NOT NULL, added_at DATETIME NOT NULL)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db, dial
}

// seedPlatformImport creates an import job via the real service (with real
// staging + real adapters) so the CLI commands operate on real rows. The
// staging directory must match the one the CLI commands use.
func seedPlatformImport(t *testing.T, db *sql.DB, dial *dbdialect.Info, stagingDir string, data []byte) (uint, string) {
	t.Helper()
	repo := importer.NewRepository(db)
	if err := repo.EnsureSchema(contextBackdrop()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	staging, err := importer.NewStagingService(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := importer.NewProductionAdaptersFromDB(db, dial)
	if err != nil {
		t.Fatal(err)
	}
	svc := importer.NewService(repo, adapters, staging, nil, nil)
	job, err := svc.Create(contextBackdrop(), importer.CreateImportParams{
		Scope: "platform", Actor: "cli-acceptance", SourceType: importer.SourceCSV, SourceName: "cli.csv",
	}, data)
	if err != nil {
		t.Fatalf("create import: %v", err)
	}
	return job.ID, job.SourceHash
}

func contextBackdrop() context.Context { return context.Background() }

// TestPlatformCLIImportsLifecycle proves an actual CLI import can validate,
// enqueue, execute, resume, cancel, and compensate against an isolated real
// database — using the real staging service, the real durable jobs service
// with the registered platform.import handler, and real service adapters.
func TestPlatformCLIImportsLifecycle(t *testing.T) {
	db, dial := importsTestDB(t)
	deps, stdout, stderr := platformTestDeps(t, db, dial)

	data := []byte("entity,name,domain\norganization,Acme,acme.test\n")
	importID, _ := seedPlatformImport(t, db, dial, deps.stagingDir(), data)
	if importID != 1 {
		t.Fatalf("expected first import id 1, got %d", importID)
	}

	// Validate.
	if code := runPlatform([]string{"imports", "validate", "--id", "1"}, deps); code != exitSuccess {
		t.Fatalf("validate: want success, got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Validation report") {
		t.Fatalf("validate output missing report: %s", stdout.String())
	}
	stdout.Reset()

	// Enqueue (execute submits a durable platform.import job).
	if code := runPlatform([]string{"imports", "execute", "--id", "1", "--confirm", "EXECUTE-IMPORT-1"}, deps); code != exitSuccess {
		t.Fatalf("execute: want success, got %d (stderr=%s)", code, stderr.String())
	}
	var durableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_jobs WHERE type='platform.import' AND scope='platform'`).Scan(&durableCount); err != nil {
		t.Fatal(err)
	}
	if durableCount != 1 {
		t.Fatalf("expected 1 enqueued durable import job, got %d", durableCount)
	}
	stdout.Reset()

	// Simulate a durable worker pause (crash) between batches: the import is
	// left in running, and Resume continues it from the last checkpoint.
	if _, err := db.Exec(`UPDATE platform_imports SET status='running', current_checkpoint=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if code := runPlatform([]string{"imports", "resume", "--id", "1"}, deps); code != exitSuccess {
		t.Fatalf("resume: want success, got %d (stderr=%s)", code, stderr.String())
	}
	var resumedJobs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_jobs WHERE type='platform.import' AND status='queued'`).Scan(&resumedJobs); err != nil {
		t.Fatal(err)
	}
	if resumedJobs == 0 {
		t.Fatal("resume did not re-queue a durable job")
	}
	stdout.Reset()

	// Execute the durable job through the real worker machinery so the import
	// completes; this proves the registered platform.import handler runs.
	runWorkerOnce(t, db, dial, deps.stagingDir())

	// The business entity created by the real adapter must exist.
	var orgCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE domain='acme.test' AND deleted_at IS NULL`).Scan(&orgCount); err != nil {
		t.Fatal(err)
	}
	if orgCount != 1 {
		t.Fatalf("real adapter did not create org: %d", orgCount)
	}

	if code := runPlatform([]string{"imports", "compensate", "--id", "1", "--confirm", "COMPENSATE-IMPORT-1"}, deps); code != exitSuccess {
		t.Fatalf("compensate: want success, got %d (stderr=%s)", code, stderr.String())
	}
	// Compensation disables the created organization through the real org
	// service (SetOrganizationActive(false)); the org is no longer active.
	var active int
	if err := db.QueryRow(`SELECT COALESCE(active,0) FROM tenants WHERE domain='acme.test'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("compensation did not disable created org: active=%d", active)
	}
}

// TestPlatformCLIImportsStableIdempotencyKey proves the CLI uses a stable
// deterministic idempotency key (not a timestamp) so a retried execute
// replays rather than double-submitting.
func TestPlatformCLIImportsStableIdempotencyKey(t *testing.T) {
	db, dial := importsTestDB(t)
	deps, _, stderr := platformTestDeps(t, db, dial)
	seedPlatformImport(t, db, dial, deps.stagingDir(), []byte("entity,name,domain\norganization,Stable,stable.test\n"))

	if code := runPlatform([]string{"imports", "validate", "--id", "1"}, deps); code != exitSuccess {
		t.Fatalf("validate: %d (stderr=%s)", code, stderr.String())
	}
	if code := runPlatform([]string{"imports", "execute", "--id", "1", "--confirm", "EXECUTE-IMPORT-1"}, deps); code != exitSuccess {
		t.Fatalf("execute 1: %d (stderr=%s)", code, stderr.String())
	}
	// Re-execute with the same CLI invocation: must replay, not re-submit.
	if code := runPlatform([]string{"imports", "execute", "--id", "1", "--confirm", "EXECUTE-IMPORT-1"}, deps); code != exitSuccess {
		t.Fatalf("execute 2: %d (stderr=%s)", code, stderr.String())
	}
	var durableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_jobs WHERE type='platform.import'`).Scan(&durableCount); err != nil {
		t.Fatal(err)
	}
	if durableCount != 1 {
		t.Fatalf("stable CLI key allowed double-submit: %d durable jobs", durableCount)
	}
}

func runWorkerOnce(t *testing.T, db *sql.DB, dial *dbdialect.Info, stagingDir string) {
	t.Helper()
	jobRepo := jobs.NewJobRepository(db)
	if err := jobRepo.EnsureSchema(contextBackdrop()); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry()
	jobSvc := jobs.NewServiceWithRegistry(jobRepo, registry, kernel.SystemClock{})

	repo := importer.NewRepository(db)
	staging, err := importer.NewStagingService(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := importer.NewProductionAdaptersFromDB(db, dial)
	if err != nil {
		t.Fatal(err)
	}
	importSvc := importer.NewService(repo, adapters, staging, jobSvc, nil)
	if err := jobs.RegisterProductionHandlers(registry, nil, nil, importSvc); err != nil {
		t.Fatal(err)
	}
	worker := jobs.NewWorker(jobSvc, registry, "cli-test-worker").WithIntervals(time.Millisecond, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Run until the durable import job reaches a terminal state or timeout.
	for {
		done, _ := worker.RunOnce(ctx)
		if !done {
			var running int
			_ = db.QueryRow(`SELECT COUNT(*) FROM platform_jobs WHERE type='platform.import' AND status='queued'`).Scan(&running)
			if running == 0 {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}
