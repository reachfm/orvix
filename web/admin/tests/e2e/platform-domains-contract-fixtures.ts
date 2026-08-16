import { type Page, type Route } from "@playwright/test";
import { PSA_ME, ORGANIZATIONS_FIXTURE } from "./mail-control-fixtures";

/**
 * Fixtures for the Platform Domains contract-closure acceptance spec
 * (existing-domain DNS, DKIM generate/rotate, lifecycle deactivate).
 *
 * Independent of mail-control-fixtures.ts's single-dispatch handler
 * because these flows need STATEFUL, METHOD-AWARE routing: a generate
 * must actually flip dkim_configured on the next GET .../dns, a rotate
 * must return a genuinely different public TXT value, and every guarded
 * mutation must verify the expected_version the UI sent against the
 * server's current version — which is exactly the behaviour the
 * regression barrier has to prove end-to-end.
 *
 * Every network call is intercepted — no real backend is contacted.
 * No DKIM private key material exists anywhere in these fixtures,
 * mirroring the real backend contract.
 */

export const domainCalls: Array<{ url: string; method: string; headers: Record<string, string>; body: string }> = [];

export function resetDomainCalls() {
  domainCalls.length = 0;
}

export interface DomainsContractOptions {
  /**
   * When true, the FIRST guarded mutation (generate/rotate/deactivate)
   * answers with the backend's real optimistic-concurrency conflict
   * (kernel.ErrCodeConflict -> HTTP 409) regardless of the version sent,
   * so the stale-version UX can be exercised in the browser.
   */
  conflictOnFirstMutation?: boolean;
}

