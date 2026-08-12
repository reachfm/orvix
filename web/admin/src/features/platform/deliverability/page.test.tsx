import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import DeliverabilityPage from "./page";
import { request } from "../../../api";
import { TENANT_SCOPE_QUERY_KEY } from "../tenant-context/contract";
import type { DeliverabilityMetricsResponse, ListDeliverabilityEventsResponse } from "./contract";

vi.mock("../../../api", () => ({ request: vi.fn() }));

const mockedRequest = vi.mocked(request);

const METRICS: DeliverabilityMetricsResponse = {
  window: {
    dimension: "tenant", dimension_value: "7", window_start: "2026-01-01T00:00:00Z", window_end: "2026-01-02T00:00:00Z",
    volume: 100, delivered: 90, temp_fail: 5, perm_fail: 2, bounced: 3, complaints: 1, avg_latency_ms: 250,
    delivery_rate: 0.9, bounce_rate: 0.03, complaint_rate: 0.01, temp_fail_rate: 0.05, perm_fail_rate: 0.02,
  },
  summary: {
    tenant_id: 7, window_start: "2026-01-01T00:00:00Z", window_end: "2026-01-02T00:00:00Z",
    volume: 100, delivered: 90, failed: 3, deferred: 5, bounced: 3, policy_denied: 2, suppressed: 4, complaints: 1,
    delivery_rate: 0.9, bounce_rate: 0.03, failure_rate: 0.03, deferred_rate: 0.05,
    by_category: [{ Key: "delivered", Count: 90 }, { Key: "suppressed", Count: 4 }],
    by_domain: [{ Key: "acme.example", Count: 70 }],
    by_provider: [{ Key: "provider-a", Count: 80 }],
    time_buckets: [
      { start: "2026-01-01T00:00:00Z", delivered: 40, failed: 1, other: 2, total: 43 },
      { start: "2026-01-01T01:00:00Z", delivered: 50, failed: 2, other: 5, total: 57 },
    ],
    bucket_size: "hourly",
  },
  volume: 100,
  delivered: 90,
  bounced: 3,
  complaints: 1,
  delivery_rate: 0.9,
  bounce_rate: 0.03,
  complaint_rate: 0.01,
};

const EVENTS: ListDeliverabilityEventsResponse = {
  events: [
    { id: 1, tenant_id: 7, dimension: "tenant", dimension_value: "7", type: "delivered", category: "delivered", recorded_at: "2026-01-01T00:00:00Z", latency_ms: 200 },
    { id: 2, tenant_id: 7, dimension: "sending_domain", dimension_value: "acme.example", type: "suppressed", category: "suppressed", recorded_at: "2026-01-01T00:05:00Z" },
  ],
  total: 2,
  limit: 100,
  offset: 0,
};

const EMPTY_METRICS: DeliverabilityMetricsResponse = {
  window: {
    dimension: "tenant", dimension_value: "7", window_start: "2026-01-01T00:00:00Z", window_end: "2026-01-02T00:00:00Z",
    volume: 0, delivered: 0, temp_fail: 0, perm_fail: 0, bounced: 0, complaints: 0, avg_latency_ms: 0,
    delivery_rate: 0, bounce_rate: 0, complaint_rate: 0, temp_fail_rate: 0, perm_fail_rate: 0,
  },
  summary: {
    tenant_id: 7, window_start: "2026-01-01T00:00:00Z", window_end: "2026-01-02T00:00:00Z",
    volume: 0, delivered: 0, failed: 0, deferred: 0, bounced: 0, policy_denied: 0, suppressed: 0, complaints: 0,
    delivery_rate: 0, bounce_rate: 0, failure_rate: 0, deferred_rate: 0,
    by_category: [], by_domain: [], by_provider: [], time_buckets: [], bucket_size: "hourly",
  },
  volume: 0, delivered: 0, bounced: 0, complaints: 0, delivery_rate: 0, bounce_rate: 0, complaint_rate: 0,
};

function renderPage(scopeTenantId: number | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (scopeTenantId !== null) {
    qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: scopeTenantId, tenantName: "Acme" });
  }
  return render(<QueryClientProvider client={qc}><DeliverabilityPage /></QueryClientProvider>);
}

describe("features/platform/deliverability", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path.includes("/metrics")) return Promise.resolve(METRICS);
      if (path.includes("/events")) return Promise.resolve(EVENTS);
      return Promise.resolve({});
    });
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("requires an explicit tenant", () => {
    renderPage(null);
    expect(screen.getByText("Select a tenant")).toBeInTheDocument();
  });

  it("renders real metrics, breakdowns and time-series buckets from the contract", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("100")).toBeInTheDocument());
    expect(screen.getAllByText("90").length).toBeGreaterThan(0); // delivered
    expect(screen.getByText("By category")).toBeInTheDocument();
    expect(screen.getAllByText("suppressed").length).toBeGreaterThan(0);
    expect(screen.getByText("provider-a")).toBeInTheDocument();
    // Real buckets table — synthetic charts must not exist.
    expect(screen.getByText(/Time series \(hourly buckets\)/)).toBeInTheDocument();
    const calls = mockedRequest.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.startsWith("/platform/deliverability/7/metrics?"))).toBe(true);
    expect(calls.some((p) => p.startsWith("/platform/deliverability/7/events?"))).toBe(true);
  });

  it("computes percentages only from returned numerators and denominators", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getByText("100")).toBeInTheDocument());
    // delivered 90/100 → 90%; suppressed 4/100 → 4%
    expect(screen.getByText("90%")).toBeInTheDocument();
    expect(screen.getByText("4%")).toBeInTheDocument();
  });

  it("renders honest empty states when no data exists — no fabricated metrics", async () => {
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith("/platform/organizations")) return Promise.resolve({ organizations: [], total: 0 });
      if (path.includes("/metrics")) return Promise.resolve(EMPTY_METRICS);
      if (path.includes("/events")) return Promise.resolve({ events: [], total: 0, limit: 100, offset: 0 });
      return Promise.resolve({});
    });
    renderPage(7);
    await waitFor(() => expect(screen.getAllByText("No data for this window.").length).toBeGreaterThan(0));
    expect(screen.getByText("No delivery events for the current filters and window.")).toBeInTheDocument();
  });

  it("renders paginated safe events with category, tenant/domain and timestamp", async () => {
    renderPage(7);
    await waitFor(() => expect(screen.getAllByText("Delivered").length).toBeGreaterThan(0));
    expect(screen.getAllByText("Suppressed").length).toBeGreaterThan(0);
    expect(screen.getAllByText("acme.example").length).toBeGreaterThan(0);
    expect(screen.getByText("200 ms")).toBeInTheDocument();
  });
});
