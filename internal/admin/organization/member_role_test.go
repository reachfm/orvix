package organization

import (
	"context"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/auth"
)

func TestOrgMemberRole_AllowedDenied(t *testing.T) {
	tests := []struct {
		role  string
		allow bool
	}{
		{"tenant_admin", true},
		{"tenant_operator", true},
		{"tenant_support", true},
		{"tenant_readonly", true},
		{"platform_super_admin", false},
		{"superadmin", false},
		{"super_admin", false},
		{"super-admin", false},
		{"admin", false},
		{"operator", false},
		{"readonly", false},
		{"user", false},
		{"billing", false},
		{"unknown_role_zzz", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.role, func(t *testing.T) {
			got := isValidOrgMemberRole(tc.role)
			if got != tc.allow {
				t.Fatalf("isValidOrgMemberRole(%q) = %v, want %v", tc.role, got, tc.allow)
			}
		})
	}
}

func TestUpdateMemberRole_RejectsPlatformSuperAdmin(t *testing.T) {
	if isValidOrgMemberRole("platform_super_admin") {
		t.Fatal("platform_super_admin must not be a valid org member role")
	}
}

func TestUpdateMemberRole_AcceptsCanonicalTenantRoles(t *testing.T) {
	for _, r := range []string{"tenant_admin", "tenant_operator", "tenant_support", "tenant_readonly"} {
		if !isValidOrgMemberRole(r) {
			t.Fatalf("expected %s to be valid org member role", r)
		}
	}
}

func TestUpdateMemberRoleAllowedCanonicalRoles(t *testing.T) {
	roles := []struct {
		request string
		oldRole string
	}{
		{"tenant_admin", "tenant_support"},
		{"tenant_operator", "tenant_support"},
		{"tenant_support", "tenant_operator"},
		{"tenant_readonly", "tenant_support"},
	}
	for _, tc := range roles {
		t.Run(tc.request, func(t *testing.T) {
			db, authr := newOrgTestDBWithAuth(t)
			svc := NewService(NewOrganizationRepo(db), nil, nil)
			now := time.Now().UTC()
			db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
			db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, ?, 1, 100, ?, ?, 'mem@test.local', 'h', 1)`, tc.oldRole, now, now)

			// Issue old token and prove it's valid.
			oldAccess, _, _, err := authr.GenerateAccessTokenWithJTI(1, auth.Role(tc.oldRole))
			if err != nil {
				t.Fatalf("issue token: %v", err)
			}
			uid, role, valErr := authr.ValidateAccessToken(oldAccess)
			if valErr != nil || uid != 1 || string(role) != tc.oldRole {
				t.Fatalf("token before mutation: err=%v uid=%d role=%s", valErr, uid, role)
			}

			// Invoke real UpdateMemberRole.
			if err := svc.UpdateMemberRole(context.Background(), 1, 1, tc.request); err != nil {
				t.Fatalf("UpdateMemberRole(%s): %v", tc.request, err)
			}

			var storedRole string
			var tv int64
			db.QueryRow("SELECT role, token_version FROM users WHERE id = 1").Scan(&storedRole, &tv)
			if storedRole != tc.request || tv != 101 {
				t.Fatalf("after: role=%s tv=%d", storedRole, tv)
			}

			// Old token must be rejected.
			uid2, role2, err2 := authr.ValidateAccessToken(oldAccess)
			if err2 == nil || uid2 != 0 || role2 != "" {
				t.Fatalf("old token after mutation: err=%v uid=%d role=%s", err2, uid2, role2)
			}
		})
	}
}

func TestUpdateMemberRoleRejectsForbiddenRolesWithoutMutation(t *testing.T) {
	forbidden := []string{"platform_super_admin", "superadmin", "super_admin", "super-admin", "admin", "operator", "readonly", "user", "billing", "nonexistent_role", ""}
	for _, r := range forbidden {
		t.Run(r, func(t *testing.T) {
			db, _ := newOrgTestDBWithAuth(t)
			svc := NewService(NewOrganizationRepo(db), nil, nil)
			now := time.Now().UTC()
			db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
			db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (1, 1, 'tenant_support', 1, 200, ?, ?, 'fs@test.local', 'h', 1)`, now, now)
			err := svc.UpdateMemberRole(context.Background(), 1, 1, r)
			if err == nil {
				t.Fatalf("UpdateMemberRole(%s): expected error", r)
			}
			var storedRole string
			var tv int64
			db.QueryRow("SELECT role, token_version FROM users WHERE id = 1").Scan(&storedRole, &tv)
			if storedRole != "tenant_support" || tv != 200 {
				t.Fatalf("mutated: role=%s tv=%d", storedRole, tv)
			}
		})
	}
}
