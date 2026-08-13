package organization

import (
	"context"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
)

func TestRemoveMember_LastActiveAdminIsRefused(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, 'tenant_admin', 1, 0, ?, ?, 'admin@test.local', 'h', 1)`, now, now)

	err := svc.RemoveMember(ctx, 1, 1)
	if err != ErrLastActiveAdmin {
		t.Fatalf("expected ErrLastActiveAdmin, got %v", err)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 1`).Scan(&count)
	if count != 1 {
		t.Fatal("the last admin must not have been removed")
	}
}

func TestRemoveMember_SecondAdminCanBeRemoved(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, 'tenant_admin', 1, 0, ?, ?, 'admin1@test.local', 'h', 1)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (2, 1, 'tenant_admin', 1, 0, ?, ?, 'admin2@test.local', 'h', 1)`, now, now)

	if err := svc.RemoveMember(ctx, 2, 1); err != nil {
		t.Fatalf("expected removal of the second admin to succeed, got %v", err)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 2`).Scan(&count)
	if count != 0 {
		t.Fatal("expected the second admin to be removed")
	}
}

func TestRemoveMember_NonAdminMemberIsUnaffectedByLockoutGuard(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, 'tenant_admin', 1, 0, ?, ?, 'admin@test.local', 'h', 1)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (2, 1, 'tenant_readonly', 1, 0, ?, ?, 'ro@test.local', 'h', 1)`, now, now)

	if err := svc.RemoveMember(ctx, 2, 1); err != nil {
		t.Fatalf("expected removal of a non-admin member to succeed, got %v", err)
	}
}

func TestRemoveMember_NotFoundIsReported(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)

	err := svc.RemoveMember(ctx, 999, 1)
	if err != ErrMemberNotFound {
		t.Fatalf("expected ErrMemberNotFound, got %v", err)
	}
}

func TestSetMemberActive_SuspendLastAdminIsRefused(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, 'tenant_admin', 1, 0, ?, ?, 'admin@test.local', 'h', 1)`, now, now)

	err := svc.SetMemberActive(ctx, 1, 1, false)
	if err != ErrLastActiveAdmin {
		t.Fatalf("expected ErrLastActiveAdmin, got %v", err)
	}
}

func TestSetMemberActive_SuspendBumpsTokenVersionImmediately(t *testing.T) {
	db, authr := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, 'tenant_admin', 1, 0, ?, ?, 'admin1@test.local', 'h', 1)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (2, 1, 'tenant_operator', 1, 42, ?, ?, 'op@test.local', 'h', 1)`, now, now)

	oldAccess, _, _, err := authr.GenerateAccessTokenWithJTI(2, auth.Role("tenant_operator"))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if _, _, err := authr.ValidateAccessToken(oldAccess); err != nil {
		t.Fatalf("expected the pre-suspend token to validate, got %v", err)
	}

	if err := svc.SetMemberActive(ctx, 2, 1, false); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	var activeVal, tv int
	db.QueryRow(`SELECT active, token_version FROM users WHERE id = 2`).Scan(&activeVal, &tv)
	if activeVal != 0 {
		t.Fatal("expected the member to be inactive after suspend")
	}
	if tv != 43 {
		t.Fatalf("expected token_version bumped to 43, got %d", tv)
	}
	if _, _, err := authr.ValidateAccessToken(oldAccess); err == nil {
		t.Fatal("expected the pre-suspend token to be rejected immediately after suspend")
	}
}

func TestSetMemberActive_ReactivateDoesNotBumpTokenVersion(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, 'tenant_admin', 1, 0, ?, ?, 'admin@test.local', 'h', 1)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (2, 1, 'tenant_operator', 0, 10, ?, ?, 'op@test.local', 'h', 1)`, now, now)

	if err := svc.SetMemberActive(ctx, 2, 1, true); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	var activeVal, tv int
	db.QueryRow(`SELECT active, token_version FROM users WHERE id = 2`).Scan(&activeVal, &tv)
	if activeVal != 1 {
		t.Fatal("expected the member to be active after reactivate")
	}
	if tv != 10 {
		t.Fatalf("expected token_version unchanged at 10, got %d", tv)
	}
}

func TestUpdateMemberRole_DemotingLastAdminIsRefused(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, 'tenant_admin', 1, 0, ?, ?, 'admin@test.local', 'h', 1)`, now, now)

	err := svc.UpdateMemberRole(ctx, 1, 1, "tenant_operator")
	if err != ErrLastActiveAdmin {
		t.Fatalf("expected ErrLastActiveAdmin, got %v", err)
	}
	var role string
	db.QueryRow(`SELECT role FROM users WHERE id = 1`).Scan(&role)
	if role != "tenant_admin" {
		t.Fatal("expected the last admin's role to remain unchanged")
	}
}

func TestUpdateMemberRole_NotFoundIsReported(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)

	err := svc.UpdateMemberRole(ctx, 999, 1, "tenant_operator")
	if err != ErrMemberNotFound {
		t.Fatalf("expected ErrMemberNotFound, got %v", err)
	}
}
