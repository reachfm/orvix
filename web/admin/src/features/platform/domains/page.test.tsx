import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import DomainsPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";
import type { PlatformDomainList } from "./contract";

const { MockApiError } = vi.hoisted(() => {
  class MockApiError extends Error {
    code: string;
    status: number;
    body: any;
    constructor(code: string, message: string, status: number, body?: any) {
      super(message);
      this.code = code;
      this.status = status;
      this.body = body;
    }
  }
  return { MockApiError };
});

vi.mock("../../../api", () => ({ request: vi.fn(), ApiError: MockApiError }));

const mockedRequest = vi.mocked(request);

const DOMAINS: PlatformDomainList = {
  domains: [
    {
      id: 1, tenant_id: 7, name: "acme.example", status: "active", plan: "business",
      mailbox_count: 12, alias_count: 3, dkim_enabled: true, dkim_selector: "mail",
      dmarc_enabled: true, mail_access_mode: "internal_external",
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z", version: 3,
    },
    {
      id: 2, tenant_id: 7, name: "beta.example", status: "suspended", plan: "starter",
      mailbox_count: 0, alias_count: 0, dkim_enabled: false, dmarc_enabled: false,
      mail_access_mode: "internal_only",
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-03T00:00:00Z", version: 1,
    },
  ],
  total: 2,
  limit: 25,
  offset: 0,
};

function renderPage(scopeTenantId: number | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (scopeTenantId !== null) {
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: scopeTenantId, tenantName: "Acme" });
  }
  return render(<QueryClientProvider client={qc}><DomainsPage /></QueryClientProvider>);
}

