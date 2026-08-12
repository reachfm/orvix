import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import MailboxesPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";
import type { PlatformMailboxList } from "./contract";

vi.mock("../../../api", () => ({ request: vi.fn() }));

const mockedRequest = vi.mocked(request);

const MAILBOXES: PlatformMailboxList = {
  mailboxes: [
    {
      id: 101, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "alice@acme.example",
      name: "Alice", status: "active", is_admin: true, quota_mb: 1024, used_bytes: 1048576,
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
    },
    {
      id: 102, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "bob@acme.example",
      name: "Bob", status: "suspended", is_admin: false, quota_mb: 512, used_bytes: 0,
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
  return render(<QueryClientProvider client={qc}><MailboxesPage /></QueryClientProvider>);
}

describe("features/platform/mailboxes (platform routes)", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/organizations")) {
        return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 2, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      }
      if (path.startsWith("/platform/mailboxes/7/101")) {
        return Promise.resolve(MAILBOXES.mailboxes[0]);
      }
      if (path.startsWith("/platform/mailboxes/7")) {
        return Promise.resolve(MAILBOXES);
      }
      return Promise.resolve({});
    });
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("requires an explicit tenant and never fabricates one", () => {
    renderPage(null);
    expect(screen.getByText("Select a tenant")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("calls /platform/mailboxes/:tenant_id — never /mailboxes and never a support header", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    const calls = mockedRequest.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.startsWith("/platform/mailboxes/7"))).toBe(true);
    expect(calls.some((p) => p.startsWith("/mailboxes") && !p.startsWith("/platform/"))).toBe(false);
    for (const call of mockedRequest.mock.calls) {
      const opts = call[1] as { headers?: Record<string, string> } | undefined;
      expect(opts?.headers?.["X-Support-Tenant-ID"]).toBeUndefined();
    }
  });

  it("renders email, status, quota and usage from the real contract", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    expect(screen.getByText("bob@acme.example")).toBeInTheDocument();
    expect(screen.getAllByText("Active").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Suspended").length).toBeGreaterThan(0);
    expect(screen.getByText("1024 MB")).toBeInTheDocument();
  });

  it("resets the password through the platform route and shows the one-time credential with copy + explicit dismissal", async () => {
    let lastCall: { path: string; method?: string } | undefined;
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (String(path).endsWith("/reset-password")) {
        lastCall = { path: String(path), method: opts?.method };
        return Promise.resolve({ status: "ok", id: 101, generated_password: "once-only-secret-abc", show_once: true });
      }
      if (path.startsWith("/platform/organizations")) {
        return Promise.resolve({ organizations: [], total: 0 });
      }
      if (path.startsWith("/platform/mailboxes/7/101")) {
        return Promise.resolve(MAILBOXES.mailboxes[0]);
      }
      if (path.startsWith("/platform/mailboxes/7")) {
        return Promise.resolve(MAILBOXES);
      }
      return Promise.resolve({});
    });

    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("alice@acme.example"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Reset password" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Reset password" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Generate password" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Generate password" }));

    await waitFor(() => expect(screen.getByTestId("generated-password")).toHaveTextContent("once-only-secret-abc"));
    expect(screen.getByText(/shown once/i)).toBeInTheDocument();
    expect(lastCall?.path).toBe("/platform/mailboxes/7/101/reset-password");
    expect(lastCall?.method).toBe("POST");

    // The one-time warning can only be dismissed explicitly.
    fireEvent.click(screen.getByRole("button", { name: "I have saved it — dismiss" }));
    await waitFor(() => expect(screen.queryByTestId("generated-password")).not.toBeInTheDocument());
  });

  it("soft-deletes with the typed X-Confirm header on the platform route", async () => {
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (String(path) === "/platform/mailboxes/7/101" && opts?.method === "DELETE") {
        return Promise.resolve({ status: "ok", id: 101 });
      }
      if (path.startsWith("/platform/organizations")) {
        return Promise.resolve({ organizations: [], total: 0 });
      }
      if (path.startsWith("/platform/mailboxes/7/101")) {
        return Promise.resolve(MAILBOXES.mailboxes[0]);
      }
      if (path.startsWith("/platform/mailboxes/7")) {
        return Promise.resolve(MAILBOXES);
      }
      return Promise.resolve({});
    });

    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("alice@acme.example"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Soft-delete mailbox" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Soft-delete mailbox" }));

    const dialog = await screen.findByRole("dialog");
    // The confirm button stays disabled until the exact phrase is typed.
    const confirmBtn = within(dialog).getByRole("button", { name: "Delete mailbox" });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/Type PURGE-MAILBOX-101 to confirm/i), { target: { value: "PURGE-MAILBOX-101" } });
    await waitFor(() => expect(confirmBtn).toBeEnabled());
    fireEvent.click(confirmBtn);

    await waitFor(() => {
      const deleteCall = mockedRequest.mock.calls.find((c) => String(c[0]) === "/platform/mailboxes/7/101" && (c[1] as { method?: string })?.method === "DELETE");
      expect(deleteCall).toBeDefined();
      expect((deleteCall![1] as { headers?: Record<string, string> }).headers?.["X-Confirm"]).toBe("PURGE-MAILBOX-101");
    });
  });
});
