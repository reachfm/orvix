// Capability inventory for the Platform Super Admin console.
//
// This is the machine-verifiable source of truth for what this frontend
// may render and navigate to. Every entry is derived from a real route
// in internal/api/router.go (verified at integration SHA aeec470, PR #65
// head f1d2954, and the frontend merge de49a8e). Classifications:
//
//   - WIRED:         real backend route + real frontend page in this console
//   - BACKEND_ONLY:  real backend route, no frontend page yet (never invent one)
//   - UNAVAILABLE:   no real backend route exists; the UI must not fabricate it
//   - TENANT_ONLY:   real backend route gated to the tenant-family roles
//                    (tenantCompatMW[0] = RequireAnyRole(tenant family)); a
//                    Platform Super Admin cannot call it even with a grant
//   - PLATFORM_ONLY: real backend route gated to platform-super-admin roles
//                    (platformMW); tenant roles cannot call it
//
// Mail-control ownership model (verified from router.go, PR #65):
//   - PSA mail control uses ONLY /api/v1/platform/domains|mailboxes|aliases|
//     groups|suppressions|deliverability|relays routes, platformMW-gated +
//     RBAC-permissioned (domains.read/write, mailboxes.read/write,
//     aliases.read/write, groups.read, suppressions.read/write,
//     deliverability.read, relay.read/write/test). Every route requires an
//     EXPLICIT target tenant_id in the path (except relays, which are
//     platform-wide). There is NO support-access requirement: no
//     X-Support-Tenant-ID header and no grant is involved.
//   - Tenant Admin continues to use tenant-owned routes (/domains,
//     /mailboxes, /enterprise/aliases, /enterprise/groups, ...) — those
//     remain TENANT_ONLY and must never be called by PSA pages.
//   - Support Access remains a separate feature (support-access page +
//     /platform/support/grants* routes) and is not part of mail control.
//
// No dead navigation: every WIRED feature below has a functional page
// plus focused tests. No route is marked WIRED without one.

export type CapabilityState = "WIRED" | "BACKEND_ONLY" | "UNAVAILABLE" | "TENANT_ONLY" | "PLATFORM_ONLY";
export type PortalOwner = "platform" | "tenant" | "shared" | "none";

export interface Capability {
  feature: string;
  route: string;
  method: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  /** Real backend route exists. */
  backendRoute: boolean;
  state: CapabilityState;
  /** Who may call this route. */
  owner: PortalOwner;
  /** Whether a PSA needs an active support-access grant + tenant header. */
  requiresSupportAccess?: boolean;
  /** Whether the route requires a tenant-family role (not usable by PSA). */
  tenantFamilyOnly?: boolean;
  /** Frontend page module, when WIRED. */
  page?: string;
  /** Frontend navigation label, when WIRED. */
  label?: string;
  /** RBAC permission(s) the backend requires on this route. */
  permission?: string;
  note?: string;
}

