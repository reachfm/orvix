import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import StoragePanel from "./StoragePanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><StoragePanel /></QueryClientProvider>);
}

describe("Reliability > StoragePanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  // Regression test: ListStorageVolumes returns {volumes: [...],
  // honest_note} — an object, not a bare array. The previous StorageTab
  // read the response as an array directly (`q.data ?? []`) and would
  // throw calling .map on an object whenever this endpoint returned data.
  it("reads volumes from the {volumes:[...]} envelope, not a bare array", async () => {
    vi.spyOn(api, "listStorageVolumes").mockResolvedValue({
      volumes: [{ mounted: "/data", role: "mailstore", total_bytes: 100, used_bytes: 50000000000, free_bytes: 50, used_pct: 50, available: true }],
      honest_note: "Single-backend deployment.",
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText("mailstore")).toBeInTheDocument());
    expect(screen.getByText("50.0 GB used (50%)")).toBeInTheDocument();
    expect(screen.getByText("Single-backend deployment.")).toBeInTheDocument();
  });

  it("shows an unavailable volume's detail reason, never a fake 0%", async () => {
    vi.spyOn(api, "listStorageVolumes").mockResolvedValue({
      volumes: [{ mounted: "/data", role: "backups", total_bytes: 0, used_bytes: 0, free_bytes: 0, used_pct: 0, available: false, detail: "directory does not exist yet" }],
      honest_note: "",
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText("directory does not exist yet")).toBeInTheDocument());
  });

  it("shows a distinct empty state when there are no configured volumes", async () => {
    vi.spyOn(api, "listStorageVolumes").mockResolvedValue({ volumes: [], honest_note: "" });
    renderPanel();
    await waitFor(() => expect(screen.getByText("No storage volumes reported")).toBeInTheDocument());
  });
});
