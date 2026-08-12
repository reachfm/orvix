import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import BulkMailboxesPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";

vi.mock("../../../api", () => ({ request: vi.fn() }));

// Exercise the bounded-maximum guard with a small bound so the test
// stays fast; the page reads the bound from the same constant the
// production code uses (real value 500, enforced by the backend too).
vi.mock("./contract", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./contract")>();
  return { ...actual, BULK_MAILBOX_MAX: 3 };
});

const mockedRequest = vi.mocked(request);

const MAILBOXES = {
  mailboxes: [
    { id: 101, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "alice@acme.example", name: "Alice", status: "active", is_admin: true, quota_mb: 1024, used_bytes: 0, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
    { id: 102, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "bob@acme.example", name: "Bob", status: "active", is_admin: false, quota_mb: 512, used_bytes: 0, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
  ],
  total: 2,
  limit: 50,
  offset: 0,
};

function renderPage(scopeTenantId: number | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (scopeTenantId !== null) {
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: scopeTenantId, tenantName: "Acme" });
  }
  return render(<QueryClientProvider client={qc}><BulkMailboxesPage /></QueryClientProvider>);
}

describe("features/platform/bulk-mailboxes", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (path.startsWith("/platform/organizations")) {
        return Promise.resolve({ organizations: [], total: 0 });
      }
      if (path.startsWith("/platform/mailboxes/7/bulk/status")) {
        const body = JSON.parse(String(opts?.body ?? "{}"));
        return Promise.resolve({ total: body.ids.length, succeeded: body.ids.length, failed: undefined });
      }
      if (path.startsWith("/platform/mailboxes/7")) {
        return Promise.resolve(MAILBOXES);
      }
      return Promise.resolve({});
    });
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("requires an explicit tenant", () => {
    renderPage(null);
    expect(screen.getByText("Select a tenant")).toBeInTheDocument();
  });

  it("calls the production bulk endpoint once with ids+action — never a loop of single calls", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByLabelText("Select mailbox 101"));
    fireEvent.click(screen.getByLabelText("Select mailbox 102"));
    fireEvent.change(screen.getByLabelText("Bulk action"), { target: { value: "suspend" } });
    fireEvent.click(screen.getByRole("button", { name: /Apply Suspend/ }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Apply to selection" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Apply to selection" }));

    await waitFor(() => expect(screen.getByText(/Bulk result: 2\/2 succeeded/)).toBeInTheDocument());
    const bulkCalls = mockedRequest.mock.calls.filter((c) => String(c[0]).includes("/bulk/status"));
    expect(bulkCalls.length).toBe(1);
    const body = JSON.parse(String((bulkCalls[0][1] as { body?: string }).body));
    expect(body.ids).toEqual([101, 102]);
    expect(body.action).toBe("suspend");
  });

  it("explains partial-vs-atomic semantics exactly as returned (per-row failures)", async () => {
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (path.startsWith("/platform/organizations")) {
        return Promise.resolve({ organizations: [], total: 0 });
      }
      if (path.startsWith("/platform/mailboxes/7/bulk/status")) {
        return Promise.resolve({ total: 2, succeeded: 1, failed: [{ id: 102, error: "delete failed" }] });
      }
      if (path.startsWith("/platform/mailboxes/7")) {
        return Promise.resolve(MAILBOXES);
      }
      return Promise.resolve({});
    });
    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByLabelText("Select mailbox 101"));
    fireEvent.click(screen.getByLabelText("Select mailbox 102"));
    fireEvent.change(screen.getByLabelText("Bulk action"), { target: { value: "delete" } });
    fireEvent.click(screen.getByRole("button", { name: /Apply Soft-delete/ }));
    const dialog = await screen.findByRole("dialog");
    // Destructive delete requires typing the confirmation phrase.
    const confirmBtn = within(dialog).getByRole("button", { name: "Apply to selection" });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/Type DELETE to confirm/i), { target: { value: "DELETE" } });
    await waitFor(() => expect(confirmBtn).toBeEnabled());
    fireEvent.click(confirmBtn);
    await waitFor(() => expect(screen.getByText(/Bulk result: 1\/2 succeeded/)).toBeInTheDocument());
    expect(screen.getByText(/Mailbox #102: delete failed/)).toBeInTheDocument();
    expect(screen.getByText(/partial results are possible/i)).toBeInTheDocument();
  });

  it("enforces the bounded maximum selection", async () => {
    const many = Array.from({ length: 5 }, (_, i) => ({
      id: 1000 + i, tenant_id: 7, domain_id: 1, domain: "acme.example", email: `u${i}@acme.example`, name: "", status: "active", is_admin: false, quota_mb: 100, used_bytes: 0, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
    }));
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path.startsWith("/platform/mailboxes/7")) return Promise.resolve({ mailboxes: many, total: many.length, limit: 50, offset: 0 });
      return Promise.resolve({});
    });
    renderPage(7);
    await waitFor(() => expect(screen.getByLabelText("Select mailbox 1000")).toBeInTheDocument());
    for (const m of many.slice(0, 3)) {
      fireEvent.click(screen.getByLabelText(`Select mailbox ${m.id}`));
    }
    await waitFor(() => expect(screen.getByText(/3 selected/)).toBeInTheDocument());
    // The bound caps the selection: every unselected checkbox beyond
    // the bound is disabled (the browser blocks its click entirely).
    expect(screen.getByLabelText("Select mailbox 1003")).toBeDisabled();
    expect(screen.getByLabelText("Select mailbox 1004")).toBeDisabled();
  });
});

