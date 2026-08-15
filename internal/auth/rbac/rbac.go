// Package rbac implements granular role-based access control
// for the Orvix admin API.
//
// The previous release used a single binary "admin" gate:
// every admin endpoint required `role == "admin" || role ==
// "superadmin"`. This is too coarse for enterprise customers
// who need to delegate, for example, the read-only auditor
// persona or the helpdesk operator who can rotate passwords
// but not modify system settings.
//
// This package introduces:
//
//   - Permission constants (queue.read, settings.write, ...).
//   - A Role → Permission mapping for the four built-in roles:
//     super_admin, admin, operator, readonly.
//   - A Fiber middleware factory (Require) that checks one or
//     more permissions against the caller's effective role.
//   - A backwards-compatible RequireRole / RequireAnyRole
//     wrapper so existing admin endpoints can be migrated
//     gradually.
//
// The mapping lives in code, not in the database, so the
// set of permissions is reviewable in source control and
// can be unit-tested. A future "custom roles" feature would
// add a database-backed role → permission map; the
// middleware API does not change.
package rbac

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
)

// Permission is a single dotted-name capability the API checks
// for. The convention is `<resource>.<action>`.
type Permission string

const (
	// Queue management.
	PermQueueRead   Permission = "queue.read"
	PermQueueAction Permission = "queue.action"

	// Settings (admin panel).
	PermSettingsRead  Permission = "settings.read"
	PermSettingsWrite Permission = "settings.write"

	// Backups.
	PermBackupsRead  Permission = "backups.read"
	PermBackupsWrite Permission = "backups.write"

	// Monitoring.
	PermMonitoringRead Permission = "monitoring.read"

	// License.
	PermLicenseRead  Permission = "license.read"
	PermLicenseWrite Permission = "license.write"

	// Users / mailboxes.
	PermUsersRead  Permission = "users.read"
	PermUsersWrite Permission = "users.write"

	// Audit log.
	PermAuditRead Permission = "audit.read"

	// Organization / tenant management.
	PermOrganizationsRead  Permission = "organizations.read"
	PermOrganizationsWrite Permission = "organizations.write"

	// Domain management.
	PermDomainsRead  Permission = "domains.read"
	PermDomainsWrite Permission = "domains.write"

	// Mailbox / user management.
	PermMailboxesRead  Permission = "mailboxes.read"
	PermMailboxesWrite Permission = "mailboxes.write"

	// Credentials management.
	PermCredentialsReset Permission = "credentials.reset"
	PermSessionsRevoke   Permission = "sessions.revoke"

	// Dashboard.
	PermDashboardRead Permission = "dashboard.read"

	// Security.
	PermSecurityRead Permission = "security.read"

	// Aliases.
	PermAliasesRead  Permission = "aliases.read"
	PermAliasesWrite Permission = "aliases.write"

	// Groups.
	PermGroupsRead  Permission = "groups.read"
	PermGroupsWrite Permission = "groups.write"

	// Invitations.
	PermInvitationsRead  Permission = "invitations.read"
	PermInvitationsWrite Permission = "invitations.write"

	// Ownership transfer.
	PermOwnershipTransfer Permission = "ownership.transfer"

	// API keys.
	PermAPIKeysRead  Permission = "apikeys.read"
	PermAPIKeysWrite Permission = "apikeys.write"

	// Durable automation jobs.
	PermJobsRead  Permission = "jobs.read"
	PermJobsWrite Permission = "jobs.write"

	// Imports.
	PermImportsRead    Permission = "imports.read"
	PermImportsWrite   Permission = "imports.write"
	PermImportsExecute Permission = "imports.execute"
	PermImportsAdmin   Permission = "imports.admin"

	// Billing.
	PermBillingRead  Permission = "billing.read"
	PermBillingWrite Permission = "billing.write"

	// Platform (cross-tenant).
	PermPlatformOrganizationsRead  Permission = "platform.organizations.read"
	PermPlatformOrganizationsWrite Permission = "platform.organizations.write"
	PermPlatformSecurityRead       Permission = "platform.security.read"
	PermPlatformSessionsRevoke     Permission = "platform.sessions.revoke"
	// PermPlatformUsersWrite governs lifecycle actions (deactivation)
	// against platform-scoped user accounts. Distinct from
	// PermPlatformSessionsRevoke — revoking a session is not the same
	// authority as disabling the identity itself. Distinct from the
	// tenant-scoped PermUsersWrite, which never applies to
	// platform_super_admin rows (tenant_id IS NULL).
	PermPlatformUsersWrite Permission = "platform.users.write"

	// Platform mail control (cross-tenant, platform_super_admin only).
	// These gate the /platform/relays, /platform/suppressions, and
	// /platform/deliverability surfaces. They are deliberately
	// platform-scoped: no tenant role inherits them, and they are NOT
	// tenant-scoped permissions (so a PSA-minted API key may carry
	// them without violating the tenant-scoped-key boundary enforced
	// by validateAPIKeyScopes).
	PermRelaysRead         Permission = "relay.read"
	PermRelaysWrite        Permission = "relay.write"
	PermRelaysTest         Permission = "relay.test"
	PermSuppressionsRead   Permission = "suppressions.read"
	PermSuppressionsWrite  Permission = "suppressions.write"
	PermDeliverabilityRead Permission = "deliverability.read"
)

