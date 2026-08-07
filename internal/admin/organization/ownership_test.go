package organization

import (
	"context"
	"database/sql"
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

// SetMaxOpenConns to 1 prevents SQLite :memory: connection issues in concurrent tests.
func newOrganizationTestDBSerialized(t *testing.T) *sql.DB {
	db := newOrganizationTestDB(t)
	db.SetMaxOpenConns(1)
	return db
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

func TestCreateOwnershipTransferReturnsPersistedIDSQLite(t *testing.T) {
	db := newOrganizationTestDB(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	tf := &OwnershipTransfer{OrganizationID: 1, FromUserID: 10, ToUserID: 20, TokenHash: "hash123", Status: TransferPending, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := repo.CreateOwnershipTransfer(ctx, tf); err != nil {
		t.Fatalf("CreateOwnershipTransfer: %v", err)
	}
	if tf.ID == 0 {
		t.Fatal("returned ID must be > 0")
	}
	var foundID uint
	db.QueryRow("SELECT id FROM org_ownership_transfers WHERE id = ?", tf.ID).Scan(&foundID)
	if foundID != tf.ID {
		t.Fatalf("query by returned ID: want %d, got %d", tf.ID, foundID)
	}
}

func TestCountAdminsTenantScoped(t *testing.T) {
	db := newOrganizationTestDB(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (2, 'b', 'b', 'b.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	// Tenant A: 2 active tenant_admin
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (1, 1, 'tenant_admin', 1, 0)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (2, 1, 'tenant_admin', 1, 0)`)
	// Tenant A: 1 inactive tenant_admin (should not count)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (3, 1, 'tenant_admin', 0, 0)`)
	// Tenant A: 1 deleted tenant_admin (should not count)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, deleted_at, token_version) VALUES (4, 1, 'tenant_admin', 1, datetime('now'), 0)`)
	// Tenant B: 3 active tenant_admin
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (5, 2, 'tenant_admin', 1, 0)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (6, 2, 'tenant_admin', 1, 0)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (7, 2, 'tenant_admin', 1, 0)`)
	// PSA with NULL tenant — should not count for any tenant
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (8, NULL, 'platform_super_admin', 1, 0)`)

	cA, _ := repo.CountAdmins(ctx, 1)
	if cA != 2 {
		t.Fatalf("tenant A: want 2, got %d", cA)
	}
	cB, _ := repo.CountAdmins(ctx, 2)
	if cB != 3 {
		t.Fatalf("tenant B: want 3, got %d", cB)
	}
}

func TestOwnershipTransferConcurrentAcceptExactlyOneSucceeds(t *testing.T) {
	db := newOrganizationTestDB(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (10, 1, 'tenant_admin', 1, 50)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (20, 1, 'tenant_support', 1, 70)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (30, 1, 'tenant_admin', 1, 99)`)

	svc := NewService(repo, nil, nil)
	transfer, _, err := svc.RequestOwnershipTransfer(ctx, 1, 10, 20)
	if err != nil {
		t.Fatalf("RequestOwnershipTransfer: %v", err)
	}

	results := make(chan error, 2)
	go func() { results <- repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC()) }()
	go func() { results <- repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC()) }()
	e1 := <-results
	e2 := <-results

	if e1 == nil && e2 == nil {
		t.Fatal("both concurrent accepts succeeded — atomicity broken")
	}
	if e1 != nil && e2 != nil {
		t.Fatalf("both concurrent accepts failed: %v / %v", e1, e2)
	}

	// Verify exactly one bump each.
	var fromTV, toTV int64
	db.QueryRow("SELECT token_version FROM users WHERE id = 10").Scan(&fromTV)
	db.QueryRow("SELECT token_version FROM users WHERE id = 20").Scan(&toTV)
	if fromTV != 51 {
		t.Fatalf("FROM token_version: want 51, got %d", fromTV)
	}
	if toTV != 71 {
		t.Fatalf("TO token_version: want 71, got %d", toTV)
	}
	var accCount int
	db.QueryRow("SELECT COUNT(*) FROM org_ownership_transfers WHERE status = ?", TransferAccepted).Scan(&accCount)
	if accCount != 1 {
		t.Fatalf("accepted transfer count: want 1, got %d", accCount)
	}
}

func TestOwnershipTransferRollbackPreservesUsersAndTransfer(t *testing.T) {
	db := newOrganizationTestDB(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (10, 1, 'tenant_admin', 1, 50)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (20, 1, 'tenant_support', 1, 70)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (30, 1, 'tenant_admin', 1, 99)`)

	svc := NewService(repo, nil, nil)
	transfer, _, err := svc.RequestOwnershipTransfer(ctx, 1, 10, 20)
	if err != nil {
		t.Fatalf("RequestOwnershipTransfer: %v", err)
	}
	// Accept transfer — must succeed.
	if err := repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC()); err != nil {
		t.Fatalf("AcceptOwnershipTransfer: %v", err)
	}

	// Second accept must fail with ErrTransferAlreadyUsed.
	err2 := repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC())
	if err2 == nil || err2 != ErrTransferAlreadyUsed {
		t.Fatalf("second accept: want ErrTransferAlreadyUsed, got %v", err2)
	}

	// Users must not be changed a second time.
	var fromTV, toTV int64
	db.QueryRow("SELECT token_version FROM users WHERE id = 10").Scan(&fromTV)
	db.QueryRow("SELECT token_version FROM users WHERE id = 20").Scan(&toTV)
	if fromTV != 51 {
		t.Fatalf("FROM token_version: want 51, got %d", fromTV)
	}
	if toTV != 71 {
		t.Fatalf("TO token_version: want 71, got %d", toTV)
	}
}