const PLATFORM_ROUTES: ReadonlyArray<{ method: Capability["method"]; route: string; feature: string; permission?: string }> = [
  // Overview / summary
  { method: "GET", route: "/platform/dashboard", feature: "overview" },
  { method: "GET", route: "/admin/summary", feature: "overview" },
  { method: "GET", route: "/monitoring/health", feature: "health" },
  { method: "GET", route: "/monitoring/alerts", feature: "monitoring" },
  { method: "GET", route: "/monitoring/snapshot", feature: "monitoring" },
  // Organizations
  { method: "GET", route: "/platform/organizations", feature: "organizations" },
  { method: "GET", route: "/platform/organizations/:id/detail", feature: "organizations" },
  { method: "GET", route: "/platform/organizations/:id", feature: "organizations" },
  { method: "PATCH", route: "/platform/organizations/:id", feature: "organizations" },
  { method: "POST", route: "/platform/organizations/:id/active", feature: "organizations" },
  // Platform billing
  { method: "GET", route: "/platform/billing/tenants/:tenant_id/balance", feature: "platform-billing" },
  { method: "GET", route: "/platform/billing/tenants/:tenant_id/adjustments", feature: "platform-billing" },
  { method: "POST", route: "/platform/billing/tenants/:tenant_id/adjustments", feature: "platform-billing" },
  { method: "GET", route: "/platform/billing/tenants/:tenant_id/reconciliation", feature: "platform-billing" },
  // Imports
  { method: "GET", route: "/platform/imports", feature: "imports" },
  { method: "GET", route: "/platform/imports/:id", feature: "imports" },
  { method: "GET", route: "/platform/imports/:id/report", feature: "imports" },
  { method: "POST", route: "/platform/imports", feature: "imports" },
  { method: "POST", route: "/platform/imports/:id/validate", feature: "imports" },
  { method: "POST", route: "/platform/imports/:id/execute", feature: "imports" },
  { method: "POST", route: "/platform/imports/:id/resume", feature: "imports" },
  { method: "POST", route: "/platform/imports/:id/cancel", feature: "imports" },
  { method: "POST", route: "/platform/imports/:id/compensate", feature: "imports" },
  // Mail operations / queue
  { method: "GET", route: "/admin/queue/summary", feature: "mail-operations" },
  { method: "GET", route: "/admin/queue/messages", feature: "mail-operations" },
  { method: "GET", route: "/admin/queue/messages/:id", feature: "mail-operations" },
  { method: "GET", route: "/admin/queue/history", feature: "mail-operations" },
  { method: "GET", route: "/admin/queue/export", feature: "mail-operations" },
  { method: "POST", route: "/admin/queue/messages/:id/retry", feature: "mail-operations" },
  { method: "POST", route: "/admin/queue/messages/:id/cancel", feature: "mail-operations" },
  { method: "POST", route: "/admin/queue/messages/:id/bounce", feature: "mail-operations" },
  { method: "POST", route: "/admin/queue/messages/bulk-action", feature: "mail-operations" },
  // Platform mail control — domains
  { method: "GET", route: "/platform/domains/:tenant_id", feature: "domains", permission: "domains.read" },
  { method: "GET", route: "/platform/domains/:tenant_id/:id", feature: "domains", permission: "domains.read" },
  { method: "POST", route: "/platform/domains/:tenant_id/:id/status", feature: "domains", permission: "domains.write" },
  { method: "POST", route: "/platform/domains/:tenant_id/:id/mail-access-mode", feature: "domains", permission: "domains.write" },
  // Platform mail control — mailboxes
  { method: "GET", route: "/platform/mailboxes/:tenant_id", feature: "mailboxes", permission: "mailboxes.read" },
  { method: "GET", route: "/platform/mailboxes/:tenant_id/:id", feature: "mailboxes", permission: "mailboxes.read" },
  { method: "POST", route: "/platform/mailboxes/:tenant_id/:id/status", feature: "mailboxes", permission: "mailboxes.write" },
  { method: "POST", route: "/platform/mailboxes/:tenant_id/:id/quota", feature: "mailboxes", permission: "mailboxes.write" },
  { method: "POST", route: "/platform/mailboxes/:tenant_id/:id/reset-password", feature: "mailboxes", permission: "mailboxes.write" },
  { method: "DELETE", route: "/platform/mailboxes/:tenant_id/:id", feature: "mailboxes", permission: "mailboxes.write" },
  { method: "POST", route: "/platform/mailboxes/:tenant_id/bulk/status", feature: "bulk-mailboxes", permission: "mailboxes.write" },
  // Platform mail control — aliases
  { method: "GET", route: "/platform/aliases/:tenant_id", feature: "aliases", permission: "aliases.read" },
  { method: "GET", route: "/platform/aliases/:tenant_id/:id", feature: "aliases", permission: "aliases.read" },
  { method: "POST", route: "/platform/aliases/:tenant_id", feature: "aliases", permission: "aliases.write" },
  { method: "DELETE", route: "/platform/aliases/:tenant_id/:id", feature: "aliases", permission: "aliases.write" },
  // Platform mail control — groups / memberships
  { method: "GET", route: "/platform/groups/:tenant_id", feature: "groups", permission: "groups.read" },
  { method: "GET", route: "/platform/groups/:tenant_id/:id", feature: "groups", permission: "groups.read" },
  { method: "GET", route: "/platform/groups/:tenant_id/:id/members", feature: "groups", permission: "groups.read" },
  // Platform mail control — suppressions
  { method: "GET", route: "/platform/suppressions/:tenant_id", feature: "suppressions", permission: "suppressions.read" },
  { method: "POST", route: "/platform/suppressions/:tenant_id", feature: "suppressions", permission: "suppressions.write" },
  { method: "GET", route: "/platform/suppressions/:tenant_id/:id", feature: "suppressions", permission: "suppressions.read" },
  { method: "GET", route: "/platform/suppressions/:tenant_id/:id/history", feature: "suppressions", permission: "suppressions.read" },
  { method: "POST", route: "/platform/suppressions/:tenant_id/:id/release", feature: "suppressions", permission: "suppressions.write" },
  { method: "POST", route: "/platform/suppressions/:tenant_id/:id/reactivate", feature: "suppressions", permission: "suppressions.write" },
  { method: "DELETE", route: "/platform/suppressions/:tenant_id/:id", feature: "suppressions", permission: "suppressions.write" },
  { method: "DELETE", route: "/platform/suppressions/:tenant_id", feature: "suppressions", permission: "suppressions.write" },
  // Platform mail control — deliverability
  { method: "GET", route: "/platform/deliverability/:tenant_id/metrics", feature: "deliverability", permission: "deliverability.read" },
  { method: "GET", route: "/platform/deliverability/:tenant_id/events", feature: "deliverability", permission: "deliverability.read" },
  { method: "GET", route: "/platform/deliverability/:tenant_id/events/:id", feature: "deliverability", permission: "deliverability.read" },
  // Platform mail control — relay administration
  { method: "GET", route: "/platform/relays", feature: "relay", permission: "relay.read" },
  { method: "GET", route: "/platform/relays/:id", feature: "relay", permission: "relay.read" },
  { method: "POST", route: "/platform/relays", feature: "relay", permission: "relay.write" },
  { method: "PATCH", route: "/platform/relays/:id", feature: "relay", permission: "relay.write" },
  { method: "POST", route: "/platform/relays/:id/enable", feature: "relay", permission: "relay.write" },
  { method: "POST", route: "/platform/relays/:id/disable", feature: "relay", permission: "relay.write" },
  { method: "POST", route: "/platform/relays/:id/rotate-credentials", feature: "relay", permission: "relay.write" },
  { method: "POST", route: "/platform/relays/:id/test", feature: "relay", permission: "relay.test" },
  { method: "DELETE", route: "/platform/relays/:id", feature: "relay", permission: "relay.write" },
  // Reliability / backups / DR / updates / cluster / monitoring / storage
  { method: "GET", route: "/admin/backups", feature: "reliability" },
  { method: "GET", route: "/admin/backups/:id", feature: "reliability" },
  { method: "GET", route: "/admin/backups/health", feature: "reliability" },
  { method: "GET", route: "/admin/backups/metrics", feature: "reliability" },
  { method: "GET", route: "/admin/backups/schedule", feature: "reliability" },
  { method: "POST", route: "/admin/backups/now", feature: "reliability" },
  { method: "POST", route: "/admin/backups/schedule", feature: "reliability" },
  { method: "POST", route: "/admin/backups/retention", feature: "reliability" },
  { method: "POST", route: "/admin/backups/:id/restore", feature: "reliability" },
  { method: "POST", route: "/admin/backups/:id/validate", feature: "reliability" },
  { method: "GET", route: "/dr/readiness", feature: "dr" },
  { method: "GET", route: "/dr/drills", feature: "dr" },
  { method: "GET", route: "/dr/operations", feature: "dr" },
  { method: "GET", route: "/dr/operations/:job_id", feature: "dr" },
  { method: "POST", route: "/dr/drills", feature: "dr" },
  { method: "POST", route: "/dr/backup", feature: "dr" },
  { method: "POST", route: "/dr/backups/:id/restore", feature: "dr" },
  { method: "GET", route: "/update/status", feature: "reliability" },
  { method: "GET", route: "/update/check", feature: "reliability" },
  { method: "GET", route: "/update/history", feature: "reliability" },
  { method: "GET", route: "/update/preflight", feature: "reliability" },
  { method: "GET", route: "/updates/artifacts/history", feature: "reliability" },
  { method: "GET", route: "/updates/artifacts/:id", feature: "reliability" },
  { method: "POST", route: "/updates/artifacts", feature: "reliability" },
  { method: "POST", route: "/updates/artifacts/:id/apply", feature: "reliability" },
  { method: "POST", route: "/updates/artifacts/:id/rollback", feature: "reliability" },
  { method: "GET", route: "/updates/operations/:job_id", feature: "reliability" },
  { method: "GET", route: "/admin/cluster/status", feature: "cluster" },
  { method: "GET", route: "/admin/storage/volumes", feature: "reliability" },
  { method: "GET", route: "/admin/runtime", feature: "health" },
  // Security / audit / compliance
  { method: "GET", route: "/audit/logs", feature: "audit" },
  { method: "GET", route: "/audit/logs/:id", feature: "audit" },
  { method: "GET", route: "/audit/logs/export", feature: "audit" },
  { method: "GET", route: "/admin/security/antivirus", feature: "security" },
  { method: "GET", route: "/admin/quarantine", feature: "security" },
  { method: "GET", route: "/admin/ssl/acme/status", feature: "security" },
  { method: "GET", route: "/admin/ssl/certificates", feature: "security" },
  { method: "GET", route: "/admin/ssl/certificates/reload", feature: "security" },
  { method: "GET", route: "/admin/ssl/expiry-warnings", feature: "security" },
  { method: "POST", route: "/admin/ssl/certificates", feature: "security" },
  { method: "GET", route: "/guardian/logs", feature: "security" },
  { method: "POST", route: "/guardian/analyze", feature: "security" },
  { method: "GET", route: "/heal/history", feature: "security" },
  { method: "POST", route: "/heal/check/:name", feature: "security" },
  { method: "GET", route: "/firewall/rules", feature: "security" },
  { method: "GET", route: "/firewall/logs", feature: "security" },
  { method: "POST", route: "/firewall/rules", feature: "security" },
  { method: "GET", route: "/admin/log-rules", feature: "security" },
  { method: "POST", route: "/admin/log-rules", feature: "security" },
  { method: "DELETE", route: "/admin/log-rules/:id", feature: "security" },
  { method: "GET", route: "/retention/policies/effective", feature: "retention" },
  { method: "GET", route: "/retention/legal-holds", feature: "retention" },
  { method: "GET", route: "/retention/custody", feature: "retention" },
  { method: "POST", route: "/retention/policies", feature: "retention" },
  { method: "POST", route: "/retention/legal-holds", feature: "retention" },
  { method: "POST", route: "/retention/legal-holds/:id/release", feature: "retention" },
  { method: "POST", route: "/retention/purge/plan", feature: "retention" },
  { method: "POST", route: "/retention/purge/execute", feature: "retention" },
  { method: "POST", route: "/retention/mailboxes/:id/recover", feature: "retention" },
  // Incidents
  { method: "GET", route: "/incidents", feature: "incidents" },
  { method: "GET", route: "/incidents/:id", feature: "incidents" },
  { method: "GET", route: "/incidents/:id/timeline", feature: "incidents" },
  { method: "POST", route: "/incidents", feature: "incidents" },
  { method: "PATCH", route: "/incidents/:id", feature: "incidents" },
  // Automation jobs
  { method: "GET", route: "/platform/automation/jobs", feature: "automation-jobs" },
  { method: "GET", route: "/platform/automation/jobs/:id", feature: "automation-jobs" },
  { method: "POST", route: "/platform/automation/jobs", feature: "automation-jobs" },
  { method: "POST", route: "/platform/automation/jobs/:id/cancel", feature: "automation-jobs" },
  { method: "POST", route: "/platform/automation/jobs/:id/retry", feature: "automation-jobs" },
  // Configuration / capabilities
  { method: "GET", route: "/platform/config", feature: "configuration" },
  { method: "GET", route: "/platform/config/:key", feature: "configuration" },
  { method: "PATCH", route: "/platform/config/:key", feature: "configuration" },
  { method: "GET", route: "/platform/capabilities", feature: "capabilities" },
  { method: "GET", route: "/feature-flags", feature: "configuration" },
  { method: "PUT", route: "/feature-flags/:id", feature: "configuration" },
  { method: "GET", route: "/modules", feature: "health" },
  { method: "GET", route: "/metrics", feature: "health" },
  // Support access
  { method: "GET", route: "/platform/support/grants", feature: "support-access" },
  { method: "GET", route: "/platform/support/grants/:id", feature: "support-access" },
  { method: "POST", route: "/platform/support/grants", feature: "support-access" },
  { method: "POST", route: "/platform/support/grants/:id/activate", feature: "support-access" },
  { method: "POST", route: "/platform/support/grants/:id/revoke", feature: "support-access" },
];

