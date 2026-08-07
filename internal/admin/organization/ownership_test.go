package organization

import (
	"context"
	"testing"
	"time"
)

func TestOwnershipTransferBumpsBothVersionsAndRevokesBothTokens(t *testing.T) {
	db := newOrganizationTestDB(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()

	// Create tenant/organization
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)

	// Create FROM user (current owner), TO user, and a third admin so
	// the ownership transfer's last-owner check passes.
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (10, 1, 'tenant_admin', 1, 50)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (20, 1, 'tenant_support', 1, 70)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (30, 1, 'tenant_admin', 1, 99)`)
	_ = 30

	// Request ownership transfer
	svc := NewService(repo, nil, nil)
	transfer, rawToken, err := svc.RequestOwnershipTransfer(ctx, 1, 10, 20)
	if err != nil {
		t.Fatalf("RequestOwnershipTransfer: %v", err)
	}
	t.Logf("transfer id=%d status=%s", transfer.ID, transfer.Status)

	// Verify the transfer row exists
	var count int
	db.QueryRow("SELECT COUNT(*) FROM org_ownership_transfers WHERE id = ?", transfer.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("transfer row not found in DB: count=%d", count)
	}

	// Accept the transfer
	if err := repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC()); err != nil {
		t.Fatalf("AcceptOwnershipTransfer: %v", err)
	}

	// Verify FROM user
	var fromRole string
	var fromTV int64
	db.QueryRow("SELECT role, token_version FROM users WHERE id = 10").Scan(&fromRole, &fromTV)
	if fromRole != "tenant_admin" {
		t.Fatalf("FROM role: want tenant_admin, got %s", fromRole)
	}
	if fromTV != 51 {
		t.Fatalf("FROM token_version: want 51, got %d", fromTV)
	}

	// Verify TO user
	var toRole string
	var toTV int64
	db.QueryRow("SELECT role, token_version FROM users WHERE id = 20").Scan(&toRole, &toTV)
	if toRole != "tenant_admin" {
		t.Fatalf("TO role: want tenant_admin, got %s", toRole)
	}
	if toTV != 71 {
		t.Fatalf("TO token_version: want 71, got %d", toTV)
	}

	// Verify transfer record
	var status TransferStatus
	db.QueryRow("SELECT status FROM org_ownership_transfers WHERE id = ?", transfer.ID).Scan(&status)
	if status != TransferAccepted {
		t.Fatalf("transfer status: want %v, got %v", TransferAccepted, status)
	}

	_ = rawToken
}

func TestUpdateMemberRoleAllowedCanonicalRoles(t *testing.T) {
	roles := []string{"tenant_admin", "tenant_operator", "tenant_support", "tenant_readonly"}
	for _, r := range roles {
		t.Run(r, func(t *testing.T) {
			db := newOrganizationTestDB(t)
			svc := NewService(NewOrganizationRepo(db), nil, nil)
			now := time.Now().UTC()
			db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
			db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (1, 1, 'tenant_support', 1, 100)`)
			if err := svc.UpdateMemberRole(context.Background(), 1, 1, r); err != nil {
				t.Fatalf("UpdateMemberRole(%s): %v", r, err)
			}
			var storedRole string
			var tv int64
			db.QueryRow("SELECT role, token_version FROM users WHERE id = 1").Scan(&storedRole, &tv)
			if storedRole != r {
				t.Fatalf("stored role: want %s, got %s", r, storedRole)
			}
			if tv != 101 {
				t.Fatalf("token_version: want 101, got %d", tv)
			}
		})
	}
}

func TestUpdateMemberRoleRejectsForbiddenRolesWithoutMutation(t *testing.T) {
	forbidden := []string{"platform_super_admin", "superadmin", "super_admin", "super-admin", "admin", "operator", "readonly", "user", "billing", "nonexistent_role", ""}
	for _, r := range forbidden {
		t.Run(r, func(t *testing.T) {
			db := newOrganizationTestDB(t)
			svc := NewService(NewOrganizationRepo(db), nil, nil)
			now := time.Now().UTC()
			db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
			db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (1, 1, 'tenant_support', 1, 200)`)
			err := svc.UpdateMemberRole(context.Background(), 1, 1, r)
			if err == nil {
				t.Fatalf("UpdateMemberRole(%s): expected error, got nil", r)
			}
			var storedRole string
			var tv int64
			db.QueryRow("SELECT role, token_version FROM users WHERE id = 1").Scan(&storedRole, &tv)
			if storedRole != "tenant_support" {
				t.Fatalf("stored role mutated: want tenant_support, got %s", storedRole)
			}
			if tv != 200 {
				t.Fatalf("token_version mutated: want 200, got %d", tv)
			}
		})
	}
}
