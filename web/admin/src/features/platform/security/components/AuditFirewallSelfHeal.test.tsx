import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import AuditPanel from "./AuditPanel";
import FirewallPanel from "./FirewallPanel";
import SelfHealPanel from "./SelfHealPanel";
import * as api from "../api";

function renderWith(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("Security > AuditPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });
  it("renders real audit rows", async () => {
    vi.spyOn(api, "listAuditLogs").mockResolvedValue([{ id: 1, action: "org.suspend", actor: "psa@orvix.email", target: "acme", result: "ok", timestamp: "2026-01-01T00:00:00Z" }]);
    renderWith(<AuditPanel />);
    await waitFor(() => expect(screen.getByText("org.suspend")).toBeInTheDocument());
  });
});

describe("Security > FirewallPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });
  it("renders rule and log counts distinctly from a loading state", async () => {
    vi.spyOn(api, "listFirewallRules").mockResolvedValue([{ id: 1, name: "block-spam", condition: "spam_score>5", action: "block", priority: 1, enabled: true }]);
    vi.spyOn(api, "listFirewallLogs").mockResolvedValue([]);
    renderWith(<FirewallPanel />);
    await waitFor(() => expect(screen.getByText("block-spam")).toBeInTheDocument());
  });
});

describe("Security > SelfHealPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });
  // Regression test: HealHistory's real fields are check_name/severity/
  // issue/fix_applied/success — the previous SelfHealTab read r.name/
  // r.result/r.status/r.timestamp, none of which exist.
  it("renders the real check_name/success/issue fields", async () => {
    vi.spyOn(api, "listHealHistory").mockResolvedValue([{ id: 1, check_name: "database", severity: "warning", issue: "slow query", fix_applied: "reindexed", success: true, created_at: "2026-01-01T00:00:00Z" }]);
    renderWith(<SelfHealPanel />);
    await waitFor(() => expect(screen.getByText(/slow query/i)).toBeInTheDocument());
    expect(screen.getAllByText("database").length).toBeGreaterThan(0);
  });
});
