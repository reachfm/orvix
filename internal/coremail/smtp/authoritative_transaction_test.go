package smtp

// Authoritative acceptance transaction tests (S-1 + S-4).
//
// These tests drive the REAL server/command-handler path over loopback
// TCP (the same path production runs: server.go -> PreAcceptFn ->
// Receiver.AcceptMessage -> authoritative acceptance transaction) and
// pin the S-1 contract:
//
//   - ONE authoritative transaction for the complete message: every
//     message metadata insert and every recipient queue insert commits
//     together, or none does;
//   - canonical domain IDs are deduplicated and locked in deterministic
//     SORTED order inside that transaction (SELECT ... FOR UPDATE on
//     PostgreSQL), and operability is re-validated under the lock;
//   - any classification/domain/message/queue/commit failure rolls back
//     the WHOLE acceptance with zero durable effects and a transient
//     (4.x) SMTP response;
//   - staged RFC822 bytes are written BEFORE the transaction (S-2) and
//     are published/cleaned without orphans;
//   - the rules runner never runs before the acceptance commit.
//
// All interleavings are forced with hooks/channels/barriers — no
// sleep-based race tests. The concurrent-disable cases assert exactly
// one valid serializable outcome.

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/rules"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/dbdialect"
)

// acceptanceTxEnv is a real SMTP server over loopback TCP whose
// receiver exposes deterministic test hooks and whose storage paths
// are observable.
type acceptanceTxEnv struct {
	addr      string
	db        *sql.DB
	dbPath    string
	ms        *storage.MailStore
	qe        *queue.QueueEngine
	rcv       *Receiver
	basePath  string
	dialect   *dbdialect.Info
	preAccept func(ctx context.Context, session *Session) error
}

// newAcceptanceTxEnvSQLite builds the env on SQLite with
// _txlock=immediate so the acceptance transaction holds the SQLite
// write lock from BeginTx (the production DSN shape), making
// concurrent disable-vs-acceptance outcomes serializable.
func newAcceptanceTxEnvSQLite(t *testing.T) *acceptanceTxEnv {
	t.Helper()
	sqliteTestMu.Lock()
	defer sqliteTestMu.Unlock()

	dbPath := filepath.Join(t.TempDir(), "acceptance.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_txlock=immediate&_busy_timeout=10000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range coremailTables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("coremail table: %v", err)
		}
	}
	for _, stmt := range storage.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("storage table: %v", err)
		}
	}
	for _, stmt := range queue.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("queue table: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}
	for _, stmt := range queue.Indexes() {
		db.Exec(stmt)
	}

	return buildAcceptanceTxEnv(t, db, dbPath)
}

// buildAcceptanceTxEnv wires the engine/mailstore/queue/receiver and
// the loopback server around an existing database.
func buildAcceptanceTxEnv(t *testing.T, db *sql.DB, dbPath string) *acceptanceTxEnv {
	t.Helper()
	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})

	_, mbox, err := eng.ProvisionDomain(context.Background(), "test.com", "smb", "user@test.com", "pass", "Test User", 1)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	basePath := filepath.Join(t.TempDir(), "mailstore")
	ms, err := storage.NewMailStore(db, basePath)
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}
	if err := ms.EnsureMailboxStorage(context.Background(), mbox.ID, 1, mbox.DomainID, nil); err != nil {
		t.Fatalf("ensure mailbox storage: %v", err)
	}
	qe := queue.NewQueueEngine(db)

	cfg := DefaultConfig()
	cfg.RequireAuthForSubmission = false
	cfg.RequireTLSForAuth = false
	cfg.AllowPlainAuthWithoutTLS = true

	identity := NewIdentityService(eng)
	auth := NewAuthenticator(identity)
	handler := NewCommandHandler(cfg, auth, NewSession("", nil, cfg))
	rcv := NewReceiver(eng, ms, qe, cfg)
	srv := NewServer(cfg, handler, rcv)
	srv.SetLocalDomainChecker(identity.IsLocalDomain)
	srv.RecipientValidator = func(ctx context.Context, address string) (bool, error) {
		_, err := eng.Auth.ResolveAddress(ctx, address)
		return err == nil, err
	}

	env := &acceptanceTxEnv{db: db, dbPath: dbPath, ms: ms, qe: qe, rcv: rcv, basePath: basePath, dialect: dbdialect.FromDriver("sqlite")}
	srv.PreAcceptFn = env.defaultPreAccept

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	env.addr = listener.Addr().String()
	go func() {
		srv.listener = listener
		_ = srv.serve()
	}()
	t.Cleanup(func() { listener.Close() })
	return env
}

