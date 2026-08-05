package handlers

// Matrix tests documenting the CURRENT RBAC map state (pre-PR#58) and
// asserting the explicit canonical-role gates delivered by PR #60
// close the RoleAdmin escalation independent of the map.
//
// See admin_queue.go queueAdminGate for the rationale on why we use
// explicit canonical checks instead of authrbac.HasPermission for
// platform-scoped gates on this base.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	authrbac "github.com/orvix/orvix/internal/auth/rbac"
)

// TestPR60_RBACMatrixMatchesExpectation documents the RBAC map state
// on this base. If this test starts failing on the RoleAdmin=true or
// RoleOperator=true cases after PR #58 merges, update the
// DOCUMENTED-CURRENT expectations to false — that IS the RBAC-map
// closure landing, and the test correctly reports the change.
func TestPR60_RBACMatrixMatchesExpectation(t *testing.T) {
	cases := []struct {
		role      auth.Role
		perm      authrbac.Permission
		wantAllow bool
		note      string
	}{
		// Canonical platform super admin — always allowed for platform perms.
		{auth.RolePlatformSuperAdmin, authrbac.PermQueueAction, true, "canonical platform role must have queue"},
		{auth.RolePlatformSuperAdmin, authrbac.PermBackupsWrite, true, "canonical platform role must run backups"},

		// Legacy super admin — documented migration-window alias.
		{auth.RoleSuperAdmin, authrbac.PermQueueAction, true, "legacy alias must still allow during migration"},

		// Tenant admin — must NEVER have platform perms.
		{auth.RoleTenantAdmin, authrbac.PermQueueAction, false, "tenant admin must not touch platform queue"},
		{auth.RoleTenantAdmin, authrbac.PermBackupsWrite, false, "tenant admin must not run backups"},

		// DOCUMENTED-CURRENT: pre-PR#58 RBAC map still grants broad
		// perms to the deprecated RoleAdmin. This is exactly WHY the
		// PR #60 gates use explicit canonical role checks and NOT
		// authrbac.HasPermission on this base. When PR #58 merges,
		// flip these expectations to false.
		{auth.RoleAdmin, authrbac.PermQueueAction, true, "DOCUMENTED-CURRENT: RBAC map still grants; PR #58 empties it"},
		{auth.RoleAdmin, authrbac.PermBackupsWrite, true, "DOCUMENTED-CURRENT: same — flip to false after PR #58"},

		// DOCUMENTED-CURRENT: helpdesk-shaped RoleOperator also gets
		// PermQueueAction on this base — surprising, and also why the
		// explicit gate does not use HasPermission here.
		{auth.RoleOperator, authrbac.PermQueueAction, true, "DOCUMENTED-CURRENT: operator granted queue by legacy map"},

		// Fail-closed cases.
		{auth.Role("unknown-role-xyz"), authrbac.PermQueueAction, false, "unknown must fail closed"},
		{auth.Role(""), authrbac.PermQueueAction, false, "empty must fail closed"},
	}
	for _, c := range cases {
		got := authrbac.HasPermission(c.role, c.perm)
		if got != c.wantAllow {
			t.Errorf("HasPermission(%q, %q) = %v, want %v — %s",
				c.role, c.perm, got, c.wantAllow, c.note)
		}
	}
}

// TestPR60_QueueAdminGateExplicitlyDeniesDeprecatedAdmin is the real
// security assertion: even though the RBAC map on this base still
// grants PermQueueAction to RoleAdmin and RoleOperator (see the
// matrix test above), the explicit canonical-role gate DENIES them.
// This is the actual defense-in-depth delivered by PR #60.
func TestPR60_QueueAdminGateExplicitlyDeniesDeprecatedAdmin(t *testing.T) {
	// Sanity: the RBAC map still says RoleAdmin is allowed. If this
	// premise ever changes, the test still passes (gate remains
	// correct) but the docstring above becomes stale.
	if !authrbac.HasPermission(auth.RoleAdmin, authrbac.PermQueueAction) {
		t.Log("note: RBAC map no longer grants PermQueueAction to RoleAdmin; " +
			"the explicit gate is now belt-and-suspenders")
	}
	if !authrbac.HasPermission(auth.RoleOperator, authrbac.PermQueueAction) {
		t.Log("note: RBAC map no longer grants PermQueueAction to RoleOperator")
	}

	deniedRoles := []auth.Role{
		auth.RoleAdmin,       // deprecated ambiguous admin — MUST be denied by the gate
		auth.RoleOperator,    // helpdesk persona — MUST NOT reach platform queue
		auth.RoleTenantAdmin, // tenant scope only
		auth.RoleTenantReadOnly,
		auth.RoleUser,
		auth.RoleBilling,
		auth.Role("unknown"),
		auth.Role(""),
	}
	for _, r := range deniedRoles {
		if runQueueGateExplicit(t, r) {
			t.Errorf("queueAdminGate MUST deny role %q (RBAC map may grant it, "+
				"but the explicit canonical gate must not)", r)
		}
	}

	allowedRoles := []auth.Role{
		auth.RolePlatformSuperAdmin,
		auth.RoleSuperAdmin, // migration-window alias
	}
	for _, r := range allowedRoles {
		if !runQueueGateExplicit(t, r) {
			t.Errorf("queueAdminGate MUST allow canonical super role %q", r)
		}
	}
}

// runQueueGateExplicit is a local copy of the runQueueGate helper
// with an explicit name to make grep/audit obvious.
func runQueueGateExplicit(t *testing.T, role auth.Role) bool {
	t.Helper()
	h := &Handler{}
	app := fiber.New()
	var got bool
	app.Get("/g", func(c fiber.Ctx) error {
		c.Locals("role", role)
		got = h.queueAdminGate(c)
		return c.SendStatus(fiber.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/g", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	_ = resp.Body.Close()
	return got
}
