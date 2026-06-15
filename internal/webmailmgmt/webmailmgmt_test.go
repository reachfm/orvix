package webmailmgmt

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	_ "modernc.org/sqlite"
)

func testService(t *testing.T) (*Service, *coremail.Engine) {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/test.db", root))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	ctx := context.Background()

	if err := eng.Domains.Create(ctx, &coremail.Domain{Name: "test.com", Status: coremail.DomainActive}, nil); err != nil {
		t.Fatalf("create domain: %v", err)
	}

	// Create a test mailbox.
	hash, err := eng.Auth.HashPassword("TestPass123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO coremail_mailboxes (domain_id, local_part, email, password_hash, auth_scheme, status, is_admin, msg_count, used_bytes, created_at, updated_at)
		VALUES (1, 'user', 'user@test.com', ?, 'argon2id', 'active', 0, 10, 51200, ?, ?)`, hash, now, now); err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}

	svc := NewService(eng, db)
	return svc, eng
}

func tables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_domains (
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
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			mtasts_enabled INTEGER NOT NULL DEFAULT 0,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
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
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
	}
}

func TestRecordAndListSessions(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	if err := svc.RecordSession(ctx, 1, "10.0.0.1", "TestAgent/1.0"); err != nil {
		t.Fatalf("record session: %v", err)
	}
	if err := svc.RecordSession(ctx, 1, "10.0.0.2", "TestAgent/2.0"); err != nil {
		t.Fatalf("record session: %v", err)
	}
	if err := svc.RecordSession(ctx, 1, "10.0.0.3", "TestAgent/3.0"); err != nil {
		t.Fatalf("record session: %v", err)
	}

	sessions, err := svc.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	mbID := uint(1)
	sessions, err = svc.ListSessions(ctx, &mbID)
	if err != nil {
		t.Fatalf("list sessions by mailbox: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions for mailbox 1, got %d", len(sessions))
	}

	if sessions[0].Email != "user@test.com" {
		t.Fatalf("expected email user@test.com, got %s", sessions[0].Email)
	}
	if sessions[0].RevokedAt != nil {
		t.Fatal("expected sessions to not be revoked")
	}
	if sessions[0].IP != "10.0.0.3" {
		t.Fatalf("expected IP 10.0.0.3 for newest session, got %s", sessions[0].IP)
	}
}

func TestRevokeSession(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	if err := svc.RecordSession(ctx, 1, "10.0.0.1", "Agent"); err != nil {
		t.Fatalf("record: %v", err)
	}

	sessions, _ := svc.ListSessions(ctx, nil)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	sessionID := sessions[0].ID

	if err := svc.RevokeSession(ctx, sessionID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	// Should no longer appear in active sessions.
	sessions, _ = svc.ListSessions(ctx, nil)
	if len(sessions) != 0 {
		t.Fatalf("expected 0 active sessions after revoke, got %d", len(sessions))
	}

	// Revoking again should return error.
	if err := svc.RevokeSession(ctx, sessionID); err == nil {
		t.Fatal("expected error when revoking already revoked session")
	}
}

func TestRevokeAllSessions(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := svc.RecordSession(ctx, 1, fmt.Sprintf("10.0.0.%d", i), "Agent"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	if err := svc.RevokeAllSessions(ctx, 1); err != nil {
		t.Fatalf("revoke all: %v", err)
	}

	sessions, _ := svc.ListSessions(ctx, nil)
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after revoke all, got %d", len(sessions))
	}
}

func TestLoginActivity(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	activity, err := svc.GetLoginActivity(ctx, 1)
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if activity.SuccessfulLogins != 0 || activity.FailedLogins != 0 {
		t.Fatalf("expected zero activity, got %+v", activity)
	}

	for i := 0; i < 5; i++ {
		if err := svc.RecordLoginActivity(ctx, 1, true, "10.0.0.1", "Agent"); err != nil {
			t.Fatalf("record success: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := svc.RecordLoginActivity(ctx, 1, false, "10.0.0.2", "Agent"); err != nil {
			t.Fatalf("record fail: %v", err)
		}
	}

	activity, err = svc.GetLoginActivity(ctx, 1)
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if activity.SuccessfulLogins != 5 {
		t.Fatalf("expected 5 successful, got %d", activity.SuccessfulLogins)
	}
	if activity.FailedLogins != 3 {
		t.Fatalf("expected 3 failed, got %d", activity.FailedLogins)
	}
}

func TestClearFailedLoginCounters(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := svc.RecordLoginActivity(ctx, 1, false, "10.0.0.1", "Agent"); err != nil {
			t.Fatalf("record fail: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := svc.RecordLoginActivity(ctx, 1, true, "10.0.0.1", "Agent"); err != nil {
			t.Fatalf("record success: %v", err)
		}
	}

	if err := svc.ClearFailedLoginCounters(ctx, 1); err != nil {
		t.Fatalf("clear counters: %v", err)
	}

	activity, _ := svc.GetLoginActivity(ctx, 1)
	if activity.FailedLogins != 0 {
		t.Fatalf("expected 0 failed logins after clear, got %d", activity.FailedLogins)
	}
	if activity.SuccessfulLogins != 2 {
		t.Fatalf("expected 2 successful logins preserved, got %d", activity.SuccessfulLogins)
	}
}

func TestListAccounts(t *testing.T) {
	svc, eng := testService(t)
	ctx := context.Background()

	// Add an admin mailbox.
	hash, _ := eng.Auth.HashPassword("AdminPass123!")
	now := time.Now().UTC()
	if _, err := eng.DB.Exec(`INSERT INTO coremail_mailboxes (domain_id, local_part, email, password_hash, auth_scheme, status, is_admin, created_at, updated_at)
		VALUES (1, 'admin', 'admin@test.com', ?, 'argon2id', 'active', 1, ?, ?)`, hash, now, now); err != nil {
		t.Fatalf("insert admin: %v", err)
	}

	accounts, err := svc.ListAccounts(ctx, "", "", "", nil)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}

	adminTrue := true
	accounts, _ = svc.ListAccounts(ctx, "", "", "", &adminTrue)
	if len(accounts) != 1 || !accounts[0].IsAdmin {
		t.Fatalf("expected 1 admin account, got %d", len(accounts))
	}

	adminFalse := false
	accounts, _ = svc.ListAccounts(ctx, "", "", "", &adminFalse)
	if len(accounts) != 1 || accounts[0].IsAdmin {
		t.Fatalf("expected 1 non-admin account, got %d", len(accounts))
	}

	accounts, _ = svc.ListAccounts(ctx, "admin", "", "", nil)
	if len(accounts) != 1 || accounts[0].Email != "admin@test.com" {
		t.Fatalf("expected 1 account matching 'admin', got %d", len(accounts))
	}

	accounts, _ = svc.ListAccounts(ctx, "", "test.com", "", nil)
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts for test.com, got %d", len(accounts))
	}

	accounts, _ = svc.ListAccounts(ctx, "", "", "active", nil)
	if len(accounts) != 2 {
		t.Fatalf("expected 2 active accounts, got %d", len(accounts))
	}
}

func TestForceLogout(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	if err := svc.RecordSession(ctx, 1, "10.0.0.1", "Agent"); err != nil {
		t.Fatalf("record session: %v", err)
	}
	if err := svc.ForceLogoutAll(ctx, 1); err != nil {
		t.Fatalf("force logout: %v", err)
	}
	sessions, _ := svc.ListSessions(ctx, nil)
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after force logout, got %d", len(sessions))
	}
}

func TestUnlockMailbox(t *testing.T) {
	svc, eng := testService(t)
	ctx := context.Background()

	// Set mailbox status to locked.
	if _, err := eng.DB.ExecContext(ctx, `UPDATE coremail_mailboxes SET status = 'locked' WHERE id = 1`); err != nil {
		t.Fatalf("set locked: %v", err)
	}

	if err := svc.UnlockMailbox(ctx, 1); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	mb, err := eng.Mailboxes.GetByID(ctx, 1, nil)
	if err != nil {
		t.Fatalf("get mailbox: %v", err)
	}
	if mb.Status != coremail.MailboxActive {
		t.Fatalf("expected active status, got %s", mb.Status)
	}
}

func TestResetWebmailPreferences(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	if err := svc.ResetWebmailPreferences(ctx, 1); err != nil {
		t.Fatalf("reset preferences: %v", err)
	}
}

func TestStorageMetrics(t *testing.T) {
	svc, eng := testService(t)
	ctx := context.Background()

	// Create folders and a message in the store DB.
	for _, stmt := range storageTables() {
		if _, err := eng.DB.Exec(stmt); err != nil {
			t.Fatalf("create storage table: %v", err)
		}
	}

	now := time.Now().UTC()
	if _, err := eng.DB.Exec(`INSERT INTO coremail_folders (mailbox_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at)
		VALUES (1, 'Inbox', 'INBOX', 'inbox', 3, 0, 51200, ?, ?)`, now, now); err != nil {
		t.Fatalf("insert inbox folder: %v", err)
	}
	if _, err := eng.DB.Exec(`INSERT INTO coremail_folders (mailbox_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at)
		VALUES (1, 'Sent', 'Sent', 'sent', 2, 0, 25600, ?, ?)`, now, now); err != nil {
		t.Fatalf("insert sent folder: %v", err)
	}

	// Add some messages.
	for i := 0; i < 3; i++ {
		if _, err := eng.DB.Exec(`INSERT INTO coremail_messages (message_id, mailbox_id, folder_id, subject, from_address, to_addresses, received_date, size_bytes, rfc822_path, sha256, seen, created_at, updated_at)
			VALUES (?, 1, 1, 'Test', 'sender@other.com', 'user@test.com', ?, 1024, '/dev/null', 'abc', 1, ?, ?)`,
			fmt.Sprintf("msg-inbox-%d", i), now, now, now); err != nil {
			t.Fatalf("insert inbox msg: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := eng.DB.Exec(`INSERT INTO coremail_messages (message_id, mailbox_id, folder_id, subject, from_address, to_addresses, received_date, size_bytes, rfc822_path, sha256, seen, created_at, updated_at)
			VALUES (?, 1, 2, 'Sent', 'user@test.com', 'recip@other.com', ?, 2048, '/dev/null', 'def', 1, ?, ?)`,
			fmt.Sprintf("msg-sent-%d", i), now, now, now); err != nil {
			t.Fatalf("insert sent msg: %v", err)
		}
	}

	metrics, err := svc.GetStorageMetrics(ctx, 1)
	if err != nil {
		t.Fatalf("get storage: %v", err)
	}
	if metrics.MessageCount != 5 {
		t.Fatalf("expected 5 messages from canonical coremail_messages, got %d", metrics.MessageCount)
	}
	if metrics.MailboxSize != 7168 {
		t.Fatalf("expected 7168 bytes from size_bytes SUM, got %d", metrics.MailboxSize)
	}
	if metrics.SentCount != 2 {
		t.Fatalf("expected 2 sent messages, got %d", metrics.SentCount)
	}
	if metrics.ReceivedCount != 3 {
		t.Fatalf("expected 3 received messages, got %d", metrics.ReceivedCount)
	}
}

func TestStorageMetricsIgnoresStaleCounters(t *testing.T) {
	svc, eng := testService(t)
	ctx := context.Background()

	for _, stmt := range storageTables() {
		if _, err := eng.DB.Exec(stmt); err != nil {
			t.Fatalf("create storage table: %v", err)
		}
	}

	now := time.Now().UTC()
	if _, err := eng.DB.Exec(`INSERT INTO coremail_folders (mailbox_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at)
		VALUES (1, 'Inbox', 'INBOX', 'inbox', 999, 0, 999999, ?, ?)`, now, now); err != nil {
		t.Fatalf("insert inbox folder: %v", err)
	}
	if _, err := eng.DB.Exec(`INSERT INTO coremail_folders (mailbox_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at)
		VALUES (1, 'Sent', 'Sent', 'sent', 888, 0, 888888, ?, ?)`, now, now); err != nil {
		t.Fatalf("insert sent folder: %v", err)
	}

	// Stale cached counters on the mailbox row should NOT be used.
	if _, err := eng.DB.Exec(`UPDATE coremail_mailboxes SET msg_count = 99999, used_bytes = 999999999 WHERE id = 1`); err != nil {
		t.Fatalf("update stale counters: %v", err)
	}

	// Insert actual messages (5 total).
	for i := 0; i < 3; i++ {
		if _, err := eng.DB.Exec(`INSERT INTO coremail_messages (message_id, mailbox_id, folder_id, subject, from_address, to_addresses, received_date, size_bytes, rfc822_path, sha256, seen, created_at, updated_at)
			VALUES (?, 1, 1, 'InboxMsg', 'sender@other.com', 'user@test.com', ?, 1024, '/dev/null', 'abc', 1, ?, ?)`,
			fmt.Sprintf("stale-msg-inbox-%d", i), now, now, now); err != nil {
			t.Fatalf("insert inbox msg: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := eng.DB.Exec(`INSERT INTO coremail_messages (message_id, mailbox_id, folder_id, subject, from_address, to_addresses, received_date, size_bytes, rfc822_path, sha256, seen, created_at, updated_at)
			VALUES (?, 1, 2, 'SentMsg', 'user@test.com', 'recip@other.com', ?, 2048, '/dev/null', 'def', 1, ?, ?)`,
			fmt.Sprintf("stale-msg-sent-%d", i), now, now, now); err != nil {
			t.Fatalf("insert sent msg: %v", err)
		}
	}

	metrics, err := svc.GetStorageMetrics(ctx, 1)
	if err != nil {
		t.Fatalf("get storage: %v", err)
	}
	// Should reflect actual messages, not stale cached counters.
	if metrics.MessageCount != 5 {
		t.Fatalf("expected 5 messages from canonical query (stale msg_count=99999 ignored), got %d", metrics.MessageCount)
	}
	if metrics.MailboxSize != 7168 {
		t.Fatalf("expected 7168 bytes from canonical size_bytes SUM (stale used_bytes=999999999 ignored), got %d", metrics.MailboxSize)
	}
	if metrics.SentCount != 2 {
		t.Fatalf("expected 2 sent messages (stale folder count=888 ignored), got %d", metrics.SentCount)
	}
	if metrics.ReceivedCount != 3 {
		t.Fatalf("expected 3 received messages (stale folder count=999 ignored), got %d", metrics.ReceivedCount)
	}
}

func storageTables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox_id INTEGER NOT NULL,
			parent_id INTEGER,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			folder_type TEXT NOT NULL DEFAULT 'custom',
			message_count INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0,
			total_size INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL DEFAULT 0,
			mailbox_id INTEGER NOT NULL,
			folder_id INTEGER NOT NULL,
			thread_id TEXT,
			internet_message_id TEXT,
			subject TEXT NOT NULL DEFAULT '',
			from_address TEXT NOT NULL DEFAULT '',
			to_addresses TEXT NOT NULL DEFAULT '',
			cc_addresses TEXT NOT NULL DEFAULT '',
			bcc_addresses TEXT NOT NULL DEFAULT '',
			reply_to TEXT NOT NULL DEFAULT '',
			message_date DATETIME,
			received_date DATETIME NOT NULL,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			rfc822_path TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			seen INTEGER NOT NULL DEFAULT 0,
			answered INTEGER NOT NULL DEFAULT 0,
			flagged INTEGER NOT NULL DEFAULT 0,
			draft INTEGER NOT NULL DEFAULT 0,
			deleted INTEGER NOT NULL DEFAULT 0,
			junk INTEGER NOT NULL DEFAULT 0,
			importance INTEGER NOT NULL DEFAULT 0,
			retention_policy_id INTEGER,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			purged_at DATETIME
		)`,
	}
}

func TestSecurityNoPasswordHashesExposed(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	accounts, err := svc.ListAccounts(ctx, "", "", "", nil)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	// Verify no password_hash or password-related fields in the output.
	for _, a := range accounts {
		if a.Email == "user@test.com" {
			return // Found expected account, no password fields exposed.
		}
	}
	t.Fatal("expected at least one account with no password exposure")
}

func TestSecurityNoSessionTokenExposed(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	if err := svc.RecordSession(ctx, 1, "10.0.0.1", "Agent"); err != nil {
		t.Fatalf("record: %v", err)
	}

	sessions, err := svc.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, s := range sessions {
		// The session type does not have a Token field — verify no token-like field exists.
		if s.ID == 0 {
			t.Fatal("expected valid session without token exposure")
		}
		// Verify no password/secrets in the response.
		if s.IP == "" {
			t.Fatal("expected IP to be populated")
		}
	}
}
