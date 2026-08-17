package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

func ptmSeedSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL,
			local_part TEXT NOT NULL,
			email TEXT UNIQUE NOT NULL,
			name TEXT,
			password_hash TEXT NOT NULL,
			auth_scheme TEXT NOT NULL,
			mfa_enabled INTEGER NOT NULL DEFAULT 0,
			mfa_secret TEXT,
			app_passwords TEXT,
			status TEXT NOT NULL,
			quota_mb INTEGER NOT NULL,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			msg_count INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0,
			is_forwarder INTEGER NOT NULL DEFAULT 0,
			forward_to TEXT,
			labels TEXT,
			send_limit_per_hour INTEGER,
			recv_limit_per_hour INTEGER,
			last_login DATETIME,
			last_ip TEXT,
			mail_access_mode TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor TEXT, role TEXT, action TEXT, target TEXT, result TEXT,
			ip TEXT, user_agent TEXT, timestamp DATETIME
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed schema: %v\n%s", err, s)
		}
	}
}

func ptmDeps(t *testing.T, db *sql.DB, dial *dbdialect.Info, isRoot bool, stdout, stderr *bytes.Buffer) adminCLIDeps {
	return adminCLIDeps{
		isRoot: func() bool { return isRoot },
		openDB: func() (*sql.DB, *dbdialect.Info, func() error, error) {
			return db, dial, func() error { return nil }, nil
		},
		now:    func() time.Time { return time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC) },
		stdout: stdout,
		stderr: stderr,
	}
}

