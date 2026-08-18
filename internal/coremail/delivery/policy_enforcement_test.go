package delivery

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/mailpolicy"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	_ "modernc.org/sqlite"
)

// ── Worker-level mail-access policy enforcement ────────────────────
//
// These tests prove the queue/retry/relay leg of the enforcement
// matrix using the REAL worker pipeline: a real SQLite queue +
// mailstore, a real fake-SMTP endpoint, and the canonical
// mailpolicy.Policy wired into the worker exactly like the runtime
// does. A queue entry that predates a policy change, or that arrives
// via a path with no interactive policy check, must still be denied
// (bounced, never delivered) when an internal-only mailbox targets an
// external recipient — and a policy-store outage must defer instead
// of delivering.

func policyWorkerEnv(t *testing.T) (*sql.DB, *queue.QueueEngine, *storage.MailStore, *DeliveryWorker, *mailpolicy.Policy) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "policy.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range queue.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range queue.Indexes() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}
	db.Exec(AttemptHistoryTable())
	for _, idx := range AttemptHistoryIndexes() {
		db.Exec(idx)
	}

	// Canonical schema for the policy store.
	for _, stmt := range []string{
		`CREATE TABLE coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			reseller_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			plan TEXT NOT NULL DEFAULT 'smb',
			description TEXT NOT NULL DEFAULT '',
			max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_aliases INTEGER NOT NULL DEFAULT 0,
			max_quota_mb INTEGER NOT NULL DEFAULT 0,
			default_mailbox_quota_mb INTEGER NOT NULL DEFAULT 0,
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			mtasts_enabled INTEGER NOT NULL DEFAULT 0,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			mail_access_mode TEXT NOT NULL DEFAULT 'internal_external',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			local_part TEXT NOT NULL,
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
			mfa_enabled INTEGER NOT NULL DEFAULT 0,
			mfa_secret TEXT NOT NULL DEFAULT '',
			app_passwords TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			quota_mb INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			msg_count INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0,
			is_forwarder INTEGER NOT NULL DEFAULT 0,
			forward_to TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			recv_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			last_login DATETIME,
			last_ip TEXT NOT NULL DEFAULT '',
			mail_access_mode TEXT NOT NULL DEFAULT 'inherit',
			version INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			from_addr TEXT NOT NULL,
			to_addr TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("policy schema: %v", err)
		}
	}

	// Seed an internal-only sender mailbox.
	if _, err := db.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, mail_access_mode, created_at, updated_at) VALUES ('internal.test', 1, 'active', 'smb', 'internal_external', datetime('now'), datetime('now'))`); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, mail_access_mode, version, created_at, updated_at)
		 VALUES (1, 1, 'alice', 'alice@internal.test', 'Alice', '', 'argon2id', 'active', 'internal_only', 1, datetime('now'), datetime('now'))`); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	// A local recipient mailbox for the local-delivery leg.
	if _, err := db.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, mail_access_mode, created_at, updated_at) VALUES ('hosted.test', 1, 'active', 'smb', 'internal_external', datetime('now'), datetime('now'))`); err != nil {
		t.Fatalf("seed recipient domain: %v", err)
	}
	var hostedID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='hosted.test'`).Scan(&hostedID); err != nil {
		t.Fatalf("recipient domain id: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, mail_access_mode, version, created_at, updated_at)
		 VALUES (?, 1, 'bob', 'bob@hosted.test', 'Bob', '', 'argon2id', 'active', 'internal_external', 1, datetime('now'), datetime('now'))`, hostedID); err != nil {
		t.Fatalf("seed recipient mailbox: %v", err)
	}

	qe := queue.NewQueueEngine(db)
	ms, _ := storage.NewMailStore(db, filepath.Join(t.TempDir(), "msgs"))

	fs := startFakeSMTP(t)
	fs.requireStartTLS = false
	fs.allowPlaintext = true
	resolver := NewFakeResolver()
	resolver.MXRecords["remote.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
	resolver.Hosts[fs.addr] = []string{fs.addr}

	transport := NewSMTPTransport(testTransportConfig())
	worker := NewDeliveryWorker(qe, ms, resolver, transport, "local.test", "policy-worker")
	worker.History = NewAttemptHistorySQLRepo(db)
	worker.Audit = NewAuditLogger()

	eng := coremail.NewEngine(coremail.EngineConfig{DB: db})
	pol := mailpolicy.New(&mailpolicy.EngineStore{Engine: eng}, mailpolicy.NopSink{})
	worker.MailAccessPolicy = pol

	return db, qe, ms, worker, pol
}

func TestWorkerPolicy_InternalOnlySenderToExternalBounces(t *testing.T) {
	_, qe, ms, worker, pol := policyWorkerEnv(t)
	ctx := context.Background()

	// This entry simulates a message that was enqueued before the
	// mailbox was set to internal_only (or through a path without an
	// interactive check): the worker must bounce it, not deliver.
	_ = pol
	entry := &queue.QueueEntry{TenantID: 1, DomainID: 1, MailboxID: uintPtr(1),
		MessageID: storage.GenerateMessageID(), FromAddress: "alice@internal.test",
		ToAddress: "r@remote.test", RecipientDomain: "remote.test",
		Direction: queue.DirectionOutbound, DeliveryMode: queue.DeliveryRemoteSMTP, MaxAttempts: 3}
	qe.Enqueue(ctx, entry)
	storeTestMessage(t, ms, entry.MessageID, "alice@internal.test", "r@remote.test")

	if worked, err := worker.ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("process: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got == nil {
		t.Fatal("entry missing")
	}
	if got.Status != queue.StatusBounced && got.Status != queue.StatusDeadLetter {
		t.Fatalf("internal-only -> external must bounce, got status %s (last_error=%q code=%d enhanced=%q)", got.Status, got.LastError, got.LastStatusCode, got.LastEnhancedCode)
	}
}

