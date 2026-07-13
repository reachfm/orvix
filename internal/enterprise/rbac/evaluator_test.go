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
	if e.HasPermission(ctx, auth.RoleUser, 3, authrbac.PermMailboxesWrite) {
		t.Fatalf("end user must not gain mailbox write permission")
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
	if _, err := db.Exec(`INSERT INTO coremail_admin_groups (id, grants) VALUES (1, 'domains.read, mailboxes.write')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO coremail_admin_group_members (group_id, user_id) VALUES (1, 42)`); err != nil {
		t.Fatal(err)
	}

	e := NewEvaluator(db)
	if !e.HasPermission(context.Background(), auth.RoleUser, 42, authrbac.PermMailboxesWrite) {
		t.Fatalf("DB-backed group grant should authorize assigned permission")
	}
	if e.HasPermission(context.Background(), auth.RoleUser, 99, authrbac.PermMailboxesWrite) {
		t.Fatalf("unassigned user must not inherit another user's grant")
	}
}
