import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import PlatformBillingPage from "./page";
import * as api from "./api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";

function renderPage(tenantId: number | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (tenantId !== null) {
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId, tenantName: "Acme" });
  }
  return render(<QueryClientProvider client={qc}><PlatformBillingPage /></QueryClientProvider>);
}

const OVERVIEW = {
  tenant_id: 7,
  subscription: {
    id: 1, tenant_id: 7, plan_id: "free", status: "active", billing_interval: "monthly",
    current_period_start: "2026-01-01T00:00:00Z", current_period_end: "2026-02-01T00:00:00Z",
    storage_mb: 1024, send_limit_day: 500, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
  },
  plan: { id: "free", name: "Free", price_monthly: 0, price_yearly: 0, max_domains: 1, max_mailboxes: 5, storage_mb: 1024, send_limit_day: 500 },
  usage: { id: 1, tenant_id: 7, period_start: "2026-01-01T00:00:00Z", period_end: "2026-02-01T00:00:00Z", emails_sent: 12, emails_received: 34, mailboxes_used: 2, domains_used: 1 },
  invoices: [],
  balance: null,
  adjustments: [],
  reconciliation: null,
  payment_provider: { provider: "", enabled: false, configured: false, note: "payment provider not configured" },
};

describe("features/platform/platform-billing — overview + honest provider state", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders the real subscription/plan/period/usage from the overview envelope", async () => {
    vi.spyOn(api, "getBillingOverview").mockResolvedValue(OVERVIEW as any);
    vi.spyOn(api, "getBalance").mockResolvedValue({ tenant_id: 7, currency: "USD", balance_cents: 0, version: 0, updated_at: "2026-01-01T00:00:00Z" } as any);
    vi.spyOn(api, "getAdjustments").mockResolvedValue({ adjustments: [] });
    vi.spyOn(api, "getReconciliation").mockResolvedValue({ tenant_id: 7, currency: "USD", stored_balance_cents: 0, recomputed_balance_cents: 0, total_credits_cents: 0, total_debits_cents: 0, discrepancy_cents: 0, discrepant: false, generated_at: "2026-01-01T00:00:00Z" });
    renderPage(7);
    await waitFor(() => expect(screen.getByText("Free (free)")).toBeInTheDocument());
    expect(screen.getByText("active")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
    // Honest provider state: configured=false surfaces "not configured".
    expect(screen.getByText(/payment provider not configured/i)).toBeInTheDocument();
    // Never a fabricated card / monthly-recurring-revenue claim.
    expect(screen.queryByText(/monthly recurring revenue/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/card ending/i)).not.toBeInTheDocument();
  });

  it("renders real invoice rows only when the backend returns them", async () => {
    vi.spyOn(api, "getBillingOverview").mockResolvedValue({
      ...OVERVIEW,
      invoices: [{ id: 1, tenant_id: 7, provider: "stripe", provider_invoice_id: "in_1", invoice_number: "INV-0001", currency: "USD", subtotal: 1000, tax: 0, total: 1000, amount_paid: 1000, amount_due: 0, status: "paid", issued_at: "2026-01-10T00:00:00Z" }],
    } as any);
    vi.spyOn(api, "getBalance").mockResolvedValue({ tenant_id: 7, currency: "USD", balance_cents: 0, version: 0, updated_at: "2026-01-01T00:00:00Z" } as any);
    vi.spyOn(api, "getAdjustments").mockResolvedValue({ adjustments: [] });
    vi.spyOn(api, "getReconciliation").mockResolvedValue({ tenant_id: 7, currency: "USD", stored_balance_cents: 0, recomputed_balance_cents: 0, total_credits_cents: 0, total_debits_cents: 0, discrepancy_cents: 0, discrepant: false, generated_at: "2026-01-01T00:00:00Z" });
    renderPage(7);
    await waitFor(() => expect(screen.getByText("INV-0001")).toBeInTheDocument());
    expect(screen.getByText("paid")).toBeInTheDocument();
  });

  it("shows an explicit unavailable state when the overview fetch fails — never fake zeros", async () => {
    vi.spyOn(api, "getBillingOverview").mockRejectedValue(new Error("backend down"));
    vi.spyOn(api, "getBalance").mockResolvedValue({ tenant_id: 7, currency: "USD", balance_cents: 0, version: 0, updated_at: "2026-01-01T00:00:00Z" } as any);
    vi.spyOn(api, "getAdjustments").mockResolvedValue({ adjustments: [] });
    vi.spyOn(api, "getReconciliation").mockResolvedValue({ tenant_id: 7, currency: "USD", stored_balance_cents: 0, recomputed_balance_cents: 0, total_credits_cents: 0, total_debits_cents: 0, discrepancy_cents: 0, discrepant: false, generated_at: "2026-01-01T00:00:00Z" });
    renderPage(7);
    await waitFor(() => expect(screen.getByText(/billing overview unavailable/i)).toBeInTheDocument());
  });
});
