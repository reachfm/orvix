package auth

import (
	"github.com/gofiber/fiber/v3"
)

// RouteSecurity bundles authentication, authorization, CSRF, and
// re-authentication into a single middleware factory for high-risk
// routes. Each middleware is applied in the correct order and only
// if the corresponding guard is non-nil.
//
// Usage:
//
//	admin.Post("/backups/:id/restore",
//	    RouteSecurity{
//	        Reauth:     ScopeBackupRestore,
//	        CSRF:       csrfMiddleware,
//	    }.Build(),
//	    h.PostRestoreBackup,
//	)

type RouteSecurity struct {
	// RequiredScope is the re-auth scope needed. If empty, no
	// re-auth middleware is applied.
	RequiredScope ReauthScope
	// ReauthManager must be provided if RequiredScope is non-empty.
	ReauthManager *ReauthManager
}

func (rs RouteSecurity) Build() fiber.Handler {
	return func(c fiber.Ctx) error {
		if rs.RequiredScope != "" && rs.ReauthManager != nil {
			return rs.ReauthManager.RequireReauth(rs.RequiredScope)(c)
		}
		return c.Next()
	}
}

// ReauthActionScope returns the ReauthScope for a given action type.
// This maps route categories to reauth scopes for consistent enforcement.
func ReauthActionScope(action string) ReauthScope {
	switch action {
	case "tenant_create", "tenant_edit", "tenant_suspend", "tenant_reactivate", "tenant_delete":
		return ScopeTenantManagement
	case "admin_create", "admin_update", "admin_role", "admin_password_reset",
		"admin_suspend", "admin_delete", "admin_disable_mfa", "admin_revoke_sessions":
		return ScopeIdentityManagement
	case "domain_create", "domain_verify", "domain_suspend", "domain_reactivate",
		"domain_delete", "domain_dkim_rotate", "domain_catchall", "domain_security":
		return ScopeDomainManagement
	case "mailbox_create", "mailbox_suspend", "mailbox_reactivate", "mailbox_delete",
		"mailbox_password_reset", "mailbox_quota", "mailbox_forwarding":
		return ScopeMailboxManagement
	case "billing_plan", "billing_subscription", "billing_credit", "billing_package":
		return ScopeBillingManagement
	case "firewall_create", "firewall_edit", "firewall_delete", "firewall_import":
		return ScopeFirewallManagement
	case "apikey_create", "apikey_rotate", "apikey_revoke":
		return ScopeAPIKeyManagement
	case "queue_retry", "queue_delete", "queue_bulk_retry", "queue_bulk_delete":
		return ScopeQueueDestructive
	case "backup_create", "backup_delete", "backup_restore":
		return ScopeBackupRestore
	case "security_settings", "mfa_disable":
		return ScopeSecuritySettings
	case "system_settings", "system_relay", "system_certificate":
		return ScopeSystemSettings
	case "system_update", "system_rollback":
		return ScopeSystemUpdate
	default:
		return ""
	}
}
