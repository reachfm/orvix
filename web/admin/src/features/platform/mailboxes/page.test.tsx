import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import MailboxesPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";
import type { PlatformMailboxList } from "./contract";

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

const MAILBOXES: PlatformMailboxList = {
  mailboxes: [
    {
      id: 101, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "alice@acme.example",
      name: "Alice", status: "active", is_admin: true, quota_mb: 1024, used_bytes: 1048576,
      mail_access_mode: "internal_external", effective_mail_access_mode: "internal_external", version: 1,
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
    },
    {
      id: 102, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "bob@acme.example",
      name: "Bob", status: "suspended", is_admin: false, quota_mb: 512, used_bytes: 0,
      mail_access_mode: "internal_only", effective_mail_access_mode: "internal_only", version: 1,
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
      if (path.startsWith("/platform/domains/7")) {
        return Promise.resolve({
          domains: [{
            id: 1, tenant_id: 7, name: "acme.example", status: "active", plan: "business",
            mailbox_count: 2, alias_count: 0, dkim_enabled: true, dmarc_enabled: true,
            mail_access_mode: "inherit", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
          }],
          total: 1, limit: 200, offset: 0,
        });
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

  it("always shows Create mailbox, even with no page-level tenant scope applied yet — the tenant selector lives inside the dialog", async () => {
    renderPage(null);
    const createButton = screen.getByRole("button", { name: "Create mailbox" });
    expect(createButton).toBeEnabled();

    fireEvent.click(createButton);
    await waitFor(() => expect(screen.getByLabelText(/Organization \/ tenant/)).toBeInTheDocument());
    // No domain can be chosen before a tenant is selected.
    expect(screen.getByLabelText("Domain *")).toBeDisabled();

    // Wait for the real organization options to load before selecting one.
    await waitFor(() => expect(screen.getByRole("option", { name: "Acme (tenant 7)" })).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText(/Organization \/ tenant/), { target: { value: "7" } });
    await waitFor(() => expect(screen.getByLabelText("Domain *")).toBeEnabled());
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

  it("requires a mail access mode choice, sends the exact create request, and never retains the password after success", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /create mailbox/i }));

    const createButton = screen.getByRole("button", { name: "Create mailbox" });
    // The domain selector is filtered to the currently-selected tenant's
    // active domains — wait for it to load before choosing one.
    await waitFor(() => expect(screen.getByRole("option", { name: "acme.example" })).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText("Domain *"), { target: { value: "1" } });
    fireEvent.change(screen.getByLabelText("Local part *"), { target: { value: "new" } });
    fireEvent.change(screen.getByLabelText("Password *"), { target: { value: "s3cret-pass!" } });
    // No access mode chosen yet — the button must stay disabled (mandatory choice).
    expect(createButton).toBeDisabled();

    fireEvent.click(screen.getByLabelText(/^Internal only/));
    expect(createButton).toBeEnabled();

    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (path === "/platform/mailboxes/7" && opts?.method === "POST") {
        return Promise.resolve({
          mailbox: { id: 200, tenant_id: 7, domain_id: 1, domain: "acme.example", email: "new@acme.example", name: "", status: "active", is_admin: false, quota_mb: 1024, used_bytes: 0, mail_access_mode: "internal_only", effective_mail_access_mode: "internal_only", version: 1, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
        });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [{ id: 7, name: "Acme", slug: "acme", domain: "acme.example", plan: "business", active: true, mailbox_count: 2, domain_count: 1, created_at: "2026-01-01T00:00:00Z" }], total: 1 });
      if (path.startsWith("/platform/mailboxes/7")) return Promise.resolve(MAILBOXES);
      return Promise.resolve({});
    });

    fireEvent.click(createButton);
    await waitFor(() => expect(screen.getByText("Mailbox created")).toBeInTheDocument());

    const createCall = mockedRequest.mock.calls.find((c) => c[0] === "/platform/mailboxes/7" && (c[1] as any)?.method === "POST");
    expect(createCall).toBeDefined();
    const opts = createCall![1] as { body: string; headers?: Record<string, string> };
    const body = JSON.parse(opts.body);
    expect(body).toEqual({ email: "new@acme.example", password: "s3cret-pass!", force_password_change: true, mail_access_mode: "internal_only" });
    expect(opts.headers?.["Idempotency-Key"]).toBeTruthy();

    // The password never appears anywhere in the now-rendered success view.
    expect(screen.queryByDisplayValue("s3cret-pass!")).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain("s3cret-pass!");
    // And the password input, if still mounted anywhere, must be empty.
    const pwField = screen.queryByLabelText("Password *") as HTMLInputElement | null;
    if (pwField) expect(pwField.value).toBe("");
  });

  it("does not offer 'inherit' as a mailbox creation access-mode choice", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /create mailbox/i }));
    expect(screen.queryByLabelText(/inherit/i)).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^Internal only/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Internal and external/)).toBeInTheDocument();
  });

  it("distinguishes configured vs effective mail access and mutates access mode with the real read version", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("alice@acme.example"));
    await waitFor(() => expect(screen.getByText("Mail access policy")).toBeInTheDocument());
    expect(screen.getByText(/Configured: Internal and external/)).toBeInTheDocument();
    expect(screen.getByText(/Effective: Internal and external/)).toBeInTheDocument();

    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (String(path).endsWith("/access-mode") && opts?.method === "POST") {
        return Promise.resolve({ id: 101, mail_access_mode: "internal_only", effective_mail_access_mode: "internal_only", version: 2 });
      }
      if (path.startsWith("/platform/mailboxes/7/101")) return Promise.resolve(MAILBOXES.mailboxes[0]);
      if (path.startsWith("/platform/mailboxes/7")) return Promise.resolve(MAILBOXES);
      return Promise.resolve({});
    });

    fireEvent.change(screen.getByLabelText("New mail access mode"), { target: { value: "internal_only" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply mode" }));

    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => String(c[0]).endsWith("/access-mode") && (c[1] as any)?.method === "POST");
      expect(call).toBeDefined();
      const body = JSON.parse((call![1] as { body: string }).body);
      // version:1 is the REAL value from the fixture's list/get response,
      // not a hardcoded assumption — proving List/Get now genuinely carry it.
      expect(body).toEqual({ mail_access_mode: "internal_only", expected_version: 1 });
      expect((call![1] as { headers?: Record<string, string> }).headers?.["Idempotency-Key"]).toBeTruthy();
    });
  });

  it("on a stale-version conflict, explains the change and refetches rather than silently overwriting", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("alice@acme.example"));
    await waitFor(() => expect(screen.getByText("Mail access policy")).toBeInTheDocument());

    let refetchCount = 0;
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (String(path).endsWith("/access-mode") && opts?.method === "POST") {
        return Promise.reject(new MockApiError("PRECONDITION_FAILED", "mailbox version conflict: re-read the mailbox and retry", 412));
      }
      if (path.startsWith("/platform/mailboxes/7/101")) {
        refetchCount += 1;
        return Promise.resolve(MAILBOXES.mailboxes[0]);
      }
      if (path.startsWith("/platform/mailboxes/7")) return Promise.resolve(MAILBOXES);
      return Promise.resolve({});
    });

    fireEvent.change(screen.getByLabelText("New mail access mode"), { target: { value: "internal_only" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply mode" }));

    await waitFor(() => expect(screen.getByText(/changed elsewhere since it was last read/)).toBeInTheDocument());
    // The mutation must trigger a refetch of the detail record (never
    // silently overwrite the local, now-stale view).
    await waitFor(() => expect(refetchCount).toBeGreaterThan(0));
  });

  it("shows Access mailbox as a distinct action, requires ticket/reason/typed confirmation, and starts a read-only session without ever sending a password", async () => {
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (String(path) === "/platform/mailboxes/7/101/support-view" && opts?.method === "POST") {
        return Promise.resolve({
          session_id: "sess-abc123",
          tenant_id: 7,
          mailbox_id: 101,
          email: "alice@acme.example",
          mode: "read_only",
          expires_at: "2026-01-01T01:00:00Z",
        });
      }
      if (String(path).includes("/support-view/sess-abc123/folders")) {
        return Promise.resolve({ folders: [{ id: 1, mailbox_id: 101, name: "Inbox", path: "INBOX", folder_type: "inbox", message_count: 1, unread_count: 0, total_size: 0 }] });
      }
      if (String(path).includes("/support-view/sess-abc123/messages")) {
        return Promise.resolve({ messages: [], total: 0 });
      }
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path.startsWith("/platform/mailboxes/7/101")) return Promise.resolve(MAILBOXES.mailboxes[0]);
      if (path.startsWith("/platform/mailboxes/7")) return Promise.resolve(MAILBOXES);
      return Promise.resolve({});
    });

    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("alice@acme.example"));
    await waitFor(() => expect(screen.getByRole("button", { name: /Access mailbox/i })).toBeInTheDocument());
    // Distinct from the other mailbox actions.
    expect(screen.getByRole("button", { name: "Reset password" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Soft-delete mailbox" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Access mailbox/i }));
    const startButton = await screen.findByRole("button", { name: "Start read-only session" });
    expect(startButton).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/Ticket \/ reference/), { target: { value: "SUP-9" } });
    fireEvent.change(screen.getByLabelText("Reason"), { target: { value: "Customer escalation" } });
    expect(startButton).toBeDisabled(); // confirmation phrase not typed yet
    fireEvent.change(screen.getByLabelText(/Type ACCESS-MAILBOX-101/), { target: { value: "ACCESS-MAILBOX-101" } });
    expect(startButton).toBeEnabled();
    fireEvent.click(startButton);

    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => c[0] === "/platform/mailboxes/7/101/support-view");
      expect(call).toBeDefined();
      const body = JSON.parse((call![1] as { body: string }).body);
      expect(body).toEqual({ ticket_ref: "SUP-9", reason: "Customer escalation", duration_minutes: 30, confirm: "ACCESS-MAILBOX-101" });
    });
    // The request body must never carry a password field of any kind.
    for (const call of mockedRequest.mock.calls) {
      if (call[0] !== "/platform/mailboxes/7/101/support-view") continue;
      const body = JSON.parse((call[1] as { body: string }).body);
      expect(body).not.toHaveProperty("password");
    }

    // Viewer opens with the persistent read-only banner.
    await waitFor(() => expect(screen.getByRole("dialog", { name: "Support mailbox viewer" })).toBeInTheDocument());
    expect(screen.getByText("Support access")).toBeInTheDocument();
    expect(screen.getByText(/Read-only/)).toBeInTheDocument();
    // No write controls anywhere in the viewer.
    for (const label of [/compose/i, /reply/i, /forward/i, /^delete$/i, /mark as read/i, /mark as unread/i]) {
      expect(screen.queryByRole("button", { name: label })).not.toBeInTheDocument();
    }
  });

  it("ends the support session via the audited end route and shows the ended state", async () => {
    let ended = false;
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (String(path) === "/platform/mailboxes/7/101/support-view" && opts?.method === "POST") {
        return Promise.resolve({ session_id: "sess-xyz", tenant_id: 7, mailbox_id: 101, email: "alice@acme.example", mode: "read_only", expires_at: "2026-01-01T01:00:00Z" });
      }
      if (String(path) === "/platform/mailboxes/7/101/support-view/sess-xyz/end" && opts?.method === "POST") {
        ended = true;
        return Promise.resolve({ session_id: "sess-xyz", ended: true });
      }
      if (String(path).includes("/support-view/sess-xyz/folders")) return Promise.resolve({ folders: [] });
      if (String(path).includes("/support-view/sess-xyz/messages")) return Promise.resolve({ messages: [], total: 0 });
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path.startsWith("/platform/mailboxes/7/101")) return Promise.resolve(MAILBOXES.mailboxes[0]);
      if (path.startsWith("/platform/mailboxes/7")) return Promise.resolve(MAILBOXES);
      return Promise.resolve({});
    });

    renderPage(7);
    await waitFor(() => expect(screen.getByText("alice@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByText("alice@acme.example"));
    fireEvent.click(await screen.findByRole("button", { name: /Access mailbox/i }));
    fireEvent.change(screen.getByLabelText(/Ticket \/ reference/), { target: { value: "SUP-9" } });
    fireEvent.change(screen.getByLabelText("Reason"), { target: { value: "Customer escalation" } });
    fireEvent.change(screen.getByLabelText(/Type ACCESS-MAILBOX-101/), { target: { value: "ACCESS-MAILBOX-101" } });
    fireEvent.click(screen.getByRole("button", { name: "Start read-only session" }));

    await waitFor(() => expect(screen.getByRole("dialog", { name: "Support mailbox viewer" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "End access" }));

    await waitFor(() => expect(ended).toBe(true));
    await waitFor(() => expect(screen.getByText(/support session has ended/i)).toBeInTheDocument());
  });
});
