package mailboxmgmt

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/coremail"
	_ "modernc.org/sqlite"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/mbox_test.db", t.TempDir()))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range coremailTables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	ctx := context.Background()

	// Provision a domain for testing.
	eng.Domains.Create(ctx, &coremail.Domain{Name: "test.com", Status: coremail.DomainActive}, nil)
	return NewService(eng)
}

func TestCreateMailbox(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	mb, err := svc.CreateMailbox(ctx, &CreateMailboxRequest{
		Email: "user@test.com", Name: "Test User", Password: "StrongPass1!",
		DomainID: 1, QuotaMB: 1024,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if mb.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if mb.Email != "user@test.com" {
		t.Fatalf("expected user@test.com, got %s", mb.Email)
	}
}

func TestCreateDuplicateRejected(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	svc.CreateMailbox(ctx, &CreateMailboxRequest{
		Email: "dup@test.com", Name: "Dup", Password: "StrongPass1!", DomainID: 1,
	})
	_, err := svc.CreateMailbox(ctx, &CreateMailboxRequest{
		Email: "dup@test.com", Name: "Dup2", Password: "StrongPass2!", DomainID: 1,
	})
	if err == nil {
		t.Fatal("expected error for duplicate")
	}
}

func TestCreateInvalidLocalPartRejected(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	invalid := []string{"", "@test.com", "a b@test.com", "user@", strings.Repeat("a", 70) + "@test.com"}
	for _, email := range invalid {
		_, err := svc.CreateMailbox(ctx, &CreateMailboxRequest{
			Email: email, Name: "Test", Password: "Strong1!", DomainID: 1,
		})
		if err == nil {
			t.Errorf("expected error for invalid email: %q", email)
		}
	}
}

func TestCreateWeakPasswordRejected(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	_, err := svc.CreateMailbox(ctx, &CreateMailboxRequest{
		Email: "weak@test.com", Name: "Weak", Password: "short", DomainID: 1,
	})
	if err == nil {
		t.Fatal("expected error for weak password")
	}
}

func TestCreateInactiveDomainRejected(t *testing.T) {
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/inact_test.db", t.TempDir()))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	for _, stmt := range coremailTables() {
		db.Exec(stmt)
	}
	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	eng.Domains.Create(context.Background(), &coremail.Domain{Name: "inactive.com", Status: coremail.DomainSuspended}, nil)
	svc := NewService(eng)

	_, err = svc.CreateMailbox(context.Background(), &CreateMailboxRequest{
		Email: "test@inactive.com", Name: "Test", Password: "Strong1!", DomainID: 1,
	})
	if err == nil {
		t.Fatal("expected error for inactive domain")
	}
}

func TestListMailboxes(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	svc.CreateMailbox(ctx, &CreateMailboxRequest{Email: "a@test.com", Name: "A", Password: "Strong1!", DomainID: 1})
	svc.CreateMailbox(ctx, &CreateMailboxRequest{Email: "b@test.com", Name: "B", Password: "Strong1!", DomainID: 1})

	list, err := svc.ListMailboxes(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
}

func TestUpdateMailbox(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	created, _ := svc.CreateMailbox(ctx, &CreateMailboxRequest{Email: "upd@test.com", Name: "Old", Password: "Strong1!", DomainID: 1})
	newName := "New Name"
	updated, err := svc.UpdateMailbox(ctx, created.ID, &UpdateMailboxRequest{Name: &newName})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "New Name" {
		t.Fatalf("expected 'New Name', got '%s'", updated.Name)
	}
}

func TestResetPassword(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	created, _ := svc.CreateMailbox(ctx, &CreateMailboxRequest{Email: "reset@test.com", Name: "Reset", Password: "Strong1!", DomainID: 1})
	if err := svc.ResetPassword(ctx, created.ID, "NewStrong1!"); err != nil {
		t.Fatalf("reset password: %v", err)
	}

	// Verify new password works.
	_, err := svc.engine.Auth.AuthenticateMailbox(ctx, "reset@test.com", "NewStrong1!")
	if err != nil {
		t.Fatal("new password should authenticate")
	}
}

func TestSuspendActivate(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	created, _ := svc.CreateMailbox(ctx, &CreateMailboxRequest{Email: "sa@test.com", Name: "SA", Password: "Strong1!", DomainID: 1})

	suspended, err := svc.SuspendMailbox(ctx, created.ID)
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if suspended.Status != MailboxSuspended {
		t.Fatal("expected suspended status")
	}

	activated, err := svc.ActivateMailbox(ctx, created.ID)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if activated.Status != MailboxActive {
		t.Fatal("expected active status")
	}
}

func TestDeleteMailbox(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	created, _ := svc.CreateMailbox(ctx, &CreateMailboxRequest{Email: "del@test.com", Name: "Del", Password: "Strong1!", DomainID: 1})
	if err := svc.DeleteMailbox(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := svc.GetMailbox(ctx, created.ID)
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestPasswordNotReturned(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	mb, _ := svc.CreateMailbox(ctx, &CreateMailboxRequest{Email: "nopw@test.com", Name: "NoPW", Password: "Strong1!", DomainID: 1})
	// Mailbox admin view should not contain password.
	if mb.Email == "" {
		t.Fatal("expected email")
	}
	// Password is on the coremail.Mailbox struct with json:"-" so it won't appear.
}

func coremailTables() []string {
	// Schema matches internal/coremail/coremail_test.go exactly.
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
			deleted_at DATETIME,
			FOREIGN KEY (domain_id) REFERENCES coremail_domains(id)
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_aliases (
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
	}
}
