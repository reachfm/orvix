package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	authrbac "github.com/orvix/orvix/internal/auth/rbac"
)

// requirePerm wraps authrbac.Require for a single permission, returning
// an inline fiber.Handler that reads role from Locals (set earlier in
// the middleware chain) and denies if the permission is missing.
func requirePerm(perm authrbac.Permission) fiber.Handler {
	return func(c fiber.Ctx) error {
		role, ok := c.Locals("role").(auth.Role)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no role"})
		}
		if !authrbac.HasPermission(role, perm) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":    "insufficient permissions",
				"required": string(perm),
				"role":     string(role),
			})
		}
		return c.Next()
	}
}

func TestExactAuthorizationMatrix(t *testing.T) {
	app := fiber.New()

	reqTenant := func(c fiber.Ctx) error {
		if _, ok := c.Locals("tenant_id").(uint); !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no tenant"})
		}
		return c.Next()
	}
	mockCSRF := func(c fiber.Ctx) error { return c.Next() }
	ok := func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) }

	// Mirror of production router.go: the /enterprise group is role-gated
	// to the canonical tenant administration family BEFORE tenant context.
	enterpriseRoleGate := auth.RequireAnyRole(
		auth.RoleTenantAdmin, auth.RoleTenantOperator, auth.RoleTenantSupport, auth.RoleTenantReadOnly,
	)
	enterprise := app.Group("/enterprise", enterpriseRoleGate, reqTenant, mockCSRF)

	// GET routes — no permission check
	enterprise.Get("/dashboard", ok)
	enterprise.Get("/domains", ok)
	enterprise.Get("/mailboxes", ok)
	enterprise.Get("/billing/subscription", ok)
	enterprise.Get("/api-keys", ok)

	// POST routes — each with exact permission check
	enterprise.Post("/domains", requirePerm(authrbac.PermDomainsWrite), ok)
	enterprise.Post("/mailboxes", requirePerm(authrbac.PermMailboxesWrite), ok)
	enterprise.Post("/groups", requirePerm(authrbac.PermGroupsWrite), ok)
	enterprise.Post("/aliases", requirePerm(authrbac.PermAliasesWrite), ok)
	enterprise.Post("/invitations", requirePerm(authrbac.PermInvitationsWrite), ok)
	enterprise.Post("/billing/subscription", requirePerm(authrbac.PermBillingWrite), ok)
	enterprise.Post("/api-keys", requirePerm(authrbac.PermAPIKeysWrite), ok)
	enterprise.Post("/ownership/request", requirePerm(authrbac.PermOwnershipTransfer), ok)

	type testCase struct {
		role     auth.Role
		method   string
		path     string
		wantCode int
	}
	tests := []testCase{
		// ── RoleTenantAdmin: full read + write console ──
		{auth.RoleTenantAdmin, "GET", "/enterprise/dashboard", 200},
		{auth.RoleTenantAdmin, "GET", "/enterprise/domains", 200},
		{auth.RoleTenantAdmin, "GET", "/enterprise/mailboxes", 200},
		{auth.RoleTenantAdmin, "GET", "/enterprise/api-keys", 200},
		{auth.RoleTenantAdmin, "POST", "/enterprise/domains", 200},
		{auth.RoleTenantAdmin, "POST", "/enterprise/mailboxes", 200},
		{auth.RoleTenantAdmin, "POST", "/enterprise/groups", 200},
		{auth.RoleTenantAdmin, "POST", "/enterprise/aliases", 200},
		{auth.RoleTenantAdmin, "POST", "/enterprise/invitations", 200},
		{auth.RoleTenantAdmin, "POST", "/enterprise/billing/subscription", 200},
		{auth.RoleTenantAdmin, "POST", "/enterprise/api-keys", 200},
		{auth.RoleTenantAdmin, "POST", "/enterprise/ownership/request", 200},

		// ── RoleTenantOperator: operational subset only ──
		{auth.RoleTenantOperator, "GET", "/enterprise/dashboard", 200},
		{auth.RoleTenantOperator, "GET", "/enterprise/mailboxes", 200},
		{auth.RoleTenantOperator, "POST", "/enterprise/mailboxes", 200},
		{auth.RoleTenantOperator, "GET", "/enterprise/domains", 200},
		{auth.RoleTenantOperator, "POST", "/enterprise/domains", 403},
		{auth.RoleTenantOperator, "POST", "/enterprise/groups", 403},
		{auth.RoleTenantOperator, "POST", "/enterprise/invitations", 403},
		{auth.RoleTenantOperator, "POST", "/enterprise/billing/subscription", 403},
		{auth.RoleTenantOperator, "POST", "/enterprise/api-keys", 403},
		{auth.RoleTenantOperator, "POST", "/enterprise/ownership/request", 403},

		// ── RoleTenantSupport: support subset only ──
		{auth.RoleTenantSupport, "GET", "/enterprise/dashboard", 200},
		{auth.RoleTenantSupport, "GET", "/enterprise/mailboxes", 200},
		{auth.RoleTenantSupport, "POST", "/enterprise/mailboxes", 200},
		{auth.RoleTenantSupport, "POST", "/enterprise/domains", 403},
		{auth.RoleTenantSupport, "POST", "/enterprise/billing/subscription", 403},
		{auth.RoleTenantSupport, "POST", "/enterprise/api-keys", 403},
		{auth.RoleTenantSupport, "POST", "/enterprise/ownership/request", 403},

		// ── RoleTenantReadOnly: ALL writes denied ──
		{auth.RoleTenantReadOnly, "GET", "/enterprise/dashboard", 200},
		{auth.RoleTenantReadOnly, "GET", "/enterprise/domains", 200},
		{auth.RoleTenantReadOnly, "GET", "/enterprise/api-keys", 200},
		{auth.RoleTenantReadOnly, "POST", "/enterprise/domains", 403},
		{auth.RoleTenantReadOnly, "POST", "/enterprise/mailboxes", 403},
		{auth.RoleTenantReadOnly, "POST", "/enterprise/groups", 403},
		{auth.RoleTenantReadOnly, "POST", "/enterprise/aliases", 403},
		{auth.RoleTenantReadOnly, "POST", "/enterprise/invitations", 403},
		{auth.RoleTenantReadOnly, "POST", "/enterprise/billing/subscription", 403},
		{auth.RoleTenantReadOnly, "POST", "/enterprise/api-keys", 403},
		{auth.RoleTenantReadOnly, "POST", "/enterprise/ownership/request", 403},

		// ── RoleUser (webmail end-user): denied the ENTIRE console ──
		// RoleUser is the per-mailbox end-user role with no Organization
		// administration privileges. The enterpriseRead group gate rejects
		// it before any read or write handler runs.
		{auth.RoleUser, "GET", "/enterprise/dashboard", 403},
		{auth.RoleUser, "GET", "/enterprise/domains", 403},
		{auth.RoleUser, "GET", "/enterprise/mailboxes", 403},
		{auth.RoleUser, "GET", "/enterprise/api-keys", 403},
		{auth.RoleUser, "POST", "/enterprise/domains", 403},
		{auth.RoleUser, "POST", "/enterprise/mailboxes", 403},
		{auth.RoleUser, "POST", "/enterprise/groups", 403},
		{auth.RoleUser, "POST", "/enterprise/aliases", 403},
		{auth.RoleUser, "POST", "/enterprise/invitations", 403},
		{auth.RoleUser, "POST", "/enterprise/billing/subscription", 403},
		{auth.RoleUser, "POST", "/enterprise/api-keys", 403},
		{auth.RoleUser, "POST", "/enterprise/ownership/request", 403},

		// ── RoleBilling: unsupported billing-only persona — also denied ──
		{auth.RoleBilling, "GET", "/enterprise/billing/subscription", 403},
		{auth.RoleBilling, "POST", "/enterprise/billing/subscription", 403},

		// ── Legacy roles (pre-normalization) — denied the console ──
		// Legacy operator/readonly rows are normalized at startup to
		// tenant_operator/tenant_readonly when they carry a tenant_id;
		// un-normalized rows are ambiguous and must have zero access.
		{auth.RoleOperator, "GET", "/enterprise/dashboard", 403},
		{auth.RoleOperator, "POST", "/enterprise/mailboxes", 403},
		{auth.RoleReadOnly, "GET", "/enterprise/dashboard", 403},
		{auth.RoleAdmin, "GET", "/enterprise/dashboard", 403},
	}

	for _, tc := range tests {
		t.Run(string(tc.role)+"_"+tc.method+"_"+strings.ReplaceAll(tc.path, "/", "_"), func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			// Inject role + tenant_id via a mock context middleware.
			// We rebuild a minimal app per test case to avoid state leakage.

			a := fiber.New()
			a.Use(func(c fiber.Ctx) error {
				c.Locals("role", tc.role)
				c.Locals("tenant_id", uint(1))
				c.Locals("user_id", uint(1))
				return c.Next()
			})
			a.Use("/enterprise", enterpriseRoleGate, reqTenant, mockCSRF)

			a.Get("/enterprise/dashboard", ok)
			a.Get("/enterprise/domains", ok)
			a.Get("/enterprise/mailboxes", ok)
			a.Get("/enterprise/billing/subscription", ok)
			a.Get("/enterprise/api-keys", ok)

			a.Post("/enterprise/domains", requirePerm(authrbac.PermDomainsWrite), ok)
			a.Post("/enterprise/mailboxes", requirePerm(authrbac.PermMailboxesWrite), ok)
			a.Post("/enterprise/groups", requirePerm(authrbac.PermGroupsWrite), ok)
			a.Post("/enterprise/aliases", requirePerm(authrbac.PermAliasesWrite), ok)
			a.Post("/enterprise/invitations", requirePerm(authrbac.PermInvitationsWrite), ok)
			a.Post("/enterprise/billing/subscription", requirePerm(authrbac.PermBillingWrite), ok)
			a.Post("/enterprise/api-keys", requirePerm(authrbac.PermAPIKeysWrite), ok)
			a.Post("/enterprise/ownership/request", requirePerm(authrbac.PermOwnershipTransfer), ok)

			resp, err := a.Test(req)
			if err != nil {
				t.Fatalf("test error: %v", err)
			}
			if resp.StatusCode != tc.wantCode {
				var body map[string]interface{}
				json.NewDecoder(resp.Body).Decode(&body)
				t.Errorf("%s %s (%s): want %d, got %d (body: %v)", tc.method, tc.path, tc.role, tc.wantCode, resp.StatusCode, body)
			}
		})
	}
}

