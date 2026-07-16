/**
 * Plan catalog — the ONLY source of truth for pricing on the
 * marketing site. The numbers are pinned to the Launch
 * Specification v1.0 §0.4 and the Go billing seed
 * (internal/billing/service.go SeedDefaultPlans()).
 *
 * Do NOT invent tiers, do NOT change these numbers without
 * a corresponding backend change. The vitest unit test
 * tests/unit/plans-truth.test.ts asserts every value below
 * against the spec text so a stray edit fails the build.
 */

export type PlanId = "free" | "starter" | "business" | "enterprise";

export interface PlanFeature {
  /** Stable feature key consumed by the backend entitlement check. */
  key: string;
  /** Human-readable label shown on the pricing table. */
  label: string;
}

export interface Plan {
  id: PlanId;
  name: string;
  /** USD per month, decimal. */
  usdMonthly: number;
  /** USD per year, decimal. The spec defines this as 10× monthly
   * (a 16% discount vs paying monthly). */
  usdYearly: number;
  domains: number;
  mailboxes: number;
  /** Storage in bytes, derived from the spec string ("1 GB" etc). */
  storageBytes: number;
  sendsPerDay: number;
  features: PlanFeature[];
  isFeatured?: boolean;
}

const GB = 1024 * 1024 * 1024;
const TB = 1024 * GB;

export const PLANS: Plan[] = [
  {
    id: "free",
    name: "Free",
    usdMonthly: 0,
    usdYearly: 0,
    domains: 1,
    mailboxes: 5,
    storageBytes: 1 * GB,
    sendsPerDay: 500,
    features: [
      { key: "custom_domain", label: "Custom domain" },
      { key: "dkim", label: "DKIM signing" },
    ],
  },
  {
    id: "starter",
    name: "Starter",
    usdMonthly: 9.99,
    usdYearly: 99.9,
    domains: 3,
    mailboxes: 25,
    storageBytes: 10 * GB,
    sendsPerDay: 2000,
    features: [
      { key: "custom_domain", label: "Custom domain" },
      { key: "dkim", label: "DKIM signing" },
      { key: "mta_sts", label: "MTA-STS policy" },
      { key: "api", label: "API access" },
      { key: "team", label: "Team seats" },
    ],
  },
  {
    id: "business",
    name: "Business",
    usdMonthly: 29.99,
    usdYearly: 299.9,
    domains: 10,
    mailboxes: 100,
    storageBytes: 100 * GB,
    sendsPerDay: 10000,
    isFeatured: true,
    features: [
      { key: "custom_domain", label: "Custom domain" },
      { key: "dkim", label: "DKIM signing" },
      { key: "mta_sts", label: "MTA-STS policy" },
      { key: "api", label: "API access" },
      { key: "team", label: "Team seats" },
      { key: "groups", label: "Distribution groups" },
      { key: "catch_all", label: "Catch-all addresses" },
      { key: "mail_forwarding", label: "Mail forwarding" },
      { key: "backup", label: "Encrypted backups" },
      { key: "audit_log", label: "Audit log" },
      { key: "mfa", label: "Multi-factor auth" },
    ],
  },
  {
    id: "enterprise",
    name: "Enterprise",
    usdMonthly: 99.99,
    usdYearly: 999.9,
    domains: 100,
    mailboxes: 1000,
    storageBytes: 1 * TB,
    sendsPerDay: 100000,
    features: [
      { key: "custom_domain", label: "Custom domain" },
      { key: "dkim", label: "DKIM signing" },
      { key: "mta_sts", label: "MTA-STS policy" },
      { key: "api", label: "API access" },
      { key: "team", label: "Team seats" },
      { key: "groups", label: "Distribution groups" },
      { key: "catch_all", label: "Catch-all addresses" },
      { key: "mail_forwarding", label: "Mail forwarding" },
      { key: "backup", label: "Encrypted backups" },
      { key: "audit_log", label: "Audit log" },
      { key: "mfa", label: "Multi-factor auth" },
      { key: "sso", label: "SAML / OIDC SSO" },
      { key: "sla", label: "99.99% uptime SLA" },
      { key: "priority_support", label: "Priority support" },
    ],
  },
];

/** Lookup a plan by its id. */
export function getPlan(id: PlanId): Plan {
  const p = PLANS.find((x) => x.id === id);
  if (!p) {
    throw new Error(`Unknown plan: ${id}`);
  }
  return p;
}

/** Format a USD amount. $0 renders as "Free". */
export function formatPrice(usd: number, cycle: "monthly" | "yearly"): string {
  if (usd === 0) {
    return "Free";
  }
  const fixed = usd.toFixed(2);
  if (cycle === "yearly") {
    return `$${fixed}/yr`;
  }
  return `$${fixed}/mo`;
}

/** Human-readable storage size. */
export function formatStorage(bytes: number): string {
  if (bytes >= TB) {
    return `${bytes / TB} TB`;
  }
  if (bytes >= GB) {
    return `${bytes / GB} GB`;
  }
  return `${bytes} MB`;
}

/** All plan ids in the order they appear in the spec. */
export const PLAN_IDS: PlanId[] = PLANS.map((p) => p.id);

/** All entitlement keys across all plans (union). */
export const ALL_FEATURE_KEYS: string[] = Array.from(
  new Set(PLANS.flatMap((p) => p.features.map((f) => f.key))),
);
