package rbac

import (
	"context"
	"database/sql"
	"testing"

	"github.com/orvix/orvix/internal/auth"
	authrbac "github.com/orvix/orvix/internal/auth/rbac"
	_ "modernc.org/sqlite"
)

func TestEvaluatorPermissionMatrixAndTenantScope(t *testing.T) {
	e := NewEvaluator(nil)
	ctx := context.Background()

	if !e.HasPermission(ctx, auth.RoleSuperAdmin, 1, authrbac.PermDomainsWrite) {
		t.Fatalf("superadmin should retain explicit platform domain write permission")
	}
	if e.HasPermission(ctx, auth.RoleReadOnly, 2, authrbac.PermDomainsWrite) {
		t.Fatalf("read-only auditor must not mutate domains")
	}
	// RoleUser is the per-mailbox webmail end-user role. It has NO
	// tenant administration privileges in the global matrix — a plain
	// mailbox user must not manage domains, mailboxes, members, or
	// billing. Signup-created Organization owners are persisted as
	// tenant_admin (customer_auth.go / customer_signup_otp.go).
	if e.HasPermission(ctx, auth.RoleUser, 3, authrbac.PermMailboxesWrite) {
		t.Fatalf("webmail end-user (RoleUser) must NOT have mailbox write permission")
	}
	if e.HasPermission(ctx, auth.RoleUser, 3, authrbac.PermDomainsWrite) {
		t.Fatalf("webmail end-user (RoleUser) must NOT have domain write permission")
	}
	// TenantAdmin keeps the full tenant administration surface.
	if !e.HasPermission(ctx, auth.RoleTenantAdmin, 3, authrbac.PermMailboxesWrite) {
		t.Fatalf("tenant_admin must have mailbox write permission")
	}
	// Platform permissions are still denied for tenant roles.
	if e.HasPermission(ctx, auth.RoleUser, 3, authrbac.PermPlatformOrganizationsWrite) {
		t.Fatalf("tenant role must not have platform organization write")
	}
	if e.HasPermission(ctx, auth.RoleTenantAdmin, 3, authrbac.PermPlatformOrganizationsWrite) {
		t.Fatalf("tenant_admin must not have platform organization write")
	}
	if e.CanManageTenant(auth.RoleAdmin, 0, 1) {
		t.Fatalf("missing actor tenant must fail closed")
	}
	if !e.CanManageTenant(auth.RoleAdmin, 7, 7) {
		t.Fatalf("tenant admin should manage own tenant")
	}
	if e.CanManageTenant(auth.RoleAdmin, 7, 8) {
		t.Fatalf("tenant admin must not manage another tenant")
	}
	if !e.CanManageTenant(auth.RoleSuperAdmin, 0, 8) {
		t.Fatalf("platform superadmin should manage tenants explicitly")
	}
}

func TestEvaluatorDBBackedGroupGrant(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE coremail_admin_groups (id INTEGER PRIMARY KEY, grants TEXT, deleted_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE coremail_admin_group_members (group_id INTEGER, user_id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO coremail_admin_groups (id, grants) VALUES (1, 'domains.read, queue.action')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO coremail_admin_group_members (group_id, user_id) VALUES (1, 42)`); err != nil {
		t.Fatal(err)
	}

	e := NewEvaluator(db)
	// RoleUser no longer holds PermQueueAction from the global matrix —
	// the DB-backed group grant is what authorizes the assigned user, so
	// this still proves grants extend permissions beyond the global
	// matrix, while unassigned users do not inherit them.
	if !e.HasPermission(context.Background(), auth.RoleUser, 42, authrbac.PermQueueAction) {
		t.Fatalf("DB-backed group grant should authorize assigned permission via group")
	}
	if e.HasPermission(context.Background(), auth.RoleUser, 99, authrbac.PermQueueAction) {
		t.Fatalf("unassigned user must not inherit another user's grant")
	}
	// Verify that the global matrix still denies for permissions
	// without DB grant.
	if e.HasPermission(context.Background(), auth.RoleUser, 42, authrbac.PermPlatformOrganizationsWrite) {
		t.Fatalf("tenant owner must not gain platform org write via group grant")
	}
}