// AllPermissions is the canonical ordered list of permissions.
// Used by the admin UI to render a "roles & permissions" table
// and by tests to assert that no permission is silently dropped.
var AllPermissions = []Permission{
	PermQueueRead,
	PermQueueAction,
	PermSettingsRead,
	PermSettingsWrite,
	PermBackupsRead,
	PermBackupsWrite,
	PermMonitoringRead,
	PermLicenseRead,
	PermLicenseWrite,
	PermUsersRead,
	PermUsersWrite,
	PermAuditRead,
	PermOrganizationsRead,
	PermOrganizationsWrite,
	PermDomainsRead,
	PermDomainsWrite,
	PermMailboxesRead,
	PermMailboxesWrite,
	PermCredentialsReset,
	PermSessionsRevoke,
	PermDashboardRead,
	PermSecurityRead,
	PermAliasesRead,
	PermAliasesWrite,
	PermGroupsRead,
	PermGroupsWrite,
	PermInvitationsRead,
	PermInvitationsWrite,
	PermOwnershipTransfer,
	PermAPIKeysRead,
	PermAPIKeysWrite,
	PermJobsRead,
	PermJobsWrite,
	PermImportsRead,
	PermImportsWrite,
	PermImportsExecute,
	PermImportsAdmin,
	PermBillingRead,
	PermBillingWrite,
	PermPlatformOrganizationsRead,
	PermPlatformOrganizationsWrite,
	PermPlatformSecurityRead,
	PermPlatformSessionsRevoke,
	PermPlatformUsersWrite,
	PermRelaysRead,
	PermRelaysWrite,
	PermRelaysTest,
	PermSuppressionsRead,
	PermSuppressionsWrite,
	PermDeliverabilityRead,
}

