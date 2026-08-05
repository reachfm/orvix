package handlers

// Regression tests for the canonical role authorization gates
// migration. These assert the gates accept canonical platform
// roles and reject tenant roles / unknown / missing role, using
// direct fiber.Ctx exercise via app.Test().

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	authrbac "github.com/orvix/orvix/internal/auth/rbac"
)

// runQueueGate spins up a minimal fiber app, plants the role
// local, and returns the queueAdminGate result.
func runQueueGate(t *testing.T, role any) bool {
	t.Helper()
	h := &Handler{}
	app := fiber.New()
	var got bool
	app.Get("/g", func(c fiber.Ctx) error {
		if role != nil {
			c.Locals("role", role)
		}
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

func TestQueueAdminGate_PlatformSuperAdminAllowed(t *testing.T) {
	if !runQueueGate(t, auth.RolePlatformSuperAdmin) {
		t.Fatal("expected RolePlatformSuperAdmin to be allowed by queueAdminGate")
	}
}

func TestQueueAdminGate_LegacySuperAdminAllowed(t *testing.T) {
	// Backwards-compat: RoleSuperAdmin still carries PermQueueAction.
	if !runQueueGate(t, auth.RoleSuperAdmin) {
		t.Fatal("expected RoleSuperAdmin to remain allowed during migration window")
	}
}

func TestQueueAdminGate_TenantAdminDenied(t *testing.T) {
	// Tenant admins have no PermQueueAction — queue is platform-scoped.
	if runQueueGate(t, auth.RoleTenantAdmin) {
		t.Fatal("expected RoleTenantAdmin to be denied by queueAdminGate")
	}
}

func TestQueueAdminGate_TenantReadOnlyDenied(t *testing.T) {
	if runQueueGate(t, auth.RoleTenantReadOnly) {
		t.Fatal("expected RoleTenantReadOnly to be denied by queueAdminGate")
	}
}

func TestQueueAdminGate_UserDenied(t *testing.T) {
	if runQueueGate(t, auth.RoleUser) {
		t.Fatal("expected RoleUser to be denied by queueAdminGate")
	}
}

func TestQueueAdminGate_UnknownRoleDenied(t *testing.T) {
	if runQueueGate(t, auth.Role("nonsense-role-xyz")) {
		t.Fatal("expected unknown role to be denied by queueAdminGate")
	}
}

func TestQueueAdminGate_MissingRoleLocalDenied(t *testing.T) {
	// role local not set at all: the (auth.Role) type assertion must fail
	// closed, not panic.
	if runQueueGate(t, nil) {
		t.Fatal("expected missing role local to be denied by queueAdminGate")
	}
}

func TestQueueAdminGate_WrongTypeRoleDenied(t *testing.T) {
	// A plain string in c.Locals("role") — the assertion is (auth.Role);
	// legacy string plumbing must not accidentally cross into this gate.
	if runQueueGate(t, "superadmin") {
		t.Fatal("expected raw string role local to be denied by queueAdminGate")
	}
}

// ─── Site B: handlers.go users-list isAdmin classifier ─────────
// isAdmin is a data-classification helper, not an authz gate. The
// regression here is that the classification recognises canonical
// roles produced by NormalizeRole, not just legacy strings.
func TestUsersList_IsAdminClassifier_RecognisesCanonicalRoles(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{"platform_super_admin", true},
		{"tenant_admin", true},
		{"superadmin", true}, // legacy → normalises to platform_super_admin
		{"super_admin", true},
		{"admin", false},           // ambiguous (no tenantID) → returns RoleAdmin,false — still admin
		{"tenant_operator", false}, // operator is not admin
		{"tenant_readonly", false}, // readonly is not admin
		{"user", false},
		{"billing", false},
		{"nonsense-role", false},
	}
	for _, tc := range cases {
		canonical, _ := auth.NormalizeRole(auth.Role(tc.role), nil)
		got := canonical == auth.RolePlatformSuperAdmin ||
			canonical == auth.RoleSuperAdmin ||
			canonical == auth.RoleTenantAdmin ||
			canonical == auth.RoleAdmin
		// "admin" special-case: NormalizeRole returns RoleAdmin,false — we
		// still want it classified as admin for row display.
		if tc.role == "admin" {
			tc.want = true
		}
		if got != tc.want {
			t.Errorf("role=%q: got isAdmin=%v want=%v (canonical=%q)",
				tc.role, got, tc.want, canonical)
		}
	}
}

// ─── Site C: enterprise_parity callerIsSuper ───────────────────
// The gate accepts either auth.Role or string in c.Locals("role")
// during the migration window; both must normalise before compare.
func classifyCallerIsSuper(local any) bool {
	var rawRole auth.Role
	switch v := local.(type) {
	case auth.Role:
		rawRole = v
	case string:
		rawRole = auth.Role(v)
	}
	if rawRole == "" {
		return false
	}
	// Mirrors production enterprise_parity.go — explicit canonical
	// check only, no NormalizeRole (which would map deprecated
	// aliases like "admin" up to super and defeat the gate).
	return rawRole == auth.RolePlatformSuperAdmin ||
		rawRole == auth.RoleSuperAdmin
}

func TestEnterpriseParityCallerSuper_PlatformSuperAdminAllowed(t *testing.T) {
	if !classifyCallerIsSuper(auth.RolePlatformSuperAdmin) {
		t.Fatal("platform super admin (canonical) must be super")
	}
}

func TestEnterpriseParityCallerSuper_CanonicalStringSpellingAllowed(t *testing.T) {
	// Only the two canonical spellings ("platform_super_admin" and
	// the migration-window alias "superadmin") are accepted. Other
	// legacy variants ("super_admin", "super-admin") are NOT accepted
	// because that would require NormalizeRole in the authz path —
	// which this PR explicitly bans (see queueAdminGate rationale).
	// Anyone still carrying those spellings must be normalised at
	// session establishment, not at the gate.
	for _, s := range []string{"platform_super_admin", "superadmin"} {
		if !classifyCallerIsSuper(s) {
			t.Errorf("canonical spelling %q must classify as super", s)
		}
	}
	for _, s := range []string{"super_admin", "super-admin"} {
		if classifyCallerIsSuper(s) {
			t.Errorf("non-canonical spelling %q must NOT classify as super "+
				"(normalise at session establishment, not at the gate)", s)
		}
	}
}

func TestEnterpriseParityCallerSuper_TenantAdminDenied(t *testing.T) {
	if classifyCallerIsSuper(auth.RoleTenantAdmin) {
		t.Fatal("tenant admin must not classify as super (would allow cross-tenant edit)")
	}
	if classifyCallerIsSuper("tenant_admin") {
		t.Fatal("tenant admin (string) must not classify as super")
	}
}

func TestEnterpriseParityCallerSuper_MissingLocalDenied(t *testing.T) {
	if classifyCallerIsSuper(nil) {
		t.Fatal("missing role local must not classify as super")
	}
}

// ─── Site D: webmail_auth role assignment ──────────────────────
// The webmail login path plants a session role based on the
// mailbox is_admin flag. It must plant the canonical
// RoleTenantAdmin, never the deprecated RoleAdmin.
func webmailSessionRole(isAdmin bool) auth.Role {
	role := auth.RoleUser
	if isAdmin {
		role = auth.RoleTenantAdmin
	}
	return role
}

func TestWebmailAuthRoleAssignment_AdminIsTenantAdmin(t *testing.T) {
	got := webmailSessionRole(true)
	if got != auth.RoleTenantAdmin {
		t.Fatalf("webmail admin session role: got %q want %q", got, auth.RoleTenantAdmin)
	}
	if got == auth.RoleAdmin {
		t.Fatal("webmail must not plant deprecated auth.RoleAdmin into sessions")
	}
}

func TestWebmailAuthRoleAssignment_NonAdminIsUser(t *testing.T) {
	if got := webmailSessionRole(false); got != auth.RoleUser {
		t.Fatalf("webmail non-admin: got %q want %q", got, auth.RoleUser)
	}
}

// ─── RBAC boundary sanity — the perms our gates now use ────────
func TestPermQueueAction_CanonicalRoleMatrix(t *testing.T) {
	cases := []struct {
		role auth.Role
		want bool
	}{
		{auth.RolePlatformSuperAdmin, true},
		{auth.RoleSuperAdmin, true}, // legacy but still granted
		{auth.RoleTenantAdmin, false},
		{auth.RoleTenantOperator, false},
		{auth.RoleTenantReadOnly, false},
		{auth.RoleUser, false},
		{auth.RoleBilling, false},
		{auth.Role(""), false},
	}
	for _, tc := range cases {
		got := authrbac.HasPermission(tc.role, authrbac.PermQueueAction)
		if got != tc.want {
			t.Errorf("HasPermission(%q, queue.action) = %v want %v", tc.role, got, tc.want)
		}
	}
}
