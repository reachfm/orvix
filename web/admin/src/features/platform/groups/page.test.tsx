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

  it("offers real group mutation controls wired to the platform routes", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("engineering@acme.example")).toBeInTheDocument());
    // Phase 1/2 added the platform mutation routes — the page must offer
    // create/delete/member-management controls backed by them, never a
    // read-only claim.
    expect(screen.queryByText(/management mutations are not exposed/i)).not.toBeInTheDocument();
    const createButton = screen.getByRole("button", { name: /create group/i });
    expect(createButton).toBeInTheDocument();
    // Delete control per row.
    expect(screen.getByRole("button", { name: /Delete group engineering@acme.example/ })).toBeInTheDocument();
    // Members drawer offers add-member.
    fireEvent.click(screen.getByRole("button", { name: /View members of engineering@acme.example/ }));
    await waitFor(() => expect(screen.getByPlaceholderText("member@example.com")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: /add/i })).toBeInTheDocument();
  });

  it("sends the typed X-Confirm DELETE-GROUP-<id> header on delete", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("engineering@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Delete group engineering@acme.example/ }));
    // ConfirmDialog requires the typed phrase before enabling the confirm.
    const confirmInput = await screen.findByRole("textbox");
    fireEvent.change(confirmInput, { target: { value: "DELETE-GROUP-21" } });
    fireEvent.click(screen.getByRole("button", { name: /Delete group/i }));
    await waitFor(() => {
      const deleteCall = mockedRequest.mock.calls.find((c) => String(c[0]) === "/platform/groups/7/21" && String(c[1]?.method) === "DELETE");
      expect(deleteCall).toBeDefined();
      expect((deleteCall![1] as { headers?: Record<string, string> }).headers?.["X-Confirm"]).toBe("DELETE-GROUP-21");
    });
  });

  it("creates a group through POST /platform/groups/:tenant_id", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByRole("button", { name: /create group/i })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /create group/i }));
    const nameInput = await screen.findByPlaceholderText("sales-team");
    fireEvent.change(nameInput, { target: { value: "sales@acme.example" } });
    fireEvent.click(screen.getByRole("button", { name: /create group/i }));
    await waitFor(() => {
      const createCall = mockedRequest.mock.calls.find((c) => String(c[0]) === "/platform/groups/7" && String(c[1]?.method) === "POST");
      expect(createCall).toBeDefined();
      expect(JSON.parse(String((createCall![1] as { body?: string }).body ?? "{}"))).toMatchObject({ name: "sales@acme.example" });
    });
  });
});
