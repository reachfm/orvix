package rules

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/mailpolicy"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

// ── Forwarding / vacation mail-access policy enforcement ───────────
//
// These tests prove that an internal-only mailbox can never escape to
// an external address through the rules-runner forwarding path, using
// the REAL runner, a REAL SQLite queue/mailstore, and the canonical
// mailpolicy.Policy wired exactly like the runtime wires it.

func policyForwardEnv(t *testing.T) (*sql.DB, *storage.MailStore, *queue.QueueEngine, *Runner) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fwd.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range storage.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("storage schema: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}
	for _, stmt := range queue.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("queue schema: %v", err)
		}
	}
	for _, stmt := range queue.Indexes() {
		db.Exec(stmt)
	}
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
			domain_id INTEGER NOT NULL DEFAULT 0,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			local_part TEXT NOT NULL DEFAULT '',
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL DEFAULT '',
			auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
			mfa_enabled INTEGER NOT NULL DEFAULT 0,
			mfa_secret TEXT NOT NULL DEFAULT '',
			app_passwords TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			quota_mb INTEGER NOT NULL DEFAULT 1024,
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

	// Domain + mailboxes: mailbox 1 = internal_only forwarder,
	// mailbox 2 = local recipient.
	if _, err := db.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, mail_access_mode, created_at, updated_at) VALUES ('fwd.test', 1, 'active', 'smb', 'internal_external', datetime('now'), datetime('now'))`); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	for _, m := range []struct {
		id        uint
		email     string
		localPart string
		mode      string
	}{
		{1, "alice@fwd.test", "alice", "internal_only"},
		{2, "bob@fwd.test", "bob", "internal_external"},
	} {
		if _, err := db.Exec(
			`INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, mail_access_mode, version, created_at, updated_at)
			 VALUES (?, 1, 1, ?, ?, ?, '', 'argon2id', 'active', ?, 1, datetime('now'), datetime('now'))`,
			m.id, m.localPart, m.email, m.localPart, m.mode); err != nil {
			t.Fatalf("seed mailbox %d: %v", m.id, err)
		}
	}

	store, err := storage.NewMailStore(db, filepath.Join(t.TempDir(), "messages"))
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}
	for _, mb := range []uint{1, 2} {
		if err := store.Folders.EnsureSystemFolders(context.Background(), mb, nil); err != nil {
			t.Fatalf("folders for %d: %v", mb, err)
		}
	}

	qe := queue.NewQueueEngine(db)
	eng := coremail.NewEngine(coremail.EngineConfig{DB: db})
	pol := mailpolicy.New(&mailpolicy.EngineStore{Engine: eng}, mailpolicy.NopSink{})
	r := NewRunner(Dependencies{
		MailStore:        store,
		QueueEngine:      qe,
		Vacation:         store.Vacation,
		Forwarding:       store.Forwarding,
		Logger:           zap.NewNop(),
		MailAccessPolicy: pol,
	})
	return db, store, qe, r
}

// forwardInput builds a RunInput for the internal-only mailbox 1.
func forwardInput(mailboxEmail, from string) RunInput {
	return RunInput{
		MailboxID:    1,
		TenantID:     1,
		DomainID:     1,
		MailboxEmail: mailboxEmail,
		FromHeader:   from,
		ReceivedAt:   time.Now().UTC(),
	}
}

func TestRunnerPolicy_InternalOnlyForwardToExternalSkipped(t *testing.T) {
	_, store, qe, r := policyForwardEnv(t)
	ctx := context.Background()

	// Forwarding row: alice (internal_only) forwards to an EXTERNAL
	// address. The policy must skip the enqueue — no queue entry, no
	// escape.
	fwd, err := store.Forwarding.GetOrCreate(ctx, 1)
	if err != nil {
		t.Fatalf("forwarding row: %v", err)
	}
	patch := &storage.ForwardingPatch{Enabled: boolPtr(true), ForwardTo: strPtr("victim@external.test"), KeepCopy: boolPtr(false)}
	if _, err := store.Forwarding.Update(ctx, fwd.ID, patch); err != nil {
		t.Fatalf("update forwarding: %v", err)
	}

	out, err := r.Run(ctx, forwardInput("alice@fwd.test", "sender@external.test"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.SkipReason == "" {
		t.Fatal("expected a SkipReason documenting the policy denial")
	}
	metrics, _ := qe.Metrics(ctx, nil)
	if metrics.Pending != 0 {
		t.Fatalf("policy-denied forward must not enqueue, pending=%d", metrics.Pending)
	}
	// The escape must never happen: no outbound message row and no
	// queue entry for the external target.
	if metrics.Delivered != 0 {
		t.Fatalf("policy-denied forward must never deliver, delivered=%d", metrics.Delivered)
	}
}

func TestRunnerPolicy_InternalOnlyForwardToLocalEnqueues(t *testing.T) {
	_, store, qe, r := policyForwardEnv(t)
	ctx := context.Background()

	fwd, err := store.Forwarding.GetOrCreate(ctx, 1)
	if err != nil {
		t.Fatalf("forwarding row: %v", err)
	}
	patch := &storage.ForwardingPatch{Enabled: boolPtr(true), ForwardTo: strPtr("bob@fwd.test"), KeepCopy: boolPtr(false)}
	if _, err := store.Forwarding.Update(ctx, fwd.ID, patch); err != nil {
		t.Fatalf("update forwarding: %v", err)
	}

	out, err := r.Run(ctx, forwardInput("alice@fwd.test", "sender@external.test"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.ForwardedTo != "bob@fwd.test" {
		t.Fatalf("internal-only forward to LOCAL recipient must be enqueued, got ForwardedTo=%q skip=%q", out.ForwardedTo, out.SkipReason)
	}
	metrics, _ := qe.Metrics(ctx, nil)
	if metrics.Pending == 0 {
		t.Fatal("expected at least one queued forward entry for the local recipient")
	}
}

func TestRunnerPolicy_ExternalEnabledForwardToExternalEnqueues(t *testing.T) {
	_, store, qe, r := policyForwardEnv(t)
	ctx := context.Background()

	fwd, err := store.Forwarding.GetOrCreate(ctx, 1)
	if err != nil {
		t.Fatalf("forwarding row: %v", err)
	}
	patch := &storage.ForwardingPatch{Enabled: boolPtr(true), ForwardTo: strPtr("victim@external.test"), KeepCopy: boolPtr(false)}
	if _, err := store.Forwarding.Update(ctx, fwd.ID, patch); err != nil {
		t.Fatalf("update forwarding: %v", err)
	}

	// Flip the mailbox to internal_external through the canonical
	// column so the policy store sees it.
	if _, err := store.DB.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_external' WHERE id=1`); err != nil {
		t.Fatalf("flip mode: %v", err)
	}

	out, err := r.Run(ctx, forwardInput("alice@fwd.test", "sender@external.test"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.ForwardedTo != "victim@external.test" {
		t.Fatalf("external-enabled forward to external must be enqueued, got ForwardedTo=%q skip=%q", out.ForwardedTo, out.SkipReason)
	}
	metrics, _ := qe.Metrics(ctx, nil)
	if metrics.Pending == 0 {
		t.Fatal("expected a queued forward entry")
	}
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }
