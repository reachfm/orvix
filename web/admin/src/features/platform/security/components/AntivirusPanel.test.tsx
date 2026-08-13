import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import AntivirusPanel from "./AntivirusPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><AntivirusPanel /></QueryClientProvider>);
}

describe("Security > AntivirusPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders the real reachable/enforced status, not a collapsed 'disabled'", async () => {
    vi.spyOn(api, "getAntivirusStatus").mockResolvedValue({
      engine: "clamav", engine_configured: true, engine_reachable: true, engine_active: true, runtime_enforced: true,
      clamav_host: "localhost", clamav_port: 3310, clamav_response: "PONG", policy_on_infected: "reject",
      policy_on_scanner_unavailable: "fail_closed", last_error: "", counts: { scanned: 10, infected: 0, rejected: 0, quarantined: 0, tagged: 0, fail_open: 0, fail_closed: 0 }, honest_notes: [],
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText("PONG")).toBeInTheDocument());
    expect(screen.getByText("enforced")).toBeInTheDocument();
  });

  it("shows a distinct error state on a genuine backend failure", async () => {
    vi.spyOn(api, "getAntivirusStatus").mockRejectedValue(new Error("antivirus status unavailable"));
    renderPanel();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/antivirus status unavailable/i));
  });
});
