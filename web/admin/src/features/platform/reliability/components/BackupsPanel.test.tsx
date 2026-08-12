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

const BACKUPS = [{ id: "b1", name: "b1", status: "completed", size_bytes: 100, created_at: "2026-01-01T00:00:00Z" }];

describe("Reliability > BackupsPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("shows a distinct empty state, not indistinguishable from an error", async () => {
    vi.spyOn(api, "listBackups").mockResolvedValue([]);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No backups found")).toBeInTheDocument());
  });

  it("shows a distinct error state on failure", async () => {
    vi.spyOn(api, "listBackups").mockRejectedValue(new Error("backup service unavailable"));
    renderPanel();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/backup service unavailable/i));
  });

  it("delete sends the exact required typed-confirmation body, not the backup id", async () => {
    vi.spyOn(api, "listBackups").mockResolvedValue(BACKUPS);
    const deleteSpy = vi.spyOn(api, "deleteBackup").mockResolvedValue(undefined);
    renderPanel();
    await waitFor(() => expect(screen.getByText("b1")).toBeInTheDocument());
    fireEvent.click(screen.getByTitle("Delete"));

    const input = await screen.findByRole("textbox");
    fireEvent.change(input, { target: { value: "b1" } });
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));

    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith("b1"));
  });

  it("restore requires typed confirmation and displays the returned restore job id", async () => {
    vi.spyOn(api, "listBackups").mockResolvedValue(BACKUPS);
    vi.spyOn(api, "restoreBackup").mockResolvedValue({ job_id: "job-42", status: "pending", poll_url: "/x", message: "accepted" });
    vi.spyOn(api, "getRestoreJobStatus").mockResolvedValue({
      job_id: "job-42", backup_id: "b1", status: "succeeded", rolled_back: false,
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText("b1")).toBeInTheDocument());
    fireEvent.click(screen.getByTitle("Restore"));
    const input = await screen.findByRole("textbox");
    fireEvent.change(input, { target: { value: "b1" } });
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));

    await waitFor(() => expect(screen.getByText("job-42")).toBeInTheDocument());
  });
});