export async function mockPlatformDomainsAPI(page: Page, opts: DomainsContractOptions = {}) {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  // Server-side state the mutations actually move.
  const state = {
    acme: {
      version: 3,
      status: "active",
      dkimConfigured: true,
      dkimSelector: "mail",
      dkimTxt: "v=DKIM1; k=rsa; p=CURRENTPUBLICKEYDATA",
    },
    beta: {
      version: 1,
      status: "suspended",
      dkimConfigured: false,
      dkimSelector: "",
      dkimTxt: "",
    },
  };
  let mutationCount = 0;

  const conflict = (r: Route) =>
    json(r, { error: "domain version is no longer current", code: "CONFLICT" }, 409);

  await page.route("**/api/**", (r) => json(r, {}));
  await page.route("**/api/v1/csrf-token", (r) => json(r, { csrf_token: "test-csrf-token" }));
  await page.route("**/api/v1/me", (r) => json(r, PSA_ME));
  await page.route(/\/platform\/organizations(\?|$)/, (r) => json(r, ORGANIZATIONS_FIXTURE));

  const acmeDomain = () => ({
    id: 1, tenant_id: 7, name: "acme.example", status: state.acme.status, plan: "business",
    mailbox_count: 12, alias_count: 3,
    dkim_enabled: state.acme.dkimConfigured, dkim_selector: state.acme.dkimSelector || undefined,
    dmarc_enabled: true, mail_access_mode: "internal_external",
    created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
    version: state.acme.version,
  });

  const betaDomain = () => ({
    id: 2, tenant_id: 7, name: "beta.example", status: state.beta.status, plan: "starter",
    mailbox_count: 0, alias_count: 0,
    dkim_enabled: state.beta.dkimConfigured, dkim_selector: state.beta.dkimSelector || undefined,
    dmarc_enabled: false, mail_access_mode: "internal_only",
    created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-03T00:00:00Z",
    version: state.beta.version,
  });

  const DNS_REQUIREMENTS = (name: string) => [
    { name, type: "MX", value: "mail.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "inbound mail routing" },
    { name, type: "TXT", value: "v=spf1 include:orvix.email ~all", ttl: 3600, required: true, purpose: "SPF" },
  ];

  const record = async (r: Route) => {
    const req = r.request();
    domainCalls.push({ url: req.url(), method: req.method(), headers: req.headers(), body: req.postData() || "" });
  };

  // ── DKIM generate (domain 2, unconfigured) ───────────────────────
  await page.route(/\/api\/v1\/platform\/domains\/7\/2\/dkim\/generate$/, async (r) => {
    await record(r);
    mutationCount += 1;
    if (opts.conflictOnFirstMutation && mutationCount === 1) return conflict(r);
    const body = JSON.parse(r.request().postData() || "{}");
    if (body.expected_version !== state.beta.version) return conflict(r);
    state.beta.version += 1;
    state.beta.dkimConfigured = true;
    state.beta.dkimSelector = body.selector || "orvix";
    state.beta.dkimTxt = "v=DKIM1; k=rsa; p=NEWLYGENERATEDPUBLICKEY";
    return json(r, {
      selector: state.beta.dkimSelector,
      public_dns_txt: state.beta.dkimTxt,
      dns_record_name: `${state.beta.dkimSelector}._domainkey.beta.example`,
      version: state.beta.version,
    });
  });

  // ── DKIM rotate (domain 1, configured) ───────────────────────────
  await page.route(/\/api\/v1\/platform\/domains\/7\/1\/dkim\/rotate$/, async (r) => {
    await record(r);
    mutationCount += 1;
    if (opts.conflictOnFirstMutation && mutationCount === 1) return conflict(r);
    const body = JSON.parse(r.request().postData() || "{}");
    if (body.confirm_rotation !== "rotate-dkim-key") {
      return json(r, { error: 'rotation requires confirm_rotation: "rotate-dkim-key"', code: "PRECONDITION_FAILED" }, 412);
    }
    if (body.expected_version !== state.acme.version) return conflict(r);
    state.acme.version += 1;
    state.acme.dkimTxt = "v=DKIM1; k=rsa; p=ROTATEDPUBLICKEYDATA";
    return json(r, {
      selector: state.acme.dkimSelector,
      public_dns_txt: state.acme.dkimTxt,
      dns_record_name: `${state.acme.dkimSelector}._domainkey.acme.example`,
      version: state.acme.version,
    });
  });

  // ── Lifecycle deactivate (domain 1) ──────────────────────────────
  await page.route(/\/api\/v1\/platform\/domains\/7\/1\/deactivate$/, async (r) => {
    await record(r);
    mutationCount += 1;
    if (opts.conflictOnFirstMutation && mutationCount === 1) return conflict(r);
    const body = JSON.parse(r.request().postData() || "{}");
    if (body.confirm !== "DEACTIVATE-DOMAIN-1") {
      return json(r, { error: "type the confirmation phrase exactly: DEACTIVATE-DOMAIN-1", code: "PRECONDITION_FAILED" }, 412);
    }
    if (!body.reason) return json(r, { error: "reason is required", code: "VALIDATION_FAILED" }, 400);
    if (body.expected_version !== state.acme.version) return conflict(r);
    state.acme.version += 1;
    state.acme.status = "disabled";
    return json(r, {
      id: 1, tenant_id: 7, status: "disabled", version: state.acme.version, request_id: "req_test_abc123",
    });
  });

  // ── Status mutation (kept operational, distinct from deactivate) ──
  await page.route(/\/api\/v1\/platform\/domains\/7\/\d+\/status$/, async (r) => {
    await record(r);
    return json(r, { status: "ok", id: 1 });
  });

  // ── Existing-domain DNS snapshots ────────────────────────────────
  await page.route(/\/api\/v1\/platform\/domains\/7\/1\/dns$/, async (r) => {
    await record(r);
    return json(r, {
      tenant_id: 7, domain_id: 1, domain: "acme.example",
      version: state.acme.version, status: state.acme.status,
      dkim_configured: state.acme.dkimConfigured,
      dkim_selector: state.acme.dkimSelector || undefined,
      dkim_dns_record_name: state.acme.dkimConfigured ? `${state.acme.dkimSelector}._domainkey.acme.example` : undefined,
      dkim_public_dns_txt: state.acme.dkimConfigured ? state.acme.dkimTxt : undefined,
      dns_requirements: DNS_REQUIREMENTS("acme.example"),
      dns_next_step: "publish_and_verify_dns",
    });
  });

  await page.route(/\/api\/v1\/platform\/domains\/7\/2\/dns$/, async (r) => {
    await record(r);
    return json(r, {
      tenant_id: 7, domain_id: 2, domain: "beta.example",
      version: state.beta.version, status: state.beta.status,
      dkim_configured: state.beta.dkimConfigured,
      dkim_selector: state.beta.dkimSelector || undefined,
      dkim_dns_record_name: state.beta.dkimConfigured ? `${state.beta.dkimSelector}._domainkey.beta.example` : undefined,
      dkim_public_dns_txt: state.beta.dkimConfigured ? state.beta.dkimTxt : undefined,
      dns_requirements: DNS_REQUIREMENTS("beta.example"),
      dns_next_step: "publish_and_verify_dns",
    });
  });

  // ── Domain detail / list ─────────────────────────────────────────
  await page.route(/\/api\/v1\/platform\/domains\/7\/1$/, async (r) => {
    await record(r);
    return json(r, acmeDomain());
  });
  await page.route(/\/api\/v1\/platform\/domains\/7\/2$/, async (r) => {
    await record(r);
    return json(r, betaDomain());
  });
  await page.route(/\/api\/v1\/platform\/domains\/7(\?.*)?$/, async (r) => {
    await record(r);
    return json(r, { domains: [acmeDomain(), betaDomain()], total: 2, limit: 25, offset: 0 });
  });

  // Tenant-family routes must never be reached by PSA domain pages.
  await page.route("**/api/v1/domains*", (r) => json(r, { error: "forbidden", code: "FORBIDDEN" }, 403));
  await page.route("**/api/v1/enterprise/**", (r) => json(r, { error: "forbidden", code: "FORBIDDEN" }, 403));
}

export async function openDomainsPage(page: Page) {
  await page.goto("/admin", { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: /Orvix Admin/i }).waitFor();
  await page.getByRole("button", { name: "Domains", exact: true }).click();
  const scoped = page.getByText(/Scoped to tenant 7/i);
  if (!(await scoped.isVisible().catch(() => false))) {
    await page.getByLabel("Select tenant").selectOption("7");
    await page.getByRole("button", { name: "Apply scope" }).click();
    await scoped.waitFor();
  }
}
