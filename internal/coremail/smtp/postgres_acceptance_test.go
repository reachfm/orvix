package smtp

// Real-PostgreSQL acceptance transaction tests (S-1/S-4, PostgreSQL
// leg).
//
// These tests run the SAME server/command-handler path as the SQLite
// acceptance tests but against a real PostgreSQL 16 database (the
// mission's "real PostgreSQL concurrency test, not SQLite-only"
// requirement). They are skipped unless PGHOST is set and
// ORVIX_RUN_POSTGRES_DML_TEST=1 (the CI postgres-dml convention).
//
// The PostgreSQL schema is created inside an isolated schema; every
// connection in the pool inherits it through the DSN search_path
// parameter, so concurrent transactions (which require more than one
// connection — NewMailStore sets MaxOpenConns(1) for SQLite) can run
// at the same time. The genuine FOR UPDATE serialization of the
// canonical domain lock is what the concurrent tests exercise here.

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// postgresAcceptanceEnv opens a dedicated PostgreSQL connection pool
// for acceptance tests. The pool is larger than one connection so
// concurrent acceptance transactions and the disable writer can run
// simultaneously (the SQLite leg cannot — single writer).
func postgresAcceptanceEnv(t *testing.T) (*acceptanceTxEnv, string) {
	t.Helper()
	if os.Getenv("PGHOST") == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres acceptance: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	baseDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("PGUSER"), os.Getenv("PGPASSWORD"), os.Getenv("PGHOST"), port, os.Getenv("PGDATABASE"))

	schema := fmt.Sprintf("orvix_acceptance_%d", time.Now().UnixNano())

	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	admin.Close()

	dsn := baseDSN + "&search_path=" + schema
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		admin2, aerr := sql.Open("pgx", baseDSN)
		if aerr == nil {
			_, _ = admin2.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
			admin2.Close()
		}
	})

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatalf("wrap postgres with gorm: %v", err)
	}
	if err := models.MigrateAllPostgres(gormDB); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}

	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	_, mbox, err := eng.ProvisionDomain(context.Background(), "test.com", "smb", "user@test.com", "pass", "Test User", 1)
	if err != nil {
		t.Fatalf("provision test.com: %v", err)
	}
	_, targetMbox, err := eng.ProvisionDomain(context.Background(), "target.test", "smb", "bob@target.test", "pass", "Bob", 1)
	if err != nil {
		t.Fatalf("provision target.test: %v", err)
	}

	basePath := filepath.Join(t.TempDir(), "mailstore")
	ms, err := storage.NewMailStore(db, basePath)
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}
	// The acceptance tx runs on the same pool as the disable writer;
	// raise the ceiling NewMailStore set for SQLite (1).
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)

	if err := ms.EnsureMailboxStorage(context.Background(), mbox.ID, 1, mbox.DomainID, nil); err != nil {
		t.Fatalf("ensure user storage: %v", err)
	}
	if err := ms.EnsureMailboxStorage(context.Background(), targetMbox.ID, 1, targetMbox.DomainID, nil); err != nil {
		t.Fatalf("ensure target storage: %v", err)
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

	env := &acceptanceTxEnv{db: db, ms: ms, qe: qe, rcv: rcv, basePath: basePath, dialect: dbdialect.FromDriver("postgres")}
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
	return env, schema
}

// TestPostgresAcceptanceTx_ActiveMultiRecipientCommitsAtomically is
// the PostgreSQL happy path: two local recipients, exactly 2 message
// rows + 2 queue rows + 2 published files.
func TestPostgresAcceptanceTx_ActiveMultiRecipientCommitsAtomically(t *testing.T) {
	env, _ := postgresAcceptanceEnv(t)

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	env.cmd(t, conn, reader, "RCPT TO:<bob@target.test>")
	final := env.sendData(t, conn, reader, "Subject: PG Multi\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 2 || queues != 2 {
		t.Fatalf("atomic acceptance: messages=%d queue=%d, want 2/2", msgs, queues)
	}
	if got := env.publishedFileCount(t); got != 2 {
		t.Fatalf("published files = %d, want 2", got)
	}
	if got := env.stagingEntries(t); got != 0 {
		t.Fatalf("staging leftovers = %d, want 0", got)
	}
}

// TestPostgresAcceptanceTx_DisableAfterPlanBuildBeforeLockRejected is
// the TOCTOU interleaving on PostgreSQL: the acceptance pauses at the
// last pre-lock point, the disable commits, and the acceptance's
// SELECT ... FOR UPDATE then observes the disabled domain and rejects.
func TestPostgresAcceptanceTx_DisableAfterPlanBuildBeforeLockRejected(t *testing.T) {
	env, _ := postgresAcceptanceEnv(t)

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

	<-paused
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
}

