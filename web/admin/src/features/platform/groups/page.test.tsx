import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import GroupsPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";
import type { PlatformGroupList } from "./contract";

vi.mock("../../../api", () => ({ request: vi.fn() }));

const mockedRequest = vi.mocked(request);

const GROUPS: PlatformGroupList = {
  groups: [
    { id: 21, tenant_id: 7, name: "engineering@acme.example", description: "Engineering team", member_count: 3, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
  ],
  total: 1,
  limit: 25,
  offset: 0,
};

function renderPage(scopeTenantId: number | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (scopeTenantId !== null) {
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: scopeTenantId, tenantName: "Acme" });
  }
  return render(<QueryClientProvider client={qc}><GroupsPage /></QueryClientProvider>);
}

describe("features/platform/groups", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path === "/platform/groups/7/21/members") {
        return Promise.resolve({ group_id: 21, members: ["alice@acme.example", "bob@acme.example", "carol@acme.example"] });
      }
      if (path.startsWith("/platform/groups/7")) return Promise.resolve(GROUPS);
      return Promise.resolve({});
    });
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("requires an explicit tenant", () => {
    renderPage(null);
    expect(screen.getByText("Select a tenant")).toBeInTheDocument();
  });

  it("renders the group inventory from the platform route", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("engineering@acme.example")).toBeInTheDocument());
    expect(screen.getByText("3")).toBeInTheDocument();
    const calls = mockedRequest.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.startsWith("/platform/groups/7"))).toBe(true);
    expect(calls.some((p) => p.startsWith("/enterprise/groups"))).toBe(false);
  });

  it("shows members via the platform members route", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("engineering@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /View members of engineering@acme.example/ }));
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    expect(screen.getByText("carol@acme.example")).toBeInTheDocument();
    const membersCall = mockedRequest.mock.calls.find((c) => String(c[0]).endsWith("/platform/groups/7/21/members"));
    expect(membersCall).toBeDefined();
  });

  it("does not fabricate group mutation controls (no platform routes exist)", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("engineering@acme.example")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: /create group/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /add member/i })).not.toBeInTheDocument();
    expect(screen.getByText(/management mutations are not exposed/i)).toBeInTheDocument();
  });
});
