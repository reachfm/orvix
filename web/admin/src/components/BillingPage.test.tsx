import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import BillingPage from "./BillingPage";
import { api } from "../api";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><BillingPage /></QueryClientProvider>);
}

describe("BillingPage — honest payment-provider state via GET /enterprise/billing/state", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it('renders "Payment provider not configured" from configured:false — never a hardcoded string', async () => {
    vi.spyOn(api, "getPlans").mockResolvedValue([]);
    vi.spyOn(api, "getSubscription").mockResolvedValue(null);
    vi.spyOn(api, "getUsage").mockResolvedValue(null);
    vi.spyOn(api, "getBillingState").mockResolvedValue({
      tenant_id: 1,
      subscription: null,
      plan: null,
      usage: null,
      invoices: [],
      payment_provider: { provider: "", enabled: false, configured: false, note: "payment provider not configured" },
    });
    renderPage();
    // The heading and the backend-provided note both carry the honest
    // copy — assert at least one element with it.
    await waitFor(() => expect(screen.getAllByText(/payment provider not configured/i).length).toBeGreaterThan(0));
    // No fabricated cards/MRR claims.
    expect(screen.queryByText(/card ending/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/monthly recurring revenue/i)).not.toBeInTheDocument();
  });

  it("renders the configured provider name when the backend reports it", async () => {
    vi.spyOn(api, "getPlans").mockResolvedValue([]);
    vi.spyOn(api, "getSubscription").mockResolvedValue(null);
    vi.spyOn(api, "getUsage").mockResolvedValue(null);
    vi.spyOn(api, "getBillingState").mockResolvedValue({
      tenant_id: 1,
      subscription: null,
      plan: null,
      usage: null,
      invoices: [],
      payment_provider: { provider: "stripe", enabled: true, configured: true, note: "" },
    });
    renderPage();
    await waitFor(() => expect(screen.getByText(/payment provider: stripe/i)).toBeInTheDocument());
    expect(screen.queryByText(/payment provider not configured/i)).not.toBeInTheDocument();
  });

  it("shows an explicit unavailable state when the billing-state fetch fails", async () => {
    vi.spyOn(api, "getPlans").mockResolvedValue([]);
    vi.spyOn(api, "getSubscription").mockResolvedValue(null);
    vi.spyOn(api, "getUsage").mockResolvedValue(null);
    vi.spyOn(api, "getBillingState").mockRejectedValue(new Error("backend unreachable"));
    renderPage();
    await waitFor(() => expect(screen.getByText(/payment provider state unavailable/i)).toBeInTheDocument());
  });
});