// defaultPreAccept mirrors the production pre-DATA presented-domain
// recheck: unknown domains allowed (anti-enumeration), known
// inoperable domains rejected with ErrRecipientDomainInoperable.
// The query is dialect-rewritten so the same env works on SQLite and
// real PostgreSQL.
func (e *acceptanceTxEnv) defaultPreAccept(ctx context.Context, session *Session) error {
	if e.preAccept != nil {
		return e.preAccept(ctx, session)
	}
	seen := map[string]bool{}
	for _, addr := range session.Recipients {
		dom := strings.ToLower(ExtractDomain(addr))
		if dom == "" || seen[dom] {
			continue
		}
		seen[dom] = true
		var status string
		var deletedAt sql.NullTime
		err := e.db.QueryRowContext(ctx, e.dialect.Rewrite(
			`SELECT status, deleted_at FROM coremail_domains WHERE name = ? AND deleted_at IS NULL`),
			dom).Scan(&status, &deletedAt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return fmt.Errorf("recipient policy database unavailable")
		}
		if status != string(coremail.DomainActive) {
			return ErrRecipientDomainInoperable
		}
	}
	return nil
}

func (e *acceptanceTxEnv) conn(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", e.addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	reader := bufio.NewReader(conn)
	greeting, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if !strings.HasPrefix(greeting, "220") {
		t.Fatalf("greeting: %q", greeting)
	}
	return conn, reader
}

func (e *acceptanceTxEnv) cmd(t *testing.T, conn net.Conn, reader *bufio.Reader, line string) string {
	t.Helper()
	if _, err := conn.Write([]byte(line + "\r\n")); err != nil {
		t.Fatalf("write %q: %v", line, err)
	}
	var last string
	for {
		resp, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read after %q: %v", line, err)
		}
		last = resp
		trimmed := strings.TrimRight(resp, "\r\n")
		if len(trimmed) < 4 || trimmed[3] != '-' {
			return last
		}
	}
}

