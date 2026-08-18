import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import MailOperationsPage from "./page";
import * as api from "./api";
import type { ListQueueMessagesResponse, QueueSummaryResponse } from "./contract";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><MailOperationsPage /></QueryClientProvider>);
}

const SUMMARY: QueueSummaryResponse = {
  metrics: { pending: 0, leased: 0, delivering: 0, deferred: 0, delivered: 0, bounced: 0, dead_letter: 0, cancelled: 0, total: 0, avg_attempts: 0 },
};

const LIST: ListQueueMessagesResponse = { messages: [], total: 0, limit: 50, offset: 0 };

describe("features/platform/mail-operations — history + export UI", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders delivery-attempt history rows from GET /admin/queue/history", async () => {
    vi.spyOn(api, "listQueueMessages").mockResolvedValue(LIST);
    vi.spyOn(api, "getQueueSummary").mockResolvedValue(SUMMARY);
    vi.spyOn(api, "getQueueHistory").mockResolvedValue({
      attempts: [
        { id: 11, queue_entry_id: 42, attempt_number: 1, status: "deferred", remote_host: "mx.b.example", remote_ip: "10.0.0.1", status_code: 450, status_msg: "try later", enhanced_code: "", duration_ms: 120, tls_used: true, worker_id: "w1", attempted_at: "2026-01-01T00:00:00Z" },
        { id: 12, queue_entry_id: 43, attempt_number: 1, status: "success", remote_host: "mx.b.example", remote_ip: "10.0.0.1", status_code: 250, status_msg: "ok", enhanced_code: "", duration_ms: 90, tls_used: true, worker_id: "w1", attempted_at: "2026-01-01T00:00:05Z" },
      ],
      next_after_id: 12,
      count: 2,
    });
    renderPage();
    // Switch to the History & export tab. "Success"/"Deferred" also
    // match the status filter options, so wait for the actual row data.
    fireEvent.mouseDown(screen.getByRole("tab", { name: /history/i }));
    await waitFor(() => expect(screen.getByText("#42")).toBeInTheDocument());
    expect(screen.getByText("#43")).toBeInTheDocument();
    // Row status labels rendered (options duplicate the text; rows are
    // inside the data table).
    const table = document.querySelector("table");
    expect(table?.textContent).toContain("Success");
    expect(table?.textContent).toContain("Deferred");
    expect(screen.getAllByText("mx.b.example").length).toBeGreaterThan(0);
  });

  it("downloads the redacted queue CSV through the blob response mode", async () => {
    vi.spyOn(api, "listQueueMessages").mockResolvedValue(LIST);
    vi.spyOn(api, "getQueueSummary").mockResolvedValue(SUMMARY);
    vi.spyOn(api, "getQueueHistory").mockResolvedValue({ attempts: [], next_after_id: 0, count: 0 });
    const exportSpy = vi.spyOn(api, "exportQueueCsv").mockResolvedValue(new Blob(["id,from,to"], { type: "text/csv" }));
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    renderPage();
    fireEvent.mouseDown(screen.getByRole("tab", { name: /history/i }));
    await waitFor(() => expect(screen.getByRole("button", { name: /export queue csv/i })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /export queue csv/i }));
    await waitFor(() => expect(exportSpy).toHaveBeenCalledTimes(1));
    expect(clickSpy).toHaveBeenCalled();
  });

  it("surfaces an export failure explicitly, never a fake download", async () => {
    vi.spyOn(api, "listQueueMessages").mockResolvedValue(LIST);
    vi.spyOn(api, "getQueueSummary").mockResolvedValue(SUMMARY);
    vi.spyOn(api, "getQueueHistory").mockResolvedValue({ attempts: [], next_after_id: 0, count: 0 });
    vi.spyOn(api, "exportQueueCsv").mockRejectedValue(new Error("queue export unavailable"));
    renderPage();
    fireEvent.mouseDown(screen.getByRole("tab", { name: /history/i }));
    await waitFor(() => expect(screen.getByRole("button", { name: /export queue csv/i })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /export queue csv/i }));
    await waitFor(() => expect(screen.getByText(/export failed/i)).toBeInTheDocument());
  });
});
