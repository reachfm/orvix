package organization

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/audit"
	_ "modernc.org/sqlite"
)

func newOrganizationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		slug TEXT,
		domain TEXT,
		plan TEXT,
		max_domains INTEGER,
		max_mailboxes INTEGER,
		logo_url TEXT,
		primary_color TEXT,
		active INTEGER,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	);
	CREATE TABLE users (id INTEGER PRIMARY KEY, tenant_id INTEGER, role TEXT, deleted_at DATETIME, token_version INTEGER NOT NULL DEFAULT 0, active INTEGER NOT NULL DEFAULT 1);
	CREATE TABLE org_ownership_transfers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		organization_id INTEGER NOT NULL,
		from_user_id INTEGER NOT NULL,
		to_user_id INTEGER NOT NULL,
		token_hash TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		expires_at DATETIME,
		accepted_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOrganizationServiceLifecycleAndDuplicateSlug(t *testing.T) {
	svc := NewService(NewOrganizationRepo(newOrganizationTestDB(t)), nil, nil)
	ctx := context.Background()

	created, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "tenant-a", Domain: "tenant-a.test"}, 1)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if created.Name != "tenant-a" || !created.Active {
		t.Fatalf("defaults not applied: %#v", created)
	}
	if _, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "tenant-a"}, 1); err != ErrOrganizationExists {
		t.Fatalf("duplicate slug should fail with ErrOrganizationExists, got %v", err)
	}
	if err := svc.SetOrganizationActive(ctx, created.ID, false, "test"); err != nil {
		t.Fatalf("disable organization: %v", err)
	}
	got, err := svc.GetOrganization(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active {
		t.Fatalf("organization remained active after disable")
	}
}

func TestOrganizationMutationRollsBackWhenAuditWriteFails(t *testing.T) {
	db := newOrganizationTestDB(t)
	db.SetMaxOpenConns(1)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	if _, err := svc.CreateOrganization(context.Background(), CreateOrganizationRequest{Slug: "audit-failure", Domain: "audit-failure.test"}, 1); err == nil {
		t.Fatal("audit failure must fail the organization mutation")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE slug = ?`, "audit-failure").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("organization mutation committed without its audit record")
	}
}

// TestCreateOrganizationWithOwner_AtomicAndOwnerInvitation proves the
// PSA organization-creation path: org + owner invitation + audit in
// ONE transaction (audit failure rolls back BOTH rows), the owner
// invitation is always tenant_admin/pending with a 7-day expiry and a
// hashed token, and an ownerless request is rejected before any write.
func TestCreateOrganizationWithOwner_AtomicAndOwnerInvitation(t *testing.T) {
	db := newOrganizationTestDB(t)
	// org_invitations is created by the billing package; mirror the
	// minimal schema the repo writes.
	if _, err := db.Exec(`CREATE TABLE org_invitations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		organization_id INTEGER NOT NULL,
		inviter_id INTEGER NOT NULL,
		email TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'user',
		status TEXT NOT NULL DEFAULT 'pending',
		expires_at DATETIME,
		accepted_at DATETIME,
		revoked_at DATETIME,
		created_at DATETIME,
		updated_at DATETIME
	)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc := NewService(NewOrganizationRepo(db), nil, nil)

	// Ownerless creation is rejected before any write.
	if _, _, _, err := svc.CreateOrganizationWithOwner(ctx, CreateOrganizationRequest{Slug: "ownerless", Name: "Ownerless"}, 0, 7, ""); err == nil {
		t.Fatal("ownerless PSA organization creation must be rejected")
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE slug='ownerless'`).Scan(&count)
	if count != 0 {
		t.Fatal("rejected ownerless creation must not leave a tenant row")
	}

	// Valid creation: org + pending tenant_admin invitation + token.
	org, inv, rawToken, err := svc.CreateOrganizationWithOwner(ctx, CreateOrganizationRequest{Slug: "acme-psa", Name: "Acme PSA", Domain: "acme.test"}, 0, 42, "owner@acme.test")
	if err != nil {
		t.Fatalf("create with owner: %v", err)
	}
	if org.ID == 0 || org.Active {
		t.Fatalf("org must start pending_activation (active=false) with a designated owner invitation: %+v", org)
	}
	if inv == nil || inv.OrganizationID != org.ID || inv.Role != "tenant_admin" || inv.Status != InvitationPending {
		t.Fatalf("owner invitation must be tenant_admin/pending for the org: %+v", inv)
	}
	if inv.Email != "owner@acme.test" {
		t.Fatalf("invitation must target the owner email: %+v", inv.Email)
	}
	if rawToken == "" || len(rawToken) < 32 {
		t.Fatalf("one-time token must be generated: %q", rawToken)
	}
	if inv.ExpiresAt.Before(time.Now().UTC().Add(6 * 24 * time.Hour)) {
		t.Fatalf("owner invitation must carry ~7-day expiry: %v", inv.ExpiresAt)
	}
	// Only the hash is persisted — never the raw token.
	var storedHash string
	if err := db.QueryRow(`SELECT token_hash FROM org_invitations WHERE id=?`, inv.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == rawToken || len(storedHash) != 64 {
		t.Fatalf("token must be persisted as a 64-char hash, got %q", storedHash)
	}

	// Duplicate slug → ErrOrganizationExists, even with a different owner.
	if _, _, _, err := svc.CreateOrganizationWithOwner(ctx, CreateOrganizationRequest{Slug: "acme-psa"}, 0, 43, "other@acme.test"); err != ErrOrganizationExists {
		t.Fatalf("duplicate slug must be ErrOrganizationExists, got %v", err)
	}

	// Invalid owner email → rejected.
	if _, _, _, err := svc.CreateOrganizationWithOwner(ctx, CreateOrganizationRequest{Slug: "bad-owner"}, 0, 44, "not-an-email"); err == nil {
		t.Fatal("invalid owner email must be rejected")
	}

	// Atomicity: audit failure rolls back org AND invitation together.
	db.SetMaxOpenConns(1)
	svcAudit := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	if _, _, _, err := svcAudit.CreateOrganizationWithOwner(ctx, CreateOrganizationRequest{Slug: "rollback-psa", Name: "Rollback"}, 0, 45, "rb@acme.test"); err == nil {
		t.Fatal("audit failure must fail the owner creation transaction")
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE slug='rollback-psa'`).Scan(&count)
	if count != 0 {
		t.Fatal("tenant row committed without audit")
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM org_invitations WHERE email='rb@acme.test'`).Scan(&count)
	if count != 0 {
		t.Fatal("invitation row committed without audit")
	}
}
