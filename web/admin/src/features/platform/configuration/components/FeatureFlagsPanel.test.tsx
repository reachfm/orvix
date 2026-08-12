import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import FeatureFlagsPanel from "./FeatureFlagsPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><FeatureFlagsPanel /></QueryClientProvider>);
}

describe("features/platform/configuration > FeatureFlagsPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders real flags and toggles with the exact typed id/enabled contract", async () => {
    vi.spyOn(api, "listFeatureFlags").mockResolvedValue([
      { id: 3, name: "advanced-routing", enabled: false, tier_required: "enterprise", module_version: "1.0.0", description: "" },
    ]);
    const updateSpy = vi.spyOn(api, "updateFeatureFlag").mockResolvedValue({ status: "updated" });
    renderPanel();
    await waitFor(() => expect(screen.getByText("advanced-routing")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Off"));
    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith(3, true));
  });

  it("shows a distinct empty state", async () => {
    vi.spyOn(api, "listFeatureFlags").mockResolvedValue([]);
    renderPanel();
    await waitFor(() => expect(screen.getByText("No feature flags configured")).toBeInTheDocument());
  });
});
