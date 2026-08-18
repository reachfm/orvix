import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import AuditPage from "./page";
import * as api from "./api";
import { downloadAuditExport } from "./api";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><AuditPage /></QueryClientProvider>);
}

const ENVELOPE = {
  entries: [
    { id: 1, actor: "psa@orvix.email", actor_id: 1, actor_role: "platform_super_admin", tenant_id: 3, action: "organization.update", target: "acme", result: "success", request_id: "req-1", ip: "127.0.0.1", user_agent: "test", timestamp: "2026-01-01T00:00:00Z" },
  ],
  total: 1,
  limit: 25,
  offset: 0,
};

describe("features/platform/audit — envelope + export", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders rich entries from the {entries,total} envelope", async () => {
    vi.spyOn(api, "listAuditLogs").mockResolvedValue(ENVELOPE);
    renderPage();
    await waitFor(() => expect(screen.getByText("organization.update")).toBeInTheDocument());
    expect(screen.getByText("psa@orvix.email (platform_super_admin)")).toBeInTheDocument();
    expect(screen.getByText("req-1")).toBeInTheDocument();
  });

  it("streams a real blob export through the shared client and downloads it", async () => {
    vi.spyOn(api, "listAuditLogs").mockResolvedValue(ENVELOPE);
    const exportSpy = vi.spyOn(api, "exportAuditLogs").mockResolvedValue(new Blob(["id,actor"], { type: "text/csv" }));
    const downloadSpy = vi.spyOn(api, "downloadAuditExport").mockImplementation(() => {});
    renderPage();
    await waitFor(() => expect(screen.getByText("organization.update")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /export csv/i }));
    await waitFor(() => expect(exportSpy).toHaveBeenCalledWith(expect.objectContaining({ format: "csv" })));
    expect(downloadSpy).toHaveBeenCalledWith(expect.any(Blob), "csv");
    // The request goes through the shared client — never raw fetch for
    // the file body.
    expect(exportSpy).toHaveBeenCalledTimes(1);
  });

  it("surfaces export failures as an explicit error, never a fake success", async () => {
    vi.spyOn(api, "listAuditLogs").mockResolvedValue(ENVELOPE);
    vi.spyOn(api, "exportAuditLogs").mockRejectedValue(new Error("500"));
    renderPage();
    await waitFor(() => expect(screen.getByText("organization.update")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /export json/i }));
    await waitFor(() => expect(screen.getByText(/export failed/i)).toBeInTheDocument());
  });

  it("downloadAuditExport triggers a real anchor download", () => {
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:fake");
    const blob = new Blob(["{}"], { type: "application/json" });
    downloadAuditExport(blob, "json");
    expect(clickSpy).toHaveBeenCalledTimes(1);
  });
});
