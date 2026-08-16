import { type Page, type Route } from "@playwright/test";
import { PSA_ME, ORGANIZATIONS_FIXTURE } from "./mail-control-fixtures";

/**
 * Fixtures for the Domain/Mailbox/Bulk provisioning acceptance specs.
 * Independent of mail-control-fixtures.ts's single-dispatch /platform/**
 * handler because provisioning needs METHOD-aware routing (POST create
 * vs GET list on the same tenant path) plus multipart/blob handling for
 * the bulk import workflow, which that handler does not model.
 *
 * Every network call is intercepted — no real backend is contacted.
 */

export const CSV_TEMPLATE_BYTES = "email,name,quota_mb,access_mode\n";
export const XLSX_TEMPLATE_BYTES = "PK\x03\x04-fake-xlsx-bytes-for-test-";

export const createdCalls: Array<{ url: string; method: string; headers: Record<string, string>; body: string }> = [];

export function resetCreatedCalls() {
  createdCalls.length = 0;
}

export async function mockProvisioningAPI(page: Page) {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  await page.route("**/api/**", (r) => json(r, {}));
  await page.route("**/api/v1/csrf-token", (r) => json(r, { csrf_token: "test-csrf-token" }));
  await page.route("**/api/v1/me", (r) => json(r, PSA_ME));
  await page.route(/\/platform\/organizations(\?|$)/, (r) => json(r, ORGANIZATIONS_FIXTURE));

  let domainVersion = 1;
  let mailboxVersion = 1;
  let mailboxAccessMode = "internal_only";
  let mailboxEffectiveAccessMode = "internal_only";

  const DOMAIN = () => ({
    id: 1, tenant_id: 7, name: "acme.example", status: "active", plan: "business",
    mailbox_count: 12, alias_count: 3, dkim_enabled: true, dkim_selector: "mail",
    dmarc_enabled: true, mail_access_mode: "internal_external",
    created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
    version: domainVersion,
  });

  const MAILBOX = () => ({
    id: 101, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "alice@acme.example",
    name: "Alice", status: "active", is_admin: false, quota_mb: 1024, used_bytes: 1048576,
    mail_access_mode: mailboxAccessMode, effective_mail_access_mode: mailboxEffectiveAccessMode,
    version: mailboxVersion, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
  });

  // ── Domains ──────────────────────────────────────────────────────
  await page.route(/\/api\/v1\/platform\/domains\/7(\?.*)?$/, async (r) => {
    const req = r.request();
    createdCalls.push({ url: req.url(), method: req.method(), headers: await req.headers(), body: req.postData() || "" });
    if (req.method() === "GET") {
      return json(r, { domains: [DOMAIN()], total: 1, limit: 25, offset: 0 });
    }
    if (req.method() === "POST") {
      const body = JSON.parse(req.postData() || "{}");
      if (!body.name) return json(r, { code: "VALIDATION_FAILED", message: "name is required" }, 400);
      domainVersion += 1;
      return json(r, {
        domain: { ...DOMAIN(), name: body.name, description: body.description || "" },
        effective_limits: {
          max_mailboxes: 100, max_aliases: 50, default_mailbox_quota_mb: 1024, max_mailbox_quota_mb: 10240,
          used_mailboxes: 0, used_aliases: 0, used_storage_mb: 0, plan_max_mailboxes: 100, plan_max_aliases: 50,
          plan_default_quota_mb: 1024, plan_max_quota_mb: 10240,
        },
        dkim: body.dkim?.generate ? { selector: body.dkim.selector || "mail", public_dns_txt: "v=DKIM1; k=rsa; p=FAKEPUBLICKEYDATA", dns_record_name: `${body.dkim.selector || "mail"}._domainkey.${body.name}` } : undefined,
        dns_requirements: [
          { name: body.name, type: "MX", value: "mail.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "inbound mail routing" },
          { name: body.name, type: "TXT", value: "v=spf1 include:orvix.email ~all", ttl: 3600, required: true, purpose: "SPF" },
        ],
        dns_next_step: "Publish the DNS records above at your registrar, then verify.",
        public_dns_changed: false,
        plan: { plan: "business", max_domains: 10, used_domains: 1, remaining_domains: 9, max_mailboxes: 100, used_mailboxes: 0, remaining_mailboxes: 100, storage_limit_mb: 102400, storage_used_mb: 0 },
        idempotent: false,
      }, 201);
    }
    return json(r, {});
  });

  // ── Mailboxes ────────────────────────────────────────────────────
  await page.route(/\/api\/v1\/platform\/mailboxes\/7(\?.*)?$/, async (r) => {
    const req = r.request();
    createdCalls.push({ url: req.url(), method: req.method(), headers: await req.headers(), body: req.postData() || "" });
    if (req.method() === "GET") {
      return json(r, { mailboxes: [MAILBOX()], total: 1, limit: 25, offset: 0 });
    }
    if (req.method() === "POST") {
      const body = JSON.parse(req.postData() || "{}");
      if (!body.mail_access_mode) return json(r, { code: "VALIDATION_FAILED", message: "mail_access_mode is required" }, 400);
      mailboxAccessMode = body.mail_access_mode;
      mailboxEffectiveAccessMode = body.mail_access_mode;
      return json(r, { mailbox: { ...MAILBOX(), email: body.email } }, 201);
    }
    return json(r, {});
  });

  await page.route(/\/api\/v1\/platform\/mailboxes\/7\/101(\?.*)?$/, async (r) => {
    const req = r.request();
    createdCalls.push({ url: req.url(), method: req.method(), headers: await req.headers(), body: req.postData() || "" });
    return json(r, MAILBOX());
  });

  let accessModeAttempt = 0;
  await page.route(/\/api\/v1\/platform\/mailboxes\/7\/101\/access-mode$/, async (r) => {
    const req = r.request();
    createdCalls.push({ url: req.url(), method: req.method(), headers: await req.headers(), body: req.postData() || "" });
    const body = JSON.parse(req.postData() || "{}");
    accessModeAttempt += 1;
    if (accessModeAttempt === 1) {
      // First attempt in the conflict test simulates a stale version.
      return json(r, { code: "PRECONDITION_FAILED", message: "mailbox was modified by another request" }, 412);
    }
    if (body.expected_version !== mailboxVersion) {
      return json(r, { code: "PRECONDITION_FAILED", message: "mailbox was modified by another request" }, 412);
    }
    mailboxVersion += 1;
    mailboxAccessMode = body.mail_access_mode;
    mailboxEffectiveAccessMode = body.mail_access_mode;
    return json(r, { id: 101, mail_access_mode: mailboxAccessMode, effective_mail_access_mode: mailboxEffectiveAccessMode, version: mailboxVersion });
  });

  // ── Bulk import ──────────────────────────────────────────────────
  await page.route(/\/platform\/mailboxes\/bulk\/template\?format=csv/, (r) =>
    r.fulfill({ status: 200, contentType: "text/csv; charset=utf-8", headers: { "Content-Disposition": 'attachment; filename="bulk-mailbox-import-template.csv"' }, body: CSV_TEMPLATE_BYTES }));
  await page.route(/\/platform\/mailboxes\/bulk\/template\?format=xlsx/, (r) =>
    r.fulfill({ status: 200, contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", headers: { "Content-Disposition": 'attachment; filename="bulk-mailbox-import-template.xlsx"' }, body: XLSX_TEMPLATE_BYTES }));

  let stageCallCount = 0;
  await page.route("**/api/v1/platform/mailboxes/bulk/7/stage", async (r) => {
    const req = r.request();
    stageCallCount += 1;
    createdCalls.push({ url: req.url(), method: req.method(), headers: await req.headers(), body: `multipart-upload-#${stageCallCount}` });
    return json(r, { staging_id: "stg_test123", source_hash: "hash_abc123", row_count: 2, format: "csv" }, 201);
  });

  await page.route("**/api/v1/platform/mailboxes/bulk/7/validate", async (r) => {
    const req = r.request();
    createdCalls.push({ url: req.url(), method: req.method(), headers: await req.headers(), body: req.postData() || "" });
    return json(r, {
      total_rows: 2, valid_rows: 2, invalid_rows: 0,
      rows: [
        { id: 1, job_id: 0, row_number: 1, email: "new1@acme.example", status: "valid", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
        { id: 2, job_id: 0, row_number: 2, email: "new2@acme.example", status: "valid", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
      ],
      capacity_remaining: 88, source_hash: "hash_abc123", schema_version: 1,
    });
  });

  let jobCreateCount = 0;
  await page.route(/\/api\/v1\/platform\/mailboxes\/bulk\/7\/jobs(\?.*)?$/, async (r) => {
    const req = r.request();
    if (req.method() === "POST") {
      jobCreateCount += 1;
      createdCalls.push({ url: req.url(), method: req.method(), headers: await req.headers(), body: req.postData() || "" });
      return json(r, {
        job: {
          id: 501, tenant_id: 7, domain_id: 1, status: "ready", strategy: "partial", conflict_policy: "fail",
          source_hash: "hash_abc123", schema_version: 1, total_rows: 2, valid_rows: 2, invalid_rows: 0,
          created_count: 0, failed_count: 0, skipped_count: 0, next_row_number: 1, version: 1, created_by: 99,
          created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
        },
      }, 201);
    }
    return json(r, { jobs: [], total: 0, limit: 25, offset: 0 });
  });

  let executeCallCount = 0;
  let pollCount = 0;
  await page.route("**/api/v1/platform/mailboxes/bulk/7/jobs/501/execute", async (r) => {
    const req = r.request();
    executeCallCount += 1;
    createdCalls.push({ url: req.url(), method: req.method(), headers: await req.headers(), body: `execute-#${executeCallCount}` });
    return json(r, { automation_job: { id: 9001 }, import_job_id: 501 }, 202);
  });

  await page.route("**/api/v1/platform/mailboxes/bulk/7/jobs/501", async (r) => {
    pollCount += 1;
    const status = executeCallCount === 0 ? "ready" : pollCount < 2 ? "running" : "completed";
    return json(r, {
      job: {
        id: 501, tenant_id: 7, domain_id: 1, status, strategy: "partial", conflict_policy: "fail",
        source_hash: "hash_abc123", schema_version: 1, total_rows: 2, valid_rows: 2, invalid_rows: 0,
        created_count: status === "completed" ? 2 : 0, failed_count: 0, skipped_count: 0,
        next_row_number: 1, version: 1, created_by: 99,
        created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
      },
    });
  });

  await page.route("**/api/v1/platform/mailboxes/bulk/7/jobs/501/rows*", (r) =>
    json(r, {
      rows: [
        { id: 1, job_id: 501, row_number: 1, email: "new1@acme.example", status: "created", mailbox_id: 201, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
        { id: 2, job_id: 501, row_number: 2, email: "new2@acme.example", status: "created", mailbox_id: 202, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
      ],
      total: 2, limit: 25, offset: 0,
    }));

  // Tenant-family routes must never be reached by PSA provisioning pages.
  await page.route("**/api/v1/domains*", (r) => json(r, { error: "forbidden", code: "FORBIDDEN" }, 403));
  await page.route("**/api/v1/mailboxes*", (r) => json(r, { error: "forbidden", code: "FORBIDDEN" }, 403));
  await page.route("**/api/v1/enterprise/**", (r) => json(r, { error: "forbidden", code: "FORBIDDEN" }, 403));
}

export async function openPlatformShell(page: Page) {
  await page.goto("/admin", { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: /Orvix Admin/i }).waitFor();
}

export async function applyTenantScope(page: Page) {
  const scoped = page.getByText(/Scoped to tenant 7/i);
  if (await scoped.isVisible().catch(() => false)) return;
  await page.getByLabel("Select tenant").selectOption("7");
  await page.getByRole("button", { name: "Apply scope" }).click();
  await scoped.waitFor();
}
