package mailbox

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/prometheus/client_golang/prometheus"
	_ "modernc.org/sqlite"
)

// testHasher is a PasswordHasher backed by the real coremail Argon2id
// implementation, so tests exercise the exact hash format Coremail
// authentication verifies.
type testHasher struct {
	s *coremail.AuthService
}

func (h testHasher) HashPassword(password string) (string, error) {
	return h.s.HashPassword(password)
}

func newTestHasher() PasswordHasher {
	return testHasher{s: coremail.NewAuthService(nil, nil, nil, coremail.DefaultAuthConfig())}
}

func newMailboxTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE coremail_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	);
	CREATE TABLE coremail_mailboxes (
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
	);
	CREATE TABLE coremail_aliases (id INTEGER PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, deleted_at DATETIME);`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// seedDomain inserts an eligible domain for a tenant.
func seedDomain(t *testing.T, db *sql.DB, tenantID uint, name, status string, deleted bool) uint {
	t.Helper()
	now := time.Now().UTC()
	var deletedAt interface{}
	if deleted {
		deletedAt = now
	}
	res, err := db.Exec(
		`INSERT INTO coremail_domains (tenant_id, name, status, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?)`,
		tenantID, name, status, now, now, deletedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return uint(id)
}

func TestMailboxMutationRollsBackWhenAuditWriteFails(t *testing.T) {
	before := metricCounterValue(t, "orvix_audit_write_failures_total")
	db := newMailboxTestDB(t)
	db.SetMaxOpenConns(1)
	store := audit.NewExtendedStore(db)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), store, nil)
	// No orvix_audit table exists in this schema, so the transactional audit
	// write fails and the whole mutation must roll back.
	seedDomain(t, db, 10, "example.test", "active", false)
	_, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "audit-failure@example.test", Password: "InitialPassword123!",
	}, 10)
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
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()

	domID := seedDomain(t, db, 10, "example.test", "active", false)
	created, err := svc.CreateMailbox(ctx, CreateMailboxRequest{
		Email:    "user@example.test",
		Password: "InitialPassword123!",
		Name:     "User",
	}, 10)
	if err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	if created.Mailbox.TenantID != 10 || created.Mailbox.DomainID != domID {
		t.Fatalf("created mailbox not scoped to resolved domain: %#v", created.Mailbox)
	}
	if _, err := svc.GetMailbox(ctx, created.Mailbox.ID, 11); err != ErrMailboxNotFound {
		t.Fatalf("cross-tenant get must fail closed with ErrMailboxNotFound, got %v", err)
	}

	// Stored password must be the argon2id format coremail verifies.
	var hash string
	if err := db.QueryRow(`SELECT password_hash, auth_scheme FROM coremail_mailboxes WHERE id = ?`, created.Mailbox.ID).Scan(&hash, new(string)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("expected argon2id hash prefix, got %q", hash)
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
	if err := db.QueryRow(`SELECT password_hash FROM coremail_mailboxes WHERE id = ?`, created.Mailbox.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("reset must store argon2id hash, got %q", hash)
	}
	// The new hash must verify against the one-time password through the
	// exact coremail verification path.
	authSvc := coremail.NewAuthService(nil, nil, nil, coremail.DefaultAuthConfig())
	if !authSvc.VerifyPassword(reset, hash) {
		t.Fatalf("reset password does not verify through coremail AuthService")
	}
}

func TestMailboxCreateDomainEligibility(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()

	seedDomain(t, db, 10, "ours.test", "active", false)
	seedDomain(t, db, 99, "other-tenant.test", "active", false)
	seedDomain(t, db, 10, "disabled.test", "disabled", false)
	seedDomain(t, db, 10, "suspended.test", "suspended", false)
	seedDomain(t, db, 10, "locked.test", "locked", false)
	seedDomain(t, db, 10, "legacy-unknown.test", "mystery-status", false)
	seedDomain(t, db, 10, "deleted.test", "active", true)

	cases := []struct {
		name    string
		email   string
		wantErr error
	}{
		{"nonexistent domain", "x@nope.test", domain.ErrDomainNotFound},
		{"cross-tenant domain", "x@other-tenant.test", domain.ErrDomainNotFound},
		{"deleted domain", "x@deleted.test", domain.ErrDomainNotFound},
		{"disabled domain", "x@disabled.test", domain.ErrDomainDisabled},
		{"suspended domain", "x@suspended.test", domain.ErrDomainSuspended},
		{"locked domain", "x@locked.test", domain.ErrDomainLocked},
		{"unknown legacy status fails closed", "x@legacy-unknown.test", domain.ErrDomainUnavailable},
		{"invalid email no at", "notanemail", ErrInvalidEmail},
		{"invalid email empty local", "@ours.test", ErrInvalidEmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateMailbox(ctx, CreateMailboxRequest{Email: tc.email, Password: "Password123!"}, 10)
			if err != tc.wantErr {
				t.Fatalf("CreateMailbox(%s) err = %v, want %v", tc.email, err, tc.wantErr)
			}
		})
	}

	// Success case persists the real domain id (never zero).
	created, err := svc.CreateMailbox(ctx, CreateMailboxRequest{Email: "ok@ours.test", Password: "Password123!"}, 10)
	if err != nil {
		t.Fatalf("create on eligible domain: %v", err)
	}
	if created.Mailbox.DomainID == 0 {
		t.Fatal("persisted mailbox must reference a real domain id, got 0")
	}
}

func TestMailboxCreateDuplicateEmail(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "dup.test", "active", false)

	if _, err := svc.CreateMailbox(ctx, CreateMailboxRequest{Email: "a@dup.test", Password: "Password123!"}, 10); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.CreateMailbox(ctx, CreateMailboxRequest{Email: "a@dup.test", Password: "Password123!"}, 10); err != ErrMailboxExists {
		t.Fatalf("duplicate create err = %v, want ErrMailboxExists", err)
	}
}

func isHexOnly(s string) bool {
	if s == "" {
		return false
	}
	return strings.Trim(s, "0123456789abcdefABCDEF") == ""
}