// Tenant-family-only mail-control routes (never callable by PSA). Kept
// so the inventory proves the PSA pages never depend on them and the
// tenant console continues to own them.
const TENANT_MAIL_CONTROL_ROUTES: ReadonlyArray<{ method: Capability["method"]; route: string; feature: string }> = [
  { method: "GET", route: "/domains", feature: "domains" },
  { method: "GET", route: "/domains/:name", feature: "domains" },
  { method: "POST", route: "/domains", feature: "domains" },
  { method: "PATCH", route: "/domains/:name", feature: "domains" },
  { method: "PATCH", route: "/domains/:name/status", feature: "domains" },
  { method: "DELETE", route: "/domains/:name", feature: "domains" },
  { method: "POST", route: "/domains/bulk/status", feature: "domains" },
  { method: "GET", route: "/mailboxes", feature: "mailboxes" },
  { method: "GET", route: "/mailboxes/:id", feature: "mailboxes" },
  { method: "POST", route: "/mailboxes", feature: "mailboxes" },
  { method: "PATCH", route: "/mailboxes/:id/status", feature: "mailboxes" },
  { method: "PATCH", route: "/mailboxes/:id/quota", feature: "mailboxes" },
  { method: "PATCH", route: "/mailboxes/:id/password", feature: "mailboxes" },
  { method: "DELETE", route: "/mailboxes/:id", feature: "mailboxes" },
  { method: "POST", route: "/mailboxes/bulk/status", feature: "mailboxes" },
  { method: "GET", route: "/enterprise/aliases", feature: "aliases" },
  { method: "POST", route: "/enterprise/aliases", feature: "aliases" },
  { method: "DELETE", route: "/enterprise/aliases/:id", feature: "aliases" },
  { method: "GET", route: "/enterprise/groups", feature: "groups" },
  { method: "POST", route: "/enterprise/groups", feature: "groups" },
  { method: "DELETE", route: "/enterprise/groups/:id", feature: "groups" },
  { method: "POST", route: "/enterprise/groups/:id/members", feature: "groups" },
  { method: "DELETE", route: "/enterprise/groups/:id/members/:memberId", feature: "groups" },
  { method: "GET", route: "/enterprise/relay/pools/:id/providers", feature: "relay" },
  { method: "POST", route: "/enterprise/relay/pools", feature: "relay" },
  { method: "POST", route: "/enterprise/relay/providers", feature: "relay" },
  { method: "POST", route: "/enterprise/relay/providers/:id/test", feature: "relay" },
  { method: "POST", route: "/enterprise/relay/routing-rules", feature: "relay" },
  { method: "POST", route: "/enterprise/relay/emergency-override", feature: "relay" },
];

