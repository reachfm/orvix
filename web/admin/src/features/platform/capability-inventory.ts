// Capability inventory for the Platform Super Admin console.
//
// This is the machine-verifiable source of truth for what this frontend
// may render and navigate to. Every entry is derived from a real
// platformMW-gated route in internal/api/router.go (verified at
// integration SHA aeec470). Classifications:
//
//   - WIRED:        real backend route + real frontend page in this console
//   - BACKEND_ONLY: real backend route, no frontend page yet (never invent one)
//   - UNAVAILABLE:  no real backend route exists; the UI must not fabricate it
//
// The Platform Super Admin shell MUST NOT include any tenant-owned route
// (portal separation rule). Tenant-owned routes live in the organization
// portal inventory, which is out of scope for this module.

export type CapabilityState = "WIRED" | "BACKEND_ONLY" | "UNAVAILABLE";

export interface Capability {
  feature: string;
  route: string;
  method: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  /** Real backend route exists (platformMW-gated). */
  backendRoute: boolean;
  state: CapabilityState;
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
]);

function classify(route: { method: Capability["method"]; route: string; feature: string }): Capability {
  const wired = WIRED_FEATURES.has(route.feature);
  return {
    feature: route.feature,
    route: route.route,
    method: route.method,
    backendRoute: true,
    state: wired ? "WIRED" : "BACKEND_ONLY",
  };
}

export const CAPABILITY_INVENTORY: readonly Capability[] = PLATFORM_ROUTES.map(classify);

export const PLATFORM_NAVIGATION: ReadonlyArray<{ label: string; route: string; feature: string }> = [
  { label: "Overview", route: "platform-home", feature: "overview" },
  { label: "Organizations", route: "organizations", feature: "organizations" },
  { label: "Platform Billing", route: "platform-billing", feature: "platform-billing" },
  { label: "Imports", route: "platform-imports", feature: "imports" },
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