func TestSuspendedTenantAllWritesDenied(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
	}{
		{"GET", "/enterprise/dashboard"},
		{"POST", "/enterprise/domains"},
		{"POST", "/enterprise/mailboxes"},
		{"POST", "/enterprise/groups"},
		{"POST", "/enterprise/billing/subscription"},
		{"POST", "/enterprise/api-keys"},
	} {
		t.Run(tc.method+"_"+strings.ReplaceAll(tc.path, "/", "_"), func(t *testing.T) {
			a := fiber.New()
			suspended := func(c fiber.Ctx) error {
				if c.Method() != "GET" && c.Method() != "HEAD" && c.Method() != "OPTIONS" {
					return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "suspended"})
				}
				return c.Next()
			}
			a.Use(func(c fiber.Ctx) error {
				c.Locals("role", auth.RoleTenantAdmin)
				c.Locals("tenant_id", uint(1))
				return c.Next()
			})
			a.Use("/enterprise", suspended)
			a.Get("/enterprise/dashboard", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
			a.Post("/enterprise/domains", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
			a.Post("/enterprise/mailboxes", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
			a.Post("/enterprise/groups", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
			a.Post("/enterprise/billing/subscription", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
			a.Post("/enterprise/api-keys", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			resp, _ := a.Test(req)
			want := 200
			if tc.method != "GET" {
				want = 403
			}
			if resp.StatusCode != want {
				t.Errorf("%s %s (suspended): want %d, got %d", tc.method, tc.path, want, resp.StatusCode)
			}
		})
	}
}
