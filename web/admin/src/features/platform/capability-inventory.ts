// Capability inventory for the Platform Super Admin console.
//
// This is the machine-verifiable source of truth for what this frontend
// may render and navigate to. Every entry is derived from a real route
// in internal/api/router.go (verified at integration SHA aeec470 and
// the Mail Control batch). Classifications:
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
// Portal ownership model (verified from router.go):
//   - platformMW routes (PSA-direct): /admin/queue/*, /dr/*, /retention/*,
//     /platform/*, /incidents, /audit/logs, /monitoring/*, /admin/backups/*,
//     /update*, /updates/*, /firewall/*, /guardian/*, /heal/*, /modules,
//     /feature-flags, /admin/security/*, /admin/ssl/*, /admin/settings/*,
//     /admin/storage/*, /admin/cluster/*, /admin/runtime
//   - Support-access-aware reads (PSA with active grant + X-Support-Tenant-ID):
//     GET /domains, GET /mailboxes, GET /users, GET
//     /enterprise/organizations/current
//   - Tenant-family-only (TENANT_ONLY): all /domains/* detail + mutations,
//     all /mailboxes/* detail + mutations, /enterprise/*, /customer/*,
//     /admin/relay/*, aliases, groups
//
// A Platform Super Admin has no owning tenant. It must NEVER fabricate a
// tenant ID or call a tenant-only route. Support-access reads require an
// explicit, backend-validated grant; the UI models this through the
// tenant-context feature.

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
  note?: string;
}

const PLATFORM_ROUTES: ReadonlyArray<{ method: Capability["method"]; route: string; feature: string }> = [
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
  "suppression",
  "deliverability",
]);

