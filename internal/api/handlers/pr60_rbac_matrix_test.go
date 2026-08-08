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

// TestPR60_RBACMatrixMatchesExpectation locks in the RBAC map state.
// PORTAL-SEPARATION-PHASE1 Phase 3 (PR#60) landed the deprecated
// RoleAdmin=empty map and the explicit canonical gates on platform
// routes. Phase 5 (PR#58) then closed the permission map itself: the
// legacy RoleOperator platform bucket (queue.*, backups.*, settings.*,
// license.*, monitoring.read) was stripped so the map, the startup
// normalizer, and the explicit canonical gates all agree that neither
// deprecated 'admin' nor legacy 'operator' can touch the platform
// surface. The prior "self-invalidating when PR#58 lands" caveat on
// the RoleOperator expectation has been removed accordingly.
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

		// PORTAL-SEPARATION-PHASE1 Phase 3 (PR#60): RBAC map for the
		// deprecated RoleAdmin is empty. Any row still carrying "admin"
		// after normalization is AMBIGUOUS_ADMIN_ROLE and must have
		// zero privilege until an operator picks a canonical role.
		{auth.RoleAdmin, authrbac.PermQueueAction, false, "PHASE3: RoleAdmin map is empty; ambiguous rows have zero privilege"},
		{auth.RoleAdmin, authrbac.PermBackupsWrite, false, "PHASE3: RoleAdmin map is empty"},

		// PORTAL-SEPARATION-PHASE1 Phase 5 (PR#58): the legacy
		// RoleOperator platform bucket has been stripped. A legacy
		// 'operator' row that survived normalization (operator+NULL,
		// logged AMBIGUOUS_OPERATOR_ROLE) is now denied at both the
		// RBAC map and the explicit gate — belt-and-suspenders.
		{auth.RoleOperator, authrbac.PermQueueAction, false, "PHASE5: RoleOperator platform bucket stripped; map and explicit gate both deny"},
		{auth.RoleOperator, authrbac.PermBackupsWrite, false, "PHASE5: RoleOperator does not touch platform backups"},
		{auth.RoleOperator, authrbac.PermSettingsRead, false, "PHASE5: RoleOperator does not touch platform settings"},

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
// security assertion: the explicit canonical-role gate denies RoleAdmin
// and RoleOperator independently of the RBAC map. After Phase 5 (PR#58)
// the map itself also denies both — the explicit gate is now belt-and-
// suspenders rather than the sole closure mechanism.
func TestPR60_QueueAdminGateExplicitlyDeniesDeprecatedAdmin(t *testing.T) {
	// Post-Phase 5 invariant: the RBAC map denies these too. If the
	// map ever silently re-grants them, this is the loud regression
	// alarm — the explicit gate would still hold the line, but the
	// map/gate agreement PR#58 established would be broken.
	if authrbac.HasPermission(auth.RoleAdmin, authrbac.PermQueueAction) {
		t.Errorf("PHASE5 regression: RBAC map re-granted PermQueueAction to RoleAdmin " +
			"(map must remain empty for deprecated 'admin' — see rbac.go)")
	}
	if authrbac.HasPermission(auth.RoleOperator, authrbac.PermQueueAction) {
		t.Errorf("PHASE5 regression: RBAC map re-granted PermQueueAction to RoleOperator " +
			"(platform bucket must remain stripped — see rbac.go)")
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
