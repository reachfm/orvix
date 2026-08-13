import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import MailOperationsPage from "./page";
import * as api from "./api";
import { ApiError } from "../../../api";
import type { ListQueueMessagesResponse, QueueSummaryResponse } from "./contract";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><MailOperationsPage /></QueryClientProvider>);
}

const SUMMARY: QueueSummaryResponse = {
  metrics: { pending: 2, leased: 0, delivering: 1, deferred: 0, delivered: 10, bounced: 1, dead_letter: 1, cancelled: 0, total: 15, avg_attempts: 1.2 },
};

const LIST: ListQueueMessagesResponse = {
  messages: [
    { id: 42, tenant_id: 1, domain_id: 1, from_address: "sender@a.example", to_address: "rcpt@b.example", recipient_domain: "b.example", status: "pending", priority: 0, attempt_count: 1, max_attempts: 16, last_status_code: 0, retryable: true, failure_category: "other", created_at: "2026-01-01T00:00:00Z" },
    { id: 43, tenant_id: 1, domain_id: 1, from_address: "s2@a.example", to_address: "r2@b.example", recipient_domain: "b.example", status: "delivered", priority: 0, attempt_count: 2, max_attempts: 16, last_status_code: 250, retryable: false, created_at: "2026-01-01T00:00:00Z" },
    { id: 44, tenant_id: 1, domain_id: 1, from_address: "s3@a.example", to_address: "r3@b.example", recipient_domain: "b.example", status: "deferred", priority: 0, attempt_count: 3, max_attempts: 16, last_status_code: 450, last_error: "recipient is suppressed", retryable: true, failure_category: "suppressed", created_at: "2026-01-01T00:00:00Z" },
  ],
  total: 3, limit: 50, offset: 0,
};

function coreMailDisabledError() {
  return new ApiError("COREMAIL_DISABLED", "mail queue unavailable", 503, { error: "mail queue unavailable", code: "COREMAIL_DISABLED" });
}

describe("features/platform/mail-operations", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("shows the CoreMail-disabled banner distinctly, never an empty queue table", async () => {
    vi.spyOn(api, "listQueueMessages").mockRejectedValue(coreMailDisabledError());
    vi.spyOn(api, "getQueueSummary").mockRejectedValue(coreMailDisabledError());
    renderPage();
    await waitFor(() => expect(screen.getByText("CoreMail is disabled")).toBeInTheDocument());
    expect(screen.queryByText("Queue is empty")).not.toBeInTheDocument();
  });

  it("shows a distinct empty state when CoreMail is enabled but the queue has no messages", async () => {
    vi.spyOn(api, "listQueueMessages").mockResolvedValue({ messages: [], total: 0, limit: 50, offset: 0 });
    vi.spyOn(api, "getQueueSummary").mockResolvedValue({ metrics: { pending: 0, leased: 0, delivering: 0, deferred: 0, delivered: 0, bounced: 0, dead_letter: 0, cancelled: 0, total: 0, avg_attempts: 0 } });
    renderPage();
    await waitFor(() => expect(screen.getByText("Queue is empty")).toBeInTheDocument());
    expect(screen.queryByText("CoreMail is disabled")).not.toBeInTheDocument();
  });

  it("renders real queue rows with the correct field names (from_address/to_address/attempt_count)", async () => {
    vi.spyOn(api, "listQueueMessages").mockResolvedValue(LIST);
    vi.spyOn(api, "getQueueSummary").mockResolvedValue(SUMMARY);
    renderPage();
    await waitFor(() => expect(screen.getByText("sender@a.example")).toBeInTheDocument());
    expect(screen.getByText("rcpt@b.example")).toBeInTheDocument();
    expect(screen.getByText("1 / 16")).toBeInTheDocument();
    // Summary: failed = dead_letter + bounced = 2.
    const failedLabel = screen.getByText("failed");
    expect(failedLabel.parentElement).toHaveTextContent("2");
  });

  it("shows a distinct genuine-error state for a sanitized 500, never an empty-success screen", async () => {
    vi.spyOn(api, "listQueueMessages").mockRejectedValue(new Error("queue unavailable"));
    vi.spyOn(api, "getQueueSummary").mockResolvedValue(SUMMARY);
    renderPage();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/queue unavailable/i));
    expect(screen.queryByText("Queue is empty")).not.toBeInTheDocument();
  });

  it("retry disables its own button while pending, preventing duplicate submission", async () => {
    vi.spyOn(api, "listQueueMessages").mockResolvedValue(LIST);
    vi.spyOn(api, "getQueueSummary").mockResolvedValue(SUMMARY);
    let resolveRetry: (v: any) => void;
    vi.spyOn(api, "retryQueueMessage").mockReturnValue(new Promise((res) => { resolveRetry = res; }));
    renderPage();
    await waitFor(() => expect(screen.getByText("sender@a.example")).toBeInTheDocument());
    const retryBtn = screen.getByRole("button", { name: "Retry message 42" });
    fireEvent.click(retryBtn);
    await waitFor(() => expect(retryBtn).toBeDisabled());
    resolveRetry!({ status: "retrying", id: 42 });
  });

  it("bounce requires the typed X-Confirm phrase before calling the API", async () => {
    vi.spyOn(api, "listQueueMessages").mockResolvedValue(LIST);
    vi.spyOn(api, "getQueueSummary").mockResolvedValue(SUMMARY);
    const bounceSpy = vi.spyOn(api, "bounceQueueMessage").mockResolvedValue({ status: "bounced", id: 42 });
    renderPage();
    await waitFor(() => expect(screen.getByText("sender@a.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Bounce message 42" }));

    const confirmBtn = screen.getByRole("button", { name: /confirm/i });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/Type BOUNCE-QUEUE-42 to confirm/i), { target: { value: "BOUNCE-QUEUE-42" } });
    await waitFor(() => expect(confirmBtn).toBeEnabled());
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(bounceSpy).toHaveBeenCalledWith(42, undefined, "BOUNCE-QUEUE-42"));
  });

  it("exposes state-aware actions: delivered entries never show retry/bounce/cancel", async () => {
    vi.spyOn(api, "listQueueMessages").mockResolvedValue(LIST);
    vi.spyOn(api, "getQueueSummary").mockResolvedValue(SUMMARY);
    renderPage();
    await waitFor(() => expect(screen.getByText("sender@a.example")).toBeInTheDocument());
    // #43 is delivered (terminal) — its action buttons are disabled.
    expect(screen.getByRole("button", { name: "Retry message 43" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Bounce message 43" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel message 43" })).toBeDisabled();
    // #42 pending — retry enabled, and the suppressed entry shows its category.
    expect(screen.getByRole("button", { name: "Retry message 42" })).toBeEnabled();
    expect(screen.getByText("Suppressed")).toBeInTheDocument();
  });
});
