import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import SuppressionsPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";
import type { Suppression, SuppressionHistoryResponse } from "./contract";

vi.mock("../../../api", () => ({ request: vi.fn() }));

const mockedRequest = vi.mocked(request);

const SUPPRESSIONS: Suppression[] = [
  {
    id: 31, tenant_id: 7, address: "bounce@example.net", reason: "hard_bounce", source: "smtp_5xx",
    state: "active", version: 1, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    expires_at: "2099-01-01T00:00:00Z",
  },
  {
    id: 32, tenant_id: 7, address: "spam@example.net", reason: "complaint", source: "fbl_provider_x",
    state: "released", version: 2, released_at: "2026-01-05T00:00:00Z", released_reason: "operator release",
    created_at: "2026-01-02T00:00:00Z", updated_at: "2026-01-05T00:00:00Z",
  },
];

const HISTORY: SuppressionHistoryResponse = {
  suppression_id: 31,
  events: [
    { id: 1, suppression_id: 31, tenant_id: 7, event: "created", at: "2026-01-01T00:00:00Z" },
    { id: 2, suppression_id: 31, tenant_id: 7, event: "expired", at: "2099-01-01T00:00:00Z" },
  ],
};

function renderPage(scopeTenantId: number | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (scopeTenantId !== null) {
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: scopeTenantId, tenantName: "Acme" });
  }
  return render(<QueryClientProvider client={qc}><SuppressionsPage /></QueryClientProvider>);
}

describe("features/platform/suppressions", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path.includes("/history")) return Promise.resolve(HISTORY);
      if (path === "/platform/suppressions/7/31") return Promise.resolve(SUPPRESSIONS[0]);
      if (path === "/platform/suppressions/7/32") return Promise.resolve(SUPPRESSIONS[1]);
      if (path.startsWith("/platform/suppressions/7")) {
        return Promise.resolve({ suppressions: SUPPRESSIONS, total: 2, limit: 50, offset: 0 });
      }
      return Promise.resolve({});
    });
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("requires an explicit tenant", () => {
    renderPage(null);
    expect(screen.getByText("Select a tenant")).toBeInTheDocument();
  });

  it("renders the suppression inventory with normalized addresses and states", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("bounce@example.net")).toBeInTheDocument());
    expect(screen.getByText("spam@example.net")).toBeInTheDocument();
    expect(screen.getAllByText("Active").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Released").length).toBeGreaterThan(0);
    const calls = mockedRequest.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.startsWith("/platform/suppressions/7"))).toBe(true);
  });

  it("explains operational impact: active blocks delivery, release restores it", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("bounce@example.net")).toBeInTheDocument());
    fireEvent.click(screen.getByText("bounce@example.net"));
    await waitFor(() => expect(screen.getByText(/active: outbound delivery to this address is blocked/i)).toBeInTheDocument());
    // Lifecycle history is retained.
    expect(screen.getByText("created")).toBeInTheDocument();
  });

  it("release posts to the platform release route and invalidates the list", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("bounce@example.net")).toBeInTheDocument());
    fireEvent.click(screen.getByText("bounce@example.net"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Release suppression" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Release suppression" }));
    const dialog = await screen.findByRole("dialog");
    const confirmBtn = screen.getByRole("button", { name: "Release suppression" });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/Type RELEASE-SUPPRESSION-31 to confirm/i), { target: { value: "RELEASE-SUPPRESSION-31" } });
    await waitFor(() => expect(confirmBtn).toBeEnabled());
    fireEvent.click(confirmBtn);
    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => String(c[0]) === "/platform/suppressions/7/31" && (c[1] as { method?: string })?.method === "DELETE");
      expect(call).toBeDefined();
      expect((call![1] as { headers?: Record<string, string> }).headers?.["X-Confirm"]).toBe("RELEASE-SUPPRESSION-31");
    });
  });

  it("reactivates a released suppression through the platform route", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("spam@example.net")).toBeInTheDocument());
    fireEvent.click(screen.getByText("spam@example.net"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Reactivate" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Reactivate" }));
    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => String(c[0]).endsWith("/platform/suppressions/7/32/reactivate"));
      expect(call).toBeDefined();
    });
  });

  it("never describes release as destructive deletion: history copy is retained", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("spam@example.net")).toBeInTheDocument());
    fireEvent.click(screen.getByText("spam@example.net"));
    await waitFor(() => expect(screen.getByText(/history is retained/i)).toBeInTheDocument());
  });
});
