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
		role auth.Role
		perm Permission
		want bool
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

		// PORTAL-SEPARATION-PHASE1: the deprecated RoleAdmin no longer
		// maps to any permission. Legacy "admin" rows are normalized at
		// startup to platform_super_admin or tenant_admin; any row that
		// still carries "admin" is AMBIGUOUS and must have zero
		// privilege until an operator intervenes.
		{auth.RoleAdmin, PermQueueRead, false},
		{auth.RoleAdmin, PermQueueAction, false},
		{auth.RoleAdmin, PermSettingsWrite, false},
		{auth.RoleAdmin, PermBackupsWrite, false},
		{auth.RoleAdmin, PermLicenseRead, false},
		{auth.RoleAdmin, PermLicenseWrite, false},
		{auth.RoleAdmin, PermAuditRead, false},

		// Operator: tenant-scoped helpdesk persona. PORTAL-SEPARATION-
		// PHASE1 Phase 5 (PR#58) stripped the platform bucket
		// (queue.*, settings.*, backups.*, license.*, monitoring.read)
		// from the legacy operator map — see rbac.go for the audit
		// rationale.
		{auth.RoleOperator, PermQueueRead, false},
		{auth.RoleOperator, PermQueueAction, false},
		{auth.RoleOperator, PermSettingsRead, false},
		{auth.RoleOperator, PermSettingsWrite, false},
		{auth.RoleOperator, PermBackupsRead, false},
		{auth.RoleOperator, PermBackupsWrite, false},
		{auth.RoleOperator, PermLicenseRead, false},
		{auth.RoleOperator, PermLicenseWrite, false},
		{auth.RoleOperator, PermMonitoringRead, false},
		{auth.RoleOperator, PermUsersRead, true},
		{auth.RoleOperator, PermUsersWrite, true},
		{auth.RoleOperator, PermAuditRead, true},
		{auth.RoleOperator, PermMailboxesWrite, true},

		// Platform Super Admin: PLATFORM permissions ONLY. PORTAL-
		// SEPARATION-PHASE1 Phase 5 (PR#58) stripped tenant-scoped
		// perms from the PSA map. Mail-Control enablement grants the
		// PSA the canonical domain/mailbox/alias/group permissions for
		// the platform-owned /platform/* surface (explicit target tenant
		// required per request); tenant-scoped ROUTE permissions
		// (users, invitations, tenant org write, ownership, api-keys,
		// billing, credentials, tenant sessions, tenant security,
		// dashboard) remain absent. PSA rows have NULL tenant_id and
		// cannot reach tenant-scoped routes; this map keeps the
		// boundary explicit.
		{auth.RolePlatformSuperAdmin, PermQueueRead, true},
		{auth.RolePlatformSuperAdmin, PermQueueAction, true},
		{auth.RolePlatformSuperAdmin, PermSettingsWrite, true},
		{auth.RolePlatformSuperAdmin, PermBackupsWrite, true},
		{auth.RolePlatformSuperAdmin, PermLicenseWrite, true},
		{auth.RolePlatformSuperAdmin, PermMonitoringRead, true},
		{auth.RolePlatformSuperAdmin, PermPlatformOrganizationsWrite, true},
		{auth.RolePlatformSuperAdmin, PermPlatformSecurityRead, true},
		{auth.RolePlatformSuperAdmin, PermPlatformSessionsRevoke, true},
		// Platform mail control (platform-owned /platform/* surface).
		{auth.RolePlatformSuperAdmin, PermDomainsRead, true},
		{auth.RolePlatformSuperAdmin, PermDomainsWrite, true},
		{auth.RolePlatformSuperAdmin, PermMailboxesRead, true},
		{auth.RolePlatformSuperAdmin, PermMailboxesWrite, true},
		{auth.RolePlatformSuperAdmin, PermAliasesRead, true},
		{auth.RolePlatformSuperAdmin, PermAliasesWrite, true},
		{auth.RolePlatformSuperAdmin, PermGroupsRead, true},
		{auth.RolePlatformSuperAdmin, PermGroupsWrite, true},
		// Tenant-scoped ROUTE permissions remain absent for the PSA.
		{auth.RolePlatformSuperAdmin, PermUsersWrite, false},
		{auth.RolePlatformSuperAdmin, PermInvitationsWrite, false},
		{auth.RolePlatformSuperAdmin, PermOrganizationsWrite, false},
		{auth.RolePlatformSuperAdmin, PermOwnershipTransfer, false},
		{auth.RolePlatformSuperAdmin, PermAPIKeysWrite, false},
		{auth.RolePlatformSuperAdmin, PermBillingWrite, false},
		{auth.RolePlatformSuperAdmin, PermCredentialsReset, false},
		{auth.RolePlatformSuperAdmin, PermSessionsRevoke, false},
		{auth.RolePlatformSuperAdmin, PermDashboardRead, false},
		{auth.RolePlatformSuperAdmin, PermSecurityRead, false},

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

		// RoleUser (tenant owner/member): full tenant-scoped access,
		// no platform privileges.
		{auth.RoleUser, PermDashboardRead, true},
		{auth.RoleUser, PermDomainsRead, true},
		{auth.RoleUser, PermDomainsWrite, true},
		{auth.RoleUser, PermMailboxesRead, true},
		{auth.RoleUser, PermMailboxesWrite, true},
		{auth.RoleUser, PermOrganizationsRead, true},
		{auth.RoleUser, PermOrganizationsWrite, true},
		{auth.RoleUser, PermUsersRead, true},
		{auth.RoleUser, PermUsersWrite, true},
		{auth.RoleUser, PermAliasesRead, true},
		{auth.RoleUser, PermAliasesWrite, true},
		{auth.RoleUser, PermGroupsWrite, true},
		{auth.RoleUser, PermInvitationsWrite, true},
		{auth.RoleUser, PermOwnershipTransfer, true},
		{auth.RoleUser, PermAPIKeysWrite, true},
		{auth.RoleUser, PermBillingWrite, true},
		{auth.RoleUser, PermAuditRead, true},
		{auth.RoleUser, PermSettingsRead, true},
		{auth.RoleUser, PermCredentialsReset, true},
		{auth.RoleUser, PermSessionsRevoke, true},
		// Platform permissions denied.
		{auth.RoleUser, PermPlatformOrganizationsRead, false},
		{auth.RoleUser, PermPlatformOrganizationsWrite, false},
		{auth.RoleUser, PermPlatformSecurityRead, false},
		{auth.RoleUser, PermPlatformSessionsRevoke, false},
		// License/admin-only denied.
		{auth.RoleUser, PermLicenseRead, false},
		{auth.RoleUser, PermLicenseWrite, false},
		{auth.RoleUser, PermBackupsWrite, false},
		{auth.RoleUser, PermQueueRead, false},
		{auth.RoleUser, PermQueueAction, false},

		// RoleBilling: read access + billing write. No other mutations.
		{auth.RoleBilling, PermDashboardRead, true},
		{auth.RoleBilling, PermDomainsRead, true},
		{auth.RoleBilling, PermDomainsWrite, false},
		{auth.RoleBilling, PermMailboxesRead, true},
		{auth.RoleBilling, PermMailboxesWrite, false},
		{auth.RoleBilling, PermOrganizationsWrite, false},
		{auth.RoleBilling, PermUsersWrite, false},
		{auth.RoleBilling, PermAliasesWrite, false},
		{auth.RoleBilling, PermGroupsWrite, false},
		{auth.RoleBilling, PermInvitationsWrite, false},
		{auth.RoleBilling, PermOwnershipTransfer, false},
		{auth.RoleBilling, PermAPIKeysWrite, false},
		{auth.RoleBilling, PermBillingWrite, true},

		// Unknown roles: deny by default.
		{auth.Role("unknown"), PermQueueRead, false},
		{auth.Role(""), PermQueueRead, false},
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
	// PORTAL-SEPARATION-PHASE1 Phase 5 (PR#58): legacy RoleOperator
	// no longer holds PermQueueRead (see rbac.go rationale — the
	// platform bucket was stripped from the tenant-scoped operator
	// map to close the "accidental privilege bridge" the audit
	// flagged). RoleReadOnly remains the read-side witness here.
	if !HasPermission(auth.RoleReadOnly, PermQueueRead) {
		t.Errorf("role readonly should have queue.read")
	}
	if HasPermission(auth.RoleReadOnly, PermQueueAction) {
		t.Errorf("readonly must NOT have queue.action")
	}
	if HasPermission(auth.RoleOperator, PermQueueRead) {
		t.Errorf("PHASE5: legacy operator must NOT have queue.read (platform-scoped)")
	}
	if HasPermission(auth.RoleOperator, PermQueueAction) {
		t.Errorf("PHASE5: legacy operator must NOT have queue.action (platform-scoped)")
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
	// PORTAL-SEPARATION-PHASE1: use RoleSuperAdmin instead of the
	// deprecated RoleAdmin (which now has zero permissions).
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals("role", auth.RoleSuperAdmin)
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
		t.Errorf("super_admin should pass queue.read+queue.action, got %d", resp.StatusCode)
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
	allRoles := []auth.Role{
		auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleOperator,
		auth.RoleReadOnly, auth.RoleUser, auth.RoleBilling,
	}
	for _, p := range AllPermissions {
		granted := false
		for _, role := range allRoles {
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

func TestPlatformMailControlPermissionsBoundary(t *testing.T) {
	// Platform mail control permissions are PLATFORM-scoped: the
	// platform super admin (and the legacy super-admin alias) hold
	// them; no tenant role inherits any of them.
	platformPerms := []Permission{
		PermRelaysRead, PermRelaysWrite, PermRelaysTest,
		PermSuppressionsRead, PermSuppressionsWrite,
		PermDeliverabilityRead,
	}
	for _, p := range platformPerms {
		if !HasPermission(auth.RolePlatformSuperAdmin, p) {
			t.Errorf("platform_super_admin must hold %q", p)
		}
		if !HasPermission(auth.RoleSuperAdmin, p) {
			t.Errorf("legacy super_admin must hold %q", p)
		}
		for _, role := range []auth.Role{
			auth.RoleTenantAdmin, auth.RoleTenantOperator,
			auth.RoleTenantSupport, auth.RoleTenantReadOnly,
			auth.RoleUser, auth.RoleBilling, auth.RoleReadOnly,
		} {
			if HasPermission(role, p) {
				t.Errorf("tenant role %s must NOT hold platform permission %q", role, p)
			}
		}
	}
	// Queue attribution reads reuse the pre-existing queue.read /
	// queue.action permissions; the PSA must hold them.
	if !HasPermission(auth.RolePlatformSuperAdmin, PermQueueRead) || !HasPermission(auth.RolePlatformSuperAdmin, PermQueueAction) {
		t.Error("platform_super_admin must hold queue.read and queue.action")
	}
}
