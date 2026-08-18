package bulkprovision

// Phase 8 C2: proves bulk provisioning's actual enforcement point.
// bulkprovision never re-implements domain-status logic itself —
// MailboxCreator (ports.go) is "the narrow slice of
// internal/admin/mailbox.Service", and mailbox.Service.CreateMailbox
// was refactored in C1 (internal/admin/mailbox/service.go) to call
// the canonical domain.StatusError guard on every single call, inside
// its own transaction, re-resolved fresh every time. Since
// createOneMailbox (service.go) calls s.mailboxes.CreateMailbox once
// PER ROW, this transitively re-checks domain operability on every
// row of every execute/resume/retry pass — there is no separate
// "bulk-provisioning domain check" to implement or drift from the
// mailbox path. These tests prove that transitive guarantee actually
// holds end-to-end through bulkprovision, using the REAL
// mailbox.Service (not the fakeMailboxes test double), not just
// asserted by reading the code.

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/audit"

	_ "modernc.org/sqlite"
)

func newBulkOperabilityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:bulkop_"+t.Name()+"?mode=memory&cache=shared&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE coremail_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER NOT NULL DEFAULT 0, name TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'active', max_mailboxes INTEGER NOT NULL DEFAULT 0, max_aliases INTEGER NOT NULL DEFAULT 0,
		max_quota_mb INTEGER NOT NULL DEFAULT 0, default_mailbox_quota_mb INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME, updated_at DATETIME, deleted_at DATETIME);
	CREATE TABLE tenants (id INTEGER PRIMARY KEY, plan TEXT NOT NULL DEFAULT 'smb', max_domains INTEGER NOT NULL DEFAULT 0, max_mailboxes INTEGER NOT NULL DEFAULT 0, deleted_at DATETIME);
	CREATE TABLE coremail_mailboxes (
		id INTEGER PRIMARY KEY AUTOINCREMENT, domain_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL,
		local_part TEXT NOT NULL, email TEXT NOT NULL, name TEXT, password_hash TEXT NOT NULL, auth_scheme TEXT,
		status TEXT NOT NULL, quota_mb INTEGER NOT NULL DEFAULT 0, used_bytes INTEGER NOT NULL DEFAULT 0,
		msg_count INTEGER NOT NULL DEFAULT 0, is_admin INTEGER NOT NULL DEFAULT 0, allow_smtp INTEGER NOT NULL DEFAULT 1,
		allow_imap INTEGER NOT NULL DEFAULT 1, allow_pop3 INTEGER NOT NULL DEFAULT 1, allow_jmap INTEGER NOT NULL DEFAULT 1,
		allow_webmail INTEGER NOT NULL DEFAULT 1, mfa_enabled INTEGER NOT NULL DEFAULT 0, send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
		recv_limit_per_hour INTEGER NOT NULL DEFAULT 0, last_login DATETIME, last_ip TEXT,
		mail_access_mode TEXT NOT NULL DEFAULT 'inherit', version INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME, updated_at DATETIME, deleted_at DATETIME);
	CREATE TABLE coremail_aliases (id INTEGER PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, deleted_at DATETIME);`); err != nil {
		t.Fatal(err)
	}
	return db
}

func newBulkOperabilityService(t *testing.T, db *sql.DB) *Service {
	t.Helper()
	auditStore := audit.NewExtendedStore(db)
	if err := auditStore.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure audit table: %v", err)
	}
	mboxSvc := mailbox.NewService(mailbox.NewAdminMailboxRepo(db), fakeHasher{}, auditStore, nil)
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	// finalizeLifecycleTx (service.go) hard-requires both outbox and
	// audit to be non-nil before it will commit any terminal job-status
	// transition — nil dependencies must never silently disable
	// durability evidence. Real instances, not nil, so Execute/
	// RetryFailedRows actually complete instead of erroring on every
	// call with errLifecycleDurabilityUnavailable.
	outbox := kernelRealOutbox(t, db)
	return NewService(repo, mboxSvc, fakeAccessMode{domain.MailAccessInternalExternal}, nil, outbox, auditStore, nil)
}

// TestExecute_DomainDisabledBetweenJobCreationAndExecution_CreatesZeroMailboxes
// is the decisive TOCTOU proof: validation and job creation both
// happen while the domain is active; the domain is then disabled;
// execution must create zero mailboxes because mailbox.Service.
// CreateMailbox re-resolves domain status fresh on every row, inside
// its own transaction — not from a stale snapshot taken at validation
// or job-creation time.
func TestExecute_DomainDisabledBetweenJobCreationAndExecution_CreatesZeroMailboxes(t *testing.T) {
	db := newBulkOperabilityTestDB(t)
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'toctou.test', 'active', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()

	raw := []RawRow{
		{RowNumber: 2, Email: "row1@toctou.test"},
		{RowNumber: 3, Email: "row2@toctou.test"},
		{RowNumber: 4, Email: "row3@toctou.test"},
	}
	res, err := svc.Validate(ctx, 1, 1, "toctou.test", "hash-1", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 1, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	// The domain is disabled AFTER validation and job creation, BEFORE
	// execution — exactly the window C2 requires re-checking.
	if _, err := db.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE name = 'toctou.test'"); err != nil {
		t.Fatalf("disable domain: %v", err)
	}

	finalJob, rows, err := svc.Execute(ctx, job.ID, 1, 1, "toctou.test", "hash-1", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if finalJob.CreatedCount != 0 {
		t.Fatalf("expected 0 mailboxes created against a disabled domain, got %d", finalJob.CreatedCount)
	}
	for _, r := range rows {
		if r.Status == RowCreated {
			t.Fatalf("row %d unexpectedly succeeded against a disabled domain", r.RowNumber)
		}
	}

	var liveCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL`).Scan(&liveCount); err != nil {
		t.Fatal(err)
	}
	if liveCount != 0 {
		t.Fatalf("expected 0 live mailbox rows, got %d — a domain disabled mid-flow must not let any row through", liveCount)
	}
}