// rolePermissions is the source of truth for "what can role X
// do?". Adding a new role requires extending this map and the
// rolePermissionList function below so it round-trips through
// the AllPermissions list.
var rolePermissions = map[auth.Role]map[Permission]bool{
	// ── Canonical roles ─────────────────────────────────────
	// Platform Super Admin: PLATFORM permissions ONLY.
	// PORTAL-SEPARATION-PHASE1 Phase 5 (PR#58) + Mail-Control enablement:
	// platform_super_admin governs the platform surface — mail queue,
	// cross-tenant mail control (domains/mailboxes/aliases/groups through
	// the /platform/* routes with explicit target tenants), platform
	// settings, backups, updater, license, monitoring, platform audit,
	// firewall/modules (inline gates), platform organizations (cross-
	// tenant list/update) and platform security. It intentionally does
	// NOT hold any tenant-scoped route permission — the /platform/*
	// routes it may call are platformMW-owned and every request must
	// name an explicit target tenant; it is never treated as a tenant
	// admin. A platform_super_admin bootstrap row has NULL tenant_id,
	// so tenant-scoped routes remain unreachable via requireTenantContext
	// regardless; this map keeps the RBAC boundary explicit rather than
	// relying only on that middleware.
	auth.RolePlatformSuperAdmin: {
		// Platform mail queue.
		PermQueueRead: true, PermQueueAction: true,
		// Platform mail control (cross-tenant platform surface). These
		// reuse the canonical domain/mailbox/alias/group permissions so
		// route-level RBAC is uniform; the /platform/* routes they gate
		// are platformMW-owned and require an explicit target tenant in
		// every request — a platform_super_admin never acquires tenant
		// identity implicitly.
		PermDomainsRead: true, PermDomainsWrite: true,
		PermMailboxesRead: true, PermMailboxesWrite: true,
		PermAliasesRead: true, PermAliasesWrite: true,
		PermGroupsRead: true, PermGroupsWrite: true,
		// Platform settings.
		PermSettingsRead: true, PermSettingsWrite: true,
		// Platform backups.
		PermBackupsRead: true, PermBackupsWrite: true,
		// Platform monitoring.
		PermMonitoringRead: true,
		// Platform licensing (updater + license).
		PermLicenseRead: true, PermLicenseWrite: true,
		// Platform audit trail.
		PermAuditRead: true,
		// Cross-tenant organization catalog (list/update tenants).
		PermPlatformOrganizationsRead: true, PermPlatformOrganizationsWrite: true,
		// Cross-tenant security controls (session revocation, security ops).
		PermPlatformSecurityRead:   true,
		PermPlatformSessionsRevoke: true,
		// Platform user lifecycle (deactivation of another platform
		// account). Deliberately NOT granted to any tenant role, support
		// operator, or ordinary platform operator role — see the
		// PermPlatformUsersWrite doc comment above.
		PermPlatformUsersWrite: true,
		PermJobsRead:           true, PermJobsWrite: true,
		// Platform imports (the /platform/imports surface is platform-scope).
		PermImportsRead: true, PermImportsWrite: true, PermImportsExecute: true, PermImportsAdmin: true,
		// Platform mail control: relay administration, suppression
		// lifecycle, and deliverability metrics. Platform-scoped only —
		// no tenant role inherits these.
		PermRelaysRead: true, PermRelaysWrite: true, PermRelaysTest: true,
		PermSuppressionsRead: true, PermSuppressionsWrite: true,
		PermDeliverabilityRead: true,
	},
	// Tenant Admin: full tenant permissions (no platform).
	auth.RoleTenantAdmin: {
		PermDashboardRead: true,
		PermDomainsRead:   true, PermDomainsWrite: true,
		PermMailboxesRead: true, PermMailboxesWrite: true,
		PermOrganizationsRead: true, PermOrganizationsWrite: true,
		PermUsersRead: true, PermUsersWrite: true,
		PermAliasesRead: true, PermAliasesWrite: true,
		PermGroupsRead: true, PermGroupsWrite: true,
		PermInvitationsRead: true, PermInvitationsWrite: true,
		PermOwnershipTransfer: true,
		PermAPIKeysRead:       true, PermAPIKeysWrite: true,
		PermJobsRead: true, PermJobsWrite: true,
		PermBillingRead: true, PermBillingWrite: true,
		PermAuditRead:        true,
		PermSettingsRead:     true,
		PermMonitoringRead:   true,
		PermCredentialsReset: true,
		PermSessionsRevoke:   true,
		PermSecurityRead:     true,
		PermImportsRead:      true, PermImportsWrite: true, PermImportsExecute: true,
	},
	// Tenant Operator: operational management without user/role/admin.
	auth.RoleTenantOperator: {
		PermDashboardRead: true,
		PermDomainsRead:   true,
		PermMailboxesRead: true, PermMailboxesWrite: true,
		PermOrganizationsRead: true,
		PermUsersRead:         true,
		PermAliasesRead:       true, PermAliasesWrite: true,
		PermGroupsRead:       true,
		PermInvitationsRead:  true,
		PermAuditRead:        true,
		PermSettingsRead:     true,
		PermMonitoringRead:   true,
		PermCredentialsReset: true,
		PermSessionsRevoke:   true,
		PermSecurityRead:     true,
		PermBillingRead:      true,
		PermJobsRead:         true, PermJobsWrite: true,
		PermImportsRead: true, PermImportsWrite: true, PermImportsExecute: true,
	},
	auth.RoleTenantSupport: {
		PermDashboardRead: true,
		PermDomainsRead:   true,
		PermMailboxesRead: true, PermMailboxesWrite: true,
		PermOrganizationsRead: true,
		PermUsersRead:         true,
		PermAliasesRead:       true,
		PermGroupsRead:        true,
		PermInvitationsRead:   true,
		PermAuditRead:         true,
		PermSettingsRead:      true,
		PermMonitoringRead:    true,
		PermCredentialsReset:  true,
		PermSessionsRevoke:    true,
		PermSecurityRead:      true,
		PermBillingRead:       true,
		PermJobsRead:          true,
		PermImportsRead:       true,
	},
	// Tenant Read Only.
	auth.RoleTenantReadOnly: {
		PermDashboardRead:     true,
		PermDomainsRead:       true,
		PermMailboxesRead:     true,
		PermOrganizationsRead: true,
		PermUsersRead:         true,
		PermAliasesRead:       true,
		PermGroupsRead:        true,
		PermInvitationsRead:   true,
		PermAuditRead:         true,
		PermSettingsRead:      true,
		PermMonitoringRead:    true,
		PermSecurityRead:      true,
		PermBillingRead:       true,
		PermJobsRead:          true,
	},

	// ── Legacy roles ────────────────────────────────────────
	auth.RoleSuperAdmin: {
		PermQueueRead: true, PermQueueAction: true,
		PermSettingsRead: true, PermSettingsWrite: true,
		PermBackupsRead: true, PermBackupsWrite: true,
		PermMonitoringRead: true,
		PermLicenseRead:    true, PermLicenseWrite: true,
		PermUsersRead: true, PermUsersWrite: true,
		PermAuditRead:         true,
		PermOrganizationsRead: true, PermOrganizationsWrite: true,
		PermDomainsRead: true, PermDomainsWrite: true,
		PermMailboxesRead: true, PermMailboxesWrite: true,
		PermCredentialsReset: true,
		PermSessionsRevoke:   true,
		PermDashboardRead:    true,
		PermSecurityRead:     true,
		PermAliasesRead:      true, PermAliasesWrite: true,
		PermGroupsRead: true, PermGroupsWrite: true,
		PermInvitationsRead: true, PermInvitationsWrite: true,
		PermOwnershipTransfer: true,
		PermAPIKeysRead:       true, PermAPIKeysWrite: true,
		PermBillingRead: true, PermBillingWrite: true,
		PermPlatformOrganizationsRead: true, PermPlatformOrganizationsWrite: true,
		PermPlatformSecurityRead:   true,
		PermPlatformSessionsRevoke: true,
		PermPlatformUsersWrite:     true,
		PermJobsRead:               true, PermJobsWrite: true,
		PermImportsRead: true, PermImportsWrite: true, PermImportsExecute: true, PermImportsAdmin: true,
		// Platform mail control (legacy super-admin mirrors the PSA).
		PermRelaysRead: true, PermRelaysWrite: true, PermRelaysTest: true,
		PermSuppressionsRead: true, PermSuppressionsWrite: true,
		PermDeliverabilityRead: true,
	},
	// PORTAL-SEPARATION-PHASE1: the deprecated auth.RoleAdmin no longer maps
	// to any permission. Legacy "admin" rows are normalized at startup
	// (see internal/models normalizeAdminRoles) to either
	// RolePlatformSuperAdmin (tenant_id IS NULL) or RoleTenantAdmin
	// (tenant_id IS NOT NULL). A row that still carries "admin" after
	// normalization is an operator-review case (AMBIGUOUS_ADMIN_ROLE) and
	// must have no privilege until a human decides.
	auth.RoleAdmin: {},
	// Legacy RoleOperator: tenant-scoped helpdesk persona.
	// PORTAL-SEPARATION-PHASE1 Phase 5 (PR#58): the legacy 'operator'
	// bucket previously carried a set of platform-adjacent single-
	// tenant admin-panel reads (queue.*, backups.read, license.read,
	// settings.read, monitoring.read) that no canonical tenant_* role
	// holds. The security audit flagged this as an "accidental privilege
	// bridge" — an un-normalized legacy 'operator' row surviving
	// startup normalization would silently retain platform reads and
	// queue.action. The platform bucket has been stripped here so the
	// RBAC map, the startup normalizer and the explicit canonical gates
	// all agree that legacy 'operator' does not touch the platform
	// surface. Rows with tenant_id are normalized to 'tenant_operator'
	// at startup; operator+NULL rows are logged AMBIGUOUS_OPERATOR_ROLE
	// and left inert.
	auth.RoleOperator: {
		PermUsersRead: true, PermUsersWrite: true,
		PermAuditRead:         true,
		PermOrganizationsRead: true,
		PermDomainsRead:       true,
		PermMailboxesRead:     true, PermMailboxesWrite: true,
		PermCredentialsReset: true,
		PermSessionsRevoke:   true,
		PermDashboardRead:    true,
		PermSecurityRead:     true,
		PermAliasesRead:      true,
		PermGroupsRead:       true,
		PermInvitationsRead:  true,
		PermAPIKeysRead:      true,
		PermBillingRead:      true,
		PermJobsRead:         true, PermJobsWrite: true,
	},
	auth.RoleReadOnly: {
		PermQueueRead:         true,
		PermSettingsRead:      true,
		PermBackupsRead:       true,
		PermMonitoringRead:    true,
		PermLicenseRead:       true,
		PermUsersRead:         true,
		PermAuditRead:         true,
		PermOrganizationsRead: true,
		PermDomainsRead:       true,
		PermMailboxesRead:     true,
		PermDashboardRead:     true,
		PermSecurityRead:      true,
		PermAliasesRead:       true,
		PermGroupsRead:        true,
		PermInvitationsRead:   true,
		PermAPIKeysRead:       true,
		PermBillingRead:       true,
		PermJobsRead:          true,
	},
	// RoleUser is a tenant owner/member who has full control over their
	// own tenant resources but NO platform-level privileges (platform
	// organizations read/write, platform security, platform sessions).
	auth.RoleUser: {
		PermDashboardRead: true,
		PermDomainsRead:   true, PermDomainsWrite: true,
		PermMailboxesRead: true, PermMailboxesWrite: true,
		PermOrganizationsRead: true, PermOrganizationsWrite: true,
		PermUsersRead: true, PermUsersWrite: true,
		PermAliasesRead: true, PermAliasesWrite: true,
		PermGroupsRead: true, PermGroupsWrite: true,
		PermInvitationsRead: true, PermInvitationsWrite: true,
		PermOwnershipTransfer: true,
		PermAPIKeysRead:       true, PermAPIKeysWrite: true,
		PermBillingRead: true, PermBillingWrite: true,
		PermJobsRead: true, PermJobsWrite: true,
		PermAuditRead:        true,
		PermSettingsRead:     true,
		PermMonitoringRead:   true,
		PermCredentialsReset: true,
		PermSessionsRevoke:   true,
		PermSecurityRead:     true,
	},
	// RoleBilling is a tenant billing-only role. Read access to tenant
	// resources. Billing write. No domain/mailbox/member mutations.
	auth.RoleBilling: {
		PermDashboardRead:     true,
		PermDomainsRead:       true,
		PermMailboxesRead:     true,
		PermOrganizationsRead: true,
		PermAuditRead:         true,
		PermSettingsRead:      true,
		PermMonitoringRead:    true,
		PermBillingRead:       true, PermBillingWrite: true,
		PermGroupsRead:      true,
		PermInvitationsRead: true,
		PermAliasesRead:     true,
		PermAPIKeysRead:     true,
		PermJobsRead:        true,
	},
}

