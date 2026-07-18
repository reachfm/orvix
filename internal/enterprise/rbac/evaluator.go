package rbac

import (
	"context"
	"database/sql"
	"strings"

	"github.com/orvix/orvix/internal/auth"
	rbacpkg "github.com/orvix/orvix/internal/auth/rbac"
)

type Evaluator struct {
	db      *sql.DB
	enabled bool
}

func NewEvaluator(db *sql.DB) *Evaluator {
	return &Evaluator{db: db, enabled: db != nil}
}

func (e *Evaluator) HasPermission(ctx context.Context, role auth.Role, userID uint, perm rbacpkg.Permission) bool {
	if rbacpkg.HasPermission(role, perm) {
		return true
	}
	if !e.enabled {
		return false
	}
	return e.hasGroupGrant(ctx, userID, perm)
}

func (e *Evaluator) HasAllPermissions(ctx context.Context, role auth.Role, userID uint, perms ...rbacpkg.Permission) bool {
	for _, p := range perms {
		if !e.HasPermission(ctx, role, userID, p) {
			return false
		}
	}
	return true
}

func (e *Evaluator) HasAnyPermission(ctx context.Context, role auth.Role, userID uint, perms ...rbacpkg.Permission) bool {
	for _, p := range perms {
		if e.HasPermission(ctx, role, userID, p) {
			return true
		}
	}
	return len(perms) == 0
}

func (e *Evaluator) hasGroupGrant(ctx context.Context, userID uint, perm rbacpkg.Permission) bool {
	if e.db == nil {
		return false
	}
	rows, err := e.db.QueryContext(ctx,
		`SELECT g.grants FROM coremail_admin_groups g
		 INNER JOIN coremail_admin_group_members m ON m.group_id = g.id
		 WHERE m.user_id = ? AND g.deleted_at IS NULL`, userID)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var grants string
		if err := rows.Scan(&grants); err != nil {
			continue
		}
		for _, grant := range strings.Split(grants, ",") {
			if strings.TrimSpace(grant) == string(perm) {
				return true
			}
		}
	}
	return false
}

func (e *Evaluator) CanManageTenant(role auth.Role, actorTenantID, targetTenantID uint) bool {
	if role == auth.RolePlatformSuperAdmin || role == auth.RoleSuperAdmin {
		return true
	}
	return actorTenantID > 0 && actorTenantID == targetTenantID
}

func (e *Evaluator) CanAccessDomain(role auth.Role, actorTenantID, domainTenantID uint) bool {
	if role == auth.RolePlatformSuperAdmin || role == auth.RoleSuperAdmin {
		return true
	}
	return actorTenantID > 0 && actorTenantID == domainTenantID
}

// IsPlatformRole returns true if the role is a platform-scoped role.
// These roles have no customer tenant ownership and can perform
// cross-tenant operations.
func IsPlatformRole(role auth.Role) bool {
	switch role {
	case auth.RolePlatformSuperAdmin, auth.RoleSuperAdmin, auth.RoleAdmin:
		return true
	default:
		return false
	}
}

// IsTenantRole returns true if the role is a tenant-scoped role.
// These roles belong to exactly one customer tenant.
func IsTenantRole(role auth.Role) bool {
	switch role {
	case auth.RoleTenantAdmin, auth.RoleTenantOperator, auth.RoleTenantSupport,
		auth.RoleTenantReadOnly, auth.RoleUser, auth.RoleOperator,
		auth.RoleReadOnly, auth.RoleBilling:
		return true
	default:
		return false
	}
}
