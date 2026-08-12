import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import DomainsPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";
import type { PlatformDomainList } from "./contract";

vi.mock("../../../api", () => ({ request: vi.fn() }));

const mockedRequest = vi.mocked(request);

const DOMAINS: PlatformDomainList = {
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
      if (path.startsWith("/platform/domains/7/1")) {
        return Promise.resolve(DOMAINS.domains[0]);
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

  it("opens the detail drawer with real fields and mutates mail-access mode via the platform route", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByText("Mail access policy")).toBeInTheDocument());
    expect(screen.getByText(/local-to-local delivery remains permitted/i)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("New mail access mode"), { target: { value: "internal_only" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply mode" }));
    await waitFor(() => {
      const calls = mockedRequest.mock.calls.filter((c) => String(c[0]).endsWith("/mail-access-mode"));
      expect(calls.length).toBe(1);
      expect(JSON.parse(String(calls[0][1] && (calls[0][1] as { body?: string }).body))).toEqual({ mail_access_mode: "internal_only" });
    });
  });

  it("does not invent DNS/DKIM-rotate/TLS controls that the platform routes do not expose", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("acme.example"));
    await waitFor(() => expect(screen.getByText("Mail access policy")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: /rotate dkim/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /verify dns/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /create domain/i })).not.toBeInTheDocument();
  });
});
