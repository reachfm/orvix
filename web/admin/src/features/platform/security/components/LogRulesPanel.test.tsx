import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import LogRulesPanel from "./LogRulesPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><LogRulesPanel /></QueryClientProvider>);
}

describe("Security > LogRulesPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("reads rules from the {rules:[...]} envelope, not a bare array", async () => {
    vi.spyOn(api, "listLogRules").mockResolvedValue({
      rules: [{ id: 1, name: "smtp-errors", source: "journald", severity: "warning", match_pattern: "SMTP.*error", destination: "syslog", enabled: true, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }],
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText("smtp-errors")).toBeInTheDocument());
  });

  // Regression test: CreateLogRule requires "name" and writes to
  // match_pattern — the previous form sent only {"pattern": ...},
  // a field the handler has never read, so creation always 400'd.
  it("create sends name and match_pattern, matching the real request contract", async () => {
    vi.spyOn(api, "listLogRules").mockResolvedValue({ rules: [] });
    const createSpy = vi.spyOn(api, "createLogRule").mockResolvedValue({ id: 1 });
    renderPanel();
    await waitFor(() => expect(screen.getByText("No log rules configured")).toBeInTheDocument());

    fireEvent.change(screen.getByPlaceholderText("Rule name…"), { target: { value: "smtp-errors" } });
    fireEvent.change(screen.getByPlaceholderText("Match pattern…"), { target: { value: "SMTP.*error" } });
    fireEvent.click(screen.getByText("Add rule"));

    await waitFor(() => expect(createSpy).toHaveBeenCalledWith({ name: "smtp-errors", match_pattern: "SMTP.*error" }));
  });

  it("the add-rule button is disabled without a name — the backend rejects an empty name with 400", async () => {
    vi.spyOn(api, "listLogRules").mockResolvedValue({ rules: [] });
    renderPanel();
    await waitFor(() => expect(screen.getByText("No log rules configured")).toBeInTheDocument());
    expect(screen.getByText("Add rule")).toBeDisabled();
  });
});
