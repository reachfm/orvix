import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import UpdatesPanel from "./UpdatesPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><UpdatesPanel /></QueryClientProvider>);
}

describe("Reliability > UpdatesPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("running an update requires typed confirmation before calling the API", async () => {
    vi.spyOn(api, "getUpdateStatus").mockResolvedValue({
      currentVersion: "1.0.0", currentSha: "abc", buildTime: "", availableVersion: "1.0.0", availableSha: "abc",
      channel: "stable", updateAvailable: false, releaseNotes: "", checkedAt: "2026-01-01T00:00:00Z", jobStatus: "idle",
    });
    vi.spyOn(api, "checkUpdates").mockResolvedValue({
      current_version: "1.0.0", current_sha: "abc", latest_version: "1.0.0", latest_sha: "abc",
      update_available: false, channel: "stable", release_notes: [],
    });
    vi.spyOn(api, "getUpdateHistory").mockResolvedValue([]);
    vi.spyOn(api, "getUpdatePreflight").mockResolvedValue({ pass: true, checks: [], message: "" });
    const runSpy = vi.spyOn(api, "runUpdate").mockResolvedValue({
      id: 1, startedAt: "2026-01-01T00:00:00Z", durationSeconds: 0, previousSha: "abc", newSha: "def",
      fromVersion: "1.0.0", toVersion: "1.0.1", status: "completed", severity: "info", actor: "psa",
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText("idle")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Run update"));

    const confirmBtn = screen.getByRole("button", { name: /confirm/i });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "run-update" } });
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(runSpy).toHaveBeenCalled());
  });
});