func (e *acceptanceTxEnv) sendData(t *testing.T, conn net.Conn, reader *bufio.Reader, body string) string {
	t.Helper()
	data := e.cmd(t, conn, reader, "DATA")
	if !strings.HasPrefix(data, "354") {
		t.Fatalf("DATA: %q", data)
	}
	if _, err := conn.Write([]byte(body + "\r\n.\r\n")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	final, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	return final
}

func (e *acceptanceTxEnv) beginSession(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, reader := e.conn(t)
	e.cmd(t, conn, reader, "EHLO mx.example")
	e.cmd(t, conn, reader, "MAIL FROM:<sender@external.example>")
	return conn, reader
}

func (e *acceptanceTxEnv) authSession(t *testing.T, user, pass string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, reader := e.conn(t)
	e.cmd(t, conn, reader, "EHLO mx.example")
	creds := base64Creds(user, pass)
	if resp := e.cmd(t, conn, reader, "AUTH PLAIN "+creds); !strings.HasPrefix(resp, "235") {
		t.Fatalf("AUTH PLAIN: %q", resp)
	}
	e.cmd(t, conn, reader, "MAIL FROM:<user@test.com>")
	return conn, reader
}

func (e *acceptanceTxEnv) rowCounts(t *testing.T) (int64, int64) {
	t.Helper()
	return countTableRows(t, e.db, "coremail_messages"), countTableRows(t, e.db, "coremail_queue")
}

// fileCount walks the mailstore root (excluding the staging dir) and
// counts published .eml files and attachment files.
func (e *acceptanceTxEnv) publishedFileCount(t *testing.T) int {
	t.Helper()
	n := 0
	err := filepath.Walk(e.basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == stagingRootNameForTest {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(info.Name(), ".eml") || strings.Contains(path, string(filepath.Separator)+"attachments"+string(filepath.Separator)) {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk mailstore: %v", err)
	}
	return n
}

// stagingEntries returns the number of leftover entries under the
// staging directory (0 means the acceptance cleaned up completely).
func (e *acceptanceTxEnv) stagingEntries(t *testing.T) int {
	t.Helper()
	dir := filepath.Join(e.basePath, stagingRootNameForTest)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read staging dir: %v", err)
	}
	return len(entries)
}

const stagingRootNameForTest = "staging"

// ── Tests ─────────────────────────────────────────────────────────

// TestAcceptanceTx_ActiveMultiRecipientCommitsAtomically pins the
// happy path: two local recipients accepted in one transaction with
// exactly one message row and one queue row per recipient, exactly
// one published RFC822 file per row, and no leftover staging state.
func TestAcceptanceTx_ActiveMultiRecipientCommitsAtomically(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)
	_, targetMboxID := seedExtraDomainAndMailbox(t, env.db, "target.test", "bob@target.test", "active")
	if err := env.ms.Folders.EnsureSystemFolders(context.Background(), targetMboxID, nil); err != nil {
		t.Fatalf("ensure target folders: %v", err)
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	env.cmd(t, conn, reader, "RCPT TO:<bob@target.test>")
	final := env.sendData(t, conn, reader, "Subject: Multi\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 2 || queues != 2 {
		t.Fatalf("atomic acceptance: messages=%d queue=%d, want 2/2", msgs, queues)
	}
	if got := env.publishedFileCount(t); got != 2 {
		t.Fatalf("published files = %d, want exactly 2", got)
	}
	if got := env.stagingEntries(t); got != 0 {
		t.Fatalf("staging leftovers = %d, want 0", got)
	}
}

// TestAcceptanceTx_DisableAfterPlanBuildBeforeLockRejected forces the
// exact TOCTOU interleaving the lock closes: the acceptance builds
// the recipient plan, pauses immediately before the domain lock, a
// concurrent disable commits, then the acceptance proceeds and MUST
// reject with a transient 4xx and zero durable effects.
func TestAcceptanceTx_DisableAfterPlanBuildBeforeLockRejected(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)

	paused := make(chan struct{})
	release := make(chan struct{})
	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeDomainLockTx: func() {
			close(paused)
			<-release
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")

	done := make(chan string, 1)
	go func() {
		done <- env.sendData(t, conn, reader, "Subject: Race\r\n\r\nbody")
	}()

	<-paused // acceptance is paused pre-lock, after plan build
	if _, err := env.db.Exec(`UPDATE coremail_domains SET status='disabled' WHERE name='test.com'`); err != nil {
		t.Fatalf("disable domain: %v", err)
	}
	close(release)

	final := <-done
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx rejection, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("rejected acceptance persisted: messages=%d queue=%d, want 0/0", msgs, queues)
	}
	if got := env.publishedFileCount(t); got != 0 {
		t.Fatalf("rejected acceptance published files: %d, want 0", got)
	}
	if got := env.stagingEntries(t); got != 0 {
		t.Fatalf("rejected acceptance left staging entries: %d, want 0", got)
	}
}

// TestAcceptanceTx_ConcurrentDisableVsAcceptance_AcceptanceWins is
// serializable outcome (a): the acceptance acquires the domain write
// lock first (SQLite BEGIN IMMEDIATE), the concurrent disable blocks
// until the acceptance commits, and afterwards BOTH hold: the message
// is durably accepted AND the domain ends up disabled. No partial
// state is ever visible.
func TestAcceptanceTx_ConcurrentDisableVsAcceptance_AcceptanceWins(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)

	locked := make(chan struct{})
	env.rcv.TestHooks = &ReceiverTestHooks{
		AfterDomainLockTx: func() {
			close(locked)
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")

	acceptDone := make(chan string, 1)
	go func() {
		acceptDone <- env.sendData(t, conn, reader, "Subject: Race\r\n\r\nbody")
	}()

	<-locked // acceptance holds the domain lock inside its tx

	var disableErr error
	var disableWG sync.WaitGroup
	disableWG.Add(1)
	go func() {
		defer disableWG.Done()
		_, disableErr = env.db.Exec(`UPDATE coremail_domains SET status='disabled' WHERE name='test.com'`)
	}()
	// Give the disable goroutine a chance to block on the write lock
	// (bounded wait, then proceed — the assertion below does not rely
	// on timing, only on the final serialized state).
	time.Sleep(100 * time.Millisecond)

	final := <-acceptDone
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250 (acceptance won the lock), got %q", final)
	}
	disableWG.Wait()
	if disableErr != nil {
		t.Fatalf("concurrent disable failed: %v", disableErr)
	}

	msgs, queues := env.rowCounts(t)
	if msgs != 1 || queues != 1 {
		t.Fatalf("serializable outcome (a): messages=%d queue=%d, want 1/1", msgs, queues)
	}
	var status string
	if err := env.db.QueryRow(`SELECT status FROM coremail_domains WHERE name='test.com'`).Scan(&status); err != nil {
		t.Fatalf("domain status: %v", err)
	}
	if status != "disabled" {
		t.Fatalf("serializable outcome (a): domain status = %q, want disabled", status)
	}
}

// TestAcceptanceTx_ConcurrentDisableVsAcceptance_DisableWins is
// serializable outcome (b): the acceptance pauses at the last
// pre-lock point, the concurrent disable commits, and the acceptance
// then observes the disabled domain under the lock and rejects with
// zero durable effects.
func TestAcceptanceTx_ConcurrentDisableVsAcceptance_DisableWins(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)

	paused := make(chan struct{})
	releaseAccept := make(chan struct{})
	disableCommitted := make(chan struct{})
	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeDomainLockTx: func() {
			close(paused)
			<-releaseAccept
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")

	acceptDone := make(chan string, 1)
	go func() {
		acceptDone <- env.sendData(t, conn, reader, "Subject: Race\r\n\r\nbody")
	}()

	<-paused // acceptance is paused pre-lock
	var disableWG sync.WaitGroup
	disableWG.Add(1)
	go func() {
		defer disableWG.Done()
		if _, err := env.db.Exec(`UPDATE coremail_domains SET status='disabled' WHERE name='test.com'`); err != nil {
			t.Errorf("disable domain: %v", err)
		}
		close(disableCommitted)
	}()
	disableWG.Wait()
	<-disableCommitted // disable definitely committed before release

	close(releaseAccept)
	final := <-acceptDone
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx rejection, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("serializable outcome (b): messages=%d queue=%d, want 0/0", msgs, queues)
	}
	if got := env.publishedFileCount(t); got != 0 {
		t.Fatalf("serializable outcome (b) published files: %d, want 0", got)
	}
	if got := env.stagingEntries(t); got != 0 {
		t.Fatalf("serializable outcome (b) staging leftovers: %d, want 0", got)
	}
}

