/**
 * Feature matrix used by the /features page. Each row is a
 * capability and the columns are the plans that include it.
 * The boolean values come from src/lib/plans.ts so they can
 * never disagree with the pricing page.
 */

import { PLANS, type PlanId } from "../lib/plans";

export interface FeatureRow {
  group: string;
  capability: string;
  body: string;
  planIds: PlanId[];
}

export const FEATURE_ROWS: FeatureRow[] = [
  {
    group: "Core",
    capability: "Custom domain",
    body: "Bring your own domain. We serve mail for it without you running a server.",
    planIds: ["free", "starter", "business", "enterprise"],
  },
  {
    group: "Core",
    capability: "Webmail client",
    body: "Webmail with folders, search, drafts, compose, attachments, settings, and notifications.",
    planIds: ["free", "starter", "business", "enterprise"],
  },
  {
    group: "Core",
    capability: "Mobile sync (IMAP / JMAP)",
    body: "Standard IMAP and JMAP so your phone's mail app just works.",
    planIds: ["free", "starter", "business", "enterprise"],
  },
  {
    group: "Core",
    capability: "MTA-STS policy",
    body: "Publish an MTA-STS policy so other servers only deliver to you over verified TLS.",
    planIds: ["starter", "business", "enterprise"],
  },
  {
    group: "Core",
    capability: "API access",
    body: "Use the documented authenticated customer and enterprise HTTP routes.",
    planIds: ["starter", "business", "enterprise"],
  },
  {
    group: "Core",
    capability: "Team seats",
    body: "Invite teammates with roles: owner, admin, operator, or member.",
    planIds: ["starter", "business", "enterprise"],
  },
  {
    group: "Mailboxes",
    capability: "Distribution groups",
    body: "Send mail to a list and have it delivered to every member.",
    planIds: ["business", "enterprise"],
  },
  {
    group: "Mailboxes",
    capability: "Catch-all addresses",
    body: "Deliver any address at your domain that doesn't exist as a real mailbox to a fallback.",
    planIds: ["business", "enterprise"],
  },
  {
    group: "Mailboxes",
    capability: "Mail forwarding",
    body: "Forward mail from a mailbox to an external address.",
    planIds: ["business", "enterprise"],
  },
  {
    group: "Mailboxes",
    capability: "Inbox rules",
    body: "Move, label, forward, or reject incoming messages based on sender, subject, or content.",
    planIds: ["free", "starter", "business", "enterprise"],
  },
  {
    group: "Mailboxes",
    capability: "Vacation reply",
    body: "Automatic out-of-office replies for a date range.",
    planIds: ["free", "starter", "business", "enterprise"],
  },
  {
    group: "Mailboxes",
    capability: "Signatures",
    body: "Per-mailbox default signatures, with optional images and links.",
    planIds: ["free", "starter", "business", "enterprise"],
  },
  {
    group: "Admin",
    capability: "Encrypted backups",
    body: "Create, list, inspect, and restore backups; storage and encryption depend on deployment configuration.",
    planIds: ["business", "enterprise"],
  },
  {
    group: "Admin",
    capability: "Audit log",
    body: "Every administrative action logged with actor, target, timestamp, and IP.",
    planIds: ["business", "enterprise"],
  },
  {
    group: "Admin",
    capability: "Multi-factor authentication",
    body: "TOTP-based MFA for every account, with optional enforcement at the org level.",
    planIds: ["business", "enterprise"],
  },
  {
    group: "Enterprise",
    capability: "SAML / OIDC SSO",
    body: "Sign in with your own identity provider — Okta, Entra ID, Google Workspace, and more.",
    planIds: ["enterprise"],
  },
  {
    group: "Enterprise",
    capability: "99.99% uptime SLA",
    body: "Published Enterprise uptime SLA; remedies are defined in the executed order and terms.",
    planIds: ["enterprise"],
  },
  {
    group: "Enterprise",
    capability: "Priority support",
    body: "Priority support entitlement. Channels and response terms are defined in the executed order.",
    planIds: ["enterprise"],
  },
];

export function rowsForPlan(planId: PlanId): FeatureRow[] {
  return FEATURE_ROWS.filter((r) => r.planIds.includes(planId));
}
