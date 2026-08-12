import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import AliasesPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";
import { ApiError } from "../../../api";
import type { PlatformAliasList } from "./contract";

vi.mock("../../../api", () => ({ request: vi.fn(), ApiError: class ApiError extends Error { code: string; status: number; body: unknown; constructor(code: string, message: string, status: number, body?: unknown) { super(message); this.code = code; this.status = status; this.body = body; } } }));

const mockedRequest = vi.mocked(request);

const ALIASES: PlatformAliasList = {
  aliases: [
    { id: 11, tenant_id: 7, domain_id: 1, from_addr: "sales@acme.example", to_addr: "alice@acme.example", active: true, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
  ],
  total: 1,
  limit: 25,
  offset: 0,
};

const DOMAINS = {
  domains: [{ id: 1, tenant_id: 7, name: "acme.example", status: "active", plan: "business", mailbox_count: 2, alias_count: 1, dkim_enabled: true, dmarc_enabled: true, mail_access_mode: "internal_external", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" }],
  total: 1,
  limit: 25,
  offset: 0,
};

function renderPage(scopeTenantId: number | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (scopeTenantId !== null) {
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: scopeTenantId, tenantName: "Acme" });
  }
  return render(<QueryClientProvider client={qc}><AliasesPage /></QueryClientProvider>);
}

describe("features/platform/aliases", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      if (path.startsWith("/platform/aliases/7")) return Promise.resolve(ALIASES);
      return Promise.resolve({});
    });
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("requires an explicit tenant", () => {
    renderPage(null);
    expect(screen.getByText("Select a tenant")).toBeInTheDocument();
  });

  it("renders the alias inventory from the platform route", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("sales@acme.example")).toBeInTheDocument());
    expect(screen.getByText("alice@acme.example")).toBeInTheDocument();
    const calls = mockedRequest.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.startsWith("/platform/aliases/7"))).toBe(true);
    expect(calls.some((p) => p.startsWith("/enterprise/aliases"))).toBe(false);
  });

  it("creates an alias through POST /platform/aliases/:tenant_id with domain scoping", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("sales@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Create alias" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => expect(within(dialog).getByLabelText("Domain")).toBeInTheDocument());
    fireEvent.change(within(dialog).getByLabelText("Domain"), { target: { value: "1" } });
    fireEvent.change(within(dialog).getByLabelText("Source address (alias)"), { target: { value: "info@acme.example" } });
    fireEvent.change(within(dialog).getByLabelText("Destination address"), { target: { value: "alice@acme.example" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Create alias" }));
    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => String(c[0]) === "/platform/aliases/7" && (c[1] as { method?: string })?.method === "POST");
      expect(call).toBeDefined();
      const body = JSON.parse(String((call![1] as { body?: string }).body));
      expect(body).toEqual({ domain_id: 1, from_addr: "info@acme.example", to_addr: "alice@acme.example" });
    });
  });

  it("surfaces a typed duplicate-alias error without inventing success", async () => {
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path.startsWith("/platform/domains/7")) return Promise.resolve(DOMAINS);
      if (path === "/platform/aliases/7" && opts?.method === "POST") {
        return Promise.reject(new ApiError("CONFLICT", "alias already exists", 409, { error: "alias already exists", code: "CONFLICT" }));
      }
      if (path.startsWith("/platform/aliases/7")) return Promise.resolve(ALIASES);
      return Promise.resolve({});
    });
    renderPage(7);
    await waitFor(() => expect(screen.getByText("sales@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Create alias" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => expect(within(dialog).getByLabelText("Domain")).toBeInTheDocument());
    fireEvent.change(within(dialog).getByLabelText("Domain"), { target: { value: "1" } });
    fireEvent.change(within(dialog).getByLabelText("Source address (alias)"), { target: { value: "dup@acme.example" } });
    fireEvent.change(within(dialog).getByLabelText("Destination address"), { target: { value: "alice@acme.example" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Create alias" }));
    await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(/Conflict/));
    expect(within(dialog).getByRole("button", { name: "Create alias" })).toBeInTheDocument(); // dialog stays open
  });

  it("deletes an alias only after typing its exact source address", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("sales@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByLabelText("Delete alias sales@acme.example"));
    const dialog = await screen.findByRole("dialog");
    const confirmBtn = within(dialog).getByRole("button", { name: "Delete alias" });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/Type sales@acme.example to confirm/i), { target: { value: "wrong" } });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/Type sales@acme.example to confirm/i), { target: { value: "sales@acme.example" } });
    await waitFor(() => expect(confirmBtn).toBeEnabled());
    fireEvent.click(confirmBtn);
    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => String(c[0]) === "/platform/aliases/7/11" && (c[1] as { method?: string })?.method === "DELETE");
      expect(call).toBeDefined();
    });
  });
});
