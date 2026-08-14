package bulkprovision

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── R1-C: lifecycle audit/outbox must be transactional, not best-effort ──
//
// These tests prove finalizeLifecycleTx's contract: the terminal
// job-status transition and its outbox event + audit entry commit or
// roll back TOGETHER, a nil dependency fails closed instead of
// silently skipping evidence, and a later successful retry never
// duplicates a mailbox that a prior (evidence-failed) attempt already
// created.

// brokenOutbox is a *kernel.OutboxRepository pointed at a database
// where the outbox table was deliberately never created, so Enqueue
// fails with a real "no such table" error — genuine failure injection,
// not a mock.
func brokenOutbox() *kernel.OutboxRepository {
	return kernel.NewOutboxRepository(dbdialect.FromDriver("sqlite"))
}

// brokenAudit is an *audit.ExtendedStore pointed at a database where
// EnsureTable was never called, so RecordTx fails with a real "no such
// table" error.
func brokenAudit(db *sql.DB) *audit.ExtendedStore {
	return audit.NewExtendedStore(db)
}

func TestExecute_NilOutboxDependencyFailsClosed(t *testing.T) {
	_, repo, _, as := newDurableTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, as, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-nilob", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	final, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-nilob", nil)
	if err == nil {
		t.Fatal("expected a nil outbox dependency to fail closed, got success")
	}
	if final != nil {
		t.Fatalf("a failed-closed Execute must not return a job, got %+v", final)
	}
	if !errors.Is(err, errLifecycleDurabilityUnavailable) {
		t.Fatalf("expected errLifecycleDurabilityUnavailable, got %v", err)
	}

	reread, rerr := repo.GetJob(ctx, job.ID, 1)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if reread.Status == JobCompleted || reread.Status == JobPartiallyFailed {
		t.Fatalf("job must NOT be reported terminal-success when lifecycle evidence could not be written, got %s", reread.Status)
	}
}

func TestExecute_NilAuditDependencyFailsClosed(t *testing.T) {
	_, repo, ob, _ := newDurableTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, nil, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-nilaudit", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	if _, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-nilaudit", nil); !errors.Is(err, errLifecycleDurabilityUnavailable) {
		t.Fatalf("expected errLifecycleDurabilityUnavailable, got %v", err)
	}
}

// TestExecute_OutboxInsertFailureRollsBackJobStatus proves that when
// the lifecycle outbox insert fails, the job-status UPDATE in the SAME
// transaction is rolled back too — the job is never left claiming
// completion with no durable evidence.
func TestExecute_OutboxInsertFailureRollsBackJobStatus(t *testing.T) {
	db, repo := newTestRepo(t) // NOTE: plain repo — outbox schema deliberately never created on this db
	as := audit.NewExtendedStore(db)
	if err := as.EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	fm := newFakeMailboxes(0)
	broken := brokenOutbox() // targets db, but the outbox table doesn't exist there
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, broken, as, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-ob-fail", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	if _, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-ob-fail", nil); err == nil {
		t.Fatal("expected the outbox insert failure to surface as an error")
	}

	reread, rerr := repo.GetJob(ctx, job.ID, 1)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if reread.Status == JobCompleted {
		t.Fatalf("job status must have rolled back with the failed outbox insert, got %s", reread.Status)
	}

	// The one-row mailbox mutation itself, made BEFORE this transaction
	// via the canonical mailbox service, is unaffected by this
	// lifecycle-only rollback — it already committed independently.
	if _, ok := fm.byEmail["a@x.test"]; !ok {
		t.Fatal("expected the canonical mailbox creation (a separate, already-committed transaction) to be unaffected by the lifecycle rollback")
	}

	// A retry with a real outbox now available reconciles cleanly:
	// no duplicate mailbox, job reaches a genuine terminal state.
	repaired := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, kernelRealOutbox(t, db), as, nil)
	final, _, ferr := repaired.Execute(ctx, job.ID, 1, 1, "x.test", "hash-ob-fail", nil)
	if ferr != nil {
		t.Fatalf("expected the retry to reconcile once real outbox evidence is available: %v", ferr)
	}
	if final.Status != JobCompleted || final.CreatedCount != 1 {
		t.Fatalf("expected the retry to complete with exactly 1 created mailbox, got status=%s created=%d", final.Status, final.CreatedCount)
	}
	if len(fm.byEmail) != 1 {
		t.Fatalf("expected exactly 1 distinct mailbox after crash+retry, got %d", len(fm.byEmail))
	}
}

