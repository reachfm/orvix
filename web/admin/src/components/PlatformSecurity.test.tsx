import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AntivirusTab } from "./PlatformSecurity";
import { api } from "../api";

function renderWithClient(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("PlatformSecurity > Antivirus tab", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  // Regression test for the operator-precedence defect:
  // `q.data?.status ?? q.data?.enabled === false ? "disabled" : "unknown"`
  // parsed as `(status ?? (enabled === false)) ? "disabled" : "unknown"`,
  // which meant ANY truthy field collapsed the whole tab to the literal
  // word "disabled" and the real reachable/enforced facts were never
  // shown. This asserts the real AdminAntivirusStatus contract renders
  // its actual fields distinctly.
  it("renders engine_reachable=true and runtime_enforced=true as their real distinct labels, not a collapsed 'disabled'", async () => {
    vi.spyOn(api, "getAntivirusStatus").mockResolvedValue({
      engine: "clamav",
      engine_configured: true,
      engine_reachable: true,
      engine_active: true,
      runtime_enforced: true,
      clamav_host: "localhost",
      clamav_port: 3310,
      clamav_response: "PONG",
      policy_on_infected: "reject",
      policy_on_scanner_unavailable: "fail_closed",
      last_error: "",
      counts: { scanned: 120, infected: 3, rejected: 3, quarantined: 0, tagged: 0, fail_open: 0, fail_closed: 0 },
      honest_notes: [],
    } as any);

    renderWithClient(<AntivirusTab />);

    await waitFor(() => expect(screen.getByText("PONG")).toBeInTheDocument());
    expect(screen.getByText("enforced")).toBeInTheDocument();
    expect(screen.queryByText(/^disabled$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^unknown$/i)).not.toBeInTheDocument();
  });

  it("renders engine_reachable=false and runtime_enforced=false distinctly, never silently reporting healthy", async () => {
    vi.spyOn(api, "getAntivirusStatus").mockResolvedValue({
      engine: "clamav",
      engine_configured: true,
      engine_reachable: false,
      engine_active: false,
      runtime_enforced: false,
      clamav_host: "localhost",
      clamav_port: 3310,
      clamav_response: "",
      policy_on_infected: "reject",
      policy_on_scanner_unavailable: "fail_closed",
      last_error: "dial tcp: connection refused",
      counts: { scanned: 0, infected: 0, rejected: 0, quarantined: 0, tagged: 0, fail_open: 0, fail_closed: 0 },
      honest_notes: ["runtime_enforced is true only when the SMTP receiver is calling the engine on every AcceptMessage call"],
    } as any);

    renderWithClient(<AntivirusTab />);

    await waitFor(() => expect(screen.getByText("unreachable")).toBeInTheDocument());
    expect(screen.getByText("not enforced")).toBeInTheDocument();
    expect(screen.getByText("dial tcp: connection refused")).toBeInTheDocument();
  });

  it("shows a distinct error state when the antivirus status request fails", async () => {
    vi.spyOn(api, "getAntivirusStatus").mockRejectedValue(new Error("request failed"));
    renderWithClient(<AntivirusTab />);
    await waitFor(() => expect(screen.getByText(/request failed/i)).toBeInTheDocument());
  });
});
