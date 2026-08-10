package rbac

import (
	"testing"

	"github.com/orvix/orvix/internal/auth"
)

func TestAutomationJobPermissionMatrix(t *testing.T) {
	tests := []struct {
		role        auth.Role
		read, write bool
	}{
		{auth.RolePlatformSuperAdmin, true, true},
		{auth.RoleTenantAdmin, true, true},
		{auth.RoleTenantOperator, true, true},
		{auth.RoleTenantSupport, true, false},
		{auth.RoleTenantReadOnly, true, false},
		{auth.RoleBilling, true, false},
		{auth.RoleAdmin, false, false},
	}
	for _, test := range tests {
		if got := HasPermission(test.role, PermJobsRead); got != test.read {
			t.Errorf("%s jobs.read=%v want=%v", test.role, got, test.read)
		}
		if got := HasPermission(test.role, PermJobsWrite); got != test.write {
			t.Errorf("%s jobs.write=%v want=%v", test.role, got, test.write)
		}
	}
}