const WIRED_FEATURES: ReadonlySet<string> = new Set([
  "overview",
  "organizations",
  "mail-operations",
  "reliability",
  "security",
  "configuration",
  "platform-billing",
  "imports",
  "automation-jobs",
  "support-access",
  "incidents",
  "retention",
  "audit",
  "dr",
  "health",
  "domains",
  "mailboxes",
  "aliases",
  "groups",
  "bulk-mailboxes",
  "relay",
  "suppressions",
  "deliverability",
]);

function classify(
  route: { method: Capability["method"]; route: string; feature: string; owner?: PortalOwner; requiresSupportAccess?: boolean; tenantFamilyOnly?: boolean; backendRoute?: boolean; permission?: string },
): Capability {
  const wired = WIRED_FEATURES.has(route.feature);
  const owner = route.owner ?? "platform";
  let state: CapabilityState;
  if (route.backendRoute === false) {
    state = "UNAVAILABLE";
  } else if (owner === "tenant") {
    state = "TENANT_ONLY";
  } else if (owner === "platform") {
    state = wired ? "WIRED" : "BACKEND_ONLY";
  } else {
    state = "UNAVAILABLE";
  }
  return {
    feature: route.feature,
    route: route.route,
    method: route.method,
    backendRoute: route.backendRoute !== false,
    state,
    owner,
    requiresSupportAccess: route.requiresSupportAccess,
    tenantFamilyOnly: route.tenantFamilyOnly,
    permission: route.permission,
  };
}