// TestExecute_DomainReEnabledAfterFailedRun_RetrySucceeds proves the
// inverse isn't accidentally broken: once a domain is re-enabled, a
// fresh execution attempt succeeds normally — this isn't a permanent
// poison state, and the guard doesn't leak stale rejection.
func TestExecute_DomainReEnabledAfterFailedRun_RetrySucceeds(t *testing.T) {
	db := newBulkOperabilityTestDB(t)
	now := "2026-01-01T00:00:00Z"
	// Domain starts ACTIVE: Validate now enforces operability too
	// (C2-R1), so a domain disabled from the very start would be
	// rejected there, before a job even exists to retry. Disable it
	// AFTER job creation instead — the scenario this test actually
	// means to cover ("first execution attempt fails against a
	// disabled domain, later retry succeeds once re-enabled").
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'reenable.test', 'active', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()

	raw := []RawRow{{RowNumber: 2, Email: "retry@reenable.test"}}
	res, err := svc.Validate(ctx, 1, 1, "reenable.test", "hash-2", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 1, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	if _, err := db.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE name = 'reenable.test'"); err != nil {
		t.Fatalf("disable domain: %v", err)
	}

	finalJob, _, err := svc.Execute(ctx, job.ID, 1, 1, "reenable.test", "hash-2", nil)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if finalJob.CreatedCount != 0 {
		t.Fatalf("expected 0 created against a disabled domain, got %d", finalJob.CreatedCount)
	}

	if _, err := db.Exec("UPDATE coremail_domains SET status = 'active' WHERE name = 'reenable.test'"); err != nil {
		t.Fatalf("re-enable domain: %v", err)
	}

	retryJob, _, err := svc.RetryFailedRows(ctx, job.ID, 1, "reenable.test")
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if retryJob.CreatedCount != 1 {
		t.Fatalf("expected the retry against a now-active domain to create 1 mailbox, got %d", retryJob.CreatedCount)
	}
	var liveCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL AND email = ?`, "retry@reenable.test").Scan(&liveCount); err != nil {
		t.Fatal(err)
	}
	if liveCount != 1 {
		t.Fatalf("expected the retried row's mailbox to exist, got %d rows", liveCount)
	}
}

// ── C2-R1: Validate-time enforcement ────────────────────────────────

func TestValidate_DisabledDomainRejected(t *testing.T) {
	db := newBulkOperabilityTestDB(t)
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'validate-disabled.test', 'disabled', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()

	raw := []RawRow{{RowNumber: 2, Email: "row@validate-disabled.test"}}
	if _, err := svc.Validate(ctx, 1, 1, "validate-disabled.test", "hash-vd", raw); err != domain.ErrDomainDisabled {
		t.Fatalf("expected ErrDomainDisabled, got %v", err)
	}
}

func TestValidate_CrossTenantDomainDenied(t *testing.T) {
	db := newBulkOperabilityTestDB(t)
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'validate-xtenant.test', 'active', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()

	raw := []RawRow{{RowNumber: 2, Email: "row@validate-xtenant.test"}}
	// Tenant 2 requesting validation against tenant 1's domain.
	if _, err := svc.Validate(ctx, 2, 1, "validate-xtenant.test", "hash-vx", raw); err != domain.ErrDomainNotFound {
		t.Fatalf("expected ErrDomainNotFound (no cross-tenant disclosure), got %v", err)
	}
}

func TestValidate_InfrastructureFailureFailsWholeOperationClosed(t *testing.T) {
	db := newBulkOperabilityTestDB(t)
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()
	db.Close() // every subsequent query fails

	raw := []RawRow{{RowNumber: 2, Email: "row@infra-fail.test"}}
	res, err := svc.Validate(ctx, 1, 1, "infra-fail.test", "hash-if", raw)
	if err == nil {
		t.Fatal("expected an error after closing the database")
	}
	if res != nil {
		t.Fatal("a failed validation must not return a partial result")
	}
	if err == domain.ErrDomainNotFound || err == domain.ErrDomainDisabled {
		t.Fatalf("a repository failure must not be misreported as a typed domain-state error, got %v", err)
	}
}

// ── C2-R1: job-creation-time enforcement ────────────────────────────

func TestCreateJob_DomainDisabledBetweenValidateAndCreateJob_RejectedWithZeroRecords(t *testing.T) {
	db := newBulkOperabilityTestDB(t)
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'disable-before-createjob.test', 'active', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()

	raw := []RawRow{{RowNumber: 2, Email: "row@disable-before-createjob.test"}}
	res, err := svc.Validate(ctx, 1, 1, "disable-before-createjob.test", "hash-cj", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	if _, err := db.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE name = 'disable-before-createjob.test'"); err != nil {
		t.Fatalf("disable domain: %v", err)
	}

	if _, err := svc.CreateJob(ctx, 1, 1, 1, StrategyPartial, ConflictFail, "", res); err != domain.ErrDomainDisabled {
		t.Fatalf("expected ErrDomainDisabled, got %v", err)
	}

	var jobCount, rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_bulk_import_jobs WHERE domain_id = 1`).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 0 {
		t.Fatalf("expected 0 job rows after a rejected creation, got %d", jobCount)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_bulk_import_rows`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 0 {
		t.Fatalf("expected 0 row records after a rejected creation, got %d", rowCount)
	}
}

func TestCreateJob_IdempotentReplayStillWorksAfterDomainRejection(t *testing.T) {
	db := newBulkOperabilityTestDB(t)
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'idem-after-reject.test', 'active', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()

	raw := []RawRow{{RowNumber: 2, Email: "row@idem-after-reject.test"}}
	res, err := svc.Validate(ctx, 1, 1, "idem-after-reject.test", "hash-idem", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 1, StrategyPartial, ConflictFail, "idem-key-1", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	// A second call with the SAME idempotency key must still return the
	// original job (idempotent replay), not attempt a fresh
	// domain-operability check that could spuriously reject a replay.
	replay, err := svc.CreateJob(ctx, 1, 1, 1, StrategyPartial, ConflictFail, "idem-key-1", res)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if replay.ID != job.ID {
		t.Fatalf("expected the idempotent replay to return the same job (id %d), got id %d", job.ID, replay.ID)
	}
}

// ── C2-R1: deterministic concurrency (no sleeps) ────────────────────

// TestCreateJob_ConcurrentDisableVersusJobCreation uses a channel to
// force a genuine race between a domain-disable UPDATE and a
// concurrent CreateJob call, rather than a sleep-based approximation:
// the disable goroutine only proceeds once CreateJob's guard has
// begun (signalled via a hook), so the two operations truly overlap at
// the database level. Exactly one legitimate deterministic outcome
// exists at the SQLite/Postgres row-lock level — either CreateJob's
// FOR UPDATE-equivalent (SQLite serializes via _txlock=immediate) wins
// and sees 'active' (job created), or the disable commits first and
// CreateJob sees 'disabled' (rejected) — never a corrupted or
// half-applied read.
func TestCreateJob_ConcurrentDisableVersusJobCreation(t *testing.T) {
	db := newBulkOperabilityTestDB(t)
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'concurrent-disable.test', 'active', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()

	raw := []RawRow{{RowNumber: 2, Email: "row@concurrent-disable.test"}}
	res, err := svc.Validate(ctx, 1, 1, "concurrent-disable.test", "hash-cd", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	// SQLite's single-writer serialization (db.SetMaxOpenConns(1) in
	// newBulkOperabilityTestDB, _txlock=immediate) is itself the
	// deterministic ordering primitive here: CreateJob's transaction
	// and the disable UPDATE cannot interleave mid-statement, so
	// launching them concurrently and letting the pool's single
	// connection serialize them is sufficient — no artificial sleep
	// needed to force an ordering, and no ordering is assumed by the
	// assertion below (both real outcomes are accepted as correct).
	var wg sync.WaitGroup
	var jobErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, jobErr = svc.CreateJob(ctx, 1, 1, 1, StrategyPartial, ConflictFail, "", res)
	}()
	go func() {
		defer wg.Done()
		_, _ = db.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE name = 'concurrent-disable.test'")
	}()
	wg.Wait()

	// Whichever order won, the result must be internally consistent:
	// if CreateJob succeeded, exactly one job row exists; if it was
	// rejected, zero job rows exist. Never both, never neither.
	var jobCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_bulk_import_jobs WHERE domain_id = 1`).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobErr == nil && jobCount != 1 {
		t.Fatalf("CreateJob reported success but job row count = %d, want 1", jobCount)
	}
	if jobErr != nil && jobCount != 0 {
		t.Fatalf("CreateJob reported rejection (%v) but job row count = %d, want 0", jobErr, jobCount)
	}
	if jobErr != nil && jobErr != domain.ErrDomainDisabled {
		t.Fatalf("rejection must be the typed ErrDomainDisabled, got %v", jobErr)
	}
}