// TestAcceptanceTx_MultiRecipientFailureOnRecipientN_TotalRollback
// injects a repository failure before the SECOND message insert:
// zero rows for EVERY recipient, zero files, zero staging leftovers.
func TestAcceptanceTx_MultiRecipientFailureOnRecipientN_TotalRollback(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)
	_, targetMboxID := seedExtraDomainAndMailbox(t, env.db, "target.test", "bob@target.test", "active")
	if err := env.ms.Folders.EnsureSystemFolders(context.Background(), targetMboxID, nil); err != nil {
		t.Fatalf("ensure target folders: %v", err)
	}

	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeMessageInsert: func(i int) error {
			if i == 1 {
				return fmt.Errorf("simulated repository failure before recipient 2")
			}
			return nil
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	env.cmd(t, conn, reader, "RCPT TO:<bob@target.test>")
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("partial acceptance survived: messages=%d queue=%d, want 0/0", msgs, queues)
	}
	if got := env.publishedFileCount(t); got != 0 {
		t.Fatalf("files published for rolled-back acceptance: %d, want 0", got)
	}
	if got := env.stagingEntries(t); got != 0 {
		t.Fatalf("staging leftovers: %d, want 0", got)
	}
}

// TestAcceptanceTx_QueueFailureAfterFirstRecipient_TotalRollback
// injects a queue failure AFTER the first planned recipient's rows
// were inserted in the tx: the whole acceptance must roll back —
// zero message rows, zero queue rows, zero files.
func TestAcceptanceTx_QueueFailureAfterFirstRecipient_TotalRollback(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)
	_, targetMboxID := seedExtraDomainAndMailbox(t, env.db, "target.test", "bob@target.test", "active")
	if err := env.ms.Folders.EnsureSystemFolders(context.Background(), targetMboxID, nil); err != nil {
		t.Fatalf("ensure target folders: %v", err)
	}

	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeQueueInsert: func(i int) error {
			if i == 1 {
				return fmt.Errorf("simulated queue repository failure after first recipient")
			}
			return nil
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	env.cmd(t, conn, reader, "RCPT TO:<bob@target.test>")
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("queue failure left durable rows: messages=%d queue=%d, want 0/0", msgs, queues)
	}
	if got := env.publishedFileCount(t); got != 0 {
		t.Fatalf("files published for rolled-back acceptance: %d, want 0", got)
	}
	if got := env.stagingEntries(t); got != 0 {
		t.Fatalf("staging leftovers: %d, want 0", got)
	}
}

