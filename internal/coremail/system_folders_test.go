package coremail

// Tests for the standalone system-folder provisioner.
//
// The installer bootstrap path and the admin CreateMailbox
// handler both call EnsureMailboxSystemFolders at the
// moment a mailbox row is inserted. Without this
// guarantee, the user opens Webmail for the first time
// and the Send handler returns
//
//   "Sent folder not found for mailbox;
//    ensure system folders are provisioned"
//
// because the MailStore's runtime MailStore has not been
// constructed yet at install time. These tests pin the
// canonical folder list and the idempotency of the
// provisioner.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "coremail_test.db")
	db, err := sql.Open("sqlite", dsn+"?_loc=auto&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustExec(t *testing.T, db *sql.DB, stmt string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("exec: %v\nSQL: %s", stmt, err)
	}
}

// TestEnsureMailboxSystemFoldersProvisionsCanonicalSet
// pins the contract that a fresh mailbox has the six
// canonical system folders after EnsureMailboxSystemFolders
// runs. The list of expected paths is the contract —
// downstream code (the webmail Send handler, the
// webmail Folders endpoint) relies on it.
func TestEnsureMailboxSystemFoldersProvisionsCanonicalSet(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	mustExec(t, db, `
		CREATE TABLE coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL,
			local_part TEXT NOT NULL,
			email TEXT NOT NULL,
			name TEXT,
			password_hash TEXT,
			auth_scheme TEXT,
			status TEXT,
			quota_mb INTEGER,
			used_bytes INTEGER,
			msg_count INTEGER,
			is_admin INTEGER,
			is_forwarder INTEGER,
			forward_to TEXT,
			labels TEXT,
			send_limit_per_hour INTEGER,
			recv_limit_per_hour INTEGER,
			last_login DATETIME,
			last_ip TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`)
	mustExec(t, db, `
		CREATE TABLE coremail_folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox_id INTEGER NOT NULL,
			parent_id INTEGER,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			folder_type TEXT NOT NULL,
			message_count INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0,
			total_size INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`)
	now := time.Now().UTC()
	res, err := db.ExecContext(ctx, `
		INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, status, quota_mb, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'active', 1024, ?, ?)`,
		1, 1, "admin", "admin@orvix.email", "Admin", now, now)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	mailboxID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last id: %v", err)
	}

	// Pre-condition: no folders yet.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM coremail_folders WHERE mailbox_id = ?", mailboxID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 folders, got %d", count)
	}

	// Provision.
	if err := EnsureMailboxSystemFolders(ctx, db, uint(mailboxID)); err != nil {
		t.Fatalf("EnsureMailboxSystemFolders: %v", err)
	}

	// Post-condition: the six canonical folders exist.
	expected := map[string]string{
		"INBOX":   "inbox",
		"Sent":    "sent",
		"Drafts":  "drafts",
		"Trash":   "trash",
		"Junk":    "junk",
		"Archive": "archive",
	}
	rows, err := db.Query("SELECT path, folder_type FROM coremail_folders WHERE mailbox_id = ? ORDER BY path", mailboxID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var p, ft string
		if err := rows.Scan(&p, &ft); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[p] = ft
	}
	if len(got) != len(expected) {
		t.Fatalf("expected %d folders, got %d (%v)", len(expected), len(got), got)
	}
	for path, ft := range expected {
		if got[path] != ft {
			t.Errorf("folder %q: expected type %q, got %q", path, ft, got[path])
		}
	}
}

// TestEnsureMailboxSystemFoldersIsIdempotent pins the
// no-regression requirement: re-running the provision
// on a mailbox that already has its system folders is
// a no-op. This is what makes the handler-level
// "login → ensure folders" path safe: a user logging
// in 100 times does not produce 600 folder rows.
func TestEnsureMailboxSystemFoldersIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	mustExec(t, db, `CREATE TABLE coremail_mailboxes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL,
		local_part TEXT NOT NULL, email TEXT NOT NULL,
		name TEXT, password_hash TEXT, auth_scheme TEXT,
		status TEXT, quota_mb INTEGER, used_bytes INTEGER,
		msg_count INTEGER, is_admin INTEGER, is_forwarder INTEGER,
		forward_to TEXT, labels TEXT, send_limit_per_hour INTEGER,
		recv_limit_per_hour INTEGER, last_login DATETIME, last_ip TEXT,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
		deleted_at DATETIME)`)
	mustExec(t, db, `CREATE TABLE coremail_folders (
		id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL,
		parent_id INTEGER, name TEXT NOT NULL, path TEXT NOT NULL,
		folder_type TEXT NOT NULL, message_count INTEGER NOT NULL DEFAULT 0,
		unread_count INTEGER NOT NULL DEFAULT 0,
		total_size INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`)

	now := time.Now().UTC()
	res, _ := db.ExecContext(ctx, `
		INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, status, quota_mb, created_at, updated_at)
		VALUES (1, 1, "alice", "alice@example.com", "Alice", 'active', 1024, ?, ?)`,
		now, now)
	mailboxID, _ := res.LastInsertId()

	// First call: provisions the six folders.
	if err := EnsureMailboxSystemFolders(ctx, db, uint(mailboxID)); err != nil {
		t.Fatalf("first provision: %v", err)
	}

	// Second + third call: no-op.
	for i := 0; i < 3; i++ {
		if err := EnsureMailboxSystemFolders(ctx, db, uint(mailboxID)); err != nil {
			t.Fatalf("re-provision #%d: %v", i, err)
		}
	}

	// Final count must still be six.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM coremail_folders WHERE mailbox_id = ?", mailboxID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 6 {
		t.Fatalf("expected 6 folders after idempotent re-runs, got %d", count)
	}
}

// TestEnsureMailboxSystemFoldersRejectsUnknownMailbox
// pins the precondition check: trying to provision
// folders for a mailbox id that does not exist returns
// a clean error rather than silently inserting orphan
// folder rows. The webmail login flow relies on this
// to fail closed.
func TestEnsureMailboxSystemFoldersRejectsUnknownMailbox(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	mustExec(t, db, `CREATE TABLE coremail_mailboxes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL,
		local_part TEXT NOT NULL, email TEXT NOT NULL,
		name TEXT, password_hash TEXT, auth_scheme TEXT,
		status TEXT, quota_mb INTEGER, used_bytes INTEGER,
		msg_count INTEGER, is_admin INTEGER, is_forwarder INTEGER,
		forward_to TEXT, labels TEXT, send_limit_per_hour INTEGER,
		recv_limit_per_hour INTEGER, last_login DATETIME, last_ip TEXT,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
		deleted_at DATETIME)`)
	mustExec(t, db, `CREATE TABLE coremail_folders (
		id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL,
		parent_id INTEGER, name TEXT NOT NULL, path TEXT NOT NULL,
		folder_type TEXT NOT NULL, message_count INTEGER NOT NULL DEFAULT 0,
		unread_count INTEGER NOT NULL DEFAULT 0,
		total_size INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`)

	err := EnsureMailboxSystemFolders(ctx, db, 9999)
	if err == nil {
		t.Fatal("expected error for unknown mailbox, got nil")
	}
	if msg := err.Error(); msg == "" || (msg != "ensure system folders: mailbox 9999 not found" &&
		!contains(msg, "mailbox 9999 not found")) {
		t.Fatalf("expected 'mailbox 9999 not found' error, got: %v", err)
	}
}

// contains is a tiny helper because strings.Contains
// cannot be used inside a t.Fatalf format that takes
// %v + an error message; we keep the import list
// tight here.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
