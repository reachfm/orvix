package rbac

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
)

func TestHasPermission_Matrix(t *testing.T) {
	// The matrix below is the single source of truth for what
	// each role can do. A change to rolePermissions in rbac.go
	// must be reflected here, and the test must be updated
	// alongside it.
	cases := []struct {
		role    auth.Role
		perm    Permission
		want    bool
	}{
		// Super admin: every permission.
		{auth.RoleSuperAdmin, PermQueueRead, true},
		{auth.RoleSuperAdmin, PermQueueAction, true},
		{auth.RoleSuperAdmin, PermSettingsRead, true},
		{auth.RoleSuperAdmin, PermSettingsWrite, true},
		{auth.RoleSuperAdmin, PermBackupsRead, true},
		{auth.RoleSuperAdmin, PermBackupsWrite, true},
		{auth.RoleSuperAdmin, PermMonitoringRead, true},
		{auth.RoleSuperAdmin, PermLicenseRead, true},
		{auth.RoleSuperAdmin, PermLicenseWrite, true},
		{auth.RoleSuperAdmin, PermUsersRead, true},
		{auth.RoleSuperAdmin, PermUsersWrite, true},
		{auth.RoleSuperAdmin, PermAuditRead, true},

		// Admin: everything except license.write.
		{auth.RoleAdmin, PermQueueRead, true},
		{auth.RoleAdmin, PermQueueAction, true},
		{auth.RoleAdmin, PermSettingsWrite, true},
		{auth.RoleAdmin, PermBackupsWrite, true},
		{auth.RoleAdmin, PermLicenseRead, true},
		{auth.RoleAdmin, PermLicenseWrite, false}, // reserved for super_admin
		{auth.RoleAdmin, PermAuditRead, true},

		// Operator: read everything, action on queue + users.
		{auth.RoleOperator, PermQueueRead, true},
		{auth.RoleOperator, PermQueueAction, true},
		{auth.RoleOperator, PermSettingsRead, true},
		{auth.RoleOperator, PermSettingsWrite, false},
		{auth.RoleOperator, PermBackupsRead, true},
		{auth.RoleOperator, PermBackupsWrite, false},
		{auth.RoleOperator, PermLicenseRead, true},
		{auth.RoleOperator, PermLicenseWrite, false},
		{auth.RoleOperator, PermUsersRead, true},
		{auth.RoleOperator, PermUsersWrite, true},
		{auth.RoleOperator, PermAuditRead, true},

		// ReadOnly: read-only on every resource.
		{auth.RoleReadOnly, PermQueueRead, true},
		{auth.RoleReadOnly, PermQueueAction, false},
		{auth.RoleReadOnly, PermSettingsRead, true},
		{auth.RoleReadOnly, PermSettingsWrite, false},
		{auth.RoleReadOnly, PermBackupsRead, true},
		{auth.RoleReadOnly, PermBackupsWrite, false},
		{auth.RoleReadOnly, PermLicenseRead, true},
		{auth.RoleReadOnly, PermLicenseWrite, false},
		{auth.RoleReadOnly, PermUsersRead, true},
		{auth.RoleReadOnly, PermUsersWrite, false},
		{auth.RoleReadOnly, PermAuditRead, true},

		// Unknown roles: deny by default.
		{auth.Role("unknown"), PermQueueRead, false},
		{auth.Role(""), PermQueueRead, false},
		// The legacy user role is not an admin role: it gets
		// nothing.
		{auth.RoleUser, PermQueueRead, false},
		{auth.RoleUser, PermSettingsRead, false},
	}
	for _, c := range cases {
		got := HasPermission(c.role, c.perm)
		if got != c.want {
			t.Errorf("HasPermission(%q, %q) = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
}

func TestQueueReadDoesNotImplyQueueAction(t *testing.T) {
	// Critical safety property: a read-only role that can
	// read the queue MUST NOT be able to retry / cancel /
	// bounce a queue entry. This is exactly the regression that
	// would happen if permissions were ever collapsed into a
	// single "queue" permission.
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleOperator} {
		if !HasPermission(role, PermQueueRead) {
			t.Errorf("role %q should have queue.read", role)
		}
	}
	if HasPermission(auth.RoleReadOnly, PermQueueAction) {
		t.Errorf("readonly must NOT have queue.action")
	}
}

func TestRolePermissionList_ExcludesDeniedPerms(t *testing.T) {
	list := RolePermissionList(auth.RoleReadOnly)
	for _, p := range list {
		if !HasPermission(auth.RoleReadOnly, p) {
			t.Errorf("RolePermissionList returned %q but HasPermission is false", p)
		}
	}
	if len(list) == 0 {
		t.Errorf("readonly should have at least one read permission")
	}
	// And verify the negative: nothing in the list should be a
	// write permission.
	for _, p := range list {
		switch p {
		case PermQueueAction, PermSettingsWrite, PermBackupsWrite,
			PermLicenseWrite, PermUsersWrite:
			t.Errorf("readonly RolePermissionList unexpectedly includes %q", p)
		}
	}
}

func TestRolePermissionList_SuperAdminIsAll(t *testing.T) {
	list := RolePermissionList(auth.RoleSuperAdmin)
	if len(list) != len(AllPermissions) {
		t.Errorf("super_admin list = %d, want %d (all)", len(list), len(AllPermissions))
	}
}

func TestRequire_AllPermsPresent(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals("role", auth.RoleAdmin)
		return c.Next()
	}, Require(PermQueueRead, PermQueueAction), func(c fiber.Ctx) error {
		return c.SendStatus(200)
	})
	req := httptest.NewRequest("GET", "/x", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("admin should pass queue.read+queue.action, got %d", resp.StatusCode)
	}
}

