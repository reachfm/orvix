import { expect, type Page, type Route } from "@playwright/test";

/**
 * End-to-end coverage for the enterprise Domains console and its DNS records
 * modal, running against the real built bundle served by scripts/serve-built.mjs.
 *
 * EVERY network call is intercepted with Playwright route mocking. Nothing in
 * this file contacts orvix.email, a real API, a real database, or a real DNS
 * resolver — the fixtures below are the only data the app ever sees, and the
 * DKIM generate/rotate flows return canned public-key payloads. That is a hard
 * requirement: these tests must be safe to run anywhere, including against a
 * developer machine with production credentials in the environment.
 */

export const DOMAIN_ID = 1;

/** Two domains: one checked and healthy-ish, one that has never been checked. */
export const DOMAINS_FIXTURE = {
  domains: [
    {
      id: DOMAIN_ID,
      name: "example.com",
      status: "active",
      plan: "enterprise",
      mailbox_count: 12,
      max_mailboxes: 50,
      alias_count: 7,
      max_aliases: 20,
      storage_used_bytes: 5_368_709_120,
      storage_limit_bytes: 10_737_418_240,
      message_count: 18432,
      dkim_enabled: true,
      dkim_selector: "mail",
      dmarc_enabled: true,
      dns_health: "warning",
      dns_score: 72,
      dns_last_checked_at: "2026-01-01T00:00:00Z",
    },
    {
      id: 2,
      name: "never-checked.com",
      status: "pending",
      plan: "smb",
      mailbox_count: 0,
      max_mailboxes: 5,
      alias_count: 0,
      max_aliases: 0,
      storage_used_bytes: 0,
      storage_limit_bytes: 0,
      message_count: 0,
      dkim_enabled: false,
      dmarc_enabled: false,
      // No dns_last_checked_at and no dns_score: this domain has never been
      // checked, and the table must say so rather than rendering "0%".
    },
  ],
  total: 2,
  offset: 0,
  limit: 25,
};

/**
 * The REAL, deployed ORVIX SPF policy. Fixtures use this specific value
 * rather than the generic "v=spf1 mx -all" a previous change hard-coded, so
 * the e2e suite would catch that regression reappearing.
 */
export const REAL_ORVIX_SPF = "v=spf1 ip4:65.75.203.74 include:spf.orvix.email -all";

/** A configured DMARC rua — never the fabricated dmarc@<domain> placeholder. */
export const CONFIGURED_DMARC = "v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@orvix.email";

function record(over: Record<string, unknown>) {
  return { name: "", type: "", status: "unknown", observed: [], optional: false, checked_at: "2026-01-01T00:00:00Z", ...over };
}

/**
 * A deliberately INCOMPLETE health payload with a HIGH score. This is the
 * regression fixture for the "100% Pass over rows that all say Not checked"
 * defect: complete:false must force an incomplete banner no matter how high
 * health_score is.
 */
