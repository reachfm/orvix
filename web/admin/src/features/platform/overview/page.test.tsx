import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import OverviewPage from "./page";
import * as api from "./api";

function renderPage(onNavigate = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { onNavigate, ...render(<QueryClientProvider client={qc}><OverviewPage email="psa@orvix.email" onNavigate={onNavigate} /></QueryClientProvider>) };
}

describe("features/platform/overview", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("shows a distinct loading state before the dashboard resolves", () => {
    vi.spyOn(api, "getPlatformDashboard").mockReturnValue(new Promise(() => {}));
    renderPage();
    expect(screen.getByRole("status")).toHaveTextContent(/loading platform totals/i);
  });

  it("renders only real fields the backend returns — no fabricated queue/storage-alert numbers", async () => {
    vi.spyOn(api, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 12,
      active_organizations: 10,
      total_domains: 30,
      total_mailboxes: 240,
      quota_used_bytes: 5368709120,
      recent_audit_entries: [],
    });
    renderPage();
    await waitFor(() => expect(screen.getByText("12")).toBeInTheDocument());
    expect(screen.getByText("10 active")).toBeInTheDocument();
    expect(screen.getByText("30")).toBeInTheDocument();
    expect(screen.getByText("240")).toBeInTheDocument();
    expect(screen.getByText("5.0 GB")).toBeInTheDocument();
    // Never a fake "Queue" stat — PlatformDashboard has no queue field.
    expect(screen.queryByText("Queue")).not.toBeInTheDocument();
  });

  it("shows a distinct, non-empty error state on failure — never a silent zeroed dashboard", async () => {
    vi.spyOn(api, "getPlatformDashboard").mockRejectedValue(new Error("dashboard unavailable"));
    renderPage();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/dashboard unavailable/i));
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  it("every nav card calls onNavigate with a real platform tab id, never a dead link", async () => {
    vi.spyOn(api, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    const { onNavigate } = renderPage();
    await waitFor(() => expect(screen.getByText("Organizations")).toBeInTheDocument());
    screen.getByText("Organizations").closest("button")!.click();
    expect(onNavigate).toHaveBeenCalledWith("organizations");
  });
});
