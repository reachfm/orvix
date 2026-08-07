package organization

import "testing"

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
