package bulkprovision

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
	_ "modernc.org/sqlite"
)

// ── in-memory fakes for fast, deterministic unit tests ──────────────

type fakeMailboxes struct {
	mu      sync.Mutex
	byEmail map[string]uint
	nextID  uint
	cap_    int // 0 = unlimited
	deleted map[uint]bool
}

func newFakeMailboxes(capLimit int) *fakeMailboxes {
	return &fakeMailboxes{byEmail: map[string]uint{}, deleted: map[uint]bool{}, cap_: capLimit}
}

func (f *fakeMailboxes) CreateMailbox(ctx context.Context, req mailbox.CreateMailboxRequest, tenantID uint) (*mailbox.CreateMailboxResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byEmail[req.Email]; ok {
		return nil, mailbox.ErrMailboxExists
	}
	live := 0
	for id, del := range f.deleted {
		if !del && id != 0 {
			live++
		}
	}
	if f.cap_ > 0 && len(f.byEmail) >= f.cap_ {
		return nil, domain.ErrMailboxLimitReached
	}
	f.nextID++
	id := f.nextID
	f.byEmail[req.Email] = id
	f.deleted[id] = false
	return &mailbox.CreateMailboxResponse{Mailbox: mailbox.AdminMailbox{ID: id, Email: req.Email, TenantID: tenantID}}, nil
}

func (f *fakeMailboxes) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byEmail[email]
	if !ok {
		return false, nil
	}
	return !f.deleted[id], nil
}

func (f *fakeMailboxes) GetIDByEmail(ctx context.Context, email string) (uint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byEmail[email]
	if !ok || f.deleted[id] {
		return 0, nil
	}
	return id, nil
}

func (f *fakeMailboxes) ResolveDomainAllocation(ctx context.Context, domainName string, tenantID uint) (*mailbox.DomainAllocation, error) {
	return &mailbox.DomainAllocation{DomainID: 1, Status: "active", MaxMailboxes: f.cap_}, nil
}

func (f *fakeMailboxes) SoftDeleteMailbox(ctx context.Context, id, tenantID uint, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted[id] = true
	return nil
}

func (f *fakeMailboxes) CountActiveByDomain(ctx context.Context, domainID, tenantID uint) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, del := range f.deleted {
		if !del {
			n++
		}
	}
	return n, nil
}

type fakeAccessMode struct{ mode domain.MailAccessMode }

func (f fakeAccessMode) GetMailAccessMode(ctx context.Context, id, tenantID uint) (domain.MailAccessMode, error) {
	return f.mode, nil
}

func newTestRepo(t *testing.T) (*sql.DB, *Repository) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db, repo
}

// newDurableTestRepo is newTestRepo plus REAL (schema-initialized)
// outbox and audit stores backed by the same database, for any test
// whose call path reaches a terminal lifecycle transition
// (Execute-to-completion, Cancel, RetryFailedRows-to-completion).
// finalizeLifecycleTx fails closed when either dependency is nil, so
// any such test needs real ones, not nil, to exercise its actual
// business logic rather than the fail-closed guard itself.
func newDurableTestRepo(t *testing.T) (*sql.DB, *Repository, *kernel.OutboxRepository, *audit.ExtendedStore) {
	t.Helper()
	db, repo := newTestRepo(t)
	ob := kernel.NewOutboxRepository(dbdialect.FromDriver("sqlite"))
	if err := ob.EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("ensure outbox schema: %v", err)
	}
	as := audit.NewExtendedStore(db)
	if err := as.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure audit schema: %v", err)
	}
	return db, repo, ob, as
}

// ── parsing ───────────────────────────────────────────────────────