export const DNS_HEALTH_FIXTURE = {
  domain_id: DOMAIN_ID,
  domain_name: "example.com",
  operational_status: "active",
  dns_health: "warning",
  health_score: 96,
  complete: false,
  last_checked_at: "2026-01-01T00:00:00Z",
  mx: {
    status: "pass",
    expected: "10 mail.example.com",
    observed: ["mail.example.com:10"],
    guidance: "Add an MX record for example.com pointing to mail.example.com with priority 10.",
    checked_at: "2026-01-01T00:00:00Z",
  },
  spf: {
    status: "pass",
    expected: REAL_ORVIX_SPF,
    observed: REAL_ORVIX_SPF,
    guidance: `Publish a single TXT record at example.com with the value "${REAL_ORVIX_SPF}". Exactly one SPF record may exist per domain.`,
    checked_at: "2026-01-01T00:00:00Z",
  },
  dkim: {
    selector: "mail",
    status: "pass",
    record_name: "mail._domainkey.example.com",
    public_txt: "v=DKIM1; k=rsa; p=PUBLICKEYDATA",
    configured: true,
    matches_dns: true,
    guidance:
      "Publish the DKIM public key as a TXT record at mail._domainkey.example.com. Use the exact value shown in the Required column; do not re-wrap or re-quote it.",
    checked_at: "2026-01-01T00:00:00Z",
  },
  dmarc: {
    status: "fail",
    expected: CONFIGURED_DMARC,
    observed: "",
    reason: "DMARC record not found",
    guidance: `Add a TXT record at _dmarc.example.com with the value "${CONFIGURED_DMARC}".`,
    checked_at: "2026-01-01T00:00:00Z",
  },
  mtasts: {
    status: "warning",
    expected: "v=STSv1; id=<policy-id>",
    observed: "v=STSv1; id=20260101",
    reason: "MTA-STS TXT valid but HTTPS policy unverified: endpoint unavailable",
    guidance:
      'Add a TXT record at _mta-sts.example.com with the value "v=STSv1; id=<policy-id>", and serve the matching policy document over HTTPS at https://mta-sts.example.com/.well-known/mta-sts.txt.',
    checked_at: "2026-01-01T00:00:00Z",
  },
  tlsrpt: {
    status: "pass",
    expected: "v=TLSRPTv1; rua=mailto:tlsrpt@example.com",
    observed: "v=TLSRPTv1; rua=mailto:tlsrpt@example.com",
    guidance: 'Add a TXT record at _smtp._tls.example.com with the value "v=TLSRPTv1; rua=mailto:tlsrpt@example.com".',
    checked_at: "2026-01-01T00:00:00Z",
  },
  // No mtasts_policy: the HTTPS policy was never verified.
  mtasts_policy: null,
  mail_host_a: record({
    name: "mail.example.com", type: "A", status: "pass",
    expected: "one or more public IPv4 addresses", observed: ["192.0.2.25"],
    guidance: "Add an A record for mail.example.com pointing to the public IPv4 address of the ORVIX mail server.",
  }),
  mail_host_aaaa: record({
    name: "mail.example.com", type: "AAAA", status: "optional", optional: true,
    expected: "one or more public IPv6 addresses (optional)",
    reason: "no AAAA record published for mail.example.com; IPv6 is not required by ORVIX",
    guidance: "Optional. If the mail server has IPv6 connectivity, add an AAAA record for mail.example.com pointing to its public IPv6 address.",
  }),
  mx_hosts: [
    record({
      name: "mail.example.com", type: "MX-host", status: "pass",
      expected: "at least one A or AAAA address", observed: ["A 192.0.2.25"],
      guidance: "Add an A record (and optionally AAAA) for the MX host mail.example.com pointing to the mail server's public IP address; an MX target that does not resolve cannot receive mail.",
    }),
  ],
  ptr: record({
    name: "192.0.2.25", type: "PTR", status: "fail", expected: "mail.example.com",
    reason: "no PTR record published for 192.0.2.25",
    guidance: "Ask the network or hosting provider that owns 192.0.2.25 to set its reverse DNS (PTR) record to mail.example.com. Receivers commonly reject mail from IPs whose rDNS is missing or generic.",
  }),
  autodiscover: record({
    name: "autodiscover.example.com", type: "CNAME", status: "optional", optional: true,
    expected: "mail.example.com",
    reason: "autodiscover.example.com is not published; Outlook autodiscover falls back to the ORVIX-hosted endpoint",
    guidance: "Optional. Add a CNAME record for autodiscover.example.com pointing to mail.example.com.",
  }),
  autoconfig: record({
    name: "autoconfig.example.com", type: "CNAME", status: "optional", optional: true,
    expected: "mail.example.com",
    reason: "autoconfig.example.com is not published; Thunderbird autoconfig falls back to the ORVIX-hosted endpoint",
    guidance: "Optional. Add a CNAME record for autoconfig.example.com pointing to mail.example.com.",
  }),
  // The default payload shows the SRV row in a NON-PASSING state (wrong
  // target), which is the state the reviewer asked to see rendered.
  autodiscover_srv: record({
    name: "_autodiscover._tcp.example.com", type: "SRV", status: "warning", optional: true,
    expected: "0 0 443 mail.example.com.",
    observed: ["0 0 443 legacy.elsewhere.example."],
    reason:
      "published autodiscover SRV does not match the expected endpoint: target legacy.elsewhere.example (want mail.example.com)",
    guidance:
      'Optional. Publish an SRV record at _autodiscover._tcp.example.com with the value "0 0 443 mail.example.com." (priority 0, weight 0, port 443, target mail.example.com).',
  }),
  tlsa: record({
    name: "_25._tcp.mail.example.com", type: "TLSA", status: "not_applicable", optional: true,
    reason: "DANE/TLSA is not a configurable feature of this ORVIX deployment, so no TLSA record is required",
    guidance: "No action required. ORVIX does not offer DANE/TLSA configuration, so this record is neither published nor validated.",
  }),
};

