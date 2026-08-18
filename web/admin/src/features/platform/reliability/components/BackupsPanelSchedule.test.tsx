import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import BackupsPanel from "./BackupsPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><BackupsPanel /></QueryClientProvider>);
}

describe("Reliability > BackupsPanel — schedule editing", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  const baseMocks = () => {
    vi.spyOn(api, "listBackups").mockResolvedValue([]);
    vi.spyOn(api, "getBackupSchedule").mockResolvedValue({ enabled: true, frequency: "daily", retentionCount: 7, updatedAt: "2026-01-01T00:00:00Z" });
    vi.spyOn(api, "getBackupMetrics").mockResolvedValue({ totalBackups: 0, totalSizeBytes: 0 });
    vi.spyOn(api, "getBackupHealth").mockResolvedValue({ schedulerEnabled: true, retentionEnabled: true, directoryExists: true, writable: true, availableDiskBytes: 0, lastBackupAgeHours: 1, lastBackupAgeWarning: false, lastBackupAgeCritical: false, status: "ok" });
  };

  it("edits the schedule through POST /admin/backups/schedule", async () => {
    baseMocks();
    const setSpy = vi.spyOn(api, "setBackupSchedule").mockResolvedValue({ enabled: false, frequency: "weekly", retentionCount: 14, updatedAt: "2026-01-02T00:00:00Z" });
    renderPanel();
    await waitFor(() => expect(screen.getByRole("button", { name: /edit schedule/i })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /edit schedule/i }));
    await waitFor(() => expect(screen.getByLabelText(/scheduled backups enabled/i)).toBeChecked());
    fireEvent.click(screen.getByLabelText(/scheduled backups enabled/i));
    fireEvent.change(screen.getByLabelText(/frequency/i), { target: { value: "weekly" } });
    fireEvent.change(screen.getByLabelText(/retention count/i), { target: { value: "14" } });
    fireEvent.click(screen.getByRole("button", { name: /save schedule/i }));
    await waitFor(() => expect(setSpy).toHaveBeenCalledWith({ enabled: false, frequency: "weekly", retentionCount: 14 }));
  });

  it("never submits an invalid retention count", async () => {
    baseMocks();
    const setSpy = vi.spyOn(api, "setBackupSchedule").mockResolvedValue({ enabled: true, frequency: "daily", retentionCount: 7, updatedAt: "2026-01-01T00:00:00Z" });
    renderPanel();
    await waitFor(() => expect(screen.getByRole("button", { name: /edit schedule/i })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /edit schedule/i }));
    await waitFor(() => expect(screen.getByLabelText(/retention count/i)).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText(/retention count/i), { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: /save schedule/i }));
    await waitFor(() => expect(screen.getByText(/positive integer/i)).toBeInTheDocument());
    expect(setSpy).not.toHaveBeenCalled();
  });
});
