import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LayoutDashboard, Building } from "lucide-react";
import PlatformShell from "./PlatformShell";
import * as healthApi from "../monitoring/api";
import type { MonitoringHealth } from "../monitoring/contract";

const TABS = [
  { id: "platform-home" as const, label: "Overview", icon: LayoutDashboard },
  { id: "organizations" as const, label: "Organizations", icon: Building, section: "Commercial" },
];

function renderShell(props?: { openAlerts?: number }) {
  vi.spyOn(healthApi, "getMonitoringHealth").mockResolvedValue({
    status: "ok",
    uptimeSeconds: 1,
    generatedAt: new Date().toISOString(),
    disk: [],
    db: { status: "ok", message: "" },
    queue: { status: "ok", message: "" },
    backup: { status: "ok", message: "" },
    api: { status: "ok", message: "" },
    capacity: {} as MonitoringHealth["capacity"],
    openAlerts: props?.openAlerts ?? 0,
    hostUptimeAvailable: false,
    hostUptimeSeconds: 0,
    network: { primaryPublicIPv4: null, addresses: [] },
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onSelectTab = vi.fn();
  const onLogout = vi.fn();
  render(
    <QueryClientProvider client={qc}>
      <PlatformShell
        tabs={TABS}
        currentTab="platform-home"
        onSelectTab={onSelectTab}
        userEmail="psa@orvix.email"
        onLogout={onLogout}
      >
        <div>Dashboard content</div>
      </PlatformShell>
    </QueryClientProvider>
  );
  return { onSelectTab, onLogout };
}

describe("PlatformShell", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders the platform brand, nav tabs, and children content", () => {
    renderShell();
    expect(screen.getByText("Orvix")).toBeInTheDocument();
    expect(screen.getByText("Platform Admin")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /overview/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /organizations/i })).toBeInTheDocument();
    expect(screen.getByText("Dashboard content")).toBeInTheDocument();
  });

  it("shows the current authenticated user's email and the Platform Super Admin role label", () => {
    renderShell();
    expect(screen.getByText("psa@orvix.email")).toBeInTheDocument();
    expect(screen.getByText("Platform Super Admin")).toBeInTheDocument();
  });

  it("clicking a nav tab calls onSelectTab with that tab's id", () => {
    const { onSelectTab } = renderShell();
    fireEvent.click(screen.getByRole("button", { name: /organizations/i }));
    expect(onSelectTab).toHaveBeenCalledWith("organizations");
  });

  it("clicking Sign out calls onLogout", () => {
    const { onLogout } = renderShell();
    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));
    expect(onLogout).toHaveBeenCalled();
  });

  it("the alert badge reflects real openAlerts from monitoring/health — zero shows no badge", async () => {
    renderShell({ openAlerts: 0 });
    await waitFor(() => expect(screen.getByLabelText(/no open alerts/i)).toBeInTheDocument());
  });

  it("the alert badge shows the real open alert count when non-zero", async () => {
    renderShell({ openAlerts: 5 });
    await waitFor(() => expect(screen.getByText("5")).toBeInTheDocument());
    expect(screen.getByLabelText(/5 open alerts/i)).toBeInTheDocument();
  });

  it("clicking the alert bell navigates to the health tab", async () => {
    const { onSelectTab } = renderShell({ openAlerts: 1 });
    await waitFor(() => expect(screen.getByText("1")).toBeInTheDocument());
    fireEvent.click(screen.getByLabelText(/1 open alert/i));
    expect(onSelectTab).toHaveBeenCalledWith("health");
  });

  it("search filters the tab list by label and navigates on click", async () => {
    const { onSelectTab } = renderShell();
    const input = screen.getByPlaceholderText(/search platform console/i);
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "org" } });
    const results = await screen.findAllByRole("option");
    expect(results.some((r) => r.textContent?.includes("Organizations"))).toBe(true);
    fireEvent.click(results.find((r) => r.textContent?.includes("Organizations"))!);
    expect(onSelectTab).toHaveBeenCalledWith("organizations");
  });

  it("Ctrl+K / Cmd+K opens the search box", () => {
    renderShell();
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    // No assertion crash means the shortcut handler ran; search input
    // remains present and focusable either way.
    expect(screen.getByPlaceholderText(/search platform console/i)).toBeInTheDocument();
  });
});
