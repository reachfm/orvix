package smtp

// Tests for the SMTP receiver's rules-runner bridge.
//
// The receiver has stored the inbound message durably in
// the recipient's mailbox BEFORE the rules runner runs.
// If the runner then panics or returns an error, the
// original MUST stay in the mailbox and the SMTP accept
// MUST succeed (because the durable storage has already
// committed). This file pins that contract.
//
// We exercise applyRulesRunner directly rather than going
// through AcceptMessage because the SMTP accept also
// depends on the coremail engine resolving
// domain/mailbox rows from the DB. The contract under
// test here is purely "the runner can crash without
// destroying the durable inbound row", and that is
// visible at the applyRulesRunner boundary.

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/rules"
	"github.com/orvix/orvix/internal/coremail/storage"
)

// fakeRunner is the test double for rules.RulesRunner. It
// returns whatever the test wants — panic, error, or a
// well-formed RunOutput. The receiver's applyRulesRunner
// is supposed to handle all three.
type fakeRunner struct {
	runFn func(ctx context.Context, in rules.RunInput) (*rules.RunOutput, error)
}

func (f *fakeRunner) Run(ctx context.Context, in rules.RunInput) (*rules.RunOutput, error) {
	return f.runFn(ctx, in)
}

// buildTestReceiver wires a Receiver backed by a real
// MailStore + QueueEngine + a fake RulesRunner. The
// fake's Run behaviour is supplied by the test.
func buildTestReceiver(t *testing.T, runnerFn func(ctx context.Context, in rules.RunInput) (*rules.RunOutput, error)) (*Receiver, *storage.MailStore, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "smtp_rules.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Logf("busy_timeout: %v", err)
	}
	for _, stmt := range storage.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("storage schema: %v\nSQL: %s", err, stmt)
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
	for _, stmt := range smtpTestMailboxesDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("coremail_mailboxes schema: %v", err)
		}
	}

	base := t.TempDir()
	store, err := storage.NewMailStore(db, filepath.Join(base, "messages"))
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}
	if err := store.Folders.EnsureSystemFolders(context.Background(), 1, nil); err != nil {
		t.Fatalf("system folders: %v", err)
	}
	qe := queue.NewQueueEngine(db)

	recv := &Receiver{
		MailStore:   store,
		QueueEngine: qe,
		RulesRunner: &fakeRunner{runFn: runnerFn},
	}
	return recv, store, db
}

// smtpTestMailboxesDDL is the minimal coremail_mailboxes
// schema for the SMTP tests.
var smtpTestMailboxesDDL = []string{
	`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain_id INTEGER NOT NULL DEFAULT 0,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		local_part TEXT NOT NULL DEFAULT '',
		email TEXT UNIQUE NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL DEFAULT '',
		auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
		status TEXT NOT NULL DEFAULT 'active',
		quota_mb INTEGER NOT NULL DEFAULT 1024,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		deleted_at DATETIME
	)`,
}