/**
 * Organization plan + live usage for the provisioning wizard. Deliberately
 * FINITE so the plan-ceiling and remaining-capacity rules are exercised: 500
 * mailboxes allowed with 100 already pinned by other domains, so only 400
 * remain allocatable.
 */
export const CAPACITY_FIXTURE = {
  capacity: {
    plan: "business",
    max_domains: 10,
    max_domains_unlimited: false,
    domains_used: 2,
    remaining_domains: 8,
    max_mailboxes: 500,
    max_mailboxes_unlimited: false,
    mailboxes_used: 12,
    remaining_mailboxes: 488,
    max_aliases_unlimited: true,
    aliases_used: 7,
    remaining_aliases: null,
    storage_used_bytes: 5_368_709_120,
    storage_allocated_bytes: 10_737_418_240,
    mailboxes_allocated: 100,
  },
};

/**
 * The provisioning response for a successful submit. PUBLIC data only — no
 * fixture in this suite contains private key material, so a leak in the UI
 * cannot be masked by a permissive fixture.
 */
export const PROVISION_RESPONSE = {
  domain: {
    id: 3,
    name: "newdomain.com",
    status: "active",
    plan: "business",
    dkim_selector: "mail",
    max_mailboxes: 0,
    max_aliases: 0,
    max_quota_mb: 0,
    mailbox_count: 0,
    alias_count: 0,
  },
  effective_limits: {
    max_mailboxes: 500,
    max_mailboxes_inherited: true,
    max_mailboxes_unlimited: false,
  },
  dkim: {
    selector: "mail",
    public_dns_txt: "v=DKIM1; k=rsa; p=NEWDOMAINPUBLICKEYDATA",
    dns_record_name: "mail._domainkey.newdomain.com",
  },
  dns: { public_dns_changed: false, next_step: "publish_and_verify_dns" },
  idempotent: false,
};

/** The domains list AFTER provisioning, proving the table really refreshes. */
export const DOMAINS_AFTER_CREATE = {
  ...DOMAINS_FIXTURE,
  domains: [
    ...DOMAINS_FIXTURE.domains,
    {
      id: 3,
      name: "newdomain.com",
      status: "active",
      plan: "business",
      mailbox_count: 0,
      max_mailboxes: 0,
      alias_count: 0,
      max_aliases: 0,
      storage_used_bytes: 0,
      storage_limit_bytes: 0,
      message_count: 0,
      dkim_enabled: true,
      dkim_selector: "mail",
      dmarc_enabled: false,
    },
  ],
  total: 3,
};

/** Counts every POST the page makes, keyed by URL suffix. */
export type Counters = { verify: number; generate: number; rotate: number; createDomain: number };

/**
 * mockAPI installs the mocked backend. `dnsHealth` replaces the DNS health
 * payload wholesale, which lets a test render any single row (notably the
 * autodiscover SRV row) in an arbitrary state without touching the shared
 * fixture.
 *
 * `capacity` and `createDomain` let a test drive the provisioning wizard:
 * `capacity` swaps the plan (finite vs unlimited), and `createDomain` returns
 * an arbitrary status/body so typed backend errors can be exercised.
 */
