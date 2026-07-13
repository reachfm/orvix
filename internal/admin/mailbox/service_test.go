package mailbox

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/audit"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func newMailboxTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE coremail_mailboxes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain_id INTEGER NOT NULL,
		tenant_id INTEGER NOT NULL,
		local_part TEXT NOT NULL,
		email TEXT NOT NULL,
		name TEXT,
		password_hash TEXT NOT NULL,
		auth_scheme TEXT,
		status TEXT NOT NULL,
		quota_mb INTEGER NOT NULL DEFAULT 0,
		used_bytes INTEGER NOT NULL DEFAULT 0,
		msg_count INTEGER NOT NULL DEFAULT 0,
		is_admin INTEGER NOT NULL DEFAULT 0,
		allow_smtp INTEGER NOT NULL DEFAULT 1,
		allow_imap INTEGER NOT NULL DEFAULT 1,
		allow_pop3 INTEGER NOT NULL DEFAULT 1,
		allow_jmap INTEGER NOT NULL DEFAULT 1,
		allow_webmail INTEGER NOT NULL DEFAULT 1,
		mfa_enabled INTEGER NOT NULL DEFAULT 0,
		send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
		recv_limit_per_hour INTEGER NOT NULL DEFAULT 0,
		last_login DATETIME,
		last_ip TEXT,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMailboxMutationRollsBackWhenAuditWriteFails(t *testing.T) {
	before := metricCounterValue(t, "orvix_audit_write_failures_total")
	db := newMailboxTestDB(t)
	db.SetMaxOpenConns(1)
	store := audit.NewExtendedStore(db)
	svc := NewService(NewAdminMailboxRepo(db), store, nil)
	_, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "audit-failure@example.test", Password: "InitialPassword123!",
	}, 10, 20)
	if err == nil {
		t.Fatal("audit failure must fail the mutation")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE email = ?`, "audit-failure@example.test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("mailbox mutation committed without its audit record")
	}
	if got := metricCounterValue(t, "orvix_audit_write_failures_total"); got != before+1 {
		t.Fatalf("audit failure metric = %v, want %v", got, before+1)
	}
}

func metricCounterValue(t *testing.T, name string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name && len(family.Metric) == 1 {
			return family.Metric[0].GetCounter().GetValue()
		}
	}
	t.Fatalf("metric %q not found", name)
	return 0
}

func TestMailboxServiceTenantScopeAndSecurePasswordReset(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), nil, nil)
	ctx := context.Background()

	created, err := svc.CreateMailbox(ctx, CreateMailboxRequest{
		Email:    "user@example.test",
		Password: "InitialPassword123!",
		Name:     "User",
	}, 10, 20)
	if err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	if created.Mailbox.TenantID != 10 || created.Mailbox.DomainID != 20 {
		t.Fatalf("created mailbox not scoped to requested tenant/domain: %#v", created.Mailbox)
	}
	if _, err := svc.GetMailbox(ctx, created.Mailbox.ID, 11); err != ErrMailboxNotFound {
		t.Fatalf("cross-tenant get must fail closed with ErrMailboxNotFound, got %v", err)
	}

	reset, err := svc.ResetPassword(ctx, created.Mailbox.ID, 10)
	if err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if len(reset) < 24 {
		t.Fatalf("reset password should be at least 24 chars, got %d", len(reset))
	}
	if isHexOnly(reset) {
		t.Fatalf("reset password must not be hex-only shortcut: %q", reset)
	}
	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM coremail_mailboxes WHERE id = ?`, created.Mailbox.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(reset)) != nil {
		t.Fatalf("stored hash does not verify returned one-time password")
	}
}

func isHexOnly(s string) bool {
	if s == "" {
		return false
	}
	return strings.Trim(s, "0123456789abcdefABCDEF") == ""
}