describe("features/platform/domains (platform routes)", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    // Tenant scope options come from the real organizations endpoint.
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/organizations")) {
        return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      }
      if (path === "/platform/domains/7/1/dns") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 1, domain: "acme.example", version: 3, status: "active",
          dkim_configured: true, dkim_selector: "mail",
          dkim_dns_record_name: "mail._domainkey.acme.example",
          dkim_public_dns_txt: "v=DKIM1; k=rsa; p=currentkey",
        });
      }
      if (path === "/platform/domains/7/2/dns") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", version: 1, status: "suspended",
          dkim_configured: false,
          dns_requirements: [
            { name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay" },
          ],
          dns_next_step: "publish_and_verify_dns",
        });
      }
      if (path === "/platform/domains/7/2/dns/verify") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:55:00Z",
          records: [
            { name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay", status: "verified", verified: true, observed: "10 relay.orvix.email" },
          ],
          total_count: 1, matched_count: 1, issue_count: 0, all_verified: true,
        });
      }
      if (path === "/platform/domains/7/1/dns/verify") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 1, domain: "acme.example", checked_at: "2026-01-05T08:55:00Z",
          records: [], total_count: 0, matched_count: 0, issue_count: 0, all_verified: true,
        });
      }
      if (path.startsWith("/platform/domains/7/1")) {
        return Promise.resolve(DOMAINS.domains[0]);
      }
      if (path.startsWith("/platform/domains/7/2")) {
        return Promise.resolve(DOMAINS.domains[1]);
      }
      if (path.startsWith("/platform/domains/7")) {
        return Promise.resolve(DOMAINS);
      }
      return Promise.resolve({});
    });
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("requires an explicit tenant and never fabricates one", async () => {
    renderPage(null);
    expect(screen.getByText("Select a tenant")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("always shows Create domain, even with no page-level tenant scope applied yet — the tenant selector lives inside the dialog", async () => {
    renderPage(null);
    const createButton = screen.getByRole("button", { name: "Create domain" });
    expect(createButton).toBeEnabled();

    fireEvent.click(createButton);
    // The dialog's own required tenant selector — not the page banner.
    await waitFor(() => expect(screen.getByLabelText(/Organization \/ tenant/)).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Create domain" })).toBeDisabled();

    // Wait for the real organization options to load before selecting one.
    await waitFor(() => expect(screen.getByRole("option", { name: "Acme (tenant 7)" })).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText(/Organization \/ tenant/), { target: { value: "7" } });
    fireEvent.change(screen.getByLabelText("Domain name *"), { target: { value: "new.example.com" } });
    expect(screen.getByRole("button", { name: "Create domain" })).toBeEnabled();
  });

  it("calls the platform route with the tenant in the path — never /domains and never a support header", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    const calls = mockedRequest.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.startsWith("/platform/domains/7"))).toBe(true);
    // PSA must never call the tenant-family /domains route.
    expect(calls.some((p) => p.startsWith("/domains") && !p.startsWith("/platform/"))).toBe(false);
    // No X-Support-Tenant-ID header anywhere.
    for (const call of mockedRequest.mock.calls) {
      const opts = call[1] as { headers?: Record<string, string> } | undefined;
      expect(opts?.headers?.["X-Support-Tenant-ID"]).toBeUndefined();
    }
  });

  it("renders lifecycle, DKIM, DMARC and mail-access mode from the real contract", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    expect(screen.getAllByText("Active").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Suspended").length).toBeGreaterThan(0);
    expect(screen.getByText(/enabled \(mail\)/)).toBeInTheDocument();
    expect(screen.getByText("Internal + external")).toBeInTheDocument();
    expect(screen.getByText("Internal only")).toBeInTheDocument();
  });

  it("opens the detail drawer with real fields and mutates lifecycle status via the platform route", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Lifecycle" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Lifecycle" }));
    await waitFor(() => expect(screen.getByText("Lifecycle status")).toBeInTheDocument());

    fireEvent.change(screen.getByLabelText("New domain status"), { target: { value: "suspended" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply status" }));
    await waitFor(() => {
      const calls = mockedRequest.mock.calls.filter((c) => String(c[0]).endsWith("/status"));
      expect(calls.length).toBe(1);
      expect(JSON.parse(String(calls[0][1] && (calls[0][1] as { body?: string }).body))).toEqual({ status: "suspended", reason: "" });
    });
  });

  it("does not present a domain-level mail-access-mode control or invent DNS-verify/DKIM-rotate/TLS controls the platform routes do not expose — mail access mode is mailbox-level in this UI", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Lifecycle" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Lifecycle" }));
    await waitFor(() => expect(screen.getByText("Lifecycle status")).toBeInTheDocument());
    expect(screen.queryByText("Mail access policy")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("New mail access mode")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /apply mode/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /rotate dkim/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /verify dns/i })).not.toBeInTheDocument();
  });

  it("creates a domain via the exact platform route with CSRF+Idempotency-Key and no access-mode field", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /create domain/i }));
    fireEvent.change(screen.getByLabelText("Domain name *"), { target: { value: "new.example.com" } });

    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (path === "/platform/domains/7" && opts?.body) {
        return Promise.resolve({
          domain: { id: 99, tenant_id: 7, name: "new.example.com", status: "active", plan: "business", mailbox_count: 0, alias_count: 0, dkim_enabled: false, dmarc_enabled: false, mail_access_mode: "inherit", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
          effective_limits: { max_mailboxes: 50, max_mailboxes_unlimited: false, max_mailboxes_inherited: true, max_aliases: 50, max_aliases_unlimited: false, max_aliases_inherited: true, default_mailbox_quota_mb: 1024, max_mailbox_quota_mb: 10240, max_mailbox_quota_mb_unlimited: false, max_mailbox_quota_mb_inherited: true, default_mailbox_quota_mb_inherited: true },
          dns_next_step: "publish_and_verify_dns",
          public_dns_changed: false,
          idempotent: false,
        });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    fireEvent.click(screen.getByRole("button", { name: "Create domain" }));

    await waitFor(() => expect(screen.getByText("Domain created")).toBeInTheDocument());

    const createCall = mockedRequest.mock.calls.find((c) => c[0] === "/platform/domains/7" && (c[1] as any)?.method === "POST");
    expect(createCall).toBeDefined();
    const opts = createCall![1] as { body: string; headers?: Record<string, string> };
    const body = JSON.parse(opts.body);
    expect(body).toEqual({ name: "new.example.com", status: "active" });
    expect(body.mail_access_mode).toBeUndefined();
    expect(opts.headers?.["Idempotency-Key"]).toBeTruthy();
    // Never a hand-rolled fetch — this goes through the shared client mock.
    expect(mockedRequest).toHaveBeenCalled();
  });

  it("shows validation errors from the server and never a false success state", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /create domain/i }));
    fireEvent.change(screen.getByLabelText("Domain name *"), { target: { value: "bad name" } });

    mockedRequest.mockImplementation((path: string, opts?: { method?: string }) => {
      if (path === "/platform/domains/7" && opts?.method === "POST") {
        return Promise.reject(new MockApiError("INVALID_DOMAIN_NAME", "That domain name is not valid.", 400));
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    fireEvent.click(screen.getByRole("button", { name: "Create domain" }));
    await waitFor(() => expect(screen.getByRole("alert")).toBeInTheDocument());
    expect(screen.queryByText("Domain created")).not.toBeInTheDocument();
  });

  it("links a newly-created domain's 'View domain' action to the DNS Setup tab, showing the real one-time creation records", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /create domain/i }));
    fireEvent.change(screen.getByLabelText("Domain name *"), { target: { value: "new.example.com" } });

    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (path === "/platform/domains/7" && opts?.body) {
        return Promise.resolve({
          domain: { id: 99, tenant_id: 7, name: "new.example.com", status: "active", plan: "business", mailbox_count: 0, alias_count: 0, dkim_enabled: true, dkim_selector: "mail", dmarc_enabled: false, mail_access_mode: "inherit", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
          effective_limits: { max_mailboxes: 50, max_mailboxes_unlimited: false, max_mailboxes_inherited: true, max_aliases: 50, max_aliases_unlimited: false, max_aliases_inherited: true, default_mailbox_quota_mb: 1024, max_mailbox_quota_mb: 10240, max_mailbox_quota_mb_unlimited: false, max_mailbox_quota_mb_inherited: true, default_mailbox_quota_mb_inherited: true },
          dkim: { selector: "mail", public_dns_txt: "v=DKIM1; k=rsa; p=abcVERYLONGKEYDATAxyz", dns_record_name: "mail._domainkey.new.example.com" },
          dns_requirements: [
            { name: "new.example.com", type: "MX", value: "mail.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail routing" },
            { name: "new.example.com", type: "TXT", value: "v=spf1 include:orvix.email ~all", ttl: 3600, required: true, purpose: "SPF" },
          ],
          dns_next_step: "publish_and_verify_dns",
          public_dns_changed: false,
          idempotent: false,
        });
      }
      if (path === "/platform/domains/7/99/dns") {
        // The live endpoint mirrors the exact records from the creation
        // response, so this deterministically settles to the same
        // rendered state as the initial-paint cache (no flake window).
        return Promise.resolve({
          tenant_id: 7, domain_id: 99, domain: "new.example.com", version: 1, status: "active",
          dkim_configured: true, dkim_selector: "mail",
          dkim_dns_record_name: "mail._domainkey.new.example.com",
          dkim_public_dns_txt: "v=DKIM1; k=rsa; p=abcVERYLONGKEYDATAxyz",
          dns_requirements: [
            { name: "new.example.com", type: "MX", value: "mail.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail routing" },
            { name: "new.example.com", type: "TXT", value: "v=spf1 include:orvix.email ~all", ttl: 3600, required: true, purpose: "SPF" },
          ],
          dns_next_step: "publish_and_verify_dns",
        });
      }
      if (path.startsWith("/platform/domains/7/99")) {
        return Promise.resolve({ id: 99, tenant_id: 7, name: "new.example.com", status: "active", plan: "business", mailbox_count: 0, alias_count: 0, dkim_enabled: true, dkim_selector: "mail", dmarc_enabled: false, mail_access_mode: "inherit", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", version: 1 });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    fireEvent.click(screen.getByRole("button", { name: "Create domain" }));
    await waitFor(() => expect(screen.getByText("Domain created")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "View domain" }));

    await waitFor(() => expect(screen.getByRole("tab", { name: "DNS Setup" })).toBeInTheDocument());

    // Lands directly on DNS Setup — no extra tab click needed.
    await waitFor(() => expect(screen.getByText("Mail routing")).toBeInTheDocument());
    expect(screen.getByText("mail.orvix.email")).toBeInTheDocument();
    expect(screen.getByText(/v=spf1 include:orvix.email/)).toBeInTheDocument();

    // Copy-all works via the real clipboard API mock.
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    fireEvent.click(screen.getByRole("button", { name: /copy all/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalled());
    expect(writeText.mock.calls[0][0]).toContain("mail.orvix.email");

    // DKIM tab shows the real public TXT value; the reassurance text
    // mentions "private key" deliberately (never shown/requested) — what
    // must never appear is an actual private-key input or PEM material.
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DKIM" }));
    await waitFor(() => expect(screen.getByText(/v=DKIM1/)).toBeInTheDocument());
    expect(screen.queryByText(/BEGIN (RSA |EC )?PRIVATE KEY/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/private key/i)).not.toBeInTheDocument();
  });

  it("closes the detail drawer when tenant scope switches — a previous tenant's domain id must not survive", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: 7, tenantName: "Acme" });
    render(<QueryClientProvider client={qc}><DomainsPage /></QueryClientProvider>);

    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument());

    // Directly simulate a tenant-scope switch (as TenantScopeBanner would trigger).
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: 8, tenantName: "Beta Co" });

    await waitFor(() => expect(screen.queryByRole("tab", { name: "Overview" })).not.toBeInTheDocument());
  });

  // ── Contract-closure regression barrier: existing-domain DNS/DKIM/
  // lifecycle-deactivate must stay wired to the real backend routes. ──

  it("fetches live existing-domain DNS via GET .../dns and renders it — no false 'records unavailable' message", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DNS Setup" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));

    await waitFor(() => {
      const dnsCalls = mockedRequest.mock.calls.filter((c) => c[0] === "/platform/domains/7/2/dns");
      expect(dnsCalls.length).toBeGreaterThan(0);
    });
    await waitFor(() => expect(screen.getByText("Mail relay")).toBeInTheDocument());
    expect(screen.getAllByText("relay.orvix.email").length).toBeGreaterThan(0);
    expect(screen.queryByText(/there is no\s*\n?\s*route to re-fetch them for an existing domain/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/only returns DNS records at the moment a domain is created/i)).not.toBeInTheDocument();
  });

  it("DKIM not configured: shows a truthful state, exposes Generate DKIM, and calls the real generate route on click", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DKIM" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DKIM" }));

    await waitFor(() => expect(screen.getByText("Not configured")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Generate DKIM" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Rotate DKIM" })).not.toBeInTheDocument();
    expect(screen.queryByText(/there is no generate or rotate route for an existing domain/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Generate DKIM" }));

    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => c[0] === "/platform/domains/7/2/dkim/generate");
      expect(call).toBeDefined();
      const opts = call![1] as { body: string; headers?: Record<string, string> };
      expect(JSON.parse(opts.body)).toEqual({ expected_version: 1 });
      expect(opts.headers?.["Idempotency-Key"]).toBeTruthy();
    });
  });

  it("DKIM configured: shows selector/hostname/TXT and Rotate DKIM, and calls the real rotate route with confirm_rotation", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DKIM" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DKIM" }));

    await waitFor(() => {
      const dnsCalls = mockedRequest.mock.calls.filter((c) => c[0] === "/platform/domains/7/1/dns");
      expect(dnsCalls.length).toBeGreaterThan(0);
    });
    await waitFor(() => expect(screen.getByText("mail._domainkey.acme.example")).toBeInTheDocument());
    expect(screen.getByText(/v=DKIM1; k=rsa; p=currentkey/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Rotate DKIM" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Rotate DKIM" }));
    // Confirm inside the dialog (danger confirm button, no typed-name gate for rotate).
    await waitFor(() => expect(screen.getByRole("dialog", { name: "Rotate DKIM key" })).toBeInTheDocument());
    const dialog = screen.getByRole("dialog", { name: "Rotate DKIM key" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Rotate DKIM" }));

    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => c[0] === "/platform/domains/7/1/dkim/rotate");
      expect(call).toBeDefined();
      const opts = call![1] as { body: string; headers?: Record<string, string> };
      expect(JSON.parse(opts.body)).toEqual({ confirm_rotation: "rotate-dkim-key", expected_version: 3 });
      expect(opts.headers?.["Idempotency-Key"]).toBeTruthy();
    });
  });

  it("security: DKIM tabs never expect, display, or reference a private_key field", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DKIM" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DKIM" }));
    await waitFor(() => expect(screen.getByText("mail._domainkey.acme.example")).toBeInTheDocument());
    expect(screen.queryByText(/BEGIN (RSA |EC )?PRIVATE KEY/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/private key/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/private_key/i)).not.toBeInTheDocument();
  });

  it("lifecycle keeps Active/Disabled/Suspended AND exposes a separate danger-zone Deactivate action requiring typed confirmation and the real version", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Lifecycle" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Lifecycle" }));
    await waitFor(() => expect(screen.getByText("Lifecycle status")).toBeInTheDocument());

    // Normal status controls remain.
    const options = Array.from(screen.getByLabelText("New domain status").querySelectorAll("option")).map((o) => o.textContent);
    expect(options).toEqual(expect.arrayContaining(["Disabled", "Suspended"]));

    // Separate danger zone.
    expect(screen.getByText("Danger zone")).toBeInTheDocument();
    const deactivateButton = screen.getByRole("button", { name: "Deactivate domain" });
    expect(deactivateButton).toBeDisabled(); // no reason typed yet

    fireEvent.change(screen.getByLabelText("Deactivate reason"), { target: { value: "Customer offboarding" } });
    expect(deactivateButton).toBeEnabled();
    fireEvent.click(deactivateButton);

    await waitFor(() => expect(screen.getByText("DEACTIVATE-DOMAIN-1")).toBeInTheDocument());
    const dialogConfirmButtons = screen.getAllByRole("button", { name: "Deactivate domain" });
    const dialogConfirm = dialogConfirmButtons[dialogConfirmButtons.length - 1];
    expect(dialogConfirm).toBeDisabled();

    fireEvent.change(screen.getByRole("textbox", { name: /Type/i }), { target: { value: "DEACTIVATE-DOMAIN-1" } });
    fireEvent.click(dialogConfirm);

    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => c[0] === "/platform/domains/7/1/deactivate");
      expect(call).toBeDefined();
      const opts = call![1] as { body: string; headers?: Record<string, string> };
      const body = JSON.parse(opts.body);
      expect(body).toEqual({ confirm: "DEACTIVATE-DOMAIN-1", reason: "Customer offboarding", expected_version: 3 });
      expect(opts.headers?.["Idempotency-Key"]).toBeTruthy();
    });

    // Successful deactivate must not leave the row visually Active — the drawer closes.
    await waitFor(() => expect(screen.queryByRole("tab", { name: "Lifecycle" })).not.toBeInTheDocument());
  });

  it("exposes Delete domain as a separate, stronger danger action from Deactivate, requiring its own reason and typed confirmation", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Lifecycle" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Lifecycle" }));
    await waitFor(() => expect(screen.getByText("Lifecycle status")).toBeInTheDocument());

    // Both actions present and distinct.
    expect(screen.getByRole("button", { name: "Deactivate domain" })).toBeInTheDocument();
    const deleteButton = screen.getByRole("button", { name: "Delete domain" });
    expect(deleteButton).toBeInTheDocument();
    expect(deleteButton).toBeDisabled(); // no reason typed yet

    fireEvent.change(screen.getByLabelText("Delete reason"), { target: { value: "Customer offboarding, fully wound down" } });
    expect(deleteButton).toBeEnabled();
    fireEvent.click(deleteButton);

    await waitFor(() => expect(screen.getByText("DELETE-DOMAIN-1")).toBeInTheDocument());
    const dialogConfirmButtons = screen.getAllByRole("button", { name: "Delete domain" });
    const dialogConfirm = dialogConfirmButtons[dialogConfirmButtons.length - 1];
    expect(dialogConfirm).toBeDisabled();

    fireEvent.change(screen.getByRole("textbox", { name: /Type/i }), { target: { value: "DELETE-DOMAIN-1" } });
    fireEvent.click(dialogConfirm);

    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => c[0] === "/platform/domains/7/1/delete");
      expect(call).toBeDefined();
      const opts = call![1] as { body: string; headers?: Record<string, string> };
      const body = JSON.parse(opts.body);
      expect(body).toEqual({ confirm: "DELETE-DOMAIN-1", reason: "Customer offboarding, fully wound down", expected_version: 3 });
      expect(opts.headers?.["Idempotency-Key"]).toBeTruthy();
    });

    // Successful delete must not fake removal client-side — the drawer closes on real server confirmation.
    await waitFor(() => expect(screen.queryByRole("tab", { name: "Lifecycle" })).not.toBeInTheDocument());
  });

  it("renders structured blocker counts on 409 DOMAIN_DELETE_BLOCKED without pretending success", async () => {
    mockedRequest.mockImplementation((path: string, opts?: any) => {
      if (path === "/platform/domains/7/1/delete") {
        return Promise.reject(
          new MockApiError("DOMAIN_DELETE_BLOCKED", "domain has live dependencies blocking deletion", 409, {
            error: "domain has live dependencies blocking deletion",
            code: "DOMAIN_DELETE_BLOCKED",
            blockers: { mailboxes: 2, aliases: 1, queued_messages: 0 },
          }),
        );
      }
      if (path.startsWith("/platform/organizations")) {
        return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      }
      if (path.startsWith("/platform/domains/7/1")) {
        return Promise.resolve(DOMAINS.domains[0]);
      }
      if (path.startsWith("/platform/domains/7")) {
        return Promise.resolve(DOMAINS);
      }
      return Promise.resolve({});
    });

    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Lifecycle" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Lifecycle" }));
    await waitFor(() => expect(screen.getByText("Lifecycle status")).toBeInTheDocument());

    fireEvent.change(screen.getByLabelText("Delete reason"), { target: { value: "Testing blockers" } });
    fireEvent.click(screen.getByRole("button", { name: "Delete domain" }));
    await waitFor(() => expect(screen.getByText("DELETE-DOMAIN-1")).toBeInTheDocument());
    const dialogConfirmButtons = screen.getAllByRole("button", { name: "Delete domain" });
    fireEvent.change(screen.getByRole("textbox", { name: /Type/i }), { target: { value: "DELETE-DOMAIN-1" } });
    fireEvent.click(dialogConfirmButtons[dialogConfirmButtons.length - 1]);

    await waitFor(() => expect(screen.getByText("2 mailboxes")).toBeInTheDocument());
    expect(screen.getByText("1 alias")).toBeInTheDocument();
    // Blocked deletion must not pretend success — the confirm dialog stays open, not silently closed/succeeded.
    expect(screen.getByText("DELETE-DOMAIN-1")).toBeInTheDocument();
  });

  it("stale-version conflict on DKIM generate does not pretend success and offers a reload of current state", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DKIM" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DKIM" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Generate DKIM" })).toBeInTheDocument());

    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (path === "/platform/domains/7/2/dkim/generate") {
        return Promise.reject(new MockApiError("CONFLICT", "domain version is no longer current", 409));
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path === "/platform/domains/7/2/dns") return Promise.resolve({ tenant_id: 7, domain_id: 2, domain: "beta.example", version: 1, status: "suspended", dkim_configured: false });
      if (path.startsWith("/platform/domains/7/2")) return Promise.resolve(DOMAINS.domains[1]);
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    fireEvent.click(screen.getByRole("button", { name: "Generate DKIM" }));

    await waitFor(() => expect(screen.getByRole("alert")).toBeInTheDocument());
    expect(screen.getByText("Not configured")).toBeInTheDocument(); // no fabricated success state
    expect(screen.getByRole("button", { name: "Reload current state" })).toBeInTheDocument();
  });

  // ── DKIM canonicalization + live DNS verification regression barrier ──

  it("DNS Setup auto-verifies once on open, calling the exact POST .../dns/verify route with tenant/domain ids", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DNS Setup" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));

    await waitFor(() => {
      const calls = mockedRequest.mock.calls.filter((c) => c[0] === "/platform/domains/7/2/dns/verify");
      expect(calls.length).toBe(1);
      expect((calls[0][1] as any)?.method).toBe("POST");
    });
  });

  it("does NOT auto-fire DNS verification when the DNS Setup tab is never visited (Overview/Lifecycle only)", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    // Drawer opens on Overview by default; the live DNS snapshot query
    // still fires (it's needed for the DKIM Overview summary), but the
    // verify MUTATION must not fire until DNS Setup is actually opened.
    await waitFor(() => expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument());
    expect(screen.queryByText("Lifecycle status")).not.toBeInTheDocument(); // sanity: still on overview
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Lifecycle" }));
    await waitFor(() => expect(screen.getByText("Lifecycle status")).toBeInTheDocument());

    expect(mockedRequest.mock.calls.filter((c) => c[0] === "/platform/domains/7/2/dns/verify")).toHaveLength(0);

    // Now visit DNS Setup — exactly then does verification fire, exactly once.
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));
    await waitFor(() => {
      expect(mockedRequest.mock.calls.filter((c) => c[0] === "/platform/domains/7/2/dns/verify")).toHaveLength(1);
    });

    // Switching away and back must not re-fire it a second time.
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Overview" }));
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));
    await new Promise((r) => setTimeout(r, 50));
    expect(mockedRequest.mock.calls.filter((c) => c[0] === "/platform/domains/7/2/dns/verify")).toHaveLength(1);
  });

  it("shows a 'Checking…' state while verification is in flight, then Matched with visible text and no color-only signal", async () => {
    renderPage(7);
    let resolveVerify!: (v: unknown) => void;
    mockedRequest.mockImplementation((path: string) => {
      if (path === "/platform/domains/7/2/dns/verify") {
        return new Promise((resolve) => { resolveVerify = resolve; });
      }
      if (path === "/platform/domains/7/2/dns") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", version: 1, status: "suspended", dkim_configured: false,
          dns_requirements: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay" }],
          dns_next_step: "publish_and_verify_dns",
        });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/domains/7/2")) return Promise.resolve(DOMAINS.domains[1]);
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DNS Setup" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));

    await waitFor(() => expect(screen.getByText("Checking DNS records…")).toBeInTheDocument());
    // Individual record badge is also "Checking…" while pending — not color-only.
    expect(screen.getAllByText("Checking…").length).toBeGreaterThan(0);

    resolveVerify({
      tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:55:00Z",
      records: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay", status: "verified", verified: true, observed: "10 relay.orvix.email" }],
      total_count: 1, matched_count: 1, issue_count: 0, all_verified: true,
    });

    await waitFor(() => expect(screen.getByText("All DNS records match")).toBeInTheDocument());
    expect(screen.getByText(/1 of 1 matched/)).toBeInTheDocument();
    expect(screen.getByText("Matched")).toBeInTheDocument();
  });

  it("Missing record renders a red Missing badge with Actual: Not found", async () => {
    renderPage(7);
    mockedRequest.mockImplementation((path: string) => {
      if (path === "/platform/domains/7/2/dns/verify") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:55:00Z",
          records: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay", status: "missing", verified: false, reason: "no MX records found" }],
          total_count: 1, matched_count: 0, issue_count: 1, all_verified: false,
        });
      }
      if (path === "/platform/domains/7/2/dns") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", version: 1, status: "suspended", dkim_configured: false,
          dns_requirements: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay" }],
          dns_next_step: "publish_and_verify_dns",
        });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/domains/7/2")) return Promise.resolve(DOMAINS.domains[1]);
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DNS Setup" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));

    await waitFor(() => expect(screen.getByText("Missing")).toBeInTheDocument());
    expect(screen.getByText("Not found")).toBeInTheDocument();
    expect(screen.getByText("DNS configuration needs attention")).toBeInTheDocument();
  });

  it("Mismatch record renders a red Mismatch badge with both Expected and Actual values visible", async () => {
    renderPage(7);
    mockedRequest.mockImplementation((path: string) => {
      if (path === "/platform/domains/7/2/dns/verify") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:55:00Z",
          records: [{ name: "beta.example", type: "TXT", value: "v=spf1 mx -all", ttl: 3600, required: true, purpose: "SPF", status: "mismatch", verified: false, observed: "v=spf1 include:someoneelse.example -all", reason: "SPF exists but differs from generated plan" }],
          total_count: 1, matched_count: 0, issue_count: 1, all_verified: false,
        });
      }
      if (path === "/platform/domains/7/2/dns") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", version: 1, status: "suspended", dkim_configured: false,
          dns_requirements: [{ name: "beta.example", type: "TXT", value: "v=spf1 mx -all", ttl: 3600, required: true, purpose: "SPF" }],
          dns_next_step: "publish_and_verify_dns",
        });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/domains/7/2")) return Promise.resolve(DOMAINS.domains[1]);
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DNS Setup" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));

    await waitFor(() => expect(screen.getByText("Mismatch")).toBeInTheDocument());
    expect(screen.getByText("v=spf1 mx -all")).toBeInTheDocument();
    expect(screen.getByText("v=spf1 include:someoneelse.example -all")).toBeInTheDocument();
  });

  it("resolver error renders an amber 'Check failed' state — never treated as mismatch/missing/verified", async () => {
    renderPage(7);
    mockedRequest.mockImplementation((path: string) => {
      if (path === "/platform/domains/7/2/dns/verify") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:55:00Z",
          records: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay", status: "error", verified: false, reason: "MX lookup failed: timeout" }],
          total_count: 1, matched_count: 0, issue_count: 1, all_verified: false,
        });
      }
      if (path === "/platform/domains/7/2/dns") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", version: 1, status: "suspended", dkim_configured: false,
          dns_requirements: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay" }],
          dns_next_step: "publish_and_verify_dns",
        });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/domains/7/2")) return Promise.resolve(DOMAINS.domains[1]);
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DNS Setup" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));

    await waitFor(() => expect(screen.getByText("Check failed")).toBeInTheDocument());
    expect(screen.getByText("MX lookup failed: timeout")).toBeInTheDocument();
    expect(screen.queryByText("Mismatch")).not.toBeInTheDocument();
    expect(screen.queryByText("Missing")).not.toBeInTheDocument();
    expect(screen.queryByText("Matched")).not.toBeInTheDocument();
  });

  it("Re-check DNS issues a second POST to the exact verify route and updates the rendered status", async () => {
    renderPage(7);
    let verifyCallCount = 0;
    mockedRequest.mockImplementation((path: string) => {
      if (path === "/platform/domains/7/2/dns/verify") {
        verifyCallCount += 1;
        if (verifyCallCount === 1) {
          return Promise.resolve({
            tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:00:00Z",
            records: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay", status: "missing", verified: false, reason: "no MX records found" }],
            total_count: 1, matched_count: 0, issue_count: 1, all_verified: false,
          });
        }
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:10:00Z",
          records: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay", status: "verified", verified: true, observed: "10 relay.orvix.email" }],
          total_count: 1, matched_count: 1, issue_count: 0, all_verified: true,
        });
      }
      if (path === "/platform/domains/7/2/dns") {
        return Promise.resolve({
          tenant_id: 7, domain_id: 2, domain: "beta.example", version: 1, status: "suspended", dkim_configured: false,
          dns_requirements: [{ name: "relay.orvix.email", type: "MX", value: "relay.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "Mail relay" }],
          dns_next_step: "publish_and_verify_dns",
        });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 12, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/domains/7/2")) return Promise.resolve(DOMAINS.domains[1]);
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      return Promise.resolve({});
    });

    await waitFor(() => expect(screen.getByText("beta.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("beta.example"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "DNS Setup" })).toBeInTheDocument());
    fireEvent.mouseDown(screen.getByRole("tab", { name: "DNS Setup" }));

    await waitFor(() => expect(screen.getByText("Missing")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Re-check DNS" }));

    await waitFor(() => expect(screen.getByText("Matched")).toBeInTheDocument());
    await waitFor(() => {
      const calls = mockedRequest.mock.calls.filter((c) => c[0] === "/platform/domains/7/2/dns/verify");
      expect(calls.length).toBe(2);
    });
    // Read-only: never mutates the domain, DKIM, or public DNS.
    expect(mockedRequest.mock.calls.some((c) => String(c[0]).includes("/dkim/"))).toBe(false);
    expect(mockedRequest.mock.calls.some((c) => String(c[0]).includes("/deactivate"))).toBe(false);
  });
});