export async function mockAPI(
  page: Page,
  opts: {
    dnsHealth?: Record<string, unknown>;
    capacity?: Record<string, unknown>;
    createDomain?: { status: number; body: unknown };
  } = {},
): Promise<Counters> {
  const counters: Counters = { verify: 0, generate: 0, rotate: 0, createDomain: 0 };
  const dnsHealth = opts.dnsHealth ?? DNS_HEALTH_FIXTURE;

  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  // Playwright matches the MOST RECENTLY registered route first, so these are
  // ordered from least to most specific: the catch-all goes in first and is
  // only reached when nothing more specific matched. Registering it last would
  // silently swallow every call.
  await page.route("**/api/**", (r) => json(r, {}));

  // GET lists domains; POST provisions one. The same path serves both, so the
  // handler branches on method rather than swallowing the create call.
  //
  // The list flips to DOMAINS_AFTER_CREATE once a domain has been created, so
  // the "new row appears" assertion is proving a real refetch rather than a
  // static fixture that always contained the row.
  await page.route("**/api/v1/enterprise/domains", (r) => {
    if (r.request().method() === "POST") {
      counters.createDomain += 1;
      const override = opts.createDomain;
      if (override) return json(r, override.body, override.status);
      return json(r, PROVISION_RESPONSE, 201);
    }
    return json(r, counters.createDomain > 0 ? DOMAINS_AFTER_CREATE : DOMAINS_FIXTURE);
  });
  await page.route("**/api/v1/enterprise/organizations/current/capacity", (r) =>
    json(r, opts.capacity ?? CAPACITY_FIXTURE),
  );
  await page.route("**/api/v1/enterprise/domains/*/dns", (r) => json(r, dnsHealth));

  await page.route("**/api/v1/enterprise/domains/*/dns/verify", (r) => {
    counters.verify += 1;
    // The fresh check returns the SAME canonical shape, still incomplete and
    // still without a verified MTA-STS policy.
    return json(r, {
      ...DNS_HEALTH_FIXTURE,
      last_checked_at: new Date().toISOString(),
      cooldown_until: new Date(Date.now() + 120_000).toISOString(),
      retry_after_seconds: 120,
    });
  });

  await page.route("**/api/v1/enterprise/domains/*/dkim/generate", (r) => {
    counters.generate += 1;
    return json(r, {
      dkim: {
        selector: "mail",
        public_dns_txt: "v=DKIM1; k=rsa; p=GENERATEDPUBLICKEY",
        dns_record_name: "mail._domainkey.example.com",
      },
    });
  });

  await page.route("**/api/v1/enterprise/domains/*/dkim/rotate", (r) => {
    counters.rotate += 1;
    return json(r, {
      dkim: {
        selector: "mail",
        public_dns_txt: "v=DKIM1; k=rsa; p=ROTATEDPUBLICKEY",
        dns_record_name: "mail._domainkey.example.com",
      },
    });
  });

  await page.route("**/api/v1/csrf-token", (r) => json(r, { csrf_token: "test-csrf-token" }));
  // portal is the authoritative platform-shell gate; role is informational.
  // PLATFORM-SHELL: the domain management page (Domains.tsx) calls
  // /enterprise/domains, which the backend gates with RequireTenantID —
  // it is tenant-owned, not platform-owned. portal="platform" (a
  // NULL-tenant identity) could never actually reach this page in
  // production (it would get 403), so the fixture uses the real
  // authorization shape: an organization-portal tenant identity.
  await page.route("**/api/v1/me", (r) => json(r, { id: 1, email: "admin@example.com", role: "tenant_admin", portal: "organization", tenant_id: 1 }));

  return counters;
}

/** Signs in via the mocked /me endpoint and lands on the Domains tab. */
export async function openDomains(page: Page) {
  await page.goto("/admin/dashboard", { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "Domains", exact: true }).first().click();
  await expect(page.getByRole("heading", { name: /Domains \(2\)/ })).toBeVisible();
}

export async function openDNSModal(page: Page) {
  await page.getByRole("button", { name: "Open DNS records for example.com" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
}