func TestParseCSV_ParsesEmailNameQuota(t *testing.T) {
	rows, err := ParseCSV([]byte("email,name,quota_mb\nalice@example.test,Alice,500\nbob@example.test,,\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Email != "alice@example.test" || rows[0].Name != "Alice" || rows[0].QuotaMB != 500 {
		t.Fatalf("row 0 = %#v", rows[0])
	}
	if rows[0].RowNumber != 2 { // header is row 1
		t.Fatalf("expected row number 2 for first data row, got %d", rows[0].RowNumber)
	}
}

func TestParseCSV_MissingEmailColumnErrors(t *testing.T) {
	_, err := ParseCSV([]byte("name\nAlice\n"))
	if err == nil {
		t.Fatal("expected an error for a missing email column")
	}
}

func TestParseJSON_ParsesArray(t *testing.T) {
	rows, err := ParseJSON([]byte(`[{"email":"a@x.test","quota_mb":100},{"email":"b@x.test"}]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 || rows[0].Email != "a@x.test" || rows[0].QuotaMB != 100 {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

// ── validation ────────────────────────────────────────────────────

func TestValidate_DetectsDuplicateInFile(t *testing.T) {
	_, repo := newTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	raw := []RawRow{{RowNumber: 2, Email: "dup@x.test"}, {RowNumber: 3, Email: "DUP@x.test"}}
	res, err := svc.Validate(context.Background(), 1, 1, "x.test", "test-hash", raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.ValidRows != 1 || res.InvalidRows != 1 {
		t.Fatalf("expected 1 valid/1 invalid, got valid=%d invalid=%d", res.ValidRows, res.InvalidRows)
	}
	if res.Rows[1].ErrorCode != ErrCodeDuplicateInFile {
		t.Fatalf("expected duplicate_in_file, got %q", res.Rows[1].ErrorCode)
	}
}

func TestValidate_DetectsDuplicateInDatabase(t *testing.T) {
	_, repo := newTestRepo(t)
	fm := newFakeMailboxes(0)
	fm.byEmail["existing@x.test"] = 1
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	res, err := svc.Validate(context.Background(), 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "existing@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.Rows[0].ErrorCode != ErrCodeDuplicateInDatabase {
		t.Fatalf("expected duplicate_in_database, got %q", res.Rows[0].ErrorCode)
	}
}

func TestValidate_RejectsInvalidEmail(t *testing.T) {
	_, repo := newTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	res, err := svc.Validate(context.Background(), 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "not-an-email"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.Rows[0].ErrorCode != ErrCodeInvalidEmail {
		t.Fatalf("expected invalid_email, got %q", res.Rows[0].ErrorCode)
	}
}

func TestValidate_EmptyFileIsRejected(t *testing.T) {
	_, repo := newTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	if _, err := svc.Validate(context.Background(), 1, 1, "x.test", "test-hash", nil); err != ErrEmptyFile {
		t.Fatalf("expected ErrEmptyFile, got %v", err)
	}
}

// ── execution strategies ─────────────────────────────────────────

func TestExecute_PartialStrategyIsolatesRowFailures(t *testing.T) {
	_, repo, ob, as := newDurableTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, as, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "a@x.test"}, {RowNumber: 3, Email: "b@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Sabotage: pre-create "b@x.test" directly in the fake so its row's
	// CreateMailbox call fails at execute time (simulating a race lost
	// after validation).
	fm.byEmail["b@x.test"] = 999

	finalJob, rows, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "test-hash", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if finalJob.Status != JobPartiallyFailed {
		t.Fatalf("expected partially_failed, got %s", finalJob.Status)
	}
	if finalJob.CreatedCount != 1 || finalJob.FailedCount != 1 {
		t.Fatalf("expected 1 created/1 failed, got created=%d failed=%d", finalJob.CreatedCount, finalJob.FailedCount)
	}
	var aRow, bRow *Row
	for i := range rows {
		switch rows[i].Email {
		case "a@x.test":
			aRow = &rows[i]
		case "b@x.test":
			bRow = &rows[i]
		}
	}
	if aRow == nil || aRow.Status != RowCreated {
		t.Fatalf("expected a@x.test created, got %#v", aRow)
	}
	if bRow == nil || bRow.Status != RowFailed {
		t.Fatalf("expected b@x.test failed, got %#v", bRow)
	}
}

func TestExecute_AtomicStrategyRollsBackAllOnAnyFailure(t *testing.T) {
	_, repo, ob, as := newDurableTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, as, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "a@x.test"}, {RowNumber: 3, Email: "b@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job, err := svc.CreateJob(ctx, 1, 1, 99, StrategyAtomic, ConflictFail, "", res)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	fm.byEmail["b@x.test"] = 999 // sabotage the second row

	finalJob, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "test-hash", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if finalJob.Status != JobFailed {
		t.Fatalf("expected failed (atomic all-or-nothing), got %s", finalJob.Status)
	}
	if finalJob.CreatedCount != 0 {
		t.Fatalf("expected 0 created after atomic rollback, got %d", finalJob.CreatedCount)
	}
	// a@x.test must have been rolled back (soft-deleted), not left live.
	if !fm.deleted[fm.byEmail["a@x.test"]] {
		t.Fatal("expected a@x.test's mailbox to be rolled back (soft-deleted) after the atomic job failed")
	}
}

// ── idempotency ───────────────────────────────────────────────────

func TestCreateJob_SameIdempotencyKeyReturnsSameJob(t *testing.T) {
	_, repo := newTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	job1, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "retry-key-1", res)
	if err != nil {
		t.Fatalf("create job 1: %v", err)
	}
	job2, err := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "retry-key-1", res)
	if err != nil {
		t.Fatalf("create job 2: %v", err)
	}
	if job1.ID != job2.ID {
		t.Fatalf("expected the same job for the same idempotency key, got %d and %d", job1.ID, job2.ID)
	}
}

// TestCreateJob_ConcurrentSameIdempotencyKeyResolvesToOneJob proves the
// fix for the real CI failure this test class caught: CreateJob's
// initial GetJobByIdempotencyKey check is check-then-act, so under true
// concurrency two callers can both pass it before either has inserted.
// The schema's unique index on (tenant_id, idempotency_key) then
// rejects whichever INSERT loses the race — CreateJob must reconcile
// that rejection into "return the winner's job", never surface it as a
// raw internal error. This mirrors kernel_test.go's
// TestIdempotency_ConcurrentBeginsOnlyOneProceeds pattern: real
// goroutines, not a synthetic hook.
func TestCreateJob_ConcurrentSameIdempotencyKeyResolvesToOneJob(t *testing.T) {
	_, repo := newTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	ctx := context.Background()

	res, err := svc.Validate(ctx, 1, 1, "x.test", "test-hash-concurrent", []RawRow{{RowNumber: 2, Email: "race@x.test"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	const n = 12
	var wg sync.WaitGroup
	jobs := make([]*Job, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			jobs[i], errs[i] = svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "concurrent-race-key", res)
		}(i)
	}
	wg.Wait()

	var firstID uint
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: expected no error (a raced INSERT must reconcile to the winner, not surface as a failure), got: %v", i, err)
		}
		if firstID == 0 {
			firstID = jobs[i].ID
		} else if jobs[i].ID != firstID {
			t.Fatalf("request %d: expected every concurrent same-key call to resolve to job %d, got %d", i, firstID, jobs[i].ID)
		}
	}

	var jobCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM platform_bulk_import_jobs WHERE tenant_id = 1 AND idempotency_key = 'concurrent-race-key'`).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 {
		t.Fatalf("expected exactly 1 durable job row despite %d concurrent same-key CreateJob calls, found %d", n, jobCount)
	}
}

// ── cancel / retry ────────────────────────────────────────────────

func TestCancel_ReadyJobCanBeCancelled(t *testing.T) {
	_, repo, ob, as := newDurableTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, as, nil)
	ctx := context.Background()
	res, _ := svc.Validate(ctx, 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	job, _ := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)

	cancelled, err := svc.Cancel(ctx, job.ID, 1)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != JobCancelled {
		t.Fatalf("expected cancelled, got %s", cancelled.Status)
	}
}

func TestCancel_CompletedJobCannotBeCancelled(t *testing.T) {
	_, repo, ob, as := newDurableTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, as, nil)
	ctx := context.Background()
	res, _ := svc.Validate(ctx, 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	job, _ := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	if _, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "test-hash", nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := svc.Cancel(ctx, job.ID, 1); err != ErrJobNotCancellable {
		t.Fatalf("expected ErrJobNotCancellable, got %v", err)
	}
}

func TestRetryFailedRows_OnlyRetriesFailedNotCreated(t *testing.T) {
	_, repo, ob, as := newDurableTestRepo(t)
	fm := newFakeMailboxes(0)
	svc := NewService(repo, fm, fakeAccessMode{domain.MailAccessInternalExternal}, nil, ob, as, nil)
	ctx := context.Background()

	res, _ := svc.Validate(ctx, 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "a@x.test"}, {RowNumber: 3, Email: "b@x.test"}})
	job, _ := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	fm.byEmail["b@x.test"] = 999 // b will fail on first execute

	finalJob, _, err := svc.Execute(ctx, job.ID, 1, 1, "x.test", "test-hash", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if finalJob.Status != JobPartiallyFailed {
		t.Fatalf("expected partially_failed, got %s", finalJob.Status)
	}

	// Fix the sabotage, then retry — only b should be attempted again.
	delete(fm.byEmail, "b@x.test")
	retried, rows, err := svc.RetryFailedRows(ctx, job.ID, 1, "x.test")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.Status != JobCompleted {
		t.Fatalf("expected completed after successful retry, got %s", retried.Status)
	}
	if retried.CreatedCount != 2 {
		t.Fatalf("expected created_count=2 after retry, got %d", retried.CreatedCount)
	}
	for _, r := range rows {
		if r.Status != RowCreated {
			t.Fatalf("expected every row created after retry, got %#v", r)
		}
	}
}

