import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import AuditPanel from "./components/AuditPanel";
import * as api from "./api";
import { request } from "../../../api";

function renderWith(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("Security > AuditPanel — envelope-aware client", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders audit rows when the route returns the {entries,total} envelope", async () => {
    // The route returns the envelope; the panel's api function unwraps
    // .entries — this pins the regression where the panel rendered
    // empty because listAuditLogs typed a bare array.
    vi.spyOn(api, "listAuditLogs").mockResolvedValue([
      { id: 1, action: "org.suspend", actor: "psa@orvix.email", target: "acme", result: "ok", timestamp: "2026-01-01T00:00:00Z" },
    ]);
    renderWith(<AuditPanel />);
    await waitFor(() => expect(screen.getByText("org.suspend")).toBeInTheDocument());
  });

  it("requests the envelope endpoint through the shared transport", async () => {
    const requestSpy = vi.spyOn(await import("../../../api"), "request").mockResolvedValue({
      entries: [{ id: 1, action: "org.suspend", actor: "psa@orvix.email", target: "acme", result: "ok", timestamp: "2026-01-01T00:00:00Z" }],
      total: 1,
      limit: 25,
      offset: 0,
    });
    renderWith(<AuditPanel />);
    await waitFor(() => expect(screen.getByText("org.suspend")).toBeInTheDocument());
    const calls = requestSpy.mock.calls.map((c) => String(c[0]));
    expect(calls).toContain("/audit/logs");
  });
});
