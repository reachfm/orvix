import { type Page, type Route } from "@playwright/test";

/**
 * End-to-end coverage for the Platform Super Admin Mail Control pages,
 * running against the real built bundle served by scripts/serve-built.mjs.
 *
 * EVERY network call is intercepted with Playwright route mocking — no
 * real API, database, or production data is contacted. The platform
 * shell is exercised through the real /me portal gate (portal:"platform"
 * for PSA flows, portal:"organization" for the tenant regression).
 */

export const PSA_ME = {
  id: 99,
  email: "ops@orvix.email",
  role: "platform_super_admin",
  portal: "platform",
};

export const TENANT_ADMIN_ME = {
  id: 1,
  email: "admin@example.com",
  role: "tenant_admin",
  portal: "organization",
  tenant_id: 1,
};

export const ORGANIZATIONS_FIXTURE = {
  organizations: [
    { id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" },
    { id: 8, name: "Beta", slug: "beta", domain: "beta.example", plan: "starter", active: true, mailbox_count: 3, domain_count: 1, created_at: "2026-01-02T00:00:00Z" },
  ],
  total: 2,
};

export const SUPPORT_GRANTS_FIXTURE = {
  grants: [
    {
      id: 11,
      ticket_ref: "T-100",
      reason: "mail control review",
      target_tenant_id: 7,
      target_tenant_name: "Acme",
      granted_by_id: 99,
      permission_scope: "domain_view",
      status: "active",
      expires_at: "2099-01-01T00:00:00Z",
      emergency_break_glass: false,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    },
  ],
};

export const DOMAINS_LIST_FIXTURE = [
  { id: 1, domain: "acme.example", plan: "business", status: "active", mailbox_count: 12 },
  { id: 2, domain: "mail.acme.example", plan: "business", status: "suspended", mailbox_count: 0 },
];

export const MAILBOXES_LIST_FIXTURE = [
  { id: 101, email: "alice@acme.example", domain: "acme.example", status: "active", is_admin: true, created_at: "2026-01-01T00:00:00Z" },
  { id: 102, email: "bob@acme.example", domain: "acme.example", status: "suspended", is_admin: false, created_at: "2026-01-02T00:00:00Z" },
];

export const QUEUE_SUMMARY_FIXTURE = {
  metrics: {
    pending: 5, leased: 1, delivering: 2, deferred: 3, delivered: 120, bounced: 4,
    dead_letter: 1, cancelled: 2, total: 138, avg_attempts: 1.4,
  },
};

export const QUEUE_MESSAGES_FIXTURE = {
  messages: [
    { id: 1001, from_address: "alice@acme.example", to_address: "x@example.net", recipient_domain: "example.net", status: "pending", priority: 1, attempt_count: 0, max_attempts: 3, next_attempt_at: "2099-01-01T00:00:00Z", last_status_code: 0, delivery_mode: "remote_smtp", remote_host: "", created_at: "2026-01-01T00:00:00Z" },
    { id: 1002, from_address: "bob@acme.example", to_address: "y@example.net", recipient_domain: "example.net", status: "deferred", priority: 2, attempt_count: 2, max_attempts: 3, next_attempt_at: "2099-01-01T00:00:00Z", last_status_code: 450, last_error: "temporary failure", delivery_mode: "remote_smtp", remote_host: "mx.example.net", created_at: "2026-01-01T00:00:00Z" },
  ],
  total: 2,
  limit: 25,
  offset: 0,
};

export const QUEUE_DETAIL_FIXTURE = {
  message: QUEUE_MESSAGES_FIXTURE.messages[0],
  attempts: [
    { attempt: 1, at: "2026-01-01T00:00:00Z", result: "deferred", error: "", remote_host: "mx.example.net", status_code: 450 },
  ],
};

/**
 * Installs the mocked backend for PSA Mail Control flows. portalOption
 * selects which identity the /me endpoint returns: "platform" exercises
 * the PSA shell, "organization" exercises the tenant regression.
 */
export async function mockMailControlAPI(page: Page, opts: { portal: "platform" | "organization" } = { portal: "platform" }) {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  await page.route("**/api/**", (r) => json(r, {}));
  await page.route("**/api/v1/csrf-token", (r) => json(r, { csrf_token: "test-csrf-token" }));
  await page.route("**/api/v1/me", (r) => json(r, opts.portal === "platform" ? PSA_ME : TENANT_ADMIN_ME));

  if (opts.portal === "platform") {
    await page.route("**/api/v1/platform/organizations*", (r) => json(r, ORGANIZATIONS_FIXTURE));
    await page.route("**/api/v1/platform/support/grants", (r) => json(r, SUPPORT_GRANTS_FIXTURE));
    await page.route("**/api/v1/domains", (r) => json(r, DOMAINS_LIST_FIXTURE));
    await page.route("**/api/v1/mailboxes", (r) => json(r, MAILBOXES_LIST_FIXTURE));
  }

  await page.route("**/api/v1/admin/queue/summary", (r) => json(r, QUEUE_SUMMARY_FIXTURE));
  // Order matters: Playwright matches the MOST RECENT route first. The
  // specific routes must be registered AFTER the broader ones so they
  // win: bare list, ?-query list, :id detail, then the literal
  // /bulk-action and action sub-routes LAST (most specific wins).
  await page.route("**/api/v1/admin/queue/messages", (r) => json(r, QUEUE_MESSAGES_FIXTURE));
  await page.route("**/api/v1/admin/queue/messages?*", (r) => json(r, QUEUE_MESSAGES_FIXTURE));
  await page.route("**/api/v1/admin/queue/messages/*", (r) => json(r, QUEUE_DETAIL_FIXTURE));
  await page.route("**/api/v1/admin/queue/messages/bulk-action", (r) => json(r, { action: "retry", total: 1, succeeded: 1, results: [{ id: 1001, success: true }] }, 200));
  await page.route("**/api/v1/admin/queue/messages/*/retry", (r) => json(r, { status: "ok", id: 1001 }, 200));
  await page.route("**/api/v1/admin/queue/messages/*/bounce", (r) => json(r, { status: "ok", id: 1001 }, 200));
  await page.route("**/api/v1/admin/queue/messages/*/cancel", (r) => json(r, { status: "ok", id: 1001 }, 200));
}

export async function openPlatformShell(page: Page) {
  await page.goto("/admin", { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: /Orvix Admin/i }).waitFor();
}
