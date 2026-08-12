import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import RelaysPage from "./page";
import { request } from "../../../api";
import type { PlatformRelay } from "./contract";

vi.mock("../../../api", () => ({ request: vi.fn() }));

const mockedRequest = vi.mocked(request);

const RELAY: PlatformRelay = {
  id: 5,
  scope: "global",
  pool_id: 1,
  name: "primary",
  host: "smtp.provider.example",
  port: 587,
  username: "relay-user",
  conn_security: "starttls",
  tls_validation: "strict",
  priority: 10,
  weight: 1,
  active: true,
  rate_limit_per_min: 600,
  circuit_state: "closed",
  circuit_failures: 0,
  has_credential: true,
  version: 3,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
};

const RELAYS = { relays: [RELAY], total: 1, limit: 50, offset: 0 };

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><RelaysPage /></QueryClientProvider>);
}

describe("features/platform/relay", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/relays/5")) return Promise.resolve(RELAY);
      if (path.startsWith("/platform/relays")) return Promise.resolve(RELAYS);
      return Promise.resolve({});
    });
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("renders the redacted relay list: has_credential boolean only, never a secret", async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText("primary")).toBeInTheDocument());
    expect(screen.getByText("configured")).toBeInTheDocument();
    // The credential itself must never appear anywhere.
    expect(document.body.textContent).not.toMatch(/relay-user/);
    expect(document.body.textContent).not.toMatch(/password|secret/i);
  });

  it("sends Idempotency-Key on create and X-Confirm on delete with the typed phrase", async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText("primary")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Create relay" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "backup" } });
    fireEvent.change(within(dialog).getByLabelText("Host"), { target: { value: "smtp2.example" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Create relay" }));

    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => String(c[0]) === "/platform/relays" && (c[1] as { method?: string })?.method === "POST");
      expect(call).toBeDefined();
      const opts = call![1] as { headers?: Record<string, string>; body?: string };
      expect(opts.headers?.["Idempotency-Key"]).toBeTruthy();
      const body = JSON.parse(String(opts.body));
      expect(body.name).toBe("backup");
      expect(body.host).toBe("smtp2.example");
    });
  });

  it("rotate requires typed confirmation and shows the generated secret exactly once", async () => {
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (String(path).endsWith("/rotate-credentials") && opts?.method === "POST") {
        return Promise.resolve({ relay: RELAY, generated_password: "once-secret-xyz", show_once: true });
      }
      if (path.startsWith("/platform/relays/5")) return Promise.resolve(RELAY);
      if (path.startsWith("/platform/relays")) return Promise.resolve(RELAYS);
      return Promise.resolve({});
    });
    renderPage();
    await waitFor(() => expect(screen.getByText("primary")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Rotate credentials for primary/ }));

    const dialog = await screen.findByRole("dialog");
    const confirmBtn = within(dialog).getByRole("button", { name: "Rotate credentials" });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/Type ROTATE-RELAY-5 to confirm/i), { target: { value: "ROTATE-RELAY-5" } });
    await waitFor(() => expect(confirmBtn).toBeEnabled());
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(screen.getByText("once-secret-xyz")).toBeInTheDocument());
    expect(screen.getByText(/shown once/i)).toBeInTheDocument();

    // Dismissal is explicit and clears the secret from the DOM.
    fireEvent.click(screen.getByRole("button", { name: /I have saved it/ }));
    await waitFor(() => expect(screen.queryByText("once-secret-xyz")).not.toBeInTheDocument());
    expect(document.body.textContent).not.toMatch(/once-secret-xyz/);
  });

  it("shows only the safe connectivity-test result", async () => {
    mockedRequest.mockImplementation((path: string, opts?: Parameters<typeof request>[1]) => {
      if (String(path).endsWith("/test") && opts?.method === "POST") {
        return Promise.resolve({ connected: true, tls_negotiated: true, auth_ok: true, duration_ms: 120 });
      }
      if (path.startsWith("/platform/relays/5")) return Promise.resolve(RELAY);
      if (path.startsWith("/platform/relays")) return Promise.resolve(RELAYS);
      return Promise.resolve({});
    });
    renderPage();
    await waitFor(() => expect(screen.getByText("primary")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Test primary/ }));
    await waitFor(() => {
      const call = mockedRequest.mock.calls.find((c) => String(c[0]).endsWith("/test"));
      expect(call).toBeDefined();
      expect((call![1] as { headers?: Record<string, string> }).headers?.["Idempotency-Key"]).toBeTruthy();
    });
  });
});
