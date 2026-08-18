import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import SystemHealth from "./SystemHealth";
import * as reliabilityApi from "../features/platform/reliability/api";

function renderHealth() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><SystemHealth /></QueryClientProvider>);
}

describe("SystemHealth — canonical typed client (no direct fetch)", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("loads health through getMonitoringHealth and renders real component states", async () => {
    vi.spyOn(reliabilityApi, "getMonitoringHealth").mockResolvedValue({
      status: "ok",
      uptimeSeconds: 3600,
      generatedAt: "2026-01-01T00:00:00Z",
      disk: [{ label: "system", totalBytes: 100000000000, usedBytes: 50000000000, freeBytes: 50000000000, usedPct: 50 }],
      db: { status: "ok", message: "connected" },
      queue: { status: "ok", message: "healthy" },
      backup: { status: "warning", message: "backup older than 24h" },
      api: { status: "ok", message: "ok" },
      openAlerts: 2,
    });
    renderHealth();
    await waitFor(() => expect(screen.getByText("1h 0m")).toBeInTheDocument());
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.getByText("backup older than 24h")).toBeInTheDocument();
  });

  it("renders an explicit error state with retry when the endpoint fails — never a fake healthy", async () => {
    vi.spyOn(reliabilityApi, "getMonitoringHealth").mockRejectedValue(new Error("service unreachable"));
    renderHealth();
    // The component retries once (transient health check); wait past the
    // retry window for the settled error state.
    await waitFor(() => expect(screen.getByText(/failed to load health data/i)).toBeInTheDocument(), { timeout: 4000 });
    expect(screen.getByRole("button", { name: /retry/i })).toBeInTheDocument();
  });

  it("no longer hardcodes a direct fetch URL — the shared client is the only transport", () => {
    const source = SystemHealth.toString();
    expect(source).not.toContain('fetch("/api/v1/monitoring/health"');
  });
});