func TestRetryFailedRows_NoFailedRowsIsRejected(t *testing.T) {
	_, repo := newTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	ctx := context.Background()
	res, _ := svc.Validate(ctx, 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	job, _ := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)
	svc.Execute(ctx, job.ID, 1, 1, "x.test", "test-hash", nil)

	if _, _, err := svc.RetryFailedRows(ctx, job.ID, 1, "x.test"); err != ErrJobNotRetryable {
		t.Fatalf("expected ErrJobNotRetryable, got %v", err)
	}
}

// ── tenant isolation ──────────────────────────────────────────────

func TestGetJob_CrossTenantIsNotFound(t *testing.T) {
	_, repo := newTestRepo(t)
	svc := NewService(repo, newFakeMailboxes(0), fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)
	ctx := context.Background()
	res, _ := svc.Validate(ctx, 1, 1, "x.test", "test-hash", []RawRow{{RowNumber: 2, Email: "a@x.test"}})
	job, _ := svc.CreateJob(ctx, 1, 1, 99, StrategyPartial, ConflictFail, "", res)

	if _, err := svc.Cancel(ctx, job.ID, 2); err != ErrJobNotFound {
		t.Fatalf("expected ErrJobNotFound for a different tenant, got %v", err)
	}
}

// ── real end-to-end concurrency: quota cannot be exceeded by
// simultaneous imports (uses the REAL admin/mailbox.Service, whose
// CreateMailbox already performs the row-locked atomic cap check —
// this test proves that guarantee survives being driven through
// bulkprovision, not just mailbox.Service directly). ──────────────