// TestPostgresAcceptanceTx_ConcurrentDisableVsAcceptance is the
// genuine race: the acceptance pauses at the last pre-lock point and
// the disable is released at the same moment. Exactly one serializable
// outcome must hold — acceptance fully commits (disable waits on the
// FOR UPDATE lock) or disable commits first (acceptance rejects with
// zero durable effects). Partial state is impossible.
func TestPostgresAcceptanceTx_ConcurrentDisableVsAcceptance(t *testing.T) {
	env, _ := postgresAcceptanceEnv(t)

	gate := make(chan struct{})
	releaseBoth := make(chan struct{})
	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeDomainLockTx: func() {
			close(gate)
			<-releaseBoth
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")

	acceptDone := make(chan string, 1)
	go func() {
		acceptDone <- env.sendData(t, conn, reader, "Subject: Race\r\n\r\nbody")
	}()

	<-gate // acceptance is paused pre-lock, having passed PreAcceptFn

	// NOW launch the disable and release the acceptance simultaneously:
	// both race for the FOR UPDATE row lock — the genuine serializable
	// race (acceptance-lock-wins => disable waits; disable-commits-first
	// => acceptance rejects).
	var disableErr error
	disableDone := make(chan struct{})
	go func() {
		_, disableErr = env.db.Exec(`UPDATE coremail_domains SET status='disabled' WHERE name='test.com'`)
		close(disableDone)
	}()
	close(releaseBoth)

	final := <-acceptDone
	<-disableDone
	if disableErr != nil {
		t.Fatalf("concurrent disable failed: %v", disableErr)
	}

	msgs, queues := env.rowCounts(t)
	switch {
	case strings.HasPrefix(final, "250"):
		// Outcome (a): acceptance won the lock; disable waited.
		if msgs != 1 || queues != 1 {
			t.Fatalf("outcome (a): messages=%d queue=%d, want 1/1", msgs, queues)
		}
	case strings.HasPrefix(final, "4"):
		// Outcome (b): disable won; acceptance rejected.
		if msgs != 0 || queues != 0 {
			t.Fatalf("outcome (b): messages=%d queue=%d, want 0/0", msgs, queues)
		}
	default:
		t.Fatalf("unexpected acceptance response %q", final)
	}
	if got := env.publishedFileCount(t); got != int(msgs) {
		t.Fatalf("published files = %d, want %d (exactly one per accepted message)", got, msgs)
	}
	var status string
	if err := env.db.QueryRow(`SELECT status FROM coremail_domains WHERE name='test.com'`).Scan(&status); err != nil {
		t.Fatalf("domain status: %v", err)
	}
	if status != "disabled" {
		t.Fatalf("domain status = %q, want disabled (disable always completes)", status)
	}
}

// TestPostgresAcceptanceTx_MultiRecipientFailureTotalRollback is the
// recipient-N failure on PostgreSQL: injected failure before the
// second message insert rolls back both recipients' rows and files.
func TestPostgresAcceptanceTx_MultiRecipientFailureTotalRollback(t *testing.T) {
	env, _ := postgresAcceptanceEnv(t)

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

// TestPostgresAcceptanceTx_CommitFailureZeroVisibleAcceptance forces
// a GENUINE PostgreSQL commit failure: the BeforeCommit hook
// terminates the backend session carrying the transaction
// (pg_terminate_backend on its own pid), so the acceptance's Commit
// errors at the driver. A fresh connection then proves zero durable
// rows and no published/staged files.
func TestPostgresAcceptanceTx_CommitFailureZeroVisibleAcceptance(t *testing.T) {
	env, schema := postgresAcceptanceEnv(t)

	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeCommit: func(tx *sql.Tx) {
			// Kill the very backend holding this transaction: the
			// following Commit must fail with a connection error.
			_, _ = tx.ExecContext(context.Background(), "SELECT pg_terminate_backend(pg_backend_pid())")
		},
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx for commit failure, got %q", final)
	}

	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable&search_path=%s",
		os.Getenv("PGUSER"), os.Getenv("PGPASSWORD"), os.Getenv("PGHOST"), port, os.Getenv("PGDATABASE"), schema)
	reopened, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("reopen postgres: %v", err)
	}
	defer reopened.Close()
	var msgs, queues int64
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM coremail_messages`).Scan(&msgs); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM coremail_queue`).Scan(&queues); err != nil {
		t.Fatalf("count queue: %v", err)
	}
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

// TestPostgresAcceptanceTx_DeterministicLockOrderNoDeadlock runs two
// concurrent acceptances touching the SAME two domains in OPPOSITE
// recipient orders. The sorted-ID lock acquisition must prevent a
// deadlock: both acceptances commit (4 rows total) — on SQLite this
// would serialize on the single write lock; on PostgreSQL the FOR
// UPDATE ordering is what prevents the classic lock-order inversion.
func TestPostgresAcceptanceTx_DeterministicLockOrderNoDeadlock(t *testing.T) {
	env, _ := postgresAcceptanceEnv(t)

	gate := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	arrived := 0
	env.rcv.TestHooks = &ReceiverTestHooks{
		BeforeDomainLockTx: func() {
			mu.Lock()
			arrived++
			if arrived == 2 {
				close(gate)
			}
			mu.Unlock()
			<-release
		},
	}

	runSession := func(rcpts []string) string {
		conn, reader := env.beginSession(t)
		for _, r := range rcpts {
			env.cmd(t, conn, reader, "RCPT TO:<"+r+">")
		}
		return env.sendData(t, conn, reader, "Subject: Order\r\n\r\nbody")
	}

	aDone := make(chan string, 1)
	bDone := make(chan string, 1)
	go func() { aDone <- runSession([]string{"user@test.com", "bob@target.test"}) }()
	go func() { bDone <- runSession([]string{"bob@target.test", "user@test.com"}) }()

	select {
	case <-gate:
	case <-time.After(30 * time.Second):
		t.Fatalf("both acceptances did not reach the pre-lock pause")
	}
	close(release)

	fa := <-aDone
	fb := <-bDone
	if !strings.HasPrefix(fa, "250") || !strings.HasPrefix(fb, "250") {
		t.Fatalf("deadlock or rejection: a=%q b=%q", fa, fb)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 4 || queues != 4 {
		t.Fatalf("concurrent multi-domain acceptances: messages=%d queue=%d, want 4/4", msgs, queues)
	}
}