const ALL_ROUTES = [
  ...PLATFORM_ROUTES.map((r) => ({ ...r, owner: "platform" as PortalOwner })),
  ...TENANT_MAIL_CONTROL_ROUTES.map((r) => ({ ...r, owner: "tenant" as PortalOwner, tenantFamilyOnly: true })),
];

export const CAPABILITY_INVENTORY: readonly Capability[] = ALL_ROUTES.map(classify);

// Navigation is organized in groups: "Mail Control" (identity
// inventory) and "Operations" (queue/suppression/deliverability/bulk).
// Visibility is derived from WIRED_FEATURES so a page is never shown
// without a real page behind it; the tenant portal never renders any of
// these (App.tsx allow-lists per portal).
export interface PlatformNavItem {
  label: string;
  route: string;
  feature: string;
  section?: "Mail Control" | "Operations" | "Commercial" | "Security" | "System";
}

export const PLATFORM_NAVIGATION: ReadonlyArray<PlatformNavItem> = [
  { label: "Overview", route: "platform-home", feature: "overview" },
  { label: "Organizations", route: "organizations", feature: "organizations", section: "Commercial" },
  { label: "Platform Billing", route: "platform-billing", feature: "platform-billing" },
  { label: "Domains", route: "platform-domains", feature: "domains", section: "Mail Control" },
  { label: "Mailboxes", route: "platform-mailboxes", feature: "mailboxes" },
  { label: "Aliases", route: "platform-aliases", feature: "aliases" },
  { label: "Groups", route: "platform-groups", feature: "groups" },
  { label: "Relays", route: "platform-relays", feature: "relay" },
  { label: "Mail Queue", route: "mail-operations", feature: "mail-operations", section: "Operations" },
  { label: "Suppressions", route: "platform-suppressions", feature: "suppressions" },
  { label: "Deliverability", route: "platform-deliverability", feature: "deliverability" },
  { label: "Bulk Mailboxes", route: "platform-bulk-mailboxes", feature: "bulk-mailboxes" },
  { label: "Imports", route: "platform-imports", feature: "imports" },
  { label: "Automation Jobs", route: "automation-jobs", feature: "automation-jobs" },
  { label: "Incidents", route: "platform-incidents", feature: "incidents" },
  { label: "Retention", route: "platform-retention", feature: "retention" },
  { label: "DR", route: "platform-dr", feature: "dr" },
  { label: "Reliability", route: "reliability", feature: "reliability" },
  { label: "Audit Log", route: "platform-audit", feature: "audit", section: "Security" },
  { label: "Support Access", route: "support-access", feature: "support-access" },
  { label: "Security", route: "platform-security", feature: "security" },
  { label: "Configuration", route: "platform-configuration", feature: "configuration", section: "System" },
  { label: "Health", route: "health", feature: "health" },
];

export function capabilitiesForFeature(feature: string): readonly Capability[] {
  return CAPABILITY_INVENTORY.filter((c) => c.feature === feature);
}

export function isWired(feature: string): boolean {
  return WIRED_FEATURES.has(feature);
}

export function navigationForFeature(feature: string): readonly PlatformNavItem[] {
  return PLATFORM_NAVIGATION.filter((n) => n.feature === feature);
}

/** Every navigation item must be backed by a WIRED feature. */
export function navigationDeadLinkCheck(): readonly PlatformNavItem[] {
  return PLATFORM_NAVIGATION.filter((n) => !isWired(n.feature));
}
