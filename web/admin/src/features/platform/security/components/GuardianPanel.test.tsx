import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import GuardianPanel from "./GuardianPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><GuardianPanel /></QueryClientProvider>);
}

describe("Security > GuardianPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  // Regression test: GuardianLog's real fields are message_id/
  // threat_score/verdict/confidence/reasons/action — the previous
  // GuardianTab read r.subject/r.summary, neither of which exists.
  it("renders the real message_id/verdict/threat_score fields, not undefined", async () => {
    vi.spyOn(api, "listGuardianLogs").mockResolvedValue([
      { id: 1, message_id: "msg-123", threat_score: 0.85, verdict: "suspicious", confidence: 0.9, reasons: "spoofed sender", action: "quarantine", created_at: "2026-01-01T00:00:00Z" },
    ]);
    renderPanel();
    await waitFor(() => expect(screen.getByText("msg-123")).toBeInTheDocument());
    expect(screen.getByText("suspicious")).toBeInTheDocument();
    expect(screen.getByText("0.85")).toBeInTheDocument();
  });

  it("shows a distinct empty state", async () => {
    vi.spyOn(api, "listGuardianLogs").mockResolvedValue([]);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No guardian analysis events")).toBeInTheDocument());
  });
});
