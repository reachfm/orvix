// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import App from "./App";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function mockResponse(data: any) {
  return { ok: true, json: () => Promise.resolve(data), status: 200 } as Response;
}

describe("Admin console certification", () => {
  beforeAll(() => {
    globalThis.fetch = vi.fn<typeof fetch>((url: any) => {
      const path = typeof url === "string" ? url.split("?")[0] : "";
      if (path.endsWith("/api/v1/me")) return Promise.resolve(mockResponse({ email: "admin@test", role: "admin" }));
      if (path.endsWith("/enterprise/dashboard")) return Promise.resolve(mockResponse({ total_domains: 3, active_mailboxes: 12, recent_actions: [] }));
      if (path.endsWith("/customer/domains")) return Promise.resolve(mockResponse([{ id: 1, name: "example.com", status: "active", verified: true, mx_status: "ok", spf_status: "ok", dkim_status: "ok", dmarc_status: "ok" }]));
      if (path.endsWith("/enterprise/audit/logs")) return Promise.resolve(mockResponse([{ id: 1, action: "user.login", actor: "admin@test", target: "", result: "success", timestamp: "2026-01-01T00:00:00Z" }]));
      if (path.includes("/enterprise/api-keys")) return Promise.resolve(mockResponse([]));
      if (path.includes("/enterprise/sessions")) return Promise.resolve(mockResponse({ sessions: [] }));
      if (path.includes("/enterprise/billing")) return Promise.resolve(mockResponse({ plan_name: "Pro", status: "active" }));
      return Promise.resolve(mockResponse({}));
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("loads the sidebar and dashboard view", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());
    await waitFor(() => expect(screen.getByText("Dashboard")).toBeInTheDocument());
  });

  it("navigates between tabs", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());

    fireEvent.click(screen.getAllByRole("button", { name: /domains/i })[0]);
    await waitFor(() => expect(screen.getByText("Domain Management")).toBeInTheDocument());

    fireEvent.click(screen.getAllByRole("button", { name: /audit log/i })[0]);
    await waitFor(() => expect(screen.getByText("Audit Log")).toBeInTheDocument());
  });

  it("exposes all admin navigation sections", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());

    for (const label of ["Domains", "Users", "Firewall", "Modules", "Audit Log"]) {
      expect(screen.getAllByRole("button", { name: new RegExp(label, "i") }).length).toBeGreaterThan(0);
    }
  });

  it("shows customer portal section", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());
    expect(screen.getByText("Customer Portal")).toBeInTheDocument();
  });

  it("shows the logout button", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: /logout/i })).toBeInTheDocument();
  });
});