// storeInboundMessage writes a real Message row in the
// mailbox's INBOX folder and returns the row + RFC822 bytes
// the test should pass to applyRulesRunner. Mirrors the
// path AcceptMessage takes right before invoking the
// runner.
func storeInboundMessage(t *testing.T, store *storage.MailStore, mailboxID, tenantID, domainID uint, mailboxEmail, from string) (*storage.Message, []byte) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO coremail_mailboxes
		 (id, domain_id, tenant_id, local_part, email, password_hash, auth_scheme, status, quota_mb, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, '', 'argon2id', 'active', 1024, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		mailboxID, domainID, tenantID, mailboxLocalPart(mailboxEmail), mailboxEmail,
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	if err := store.Folders.EnsureSystemFolders(ctx, mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	inbox, err := store.Folders.GetByPath(ctx, mailboxID, "INBOX", nil)
	if err != nil || inbox == nil {
		t.Fatalf("inbox: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC1123Z)
	rfc822 := []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: hi\r\nDate: %s\r\nMessage-ID: <inb-%d@external.test>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello\r\n",
		from, mailboxEmail, now, time.Now().UnixNano(),
	))
	msg := &storage.Message{
		MessageID:         storage.GenerateMessageID(),
		InternetMessageID: fmt.Sprintf("<inb-%d@external.test>", time.Now().UnixNano()),
		TenantID:          tenantID,
		DomainID:          domainID,
		MailboxID:         mailboxID,
		FolderID:          inbox.ID,
		FromAddress:       from,
		ToAddresses:       mailboxEmail,
		Subject:           "hi",
		ReceivedDate:      time.Now().UTC(),
	}
	if err := store.StoreMessage(ctx, msg, rfc822, nil); err != nil {
		t.Fatalf("store inbound: %v", err)
	}
	return msg, rfc822
}

// mailboxLocalPart returns the part before '@'.
func mailboxLocalPart(email string) string {
	if i := strings.Index(email, "@"); i >= 0 {
		return email[:i]
	}
	return email
}

// countMessagesInFolder asserts the message is still in
// the named folder. Used to prove the runner did not
// delete the durable row.
func countMessagesInFolder(t *testing.T, db *sql.DB, messageID, folderPath string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM coremail_messages m
		JOIN coremail_folders f ON f.id = m.folder_id
		WHERE m.message_id = ? AND f.path = ? AND m.deleted = 0`, messageID, folderPath).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

// ── Test: rules runner panic does NOT lose the original ──────
//
// The runner's Run panics. The receiver's applyRulesRunner
// MUST recover, log, and leave the original message in
// INBOX. This is the headline guarantee of the
// "rules runner failure must not fail SMTP acceptance
// after the original message is stored" constraint.

func TestApplyRulesRunner_MessageDurable_OnRunnerPanic(t *testing.T) {
	recv, store, db := buildTestReceiver(t, func(ctx context.Context, in rules.RunInput) (*rules.RunOutput, error) {
		panic("simulated rules-runner crash")
	})

	msg, rfc822 := storeInboundMessage(t, store, 1, 1, 1,
		"alice@example.com", "Carol <carol@external.test>")

	rcpt := resolvedRecipient{
		Email:     "alice@example.com",
		MailboxID: 1,
		DomainID:  1,
		TenantID:  1,
		Domain:    "example.com",
	}

	// applyRulesRunner is documented to never panic
	// outward. The test must not crash.
	recv.applyRulesRunner(context.Background(), rcpt, msg, rfc822)

	// The original message MUST still be in INBOX.
	if got := countMessagesInFolder(t, db, msg.MessageID, "INBOX"); got != 1 {
		t.Fatalf("expected original message still in INBOX after runner panic, got count=%d", got)
	}
}

// ── Test: rules runner error does NOT lose the original ─────
//
// Runner.Run returns an error (e.g. transient DB failure).
// The original message stays put.

func TestApplyRulesRunner_MessageDurable_OnRunnerError(t *testing.T) {
	recv, store, db := buildTestReceiver(t, func(ctx context.Context, in rules.RunInput) (*rules.RunOutput, error) {
		return nil, fmt.Errorf("simulated runner failure")
	})
	msg, rfc822 := storeInboundMessage(t, store, 1, 1, 1,
		"alice@example.com", "Carol <carol@external.test>")
	rcpt := resolvedRecipient{Email: "alice@example.com", MailboxID: 1, DomainID: 1, TenantID: 1, Domain: "example.com"}
	recv.applyRulesRunner(context.Background(), rcpt, msg, rfc822)
	if got := countMessagesInFolder(t, db, msg.MessageID, "INBOX"); got != 1 {
		t.Fatalf("expected original in INBOX after runner error, got %d", got)
	}
}

// ── Test: MoveToFolder applied on a healthy run ──────────────
//
// When the runner returns MoveToFolder="Sent", the
// receiver MUST move the message from INBOX to Sent —
// and the durable row MUST stay (just relocated). This
// pins the contract that the receiver applies the
// runner's outputs even on the happy path.

func TestApplyRulesRunner_MoveAppliedOnHealthyRun(t *testing.T) {
	recv, store, db := buildTestReceiver(t, func(ctx context.Context, in rules.RunInput) (*rules.RunOutput, error) {
		return &rules.RunOutput{MoveToFolder: "Sent"}, nil
	})
	msg, rfc822 := storeInboundMessage(t, store, 1, 1, 1,
		"alice@example.com", "Carol <carol@external.test>")
	rcpt := resolvedRecipient{Email: "alice@example.com", MailboxID: 1, DomainID: 1, TenantID: 1, Domain: "example.com"}
	recv.applyRulesRunner(context.Background(), rcpt, msg, rfc822)
	if got := countMessagesInFolder(t, db, msg.MessageID, "INBOX"); got != 0 {
		t.Fatalf("expected message moved out of INBOX, got %d in INBOX", got)
	}
	if got := countMessagesInFolder(t, db, msg.MessageID, "Sent"); got != 1 {
		t.Fatalf("expected message moved to Sent, got %d", got)
	}
}

// ── Test: SetFlag is applied on a healthy run ────────────────
//
// A rule that flips Seen=true on a message MUST result in
// the row's seen column being 1.

func TestApplyRulesRunner_SetFlagAppliedOnHealthyRun(t *testing.T) {
	seen := true
	recv, store, _ := buildTestReceiver(t, func(ctx context.Context, in rules.RunInput) (*rules.RunOutput, error) {
		return &rules.RunOutput{SetFlag: &storage.SetFlagValue{Seen: &seen}}, nil
	})
	msg, rfc822 := storeInboundMessage(t, store, 1, 1, 1,
		"alice@example.com", "Carol <carol@external.test>")
	rcpt := resolvedRecipient{Email: "alice@example.com", MailboxID: 1, DomainID: 1, TenantID: 1, Domain: "example.com"}
	recv.applyRulesRunner(context.Background(), rcpt, msg, rfc822)
	got, err := store.Messages.GetByID(context.Background(), msg.ID, nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.Seen {
		t.Fatalf("expected Seen=true after SetFlag, got false")
	}
}

// ── Test: rules runner panic does not propagate to AcceptMessage
// path. We exercise applyRulesRunner with a nil
// RulesRunner — the "rules disabled" path. The original
// message MUST stay in INBOX. This pins the contract that
// operators can disable the rules engine without affecting
// delivery.

func TestApplyRulesRunner_NilRunnerIsSafe(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "smtp_nil.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range storage.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}
	for _, stmt := range smtpTestMailboxesDDL {
		db.Exec(stmt)
	}

	store, err := storage.NewMailStore(db, t.TempDir())
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}
	recv := &Receiver{MailStore: store, RulesRunner: nil}

	msg, rfc822 := storeInboundMessage(t, store, 1, 1, 1,
		"alice@example.com", "Carol <carol@external.test>")
	rcpt := resolvedRecipient{Email: "alice@example.com", MailboxID: 1, DomainID: 1, TenantID: 1, Domain: "example.com"}

	// Nil runner — applyRulesRunner must short-circuit,
	// not panic.
	recv.applyRulesRunner(context.Background(), rcpt, msg, rfc822)

	if got := countMessagesInFolder(t, db, msg.MessageID, "INBOX"); got != 1 {
		t.Fatalf("expected message in INBOX with nil runner, got %d", got)
	}
}
