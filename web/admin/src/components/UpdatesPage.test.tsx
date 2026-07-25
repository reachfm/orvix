// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import UpdatesPage from "./UpdatesPage";

function makeQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
}

function Wrapper({ children, qc }: { children: ReactNode; qc: QueryClient }) {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function mockResponse(data: any, status = 200) {
  return { ok: status < 400, status, json: () => Promise.resolve(data) } as Response;
}

const job = {
  id: "job-1",
  kind: "install",
  idempotency_key: "k1",
  requested_version: "1.0.4",
  initiated_by: "admin@test",
  phase: "downloading",
  progress_percent: 42,
  created_at: "2026-07-25T00:00:00Z",
  updated_at: "2026-07-25T00:01:00Z",
};

describe("UpdatesPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.useRealTimers();
  });

  it("shows current version and no in-progress job when status has no job", async () => {
    globalThis.fetch = vi.fn<typeof fetch>((url: any) => {
      const path = typeof url === "string" ? url.split("?")[0] : "";
      if (path.endsWith("/admin/updates/status")) return Promise.resolve(mockResponse({ job: null }));
      if (path.endsWith("/admin/updates/releases")) return Promise.resolve(mockResponse({ releases: [] }));
      if (path.endsWith("/admin/updates/history")) return Promise.resolve(mockResponse({ history: [] }));
      if (path.endsWith("/admin/updates/snapshots")) return Promise.resolve(mockResponse({ snapshots: [] }));
      return Promise.resolve(mockResponse({}));
    });

    render(<Wrapper qc={makeQueryClient()}><UpdatesPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Updates")).toBeInTheDocument());
    expect(screen.getByText("Current Version")).toBeInTheDocument();
    expect(screen.queryByText(/Update in progress/i)).not.toBeInTheDocument();
  });

  it("reconnects to an in-progress job reported by GET /status on load", async () => {
    globalThis.fetch = vi.fn<typeof fetch>((url: any) => {
      const path = typeof url === "string" ? url.split("?")[0] : "";
      if (path.endsWith("/admin/updates/status")) return Promise.resolve(mockResponse({ job }));
      if (path.endsWith("/admin/updates/jobs/job-1")) return Promise.resolve(mockResponse({ job }));
      if (path.endsWith("/admin/updates/releases")) return Promise.resolve(mockResponse({ releases: [] }));
      if (path.endsWith("/admin/updates/history")) return Promise.resolve(mockResponse({ history: [] }));
      if (path.endsWith("/admin/updates/snapshots")) return Promise.resolve(mockResponse({ snapshots: [] }));
      return Promise.resolve(mockResponse({}));
    });

    render(<Wrapper qc={makeQueryClient()}><UpdatesPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText(/Update in progress/i)).toBeInTheDocument());
    expect(screen.getByText(/42% —/)).toBeInTheDocument();
  });

  it("shows the updater-offline state on a 503 status response", async () => {
    globalThis.fetch = vi.fn<typeof fetch>(() =>
      Promise.resolve(mockResponse({ error: "self-update is not available on this deployment" }, 503))
    );

    render(<Wrapper qc={makeQueryClient()}><UpdatesPage /></Wrapper>);
    await waitFor(() =>
      expect(screen.getByText(/Self-update is not available on this deployment/i)).toBeInTheDocument()
    );
  });

  it("shows no-update-available message when check returns an empty release list", async () => {
    globalThis.fetch = vi.fn<typeof fetch>((url: any) => {
      const path = typeof url === "string" ? url.split("?")[0] : "";
      if (path.endsWith("/admin/updates/status")) return Promise.resolve(mockResponse({ job: null }));
      if (path.endsWith("/admin/updates/releases")) return Promise.resolve(mockResponse({ releases: [] }));
      if (path.endsWith("/admin/updates/history")) return Promise.resolve(mockResponse({ history: [] }));
      if (path.endsWith("/admin/updates/snapshots")) return Promise.resolve(mockResponse({ snapshots: [] }));
      if (path.endsWith("/csrf-token")) return Promise.resolve(mockResponse({ csrf_token: "t" }));
      if (path.endsWith("/admin/updates/check")) return Promise.resolve(mockResponse({ releases: [] }));
      return Promise.resolve(mockResponse({}));
    });

    render(<Wrapper qc={makeQueryClient()}><UpdatesPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Updates")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Check for Updates/i }));
    await waitFor(() =>
      expect(screen.getByText(/No update available/i)).toBeInTheDocument()
    );
  });

  it("keeps Install Update disabled until a version is selected", async () => {
    globalThis.fetch = vi.fn<typeof fetch>((url: any) => {
      const path = typeof url === "string" ? url.split("?")[0] : "";
      if (path.endsWith("/admin/updates/status")) return Promise.resolve(mockResponse({ job: null }));
      if (path.endsWith("/admin/updates/releases")) return Promise.resolve(mockResponse({ releases: [] }));
      if (path.endsWith("/admin/updates/history")) return Promise.resolve(mockResponse({ history: [] }));
      if (path.endsWith("/admin/updates/snapshots")) return Promise.resolve(mockResponse({ snapshots: [] }));
      return Promise.resolve(mockResponse({}));
    });

    render(<Wrapper qc={makeQueryClient()}><UpdatesPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Updates")).toBeInTheDocument());
    // No release selected -> no Install Update button rendered at all yet.
    expect(screen.queryByRole("button", { name: /Install Update/i })).not.toBeInTheDocument();
  });

  it("shows failure + rollback outcome for a failed, rolled-back job", async () => {
    const failedJob = {
      ...job,
      phase: "rolled_back",
      failure_code: "health_check_failed",
      failure_message: "post-update health check did not report healthy within timeout",
      rollback_result: "rollback completed successfully",
    };
    globalThis.fetch = vi.fn<typeof fetch>((url: any) => {
      const path = typeof url === "string" ? url.split("?")[0] : "";
      if (path.endsWith("/admin/updates/status")) return Promise.resolve(mockResponse({ job: failedJob }));
      if (path.endsWith("/admin/updates/jobs/job-1")) return Promise.resolve(mockResponse({ job: failedJob }));
      if (path.endsWith("/admin/updates/releases")) return Promise.resolve(mockResponse({ releases: [] }));
      if (path.endsWith("/admin/updates/history")) return Promise.resolve(mockResponse({ history: [] }));
      if (path.endsWith("/admin/updates/snapshots")) return Promise.resolve(mockResponse({ snapshots: [] }));
      return Promise.resolve(mockResponse({}));
    });

    render(<Wrapper qc={makeQueryClient()}><UpdatesPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText(/Update failed and was rolled back/i)).toBeInTheDocument());
    expect(screen.getByText(/post-update health check did not report healthy/i)).toBeInTheDocument();
    expect(screen.getByText(/rollback completed successfully/i)).toBeInTheDocument();
  });
});
