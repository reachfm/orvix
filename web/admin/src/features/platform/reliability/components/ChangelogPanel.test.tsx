import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ChangelogPanel from "./ChangelogPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><ChangelogPanel /></QueryClientProvider>);
}

describe("Reliability > ChangelogPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  // Regression-shaped test: ChangelogEntry has no json tags, so the
  // real wire format is capitalized (ID/ModuleID/Version/Changes/
  // ReleasedAt) — this proves the panel reads those exact keys, not
  // a guessed camelCase/snake_case shape.
  it("renders real changelog entries using the capitalized field names the backend actually returns", async () => {
    vi.spyOn(api, "getChangelog").mockResolvedValue([
      { ID: 1, ModuleID: "orvix-core", Version: "1.2.0", Changes: "Fixed queue retry bug", ReleasedAt: "2026-01-01T00:00:00Z" },
    ]);
    renderPanel();
    await waitFor(() => expect(screen.getByText("1.2.0")).toBeInTheDocument());
    expect(screen.getByText("Fixed queue retry bug")).toBeInTheDocument();
  });

  it("shows a distinct empty state, not indistinguishable from an error", async () => {
    vi.spyOn(api, "getChangelog").mockResolvedValue([]);
    renderPanel();
    await waitFor(() => expect(screen.getByText(/No changelog entries/i)).toBeInTheDocument());
  });

  it("shows a distinct error state on failure", async () => {
    vi.spyOn(api, "getChangelog").mockRejectedValue(new Error("changelog unavailable"));
    renderPanel();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/changelog unavailable/i));
  });
});
