package handlers

// validateAPIKeyScopes is the single validator both CreateAPIKey and
// RotateEnterpriseAPIKey call. It must reject empty, blank, duplicate, unknown,
// and over-privileged scopes, and accept scopes the caller actually holds — so
// a key can never be granted permissions its creator lacks.

import (
	"testing"

	"github.com/orvix/orvix/internal/auth"
)

func TestValidateAPIKeyScopes(t *testing.T) {
	cases := []struct {
		name    string
		role    auth.Role
		scopes  []string
		wantErr bool
	}{
		// PORTAL-SEPARATION-PHASE1 Phase 3: use RolePlatformSuperAdmin as the
		// "caller with broad permissions" fixture. The deprecated RoleAdmin
		// now has an empty permission map (see internal/auth/rbac/rbac.go)
		// so it cannot grant any scope — the "admin cannot grant" case
		// below asserts exactly that.
		// PORTAL-SEPARATION-PHASE1 Phase 5 (PR#58): PSA's permission map
		// was narrowed to platform-only scopes. It no longer holds
		// tenant-scoped domains.write, so the "may grant a scope it
		// holds" positive case uses queue.action (a platform scope PSA
		// still holds). The "may NOT grant a scope it does not hold"
		// negative case uses domains.write to lock in the boundary.
		{"empty set rejected", auth.RolePlatformSuperAdmin, nil, true},
		{"blank element rejected", auth.RolePlatformSuperAdmin, []string{"queue.action", ""}, true},
		{"duplicate rejected", auth.RolePlatformSuperAdmin, []string{"queue.action", "queue.action"}, true},
		{"unknown scope rejected", auth.RolePlatformSuperAdmin, []string{"totally.bogus"}, true},
		{"platform super admin may grant a scope it holds", auth.RolePlatformSuperAdmin, []string{"queue.action"}, false},
		{"platform super admin cannot grant tenant-scoped domains.write", auth.RolePlatformSuperAdmin, []string{"domains.write"}, true},
		{"deprecated admin cannot grant any scope", auth.RoleAdmin, []string{"domains.write"}, true},
		{"readonly cannot grant a write scope", auth.RoleReadOnly, []string{"domains.write"}, true},
		{"billing cannot grant a domain scope", auth.RoleBilling, []string{"domains.write"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAPIKeyScopes(tc.scopes, tc.role)
			if tc.wantErr && err == nil {
				t.Fatalf("expected rejection for role=%s scopes=%v, got nil", tc.role, tc.scopes)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected acceptance for role=%s scopes=%v, got %v", tc.role, tc.scopes, err)
			}
		})
	}
}