func TestExecute_ConcurrentImportsNeverExceedDomainQuota(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

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
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, max_mailboxes, created_at, updated_at) VALUES (1, 'race.test', 'active', 5, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}

	// A real audit store is required here, not nil: mailbox.Service's
	// mutateWithAudit only opens a transaction (the thing that makes
	// the domain-cap check-then-insert atomic) when an audit store is
	// configured — with auditStore==nil it runs the mutation directly
	// against the pool, which is exactly the TOCTOU window this test
	// exists to prove is closed.
	auditStore := audit.NewExtendedStore(db)
	if err := auditStore.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure audit table: %v", err)
	}
	mboxSvc := mailbox.NewService(mailbox.NewAdminMailboxRepo(db), fakeHasher{}, auditStore, nil)
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	svc := NewService(repo, mboxSvc, fakeAccessMode{domain.MailAccessInternalExternal}, nil, nil, nil, nil)

	const goroutines = 10
	var wg sync.WaitGroup
	results := make([]*Job, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			raw := []RawRow{{RowNumber: 2, Email: fmt.Sprintf("racer%d@race.test", i)}}
			res, err := svc.Validate(ctx, 1, 1, "race.test", "test-hash", raw)
			if err != nil {
				return
			}
			job, err := svc.CreateJob(ctx, 1, 1, 1, StrategyPartial, ConflictFail, "", res)
			if err != nil {
				return
			}
			finalJob, _, _ := svc.Execute(ctx, job.ID, 1, 1, "race.test", "test-hash", nil)
			results[i] = finalJob
		}(i)
	}
	wg.Wait()

	var liveCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL`).Scan(&liveCount); err != nil {
		t.Fatal(err)
	}
	if liveCount > 5 {
		t.Fatalf("domain cap is 5 but %d live mailboxes were created under concurrent bulk imports", liveCount)
	}
	if liveCount == 0 {
		t.Fatal("expected at least some mailboxes to have been created")
	}
}

type fakeHasher struct{}

func (fakeHasher) HashPassword(password string) (string, error) { return "hashed:" + password, nil }
