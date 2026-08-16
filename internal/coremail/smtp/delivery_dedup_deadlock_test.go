package smtp

// Regression test for a live production outage: claimDeliveryDedup
// (the delivery-idempotency claim added alongside InternetMessageID
// persistence) called dbdialect.Detect(r.DB) — a fresh query against
// the connection POOL — from INSIDE the already-open acceptance
// transaction. On this deployment's driver (SQLite, single-connection
// pool per the documented "_txlock=immediate" contract already noted
// elsewhere in this file), the open transaction holds the pool's only
// connection, so the nested Detect() call deadlocked forever waiting
// for a second connection that could never be granted — for every
// inbound message carrying a Message-ID header, i.e. virtually all
// real mail. The stuck transaction permanently starved the single
// SQLite connection, hanging every other DB-dependent request in the
// entire application, including totally unrelated CSRF token
// generation and login — a full admin+webmail auth outage traced via
// a live goroutine dump (SIGQUIT) to exactly this call site.
//
// The fix: claimDeliveryDedup uses the ALREADY-CACHED r.getDialect()
// (warmed once, before BeginTx, exactly as AcceptMessage's own
// "Dialect detection MUST run before BeginTx" comment already
// documents for the domain-lock path) instead of re-detecting inside
// the transaction. This test pins that fix with a real
// SetMaxOpenConns(1) pool — the exact single-connection contract that
// makes the bug deterministic rather than a rare timing coincidence —
// and fails on a hang rather than hanging the test suite itself.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestClaimDeliveryDedup_DoesNotDeadlockSingleConnectionPool(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dedup.db")+"?_journal_mode=WAL&_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// The exact production configuration that makes the deadlock
	// deterministic: SQLite's single-writer model is normally paired
	// with a single-connection pool so every write serializes through
	// one connection rather than racing SQLITE_BUSY across many.
	db.SetMaxOpenConns(1)

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS coremail_delivery_dedup (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox_id INTEGER NOT NULL,
			dedup_key TEXT NOT NULL,
			message_id TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE(mailbox_id, dedup_key)
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}

	r := &Receiver{DB: db}
	// getDialect() is NOT pre-warmed here on purpose: this test proves
	// claimDeliveryDedup itself never triggers a fresh pool-level
	// Detect() call while a transaction is open, regardless of whether
	// some earlier code path happened to warm the cache first.

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	done := make(chan struct{})
	var claimed bool
	var claimErr error
	go func() {
		defer close(done)
		claimed, claimErr = r.claimDeliveryDedup(ctx, tx, 42, "mid:<deadlock-regression@sender.example>", "internal-msg-id-1")
	}()

	select {
	case <-done:
		// Completed — the deadlock is fixed.
	case <-time.After(5 * time.Second):
		t.Fatal("claimDeliveryDedup deadlocked on a single-connection pool while holding the transaction's only connection — this is the exact live production outage (admin/webmail auth hang) this test exists to catch")
	}

	if claimErr != nil {
		t.Fatalf("claimDeliveryDedup error: %v", claimErr)
	}
	if !claimed {
		t.Fatal("expected the first claim to succeed")
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A concurrent, totally unrelated write against the SAME
	// single-connection pool — modeling the CSRF token generation
	// that hung in production — must also complete promptly, proving
	// the pool was never left starved by the dedup claim above.
	unrelatedDone := make(chan error, 1)
	go func() {
		_, err := db.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS unrelated_probe (id INTEGER PRIMARY KEY)`)
		unrelatedDone <- err
	}()
	select {
	case err := <-unrelatedDone:
		if err != nil {
			t.Fatalf("unrelated pool query failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pool starved after claimDeliveryDedup: an unrelated write never completed")
	}
}

func TestClaimDeliveryDedup_UsesCachedDialectNotFreshDetection(t *testing.T) {
	// A second, cheaper regression: once getDialect() has been warmed
	// (as AcceptMessage always does before BeginTx in production),
	// claimDeliveryDedup must reuse that cached value — never issue
	// its own dbdialect.Detect call — so behavior is identical whether
	// or not this test pre-warms the cache.
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dedup2.db")+"?_journal_mode=WAL&_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS coremail_delivery_dedup (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox_id INTEGER NOT NULL,
		dedup_key TEXT NOT NULL,
		message_id TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		UNIQUE(mailbox_id, dedup_key)
	)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	r := &Receiver{DB: db}
	// Explicitly pre-warm, mirroring AcceptMessage's real call order.
	r.getDialect()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	done := make(chan error, 1)
	go func() {
		_, err := r.claimDeliveryDedup(ctx, tx, 7, "mid:<prewarmed@sender.example>", "internal-msg-id-2")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("claimDeliveryDedup error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("claimDeliveryDedup deadlocked even with a pre-warmed dialect cache")
	}
}