// HasPermission reports whether the role carries the
// permission. Unknown roles get the empty permission set;
// callers should treat that as "deny" by default.
func HasPermission(role auth.Role, p Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	return perms[p]
}

// RolePermissionList returns the set of permissions granted
// to the role, in the same order as AllPermissions. Useful
// for serializing the role/permission matrix to the admin UI.
func RolePermissionList(role auth.Role) []Permission {
	out := make([]Permission, 0, len(AllPermissions))
	for _, p := range AllPermissions {
		if HasPermission(role, p) {
			out = append(out, p)
		}
	}
	return out
}

// Require returns a Fiber middleware that checks the caller
// has ALL of the given permissions. If the caller is missing
// any of them, the request is rejected with 403 Forbidden and
// a body that lists the missing permissions — never a
// "permission denied" without context, because operators need
// to understand why their request was rejected.
//
// The role is read from c.Locals("role") which the standard
// auth middleware populates from the JWT.
func Require(perms ...Permission) fiber.Handler {
	return func(c fiber.Ctx) error {
		role, ok := c.Locals("role").(auth.Role)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "no role on request",
			})
		}
		var missing []string
		for _, p := range perms {
			if !HasPermission(role, p) {
				missing = append(missing, string(p))
			}
		}
		if len(missing) > 0 {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":   "insufficient permissions",
				"missing": missing,
				"role":    string(role),
			})
		}
		return c.Next()
	}
}

// RequireAny returns a Fiber middleware that checks the
// caller has AT LEAST ONE of the given permissions. Used
// when an endpoint is reachable by both readers and writers
// (e.g., list endpoints are readable by multiple roles).
func RequireAny(perms ...Permission) fiber.Handler {
	return func(c fiber.Ctx) error {
		role, ok := c.Locals("role").(auth.Role)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "no role on request",
			})
		}
		for _, p := range perms {
			if HasPermission(role, p) {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":           "insufficient permissions",
			"required_any_of": perms,
			"role":            string(role),
		})
	}
}
