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
		{"empty set rejected", auth.RoleAdmin, nil, true},
		{"blank element rejected", auth.RoleAdmin, []string{"domains.write", ""}, true},
		{"duplicate rejected", auth.RoleAdmin, []string{"domains.write", "domains.write"}, true},
		{"unknown scope rejected", auth.RoleAdmin, []string{"totally.bogus"}, true},
		{"admin may grant a scope it holds", auth.RoleAdmin, []string{"domains.write"}, false},
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