// Mail-control routes with verified ownership (see header comment for
// the middleware evidence). owner values:
//   - "platform": PSA-direct (platformMW)
//   - "tenant": tenant-family role only (tenantCompatMW[0] role gate)
//   - "shared": support-access-aware read (PSA needs grant + tenant header)
const MAIL_CONTROL_ROUTES: ReadonlyArray<{
  method: Capability["method"];
  route: string;
  feature: string;
  owner: PortalOwner;
  requiresSupportAccess?: boolean;
  tenantFamilyOnly?: boolean;
  backendRoute?: boolean;
}> = [
  // Platform domains (list = support-access-aware read; detail/mutations = tenant-only)
  { method: "GET", route: "/domains", feature: "domains", owner: "shared", requiresSupportAccess: true },
  { method: "GET", route: "/domains/:name", feature: "domains", owner: "tenant", tenantFamilyOnly: true },
  { method: "GET", route: "/domains/:name/audit", feature: "domains", owner: "tenant", tenantFamilyOnly: true },
  { method: "GET", route: "/domains/export", feature: "domains", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/domains", feature: "domains", owner: "tenant", tenantFamilyOnly: true },
  { method: "PATCH", route: "/domains/:name", feature: "domains", owner: "tenant", tenantFamilyOnly: true },
  { method: "PATCH", route: "/domains/:name/status", feature: "domains", owner: "tenant", tenantFamilyOnly: true },
  { method: "DELETE", route: "/domains/:name", feature: "domains", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/domains/bulk/status", feature: "domains", owner: "tenant", tenantFamilyOnly: true },
  // Platform mailboxes (list = support-access-aware read; detail/mutations = tenant-only)
  { method: "GET", route: "/mailboxes", feature: "mailboxes", owner: "shared", requiresSupportAccess: true },
  { method: "GET", route: "/mailboxes/:id", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "GET", route: "/mailboxes/:id/audit", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "GET", route: "/mailboxes/export", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/mailboxes", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "PATCH", route: "/mailboxes/:id/status", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "PATCH", route: "/mailboxes/:id/quota", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "PATCH", route: "/mailboxes/:id/password", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "PATCH", route: "/mailboxes/:id/protocols", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "DELETE", route: "/mailboxes/:id", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/mailboxes/bulk/status", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/mailboxes/import", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/mailboxes/import/dry-run", feature: "mailboxes", owner: "tenant", tenantFamilyOnly: true },
  // Aliases / groups (tenant-family only; no PSA route exists)
  { method: "GET", route: "/enterprise/aliases", feature: "aliases", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/enterprise/aliases", feature: "aliases", owner: "tenant", tenantFamilyOnly: true },
  { method: "DELETE", route: "/enterprise/aliases/:id", feature: "aliases", owner: "tenant", tenantFamilyOnly: true },
  { method: "GET", route: "/enterprise/groups", feature: "groups", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/enterprise/groups", feature: "groups", owner: "tenant", tenantFamilyOnly: true },
  { method: "DELETE", route: "/enterprise/groups/:id", feature: "groups", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/enterprise/groups/:id/members", feature: "groups", owner: "tenant", tenantFamilyOnly: true },
  { method: "DELETE", route: "/enterprise/groups/:id/members/:memberId", feature: "groups", owner: "tenant", tenantFamilyOnly: true },
  // Relay (tenant-family only)
  { method: "GET", route: "/enterprise/relay/pools/:id/providers", feature: "relay", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/enterprise/relay/pools", feature: "relay", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/enterprise/relay/providers", feature: "relay", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/enterprise/relay/providers/:id/test", feature: "relay", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/enterprise/relay/routing-rules", feature: "relay", owner: "tenant", tenantFamilyOnly: true },
  { method: "POST", route: "/enterprise/relay/emergency-override", feature: "relay", owner: "tenant", tenantFamilyOnly: true },
  // Mail queue (platform-owned) — already present in PLATFORM_ROUTES;
  // the base array maps to owner "platform" in ALL_ROUTES, so the queue
  // routes are intentionally NOT duplicated here.
  // Suppression / deliverability: no backend routes exist anywhere
  { method: "GET", route: "/suppression", feature: "suppression", owner: "none", backendRoute: false },
  { method: "GET", route: "/deliverability", feature: "deliverability", owner: "none", backendRoute: false },
];

function classify(
  route: { method: Capability["method"]; route: string; feature: string; owner?: PortalOwner; requiresSupportAccess?: boolean; tenantFamilyOnly?: boolean; backendRoute?: boolean },
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
  } else if (owner === "shared") {
    state = "WIRED"; // support-access-aware read; the tenant-context feature gates it
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
  };
}

const ALL_ROUTES = [
  ...PLATFORM_ROUTES.map((r) => ({ ...r, owner: "platform" as PortalOwner })),
  ...MAIL_CONTROL_ROUTES,
];

export const CAPABILITY_INVENTORY: readonly Capability[] = ALL_ROUTES.map(classify);

export const PLATFORM_NAVIGATION: ReadonlyArray<{ label: string; route: string; feature: string }> = [
  { label: "Overview", route: "platform-home", feature: "overview" },
  { label: "Organizations", route: "organizations", feature: "organizations" },
  { label: "Platform Billing", route: "platform-billing", feature: "platform-billing" },
  { label: "Imports", route: "platform-imports", feature: "imports" },
  { label: "Domains", route: "platform-domains", feature: "domains" },
  { label: "Mailboxes", route: "platform-mailboxes", feature: "mailboxes" },
  { label: "Mail Operations", route: "mail-operations", feature: "mail-operations" },
  { label: "Automation Jobs", route: "automation-jobs", feature: "automation-jobs" },
  { label: "Incidents", route: "platform-incidents", feature: "incidents" },
  { label: "Retention", route: "platform-retention", feature: "retention" },
  { label: "DR", route: "platform-dr", feature: "dr" },
  { label: "Reliability", route: "reliability", feature: "reliability" },
  { label: "Audit Log", route: "platform-audit", feature: "audit" },
  { label: "Support Access", route: "support-access", feature: "support-access" },
  { label: "Security", route: "platform-security", feature: "security" },
  { label: "Configuration", route: "platform-configuration", feature: "configuration" },
  { label: "Health", route: "health", feature: "health" },
];

export function capabilitiesForFeature(feature: string): readonly Capability[] {
  return CAPABILITY_INVENTORY.filter((c) => c.feature === feature);
}

export function isWired(feature: string): boolean {
  return WIRED_FEATURES.has(feature);
}
