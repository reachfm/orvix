import { type Page, type Route } from "@playwright/test";

/**
 * End-to-end coverage for the Platform Super Admin Mail Control pages,
 * running against the real built bundle served by scripts/serve-built.mjs.
 *
 * EVERY network call is intercepted with Playwright route mocking — no
 * real API, database, or production data is contacted. The platform
 * shell is exercised through the real /me portal gate (portal:"platform"
 * for PSA flows, portal:"organization" for the tenant regression).
 *
 * Fixtures match the PR #65 backend contracts field-for-field
 * (internal/platform/mailcontrol, deliverability, relay).
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

export const DOMAINS_LIST_FIXTURE = {
  domains: [
    {
      id: 1, tenant_id: 7, name: "acme.example", status: "active", plan: "business",
      mailbox_count: 12, alias_count: 3, dkim_enabled: true, dkim_selector: "mail",
      dmarc_enabled: true, mail_access_mode: "internal_external",
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
    },
    {
      id: 2, tenant_id: 7, name: "beta.example", status: "suspended", plan: "starter",
      mailbox_count: 0, alias_count: 0, dkim_enabled: false, dmarc_enabled: false,
      mail_access_mode: "internal_only",
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-03T00:00:00Z",
    },
  ],
  total: 2,
  limit: 25,
  offset: 0,
};

export const MAILBOXES_LIST_FIXTURE = {
  mailboxes: [
    { id: 101, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "alice@acme.example", name: "Alice", status: "active", is_admin: true, quota_mb: 1024, used_bytes: 1048576, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
    { id: 102, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "bob@acme.example", name: "Bob", status: "suspended", is_admin: false, quota_mb: 512, used_bytes: 0, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-03T00:00:00Z" },
  ],
  total: 2,
  limit: 25,
  offset: 0,
};

export const ALIASES_LIST_FIXTURE = {
  aliases: [
    { id: 11, tenant_id: 7, domain_id: 1, from_addr: "sales@acme.example", to_addr: "alice@acme.example", active: true, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
  ],
  total: 1,
  limit: 25,
  offset: 0,
};

export const GROUPS_LIST_FIXTURE = {
  groups: [
    { id: 21, tenant_id: 7, name: "engineering@acme.example", description: "Engineering team", member_count: 2, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
  ],
  total: 1,
  limit: 25,
  offset: 0,
};

export const GROUP_MEMBERS_FIXTURE = {
  group_id: 21,
  members: ["alice@acme.example", "bob@acme.example"],
};

export const RELAYS_LIST_FIXTURE = {
  relays: [
    {
      id: 5, scope: "global", pool_id: 1, name: "primary", host: "smtp.provider.example", port: 587,
      username: "relay-user", conn_security: "starttls", tls_validation: "strict", priority: 10, weight: 1,
      active: true, rate_limit_per_min: 600, circuit_state: "closed", circuit_failures: 0,
      last_test_at: "2026-01-02T00:00:00Z", last_test_result: "ok", version: 3,
      has_credential: true, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
    },
  ],
  total: 1,
  limit: 50,
  offset: 0,
};

export const SUPPRESSIONS_LIST_FIXTURE = {
  suppressions: [
    {
      id: 31, tenant_id: 7, address: "bounce@example.net", reason: "hard_bounce", source: "smtp_5xx",
      state: "active", version: 1, expires_at: "2099-01-01T00:00:00Z",
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    },
    {
      id: 32, tenant_id: 7, address: "spam@example.net", reason: "complaint", source: "fbl_provider_x",
      state: "released", version: 2, released_at: "2026-01-05T00:00:00Z", released_reason: "operator release",
      created_at: "2026-01-02T00:00:00Z", updated_at: "2026-01-05T00:00:00Z",
    },
  ],
  total: 2,
  limit: 50,
  offset: 0,
};

export const SUPPRESSION_HISTORY_FIXTURE = {
  suppression_id: 31,
  events: [
    { id: 1, suppression_id: 31, tenant_id: 7, event: "created", at: "2026-01-01T00:00:00Z" },
    { id: 2, suppression_id: 31, tenant_id: 7, event: "released", reason: "operator release", at: "2026-01-06T00:00:00Z" },
  ],
};

export const DELIVERABILITY_METRICS_FIXTURE = {
  window: {
    dimension: "tenant", dimension_value: "7", window_start: "2026-01-01T00:00:00Z", window_end: "2026-01-02T00:00:00Z",
    volume: 100, delivered: 90, temp_fail: 5, perm_fail: 2, bounced: 3, complaints: 1, avg_latency_ms: 250,
    delivery_rate: 0.9, bounce_rate: 0.03, complaint_rate: 0.01, temp_fail_rate: 0.05, perm_fail_rate: 0.02,
  },
  summary: {
    tenant_id: 7, window_start: "2026-01-01T00:00:00Z", window_end: "2026-01-02T00:00:00Z",
    volume: 100, delivered: 90, failed: 3, deferred: 5, bounced: 3, policy_denied: 2, suppressed: 4, complaints: 1,
    delivery_rate: 0.9, bounce_rate: 0.03, failure_rate: 0.03, deferred_rate: 0.05,
    by_category: [{ Key: "delivered", Count: 90 }, { Key: "suppressed", Count: 4 }],
    by_domain: [{ Key: "acme.example", Count: 70 }],
    by_provider: [{ Key: "provider-a", Count: 80 }],
    time_buckets: [
      { start: "2026-01-01T00:00:00Z", delivered: 40, failed: 1, other: 2, total: 43 },
      { start: "2026-01-01T01:00:00Z", delivered: 50, failed: 2, other: 5, total: 57 },
    ],
    bucket_size: "hourly",
  },
  volume: 100,
  delivered: 90,
  bounced: 3,
  complaints: 1,
  delivery_rate: 0.9,
  bounce_rate: 0.03,
  complaint_rate: 0.01,
};

export const DELIVERABILITY_EVENTS_FIXTURE = {
  events: [
    { id: 1, tenant_id: 7, dimension: "tenant", dimension_value: "7", type: "delivered", category: "delivered", recorded_at: "2026-01-01T00:00:00Z", latency_ms: 200 },
    { id: 2, tenant_id: 7, dimension: "sending_domain", dimension_value: "acme.example", type: "suppressed", category: "suppressed", recorded_at: "2026-01-01T00:05:00Z" },
  ],
  total: 2,
  limit: 100,
  offset: 0,
};

export const QUEUE_SUMMARY_FIXTURE = {
  metrics: {
    pending: 5, leased: 1, delivering: 2, deferred: 3, delivered: 120, bounced: 4,
    dead_letter: 1, cancelled: 2, total: 138, avg_attempts: 1.4,
  },
};

export const QUEUE_MESSAGES_FIXTURE = {
  messages: [
    { id: 1001, tenant_id: 7, domain_id: 1, from_address: "alice@acme.example", to_address: "x@example.net", recipient_domain: "example.net", status: "pending", priority: 1, attempt_count: 0, max_attempts: 3, next_attempt_at: "2099-01-01T00:00:00Z", last_status_code: 0, delivery_mode: "remote_smtp", remote_host: "", retryable: true, failure_category: "other", created_at: "2026-01-01T00:00:00Z" },
    { id: 1002, tenant_id: 7, domain_id: 1, from_address: "bob@acme.example", to_address: "y@example.net", recipient_domain: "example.net", status: "deferred", priority: 2, attempt_count: 2, max_attempts: 3, next_attempt_at: "2099-01-01T00:00:00Z", last_status_code: 450, last_error: "recipient is suppressed", delivery_mode: "remote_smtp", remote_host: "mx.example.net", retryable: true, failure_category: "suppressed", created_at: "2026-01-01T00:00:00Z" },
    { id: 1003, tenant_id: 7, domain_id: 1, from_address: "alice@acme.example", to_address: "z@example.net", recipient_domain: "example.net", status: "delivered", priority: 1, attempt_count: 1, max_attempts: 3, last_status_code: 250, delivery_mode: "remote_smtp", remote_host: "mx.example.net", retryable: false, created_at: "2026-01-01T00:00:00Z" },
  ],
  total: 3,
  limit: 25,
  offset: 0,
};

export const QUEUE_DETAIL_FIXTURE = {
  message: QUEUE_MESSAGES_FIXTURE.messages[0],
  attempts: [
    { attempt: 1, at: "2026-01-01T00:00:00Z", result: "deferred", error: "", remote_host: "mx.example.net", status_code: 450 },
  ],
};

/** Captured platform API calls for the "no support-context header" assertions. */
export const platformCalls: Array<{ url: string; headers: Record<string, string> }> = [];

