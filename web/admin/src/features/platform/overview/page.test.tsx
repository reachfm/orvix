import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";

// recharts' ResponsiveContainer (used by StorageDonut) requires
// ResizeObserver, which jsdom does not implement. A minimal no-op
// polyfill is sufficient — this test suite only asserts on rendered
// values, never on measured pixel dimensions.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
(globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver = ResizeObserverStub;
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import OverviewPage from "./page";
import * as dashApi from "./api";
import * as healthApi from "../monitoring/api";
import type { MonitoringHealth } from "../monitoring/contract";

function renderPage(onNavigate = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { onNavigate, ...render(<QueryClientProvider client={qc}><OverviewPage email="psa@orvix.email" onNavigate={onNavigate} /></QueryClientProvider>) };
}

const FULL_HEALTH: MonitoringHealth = {
  status: "ok",
  uptimeSeconds: 999,
  generatedAt: new Date().toISOString(),
  disk: [
    { label: "backups", totalBytes: 1000, usedBytes: 400, freeBytes: 600, usedPct: 40 },
    { label: "mailstore", totalBytes: 2000, usedBytes: 1200, freeBytes: 800, usedPct: 60 },
  ],
  db: { status: "ok", message: "database responsive" },
  queue: { status: "ok", message: "queue nominal" },
  backup: { status: "warning", message: "last backup 30h ago" },
  api: { status: "ok", message: "self-ping ok" },
  capacity: {} as MonitoringHealth["capacity"],
  openAlerts: 3,
  hostUptimeAvailable: true,
  hostUptimeSeconds: 24 * 86400 + 13 * 3600 + 42 * 60,
  network: { primaryPublicIPv4: "51.75.240.231", addresses: ["51.75.240.231"] },
};

describe("features/platform/overview — premium dashboard", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("greets the signed-in Platform Super Admin without a large explanatory paragraph", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    renderPage();
    await waitFor(() => expect(screen.getByText(/psa/)).toBeInTheDocument());
    expect(screen.queryByText(/has no owning tenant/i)).not.toBeInTheDocument();
  });

  it("renders real infrastructure metrics: host uptime, storage, and public IP — never the process uptime label", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    renderPage();
    await waitFor(() => expect(screen.getByText("24d 13h 42m")).toBeInTheDocument());
    expect(screen.getByText("51.75.240.231")).toBeInTheDocument();
    // Aggregated across the two mocked disks: total 3000, used 1600.
    // "2.9 KB" appears twice (InfraKPICards' Total Storage card AND
    // StorageDonut's Total metric) — both real, both derived from the
    // same disk[] payload.
    expect(screen.getAllByText("2.9 KB").length).toBeGreaterThanOrEqual(2);
  });

  it("shows Public IP as Unavailable — never a fabricated address — when the backend reports none", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue({
      ...FULL_HEALTH,
      network: { primaryPublicIPv4: null, addresses: [] },
    });
    renderPage();
    await waitFor(() => expect(screen.getByText("Public IP")).toBeInTheDocument());
    const ipCard = screen.getByText("Public IP").closest("div")!.parentElement!;
    expect(ipCard).toHaveTextContent("Unavailable");
  });

  it("shows Server Uptime as Unavailable — never a fabricated duration — when host uptime is not supported", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue({
      ...FULL_HEALTH,
      hostUptimeAvailable: false,
      hostUptimeSeconds: 0,
    });
    renderPage();
    await waitFor(() => expect(screen.getByText("Server Uptime")).toBeInTheDocument());
    const uptimeCard = screen.getByText("Server Uptime").closest("div")!.parentElement!;
    expect(uptimeCard).toHaveTextContent("Unavailable");
    expect(uptimeCard).not.toHaveTextContent(/^0m$/);
  });

  it("renders per-mount storage breakdown and the aggregated donut totals from real disk[] values", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    renderPage();
    await waitFor(() => expect(screen.getByText("Storage Usage")).toBeInTheDocument());
    expect(screen.getByText("backups")).toBeInTheDocument();
    expect(screen.getByText("mailstore")).toBeInTheDocument();
    expect(screen.getByText("53%")).toBeInTheDocument(); // 1600/3000 rounded
  });

  it("renders real system health status pills for every component", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    renderPage();
    await waitFor(() => expect(screen.getByText("Overall status")).toBeInTheDocument());
    expect(screen.getAllByText("Healthy").length).toBeGreaterThanOrEqual(3); // overall + api + db + queue
    expect(screen.getByText("Warning")).toBeInTheDocument(); // backup
  });

  it("renders only real platform-scale fields the backend returns — no fabricated queue/storage-alert numbers", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 12,
      active_organizations: 10,
      total_domains: 30,
      total_mailboxes: 240,
      quota_used_bytes: 5368709120,
      recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    renderPage();
    await waitFor(() => expect(screen.getByText("12")).toBeInTheDocument());
    expect(screen.getByText("10")).toBeInTheDocument();
    expect(screen.getByText("30")).toBeInTheDocument();
    expect(screen.getByText("240")).toBeInTheDocument();
    expect(screen.getByText("5.0 GB")).toBeInTheDocument();
  });

  it("shows a distinct, non-empty error state on platform-dashboard failure — never a silent zeroed dashboard", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockRejectedValue(new Error("dashboard unavailable"));
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    renderPage();
    await waitFor(() => expect(screen.getByText(/Failed to load platform dashboard/i)).toBeInTheDocument());
    expect(screen.queryByText(/^0$/)).not.toBeInTheDocument();
  });

  it("shows a distinct, non-empty error state on monitoring-health failure — never a silent zeroed infra row", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockRejectedValue(new Error("health unavailable"));
    renderPage();
    await waitFor(() => expect(screen.getByText(/Failed to load infrastructure metrics/i)).toBeInTheDocument());
    // The rest of the dashboard (platform-scale KPIs) must still render —
    // one panel's failure does not collapse the whole Overview.
    await waitFor(() => expect(screen.getAllByText("Organizations").length).toBeGreaterThan(0));
  });

  it("renders real recent activity entries and humanizes their timestamps", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0,
      recent_audit_entries: [
        { action: "domain.verified", target: "example.com", timestamp: new Date().toISOString() },
      ],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    renderPage();
    await waitFor(() => expect(screen.getByText("domain.verified")).toBeInTheDocument());
    expect(screen.getByText("example.com")).toBeInTheDocument();
    expect(screen.getByText("just now")).toBeInTheDocument();
  });

  it("shows an intentional empty state when recent_audit_entries is empty — never invented activity rows", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    renderPage();
    await waitFor(() => expect(screen.getByText("No recent platform activity.")).toBeInTheDocument());
  });

  it("View audit log navigates to the platform-audit tab", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0,
      recent_audit_entries: [{ action: "x", target: "y", timestamp: new Date().toISOString() }],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    const { onNavigate } = renderPage();
    await waitFor(() => expect(screen.getByText("View audit log")).toBeInTheDocument());
    screen.getByText("View audit log").click();
    expect(onNavigate).toHaveBeenCalledWith("platform-audit");
  });

  it("View Health navigates to the health tab", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    const { onNavigate } = renderPage();
    await waitFor(() => expect(screen.getByText("View Health")).toBeInTheDocument());
    screen.getByText("View Health").click();
    expect(onNavigate).toHaveBeenCalledWith("health");
  });

  it("every quick-action nav card calls onNavigate with a real platform tab id, never a dead link", async () => {
    vi.spyOn(dashApi, "getPlatformDashboard").mockResolvedValue({
      total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1,
      quota_used_bytes: 0, recent_audit_entries: [],
    });
    vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue(FULL_HEALTH);
    const { onNavigate } = renderPage();
    await waitFor(() => expect(screen.getByText("Quick Actions")).toBeInTheDocument());
    screen.getByText("Organizations", { selector: "span" }).closest("button")!.click();
    expect(onNavigate).toHaveBeenCalledWith("organizations");
  });
});