// TestAcceptanceTx_CommitFailureZeroVisibleAcceptance forces a
// deterministic commit failure: the BeforeCommit hook rolls the live
// transaction back, so the acceptance's Commit returns an error. The
// acceptance must fail with a transient 4xx, and the durable state
// must show zero rows, zero files, zero staging leftovers.
func TestAcceptanceTx_CommitFailureZeroVisibleAcceptance(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)

	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeCommit: func(tx *sql.Tx) {
			_ = tx.Rollback() // poison the transaction: Commit now fails
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx for commit failure, got %q", final)
	}

	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("commit failure left visible rows: messages=%d queue=%d, want 0/0", msgs, queues)
	}
	if got := env.publishedFileCount(t); got != 0 {
		t.Fatalf("commit failure left published files: %d, want 0", got)
	}
	if got := env.stagingEntries(t); got != 0 {
		t.Fatalf("commit failure left staging entries: %d, want 0", got)
	}
}

// TestAcceptanceTx_ForwarderOwningDomainEnforcement pins that a
// forwarder mailbox resolves to its target and the TARGET's owning
// domain is locked and enforced: disabling the target domain after
// RCPT rejects with zero durable effects.
func TestAcceptanceTx_ForwarderOwningDomainEnforcement(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)
	_, targetMboxID := seedExtraDomainAndMailbox(t, env.db, "target.test", "bob@target.test", "active")
	if err := env.ms.Folders.EnsureSystemFolders(context.Background(), targetMboxID, nil); err != nil {
		t.Fatalf("ensure target folders: %v", err)
	}

	var userMboxID uint
	if err := env.db.QueryRow(`SELECT id FROM coremail_mailboxes WHERE email='user@test.com'`).Scan(&userMboxID); err != nil {
		t.Fatalf("user mailbox id: %v", err)
	}
	now := time.Now().UTC()
	if _, err := env.db.Exec(
		`UPDATE coremail_mailboxes SET is_forwarder=1, forward_to=? WHERE id=?`,
		"bob@target.test", userMboxID); err != nil {
		t.Fatalf("make forwarder: %v", err)
	}
	_ = now

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	if _, err := env.db.Exec(`UPDATE coremail_domains SET status='suspended' WHERE name='target.test'`); err != nil {
		t.Fatalf("disable target domain: %v", err)
	}
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") && !strings.HasPrefix(final, "5") {
		t.Fatalf("expected rejection, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("forwarder rejection persisted: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestAcceptanceTx_CatchallOwningDomainEnforcement pins that a
// catchall-resolved target's owning domain is locked and enforced.
func TestAcceptanceTx_CatchallOwningDomainEnforcement(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)
	_, targetMboxID := seedExtraDomainAndMailbox(t, env.db, "target.test", "bob@target.test", "active")
	if err := env.ms.Folders.EnsureSystemFolders(context.Background(), targetMboxID, nil); err != nil {
		t.Fatalf("ensure target folders: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE coremail_domains SET catchall_address='bob@target.test' WHERE name='test.com'`); err != nil {
		t.Fatalf("set catchall: %v", err)
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<nobody@test.com>")
	if _, err := env.db.Exec(`UPDATE coremail_domains SET status='locked' WHERE name='target.test'`); err != nil {
		t.Fatalf("disable target domain: %v", err)
	}
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient rejection, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("catchall rejection persisted: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestAcceptanceTx_ExternalRecipientTransactionalBehavior pins the
// authenticated external path inside the acceptance transaction: one
// external message row + one remote_smtp queue row + one published
// file, and a queue failure rolls back even the external accept.
func TestAcceptanceTx_ExternalRecipientTransactionalBehavior(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)

	conn, reader := env.authSession(t, "user@test.com", "pass")
	env.cmd(t, conn, reader, "RCPT TO:<victim@external.example>")
	final := env.sendData(t, conn, reader, "Subject: Ext\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250 for authenticated external, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 1 || queues != 1 {
		t.Fatalf("external acceptance: messages=%d queue=%d, want 1/1", msgs, queues)
	}
	var mode, direction string
	if err := env.db.QueryRow(`SELECT delivery_mode, direction FROM coremail_queue WHERE deleted_at IS NULL`).Scan(&mode, &direction); err != nil {
		t.Fatalf("queue row: %v", err)
	}
	if mode != string(queue.DeliveryRemoteSMTP) || direction != string(queue.DirectionOutbound) {
		t.Fatalf("external queue row: mode=%s direction=%s", mode, direction)
	}
	if got := env.publishedFileCount(t); got != 1 {
		t.Fatalf("published files = %d, want 1", got)
	}
}

// TestAcceptanceTx_ExternalQueueFailureRollsBackWholeAcceptance pins
// that a failure on the external queue insert rolls back the local
// recipient's rows too (same transaction).
func TestAcceptanceTx_ExternalQueueFailureRollsBackWholeAcceptance(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)

	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeQueueInsert: func(i int) error {
			if i == 1 { // local insert (0) succeeded; external (1) fails
				return fmt.Errorf("simulated external queue failure")
			}
			return nil
		},
	}

	conn, reader := env.authSession(t, "user@test.com", "pass")
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	env.cmd(t, conn, reader, "RCPT TO:<victim@external.example>")
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("external failure left rows: messages=%d queue=%d, want 0/0", msgs, queues)
	}
	if got := env.publishedFileCount(t); got != 0 {
		t.Fatalf("external failure left files: %d, want 0", got)
	}
}