export function resetPlatformCalls() {
  platformCalls.length = 0;
}

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
    // Single dispatch handler for every /platform/* call: records the
    // request (for the no-support-header assertion) and fulfills the
    // matching fixture. A URL with no fixture falls back to an empty
    // 200 so unknown sub-calls never 404 the UI under test.
    const platformFixtures: Array<{ match: RegExp; body: unknown }> = [
      { match: /\/platform\/organizations\?/, body: ORGANIZATIONS_FIXTURE },
      { match: /\/platform\/organizations$/, body: ORGANIZATIONS_FIXTURE },
      { match: /\/platform\/domains\/7\/1$/, body: DOMAINS_LIST_FIXTURE.domains[0] },
      { match: /\/platform\/domains\/7\//, body: DOMAINS_LIST_FIXTURE.domains[0] },
      { match: /\/platform\/domains\/7$/, body: DOMAINS_LIST_FIXTURE },
      { match: /\/platform\/domains\/7\?/, body: DOMAINS_LIST_FIXTURE },
      { match: /\/platform\/mailboxes\/7\/101$/, body: MAILBOXES_LIST_FIXTURE.mailboxes[0] },
      { match: /\/platform\/mailboxes\/7\/101\//, body: MAILBOXES_LIST_FIXTURE.mailboxes[0] },
      { match: /\/platform\/mailboxes\/7$/, body: MAILBOXES_LIST_FIXTURE },
      { match: /\/platform\/mailboxes\/7\?/, body: MAILBOXES_LIST_FIXTURE },
      { match: /\/platform\/mailboxes\/7\/bulk\/status/, body: { total: 2, succeeded: 2 } },
      { match: /\/platform\/aliases\/7\/\d+$/, body: ALIASES_LIST_FIXTURE.aliases[0] },
      { match: /\/platform\/aliases\/7$/, body: ALIASES_LIST_FIXTURE },
      { match: /\/platform\/aliases\/7\?/, body: ALIASES_LIST_FIXTURE },
      { match: /\/platform\/groups\/7\/21\/members/, body: GROUP_MEMBERS_FIXTURE },
      { match: /\/platform\/groups\/7\/\d+$/, body: GROUPS_LIST_FIXTURE.groups[0] },
      { match: /\/platform\/groups\/7$/, body: GROUPS_LIST_FIXTURE },
      { match: /\/platform\/groups\/7\?/, body: GROUPS_LIST_FIXTURE },
      { match: /\/platform\/relays\/\d+\/test/, body: { connected: true, tls_negotiated: true, auth_ok: true, duration_ms: 120 } },
      { match: /\/platform\/relays\/\d+\/enable/, body: RELAYS_LIST_FIXTURE.relays[0] },
      { match: /\/platform\/relays\/\d+\/disable/, body: RELAYS_LIST_FIXTURE.relays[0] },
      { match: /\/platform\/relays\/\d+\/rotate-credentials/, body: { relay: RELAYS_LIST_FIXTURE.relays[0], generated_password: "once-secret-xyz", show_once: true } },
      { match: /\/platform\/relays\/\d+$/, body: RELAYS_LIST_FIXTURE.relays[0] },
      { match: /\/platform\/relays\/\d+\//, body: RELAYS_LIST_FIXTURE.relays[0] },
      { match: /\/platform\/relays$/, body: RELAYS_LIST_FIXTURE },
      { match: /\/platform\/relays\?/, body: RELAYS_LIST_FIXTURE },
      { match: /\/platform\/suppressions\/7\/31\/history/, body: SUPPRESSION_HISTORY_FIXTURE },
      { match: /\/platform\/suppressions\/7\/31\/release/, body: { status: "ok", id: 31, state: "released" } },
      { match: /\/platform\/suppressions\/7\/31\/reactivate/, body: { status: "ok", id: 31, state: "active" } },
      { match: /\/platform\/suppressions\/7\/31$/, body: SUPPRESSIONS_LIST_FIXTURE.suppressions[0] },
      { match: /\/platform\/suppressions\/7\/\d+$/, body: SUPPRESSIONS_LIST_FIXTURE.suppressions[0] },
      { match: /\/platform\/suppressions\/7\/\d+\//, body: SUPPRESSION_HISTORY_FIXTURE },
      { match: /\/platform\/suppressions\/7$/, body: SUPPRESSIONS_LIST_FIXTURE },
      { match: /\/platform\/suppressions\/7\?/, body: SUPPRESSIONS_LIST_FIXTURE },
      { match: /\/platform\/deliverability\/7\/metrics\?/, body: DELIVERABILITY_METRICS_FIXTURE },
      { match: /\/platform\/deliverability\/7\/events\?/, body: DELIVERABILITY_EVENTS_FIXTURE },
      { match: /\/platform\/deliverability\/7\/events$/, body: DELIVERABILITY_EVENTS_FIXTURE },
      { match: /\/platform\/deliverability\/7\/events\//, body: DELIVERABILITY_EVENTS_FIXTURE.events[0] },
    ];
    await page.route("**/api/v1/platform/**", (r) => {
      const url = r.request().url();
      platformCalls.push({ url, headers: r.request().headers() });
      const hit = platformFixtures.find((f) => f.match.test(url));
      return json(r, hit ? hit.body : {});
    });

    // Tenant-family routes must NEVER be called by PSA pages.
    await page.route("**/api/v1/domains*", (r) => json(r, { error: "forbidden", code: "FORBIDDEN" }, 403));
    await page.route("**/api/v1/mailboxes*", (r) => json(r, { error: "forbidden", code: "FORBIDDEN" }, 403));
    await page.route("**/api/v1/enterprise/**", (r) => json(r, { error: "forbidden", code: "FORBIDDEN" }, 403));
  }

  await page.route("**/api/v1/admin/queue/summary", (r) => json(r, QUEUE_SUMMARY_FIXTURE));
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

/** Applies the tenant scope selector to tenant 7 (idempotent). */
export async function applyTenantScope(page: Page, label: string) {
  const scoped = page.getByText(/Scoped to tenant 7/i);
  if (await scoped.isVisible().catch(() => false)) return;
  await page.getByLabel("Select tenant").selectOption("7");
  await page.getByRole("button", { name: "Apply scope" }).click();
  await scoped.waitFor();
}
