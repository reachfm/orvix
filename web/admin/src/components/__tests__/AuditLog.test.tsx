// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import AuditLog from "../AuditLog";
import { ToastProvider } from "../ui/Toast";
import * as apiModule from "../../api";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}><ToastProvider>{children}</ToastProvider></QueryClientProvider>;
}

function mockLogs() {
  return {
    logs: [
      { id: 1, action: "user.login", actor: "admin@orvix.io", role: "superadmin", target: "session", result: "success", timestamp: new Date().toISOString() },
      { id: 2, action: "user.status_update", actor: "op@orvix.io", role: "admin", target: "user:3", result: "success", timestamp: new Date(Date.now() - 120000).toISOString() },
      { id: 3, action: "org.create", actor: "system", role: "", target: "org:5", result: "failure", timestamp: new Date(Date.now() - 3600000).toISOString() },
    ],
    total: 3,
  };
}

describe("AuditLog", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders log list from mock API", async () => {
    vi.spyOn(apiModule.api, "listAdminAuditLogs").mockResolvedValue(mockLogs());
    render(<Wrapper><AuditLog /></Wrapper>);
    await waitFor(() => expect(screen.getByText("admin@orvix.io")).toBeInTheDocument());
    expect(screen.getByText("op@orvix.io")).toBeInTheDocument();
    expect(screen.getByText("system")).toBeInTheDocument();
  });

  it("result badges shown correctly", async () => {
    vi.spyOn(apiModule.api, "listAdminAuditLogs").mockResolvedValue(mockLogs());
    render(<Wrapper><AuditLog /></Wrapper>);
    await waitFor(() => expect(screen.getAllByText("success").length).toBeGreaterThan(0));
    expect(screen.getByText("failure")).toBeInTheDocument();
  });

  it("action formatting works", async () => {
    vi.spyOn(apiModule.api, "listAdminAuditLogs").mockResolvedValue(mockLogs());
    render(<Wrapper><AuditLog /></Wrapper>);
    await waitFor(() => expect(screen.getByText("User → Login")).toBeInTheDocument());
    await waitFor(() => expect(screen.getByText("User → Status update")).toBeInTheDocument());
  });

  it("export CSV button present and clickable", async () => {
    vi.spyOn(apiModule.api, "listAdminAuditLogs").mockResolvedValue(mockLogs());
    render(<Wrapper><AuditLog /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Export CSV")).toBeInTheDocument());
    const btn = screen.getByText("Export CSV");
    expect(btn).not.toBeDisabled();
    fireEvent.click(btn);
  });
});