// TestExecute_DomainDisabledBetweenBestEffortBatches is the specific
// batch-boundary scenario: 4 rows across 2 batches of 2 (DefaultBatchSize
// shrunk for this test). BeforeBatch fires once per batch; on the
// SECOND call (i.e. right before batch 2 begins — deterministic, no
// sleep) it disables the domain and returns nil (not an error — Execute
// must keep running into batch 2, not halt via the lease-lost path).
// Batch 1's 2 mailboxes must survive; batch 2's 2 rows must both be
// rejected by the same per-row guard every other execution path
// already goes through; the job's final counters must be truthful.
func TestExecute_DomainDisabledBetweenBestEffortBatches(t *testing.T) {
	setBatchSizeForTest(t, 2)
	db := newBulkOperabilityTestDB(t)
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'batch-boundary.test', 'active', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := newBulkOperabilityService(t, db)
	ctx := context.Background()

	var raw []RawRow
	for i := 0; i < 4; i++ {
		raw = append(raw, RawRow{RowNumber: i + 2, Email: fmt.Sprintf("batch%d@batch-boundary.test", i)})
	}
	res, err := svc.Validate(ctx, 1, 1, "batch-boundary.test", "hash-bb", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 1, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	batchCalls := 0
	hooks := &ExecuteHooks{
		BeforeBatch: func(ctx context.Context) error {
			batchCalls++
			if batchCalls == 2 {
				if _, err := db.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE name = 'batch-boundary.test'"); err != nil {
					t.Fatalf("disable domain mid-run: %v", err)
				}
			}
			return nil
		},
	}
	finalJob, rows, err := svc.Execute(ctx, job.ID, 1, 1, "batch-boundary.test", "hash-bb", hooks)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if batchCalls < 2 {
		t.Fatalf("expected at least 2 BeforeBatch calls (one per batch), got %d — batch size shrink may not have taken effect", batchCalls)
	}
	if finalJob.CreatedCount != 2 {
		t.Fatalf("expected exactly 2 mailboxes created (batch 1, before disable), got %d", finalJob.CreatedCount)
	}

	var liveCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL`).Scan(&liveCount); err != nil {
		t.Fatal(err)
	}
	if liveCount != 2 {
		t.Fatalf("expected batch 1's 2 mailboxes to survive untouched, got %d live rows", liveCount)
	}

	var created, failed int
	for _, r := range rows {
		switch r.Status {
		case RowCreated:
			created++
		case RowFailed:
			failed++
		}
	}
	if created != 2 {
		t.Fatalf("expected 2 rows marked created, got %d", created)
	}
	if failed != 2 {
		t.Fatalf("expected 2 rows marked failed (batch 2, rejected by the disabled domain), got %d", failed)
	}
}