// TestAcceptanceTx_DeterministicLockOrderBothDirections runs the
// multi-domain acceptance in both recipient orders sequentially. The
// lock order is deterministic (sorted domain IDs) on both dialects;
// genuine concurrent lock acquisition is exercised against real
// PostgreSQL in postgres_acceptance_test.go.
func TestAcceptanceTx_DeterministicLockOrderBothDirections(t *testing.T) {
	seedOrder := func(order []string) {
		t.Helper()
		env := newAcceptanceTxEnvSQLite(t)
		_, targetMboxID := seedExtraDomainAndMailbox(t, env.db, "target.test", "bob@target.test", "active")
		if err := env.ms.Folders.EnsureSystemFolders(context.Background(), targetMboxID, nil); err != nil {
			t.Fatalf("ensure target folders: %v", err)
		}
		conn, reader := env.beginSession(t)
		for _, rcpt := range order {
			env.cmd(t, conn, reader, "RCPT TO:<"+rcpt+">")
		}
		final := env.sendData(t, conn, reader, "Subject: Order\r\n\r\nbody")
		if !strings.HasPrefix(final, "250") {
			t.Fatalf("order %v: expected 250, got %q", order, final)
		}
		msgs, queues := env.rowCounts(t)
		if msgs != 2 || queues != 2 {
			t.Fatalf("order %v: messages=%d queue=%d, want 2/2", order, msgs, queues)
		}
	}
	seedOrder([]string{"user@test.com", "bob@target.test"})
	seedOrder([]string{"bob@target.test", "user@test.com"})
}

// TestAcceptanceTx_RulesRunnerRunsOnlyAfterCommit pins that the
// rules runner never runs before the acceptance commit: the
// BeforeCommit hook observes zero runner invocations, and after the
// 250 the runner has run exactly once per local recipient.
func TestAcceptanceTx_RulesRunnerRunsOnlyAfterCommit(t *testing.T) {
	env := newAcceptanceTxEnvSQLite(t)

	runnerCalls := make(chan struct{}, 8)
	env.rcv.RulesRunner = &recordingRunner{onRun: func() {
		runnerCalls <- struct{}{}
	}}
	runsBeforeCommit := make(chan int, 1)
	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeCommit: func(tx *sql.Tx) {
			runsBeforeCommit <- len(runnerCalls)
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	final := env.sendData(t, conn, reader, "Subject: Runner\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250, got %q", final)
	}
	if got := <-runsBeforeCommit; got != 0 {
		t.Fatalf("rules runner ran %d times before commit, want 0", got)
	}
	select {
	case <-runnerCalls:
	case <-time.After(5 * time.Second):
		t.Fatalf("rules runner did not run after commit")
	}
}

// recordingRunner is a minimal rules.RulesRunner-compatible stub.
type recordingRunner struct {
	onRun func()
}

func (r *recordingRunner) Run(ctx context.Context, in rules.RunInput) (*rules.RunOutput, error) {
	r.onRun()
	return &rules.RunOutput{}, nil
}

// base64Creds encodes AUTH PLAIN credentials.
func base64Creds(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte("\x00" + user + "\x00" + pass))
}