func TestRequire_MissingPermForbidden(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals("role", auth.RoleReadOnly)
		return c.Next()
	}, Require(PermQueueAction))
	req := httptest.NewRequest("GET", "/x", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("readonly should be denied queue.action, got %d", resp.StatusCode)
	}
	// Verify the body lists the missing permission so operators
	// can debug.
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	missing, _ := body["missing"].([]interface{})
	if len(missing) == 0 {
		t.Errorf("response must list missing permissions, got %+v", body)
	}
}

func TestRequireAny_AnyPermSuffices(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals("role", auth.RoleReadOnly)
		return c.Next()
	}, RequireAny(PermQueueAction, PermQueueRead), func(c fiber.Ctx) error {
		return c.SendStatus(200)
	})
	req := httptest.NewRequest("GET", "/x", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("readonly with queue.read should pass, got %d", resp.StatusCode)
	}
}

func TestRequireAny_NoneForbidden(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals("role", auth.RoleReadOnly)
		return c.Next()
	}, RequireAny(PermSettingsWrite, PermLicenseWrite))
	req := httptest.NewRequest("GET", "/x", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("readonly with no relevant perms should be denied, got %d", resp.StatusCode)
	}
}

func TestRequire_NoRoleForbidden(t *testing.T) {
	app := fiber.New()
	app.Get("/x", Require(PermQueueRead))
	req := httptest.NewRequest("GET", "/x", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("missing role should be 403, got %d", resp.StatusCode)
	}
}

func TestAllPermissions_NoDuplicates(t *testing.T) {
	seen := make(map[Permission]bool, len(AllPermissions))
	for _, p := range AllPermissions {
		if seen[p] {
			t.Errorf("duplicate permission %q in AllPermissions", p)
		}
		seen[p] = true
	}
}

func TestAllPermissions_AllInRoleMap(t *testing.T) {
	// Every permission listed in AllPermissions must appear in
	// the rolePermission map for at least one role. A
	// permission that is never granted is dead code.
	for _, p := range AllPermissions {
		granted := false
		for _, role := range []auth.Role{
			auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleOperator, auth.RoleReadOnly,
		} {
			if HasPermission(role, p) {
				granted = true
				break
			}
		}
		if !granted {
			t.Errorf("permission %q is not granted to any built-in role", p)
		}
	}
}
