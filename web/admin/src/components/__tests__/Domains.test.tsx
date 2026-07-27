// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import Domains from "../Domains";
import { ToastProvider } from "../ui/Toast";
import * as apiModule from "../../api";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}><ToastProvider>{children}</ToastProvider></QueryClientProvider>;
}

function mockDomains() {
  return [
    { id: 1, domain: "example.com", plan: "pro", status: "active", mailbox_count: 12, tenant_id: 1, mx_status: "ok", spf_status: "ok", dkim_status: "ok", dmarc_status: "ok", created_at: "2026-01-15T00:00:00Z", updated_at: "2026-06-01T00:00:00Z" },
    { id: 2, domain: "test.org", plan: "starter", status: "disabled", mailbox_count: 3, tenant_id: 2, mx_status: "error", spf_status: "ok", dkim_status: "pending", dmarc_status: "unknown", created_at: "2026-03-20T00:00:00Z", updated_at: "2026-07-10T00:00:00Z" },
  ];
}

describe("Domains", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders domain list from mock", async () => {
    vi.spyOn(apiModule.api, "listAdminDomains").mockResolvedValue(mockDomains());
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());
    expect(screen.getByText("test.org")).toBeInTheDocument();
    expect(screen.getByText("pro")).toBeInTheDocument();
  });

  it("shows status badges correctly", async () => {
    vi.spyOn(apiModule.api, "listAdminDomains").mockResolvedValue(mockDomains());
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());
    const badges = screen.getAllByText(/active|disabled/i);
    expect(badges.length).toBeGreaterThanOrEqual(2);
  });

  it("opens detail drawer on row click", async () => {
    vi.spyOn(apiModule.api, "listAdminDomains").mockResolvedValue(mockDomains());
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());
    const viewBtns = screen.getAllByText("View");
    fireEvent.click(viewBtns[0]);
    await waitFor(() => expect(screen.getByText("DNS Verification")).toBeInTheDocument());
  });

  it("disable action shows confirmation dialog", async () => {
    vi.spyOn(apiModule.api, "listAdminDomains").mockResolvedValue(mockDomains());
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());
    const viewBtns = screen.getAllByText("View");
    fireEvent.click(viewBtns[0]);
    await waitFor(() => expect(screen.getByText("Disable")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Disable"));
    await waitFor(() => expect(screen.getByText(/disable example/i)).toBeInTheDocument());
  });
});