// TestExecute_AuditInsertFailureRollsBackJobStatus mirrors the outbox
// case for the audit dependency.
func TestExecute_AuditInsertFailureRollsBackJobStatus(t *testing.T) {
	db, repo := newTestRepo(t) // NOTE: plain repo — audit table deliberately never created on this db
	ob := kernelRealOutbox(t, db)
	fm := newFakeMailboxes(0)
	broken := brokenAudit(db) // EnsureTable never called
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, broken, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-audit-fail", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	if _, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-audit-fail", nil); err == nil {
		t.Fatal("expected the audit insert failure to surface as an error")
	}
	reread, rerr := repo.GetJob(ctx, job.ID, 1)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if reread.Status == JobCompleted {
		t.Fatalf("job status must have rolled back with the failed audit insert, got %s", reread.Status)
	}
}

// TestCancel_EventFailurePreservesRetryableState proves a lifecycle
// evidence failure during Cancel leaves the job in a state that is
// STILL cancellable (never falsely reports "cancelled" and never gets
// stuck un-cancellable).
func TestCancel_EventFailurePreservesRetryableState(t *testing.T) {
	db, repo := newTestRepo(t) // NOTE: plain repo — audit table deliberately never created on this db
	ob := kernelRealOutbox(t, db)
	fm := newFakeMailboxes(0)
	broken := brokenAudit(db)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, broken, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-cancel-fail", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	if _, err := svc.Cancel(ctx, job.ID, 1); err == nil {
		t.Fatal("expected cancel to fail when its lifecycle evidence cannot be recorded")
	}
	reread, rerr := repo.GetJob(ctx, job.ID, 1)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if reread.Status == JobCancelled {
		t.Fatal("job must not be falsely reported cancelled when the cancellation event failed to persist")
	}
	if reread.Version != job.Version {
		t.Fatalf("version must not have advanced: started %d, now %d", job.Version, reread.Version)
	}

	// A real audit store now lets the SAME cancel request succeed —
	// the job was left in a genuinely retryable state, not corrupted.
	realAudit := audit.NewExtendedStore(db)
	if err := realAudit.EnsureTable(ctx); err != nil {
		t.Fatal(err)
	}
	repaired := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, realAudit, nil)
	final, cerr := repaired.Cancel(ctx, job.ID, 1)
	if cerr != nil {
		t.Fatalf("expected cancel to succeed once evidence recording is available: %v", cerr)
	}
	if final.Status != JobCancelled {
		t.Fatalf("expected job cancelled, got %s", final.Status)
	}
}

// TestLifecycleEvent_PayloadsContainNoSecrets proves the outbox and
// audit payloads for a completed job carry only counts/identifiers —
// never the row content, generated password, or setup-token hash.
func TestLifecycleEvent_PayloadsContainNoSecrets(t *testing.T) {
	db, repo, ob, as := newDurableTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, as, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "hash-secret-check", []RawRow{{RowNumber: 2, Email: "secretcheck@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "hash-secret-check", nil); err != nil {
		t.Fatalf("execute: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT payload FROM platform_outbox_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		seen++
		lower := strings.ToLower(payload)
		for _, forbidden := range []string{"password", "setup_token_hash", "seed-password"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("outbox payload leaked %q: %s", forbidden, payload)
			}
		}
	}
	if seen == 0 {
		t.Fatal("expected at least one outbox event to have been recorded")
	}

	tenantID := uint(1)
	entries, _, serr := as.Search(ctx, &audit.ExtendedQuery{TenantID: &tenantID, Limit: 20})
	if serr != nil {
		t.Fatal(serr)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one audit entry")
	}
	for _, e := range entries {
		combined := strings.ToLower(e.Before + e.After + e.Reason)
		for _, forbidden := range []string{"password", "setup_token_hash", "seed-password"} {
			if strings.Contains(combined, forbidden) {
				t.Fatalf("audit entry leaked %q: %+v", forbidden, e)
			}
		}
	}
}

func kernelRealOutbox(t *testing.T, db *sql.DB) *kernel.OutboxRepository {
	t.Helper()
	ob := kernel.NewOutboxRepository(dbdialect.FromDriver("sqlite"))
	if err := ob.EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("ensure outbox schema: %v", err)
	}
	return ob
}
