import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type ReactNode } from "react";
import { useBalance, useAdjustments, useReconciliation, useCreateAdjustment, billingKeys } from "./queries";
import { useSetTenantScope } from "../tenant-context/queries";
import { request } from "../../../api";

// B-3 regression suite.
//
// Platform Billing was rendered as <PlatformBillingPage tenantId={1} />, so a
// Platform Super Admin always saw — and could mutate — the FIRST customer's
// billing data no matter which customer they meant. These tests pin the
// replacement contract: nothing is requested without an explicit selection,
// every key is tenant-scoped, and switching tenants evicts the old tenant's
// cached billing data so it can never remain on screen.

vi.mock("../../../api", () => ({
  request: vi.fn(),
}));

const mockedRequest = vi.mocked(request);

function makeWrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

describe("platform billing tenant selection", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedRequest.mockResolvedValue({} as never);
  });

  it("issues NO request before a tenant is selected", async () => {
    const qc = newClient();
    const wrapper = makeWrapper(qc);

    renderHook(() => useBalance(null), { wrapper });
    renderHook(() => useAdjustments(null), { wrapper });
    renderHook(() => useReconciliation(null), { wrapper });

    // Give react-query a chance to fire anything it intended to.
    await new Promise((r) => setTimeout(r, 20));
    expect(mockedRequest).not.toHaveBeenCalled();
  });

  it("requests only the selected tenant once one is chosen", async () => {
    const qc = newClient();
    const wrapper = makeWrapper(qc);

    const { result } = renderHook(() => useBalance(42), { wrapper });
    await waitFor(() => expect(mockedRequest).toHaveBeenCalled());

    for (const call of mockedRequest.mock.calls) {
      const url = String(call[0]);
      expect(url).toContain("42");
      // The previously hardcoded tenant must never be requested implicitly.
      expect(url).not.toMatch(/\/tenants\/1\b/);
    }
    expect(result.current).toBeDefined();
  });

  it("scopes every query key by tenant so two tenants cannot share a cache entry", () => {
    expect(billingKeys.balance(1)).not.toEqual(billingKeys.balance(2));
    expect(billingKeys.adjustments(1)).not.toEqual(billingKeys.adjustments(2));
    expect(billingKeys.reconciliation(1)).not.toEqual(billingKeys.reconciliation(2));
    // Tenant id must actually appear in the key.
    expect(billingKeys.balance(99)).toContain(99);
  });

  it("switching tenants evicts the previous tenant's billing cache", async () => {
    const qc = newClient();
    // Tenant 1 data already cached (the old hardcoded tenant).
    qc.setQueryData(billingKeys.balance(1), { balance_cents: 12345, currency: "USD" });
    qc.setQueryData(billingKeys.adjustments(1), { adjustments: [{ id: 1 }] });
    qc.setQueryData(billingKeys.reconciliation(1), { total_credits_cents: 1 });

    const { result } = renderHook(() => useSetTenantScope(), { wrapper: makeWrapper(qc) });
    result.current.mutate({ tenantId: 2, tenantName: "Beta" });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    // No trace of tenant 1's figures may remain — otherwise the operator
    // would be looking at Acme's balance under Beta's heading.
    expect(qc.getQueryData(billingKeys.balance(1))).toBeUndefined();
    expect(qc.getQueryData(billingKeys.adjustments(1))).toBeUndefined();
    expect(qc.getQueryData(billingKeys.reconciliation(1))).toBeUndefined();
  });

  it("refuses an adjustment when no tenant is selected", async () => {
    const qc = newClient();
    const { result } = renderHook(() => useCreateAdjustment(null), { wrapper: makeWrapper(qc) });

    result.current.mutate({
      data: { type: "credit", amount_cents: 100, currency: "USD", reason: "test" },
      idempotencyKey: "k1",
    });

    await waitFor(() => expect(result.current.isError).toBe(true));
    // Nothing may reach the network without an explicit target.
    expect(mockedRequest).not.toHaveBeenCalled();
  });

  it("targets the selected tenant when applying an adjustment", async () => {
    const qc = newClient();
    const { result } = renderHook(() => useCreateAdjustment(7), { wrapper: makeWrapper(qc) });

    result.current.mutate({
      data: { type: "credit", amount_cents: 500, currency: "USD", reason: "goodwill" },
      idempotencyKey: "k2",
    });

    await waitFor(() => expect(mockedRequest).toHaveBeenCalled());
    const url = String(mockedRequest.mock.calls[0][0]);
    expect(url).toContain("7");
    expect(url).not.toMatch(/\/tenants\/1\b/);
  });
});
