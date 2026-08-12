import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import MonitoringPanel from "./MonitoringPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><MonitoringPanel /></QueryClientProvider>);
}

describe("Reliability > MonitoringPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders active alerts and resolves one without a raw JSON dump", async () => {
    vi.spyOn(api, "getMonitoringAlerts").mockResolvedValue({
      alerts: [{ id: 1, category: "queue", severity: "warning", title: "Queue depth high", message: "", source: "monitor", active: true, createdAt: "2026-01-01T00:00:00Z" }],
    });
    const resolveSpy = vi.spyOn(api, "resolveMonitoringAlert").mockResolvedValue({ status: "resolved", id: 1 });
    renderPanel();
    await waitFor(() => expect(screen.getByText("Queue depth high")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Resolve"));
    await waitFor(() => expect(resolveSpy).toHaveBeenCalledWith(1));
  });

  it("renders capacity as deliberate typed stat tiles, never JSON.stringify", async () => {
    vi.spyOn(api, "getMonitoringCapacity").mockResolvedValue({
      domainCount: 3, mailboxCount: 40, messageCount: 900, attachmentCount: 12, queueCount: 2,
      queueDeadLetter: 0, storageBytes: 1000, databaseSize: 500, backupCount: 5, backupBytes: 2000,
    });
    renderPanel();
    fireEvent.click(screen.getByText("capacity"));
    await waitFor(() => expect(screen.getByText("40")).toBeInTheDocument());
    expect(screen.queryByText(/"domainCount"/)).not.toBeInTheDocument();
  });

  it("reads deliveries from the {deliveries:[...]} envelope, not a bare array", async () => {
    vi.spyOn(api, "listAlertDeliveries").mockResolvedValue({
      deliveries: [{ id: 1, alertTitle: "Disk warning", alertSeverity: "warning", alertCategory: "storage", provider: "webhook", status: "delivered", detail: "", createdAt: "2026-01-01T00:00:00Z" }],
      limit: 100,
    });
    renderPanel();
    fireEvent.click(screen.getByText("deliveries"));
    await waitFor(() => expect(screen.getByText("webhook")).toBeInTheDocument());
  });
});