func TestProvisionTransactionalMailbox_NonRootDenied(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	ptmSeedSchema(t, db)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	deps := ptmDeps(t, db, dial, false, stdout, stderr)

	code := runAdminProvisionTransactionalMailbox([]string{"--domain", "orvix.email", "--confirm", "PROVISION-MAILBOX"}, deps)
	if code != 1 {
		t.Fatalf("want exit 1, got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "root") {
		t.Fatalf("want root error, got %s", stderr.String())
	}
}

func TestProvisionTransactionalMailbox_BadConfirmRejected(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	ptmSeedSchema(t, db)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	deps := ptmDeps(t, db, dial, true, stdout, stderr)

	code := runAdminProvisionTransactionalMailbox([]string{"--domain", "orvix.email", "--confirm", "nope"}, deps)
	if code != 2 {
		t.Fatalf("want exit 2, got %d (stderr=%s)", code, stderr.String())
	}
}

func TestProvisionTransactionalMailbox_MissingDomainFailsClosed(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	ptmSeedSchema(t, db)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	deps := ptmDeps(t, db, dial, true, stdout, stderr)

	// No coremail_domains row exists at all — must fail closed, never create one.
	code := runAdminProvisionTransactionalMailbox([]string{"--domain", "orvix.email", "--confirm", "PROVISION-MAILBOX"}, deps)
	if code != 1 {
		t.Fatalf("want exit 1, got %d (stdout=%s stderr=%s)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "never creates a domain") {
		t.Fatalf("want domain-not-found error, got %s", stderr.String())
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM coremail_domains").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a domain row was created — this must never happen, got %d rows", count)
	}
}

func TestProvisionTransactionalMailbox_CreatesMailboxAndPreservesDKIM(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	ptmSeedSchema(t, db)
	if _, err := db.Exec(`INSERT INTO coremail_domains (name, tenant_id, status, dkim_enabled, dkim_selector) VALUES ('orvix.email', 1, 'active', 1, 'orvix')`); err != nil {
		t.Fatal(err)
	}

	envPath := filepath.Join(t.TempDir(), "coremail-transactional.env")
	origPath := transactionalCredentialsEnvPath
	transactionalCredentialsEnvPath = envPath
	defer func() { transactionalCredentialsEnvPath = origPath }()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	deps := ptmDeps(t, db, dial, true, stdout, stderr)

	code := runAdminProvisionTransactionalMailbox([]string{"--domain", "orvix.email", "--local-part", "noreply", "--confirm", "PROVISION-MAILBOX"}, deps)
	if code != 0 {
		t.Fatalf("want success, got %d (stdout=%s stderr=%s)", code, stdout.String(), stderr.String())
	}

	// DKIM row must be byte-for-byte untouched.
	var dkimEnabled int
	var dkimSelector string
	if err := db.QueryRow("SELECT dkim_enabled, dkim_selector FROM coremail_domains WHERE name='orvix.email'").Scan(&dkimEnabled, &dkimSelector); err != nil {
		t.Fatal(err)
	}
	if dkimEnabled != 1 || dkimSelector != "orvix" {
		t.Fatalf("DKIM config was mutated: enabled=%d selector=%q", dkimEnabled, dkimSelector)
	}
	var domainCount int
	db.QueryRow("SELECT COUNT(*) FROM coremail_domains").Scan(&domainCount)
	if domainCount != 1 {
		t.Fatalf("want exactly 1 domain row (no new domain created), got %d", domainCount)
	}

	var email, status, accessMode string
	var mailboxCount int
	if err := db.QueryRow("SELECT email, status, mail_access_mode FROM coremail_mailboxes WHERE email='noreply@orvix.email'").Scan(&email, &status, &accessMode); err != nil {
		t.Fatalf("mailbox row not found: %v", err)
	}
	if status != "active" || accessMode != "internal_external" {
		t.Fatalf("unexpected mailbox state: status=%q access_mode=%q", status, accessMode)
	}
	db.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes").Scan(&mailboxCount)
	if mailboxCount != 1 {
		t.Fatalf("want exactly 1 mailbox row, got %d", mailboxCount)
	}

	var auditCount int
	db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action='admin.transactional_mailbox_provision'").Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("want 1 audit row, got %d", auditCount)
	}

	// Password must never appear in stdout/stderr.
	if strings.Contains(stdout.String(), "COREMAIL_TRANSACTIONAL_SMTP_PASSWORD=") {
		t.Fatalf("password leaked into stdout: %s", stdout.String())
	}

	// Env file must exist, be non-empty, and carry both vars — but its
	// content (the password) is not asserted here beyond presence, since
	// this test only proves plumbing, not entropy.
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("env file not written: %v", err)
	}
	if !strings.Contains(string(content), "COREMAIL_TRANSACTIONAL_SMTP_USERNAME=noreply@orvix.email") {
		t.Fatalf("env file missing username: %s", content)
	}
	if !strings.Contains(string(content), "COREMAIL_TRANSACTIONAL_SMTP_PASSWORD=") {
		t.Fatalf("env file missing password line")
	}
}

func TestProvisionTransactionalMailbox_IdempotentNoPasswordReset(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	ptmSeedSchema(t, db)
	if _, err := db.Exec(`INSERT INTO coremail_domains (name, tenant_id, status, dkim_enabled, dkim_selector) VALUES ('orvix.email', 1, 'active', 1, 'orvix')`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, password_hash, auth_scheme, status, quota_mb, mail_access_mode, version, created_at, updated_at) VALUES (1, 1, 'noreply', 'noreply@orvix.email', 'EXISTING_HASH', 'argon2id', 'active', 256, 'internal_external', 1, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	deps := ptmDeps(t, db, dial, true, stdout, stderr)

	code := runAdminProvisionTransactionalMailbox([]string{"--domain", "orvix.email", "--confirm", "PROVISION-MAILBOX"}, deps)
	if code != 0 {
		t.Fatalf("want success (idempotent no-op), got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already exists") {
		t.Fatalf("want already-exists message, got %s", stdout.String())
	}

	var hash string
	db.QueryRow("SELECT password_hash FROM coremail_mailboxes WHERE email='noreply@orvix.email'").Scan(&hash)
	if hash != "EXISTING_HASH" {
		t.Fatalf("password was reset on an idempotent re-run: got hash %q", hash)
	}
}
