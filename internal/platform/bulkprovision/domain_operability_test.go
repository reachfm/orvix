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
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'reenable.test', 'disabled', 0, ?, ?)`, now, now); err != nil {
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