func TestWorkerPolicy_InternalOnlySenderToLocalDeliversLocally(t *testing.T) {
	_, qe, ms, worker, _ := policyWorkerEnv(t)
	ctx := context.Background()

	entry := &queue.QueueEntry{TenantID: 1, DomainID: 1, MailboxID: uintPtr(2),
		MessageID: storage.GenerateMessageID(), FromAddress: "alice@internal.test",
		ToAddress: "bob@hosted.test", RecipientDomain: "hosted.test",
		Direction: queue.DirectionOutbound, DeliveryMode: queue.DeliveryLocal, MaxAttempts: 3}
	qe.Enqueue(ctx, entry)
	// Local delivery needs an INBOX folder for the recipient mailbox.
	if err := ms.Folders.EnsureSystemFolders(ctx, 2, nil); err != nil {
		t.Fatalf("inbox: %v", err)
	}
	storeTestMessage(t, ms, entry.MessageID, "alice@internal.test", "bob@hosted.test")

	if worked, err := worker.ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("process: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got == nil {
		t.Fatal("entry missing")
	}
	if got.Status != queue.StatusDelivered {
		t.Fatalf("internal-only -> local must deliver, got status %s (last_error=%q)", got.Status, got.LastError)
	}
}

func TestWorkerPolicy_RetryAfterPolicyChangeBounces(t *testing.T) {
	db, qe, ms, worker, _ := policyWorkerEnv(t)
	ctx := context.Background()

	// Enqueue while the mailbox is external-enabled.
	if _, err := db.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_external' WHERE email='alice@internal.test'`); err != nil {
		t.Fatalf("update mode: %v", err)
	}
	entry := &queue.QueueEntry{TenantID: 1, DomainID: 1, MailboxID: uintPtr(1),
		MessageID: storage.GenerateMessageID(), FromAddress: "alice@internal.test",
		ToAddress: "r@remote.test", RecipientDomain: "remote.test",
		Direction: queue.DirectionOutbound, DeliveryMode: queue.DeliveryRemoteSMTP, MaxAttempts: 5}
	qe.Enqueue(ctx, entry)
	storeTestMessage(t, ms, entry.MessageID, "alice@internal.test", "r@remote.test")

	// Flip to internal_only BEFORE the worker drains the queue —
	// exactly the retry/redelivery race the worker check exists for.
	if _, err := db.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_only' WHERE email='alice@internal.test'`); err != nil {
		t.Fatalf("flip mode: %v", err)
	}

	if worked, err := worker.ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("process: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got == nil {
		t.Fatal("entry missing")
	}
	if got.Status != queue.StatusBounced && got.Status != queue.StatusDeadLetter {
		t.Fatalf("retry after policy change must bounce, got status %s", got.Status)
	}
}

func TestWorkerPolicy_StoreFailureDefers(t *testing.T) {
	_, qe, ms, worker, _ := policyWorkerEnv(t)
	ctx := context.Background()

	// A failing store makes the policy unavailable; the worker must
	// DEFER (temp fail), never deliver.
	failPol := mailpolicy.New(failingDeliveryStore{}, mailpolicy.NopSink{})
	worker.MailAccessPolicy = failPol

	entry := &queue.QueueEntry{TenantID: 1, DomainID: 1, MailboxID: uintPtr(1),
		MessageID: storage.GenerateMessageID(), FromAddress: "alice@internal.test",
		ToAddress: "r@remote.test", RecipientDomain: "remote.test",
		Direction: queue.DirectionOutbound, DeliveryMode: queue.DeliveryRemoteSMTP, MaxAttempts: 5}
	qe.Enqueue(ctx, entry)
	storeTestMessage(t, ms, entry.MessageID, "alice@internal.test", "r@remote.test")

	if worked, err := worker.ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("process: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(ctx, entry.ID, nil)
	if got == nil {
		t.Fatal("entry missing")
	}
	if got.Status != queue.StatusDeferred && got.Status != queue.StatusPending {
		t.Fatalf("policy store failure must defer, got status %s", got.Status)
	}
}

type failingDeliveryStore struct{}

func (failingDeliveryStore) SenderIdentity(context.Context, string) (mailpolicy.SenderIdentity, error) {
	return mailpolicy.SenderIdentity{}, errors.New("database unavailable")
}
func (failingDeliveryStore) RecipientEffectiveMode(context.Context, string) (mailpolicy.EffectiveMode, error) {
	return mailpolicy.EffectiveMode{}, errors.New("database unavailable")
}
func (failingDeliveryStore) RecipientIsLocal(context.Context, string) (bool, error) {
	return false, errors.New("database unavailable")
}
func (failingDeliveryStore) IsLocalDomain(context.Context, string) (bool, error) {
	return false, errors.New("database unavailable")
}
