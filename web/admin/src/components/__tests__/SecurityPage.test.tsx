// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import SecurityPage from "../SecurityPage";
import { ToastProvider } from "../ui/Toast";
import * as apiModule from "../../api";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}><ToastProvider>{children}</ToastProvider></QueryClientProvider>;
}

function mockRules() {
  return [
    { id: 1, type: "allow", ip_range: "10.0.0.0/8", description: "Internal network", created_at: "2026-01-15T00:00:00Z" },
    { id: 2, type: "block", ip_range: "203.0.113.0/24", description: "Known spammer", created_at: "2026-03-20T00:00:00Z" },
  ];
}

function mockSessions() {
  return {
    sessions: [
      { id: "sess_1", user_email: "admin@orvix.io", ip_address: "192.168.1.1", user_agent: "Mozilla/5.0", created_at: "2026-07-27T00:00:00Z", last_active_at: "2026-07-27T04:00:00Z", is_current: true },
      { id: "sess_2", user_email: "user@example.com", ip_address: "10.0.0.5", user_agent: "Chrome/120", created_at: "2026-07-26T00:00:00Z", last_active_at: "2026-07-26T12:00:00Z", is_current: false },
    ],
  };
}

describe("SecurityPage", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders firewall tab with rules", async () => {
    vi.spyOn(apiModule.api, "listFirewallRules").mockResolvedValue(mockRules());
    render(<Wrapper><SecurityPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Internal network")).toBeInTheDocument());
    expect(screen.getByText("Known spammer")).toBeInTheDocument();
  });

  it("firewall rule type badge shown (allow=teal)", async () => {
    vi.spyOn(apiModule.api, "listFirewallRules").mockResolvedValue(mockRules());
    render(<Wrapper><SecurityPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("allow")).toBeInTheDocument());
    expect(screen.getByText("block")).toBeInTheDocument();
  });

  it("sessions tab shows terminate button", async () => {
    vi.spyOn(apiModule.api, "listFirewallRules").mockResolvedValue([]);
    vi.spyOn(apiModule.api, "listSessions").mockResolvedValue(mockSessions());
    render(<Wrapper><SecurityPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Active Sessions")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Active Sessions"));
    await waitFor(() => expect(screen.getByText("user@example.com")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Terminate"));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
  });
});
