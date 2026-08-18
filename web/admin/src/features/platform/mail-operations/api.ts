// HTTP transport only, through the shared CSRF/auth-aware client.
import { request } from "../../../api";
import type {
  ListQueueMessagesResponse,
  QueueMessageFilter,
  QueueSummaryResponse,
  QueueDetailResponse,
  QueueActionResponse,
  BulkQueueAction,
  BulkQueueActionResponse,
  QueueHistoryFilter,
  QueueHistoryResponse,
} from "./contract";

export function listQueueMessages(filter: QueueMessageFilter): Promise<ListQueueMessagesResponse> {
  const params = new URLSearchParams();
  if (filter.status) params.set("status", filter.status);
  // AdminQueueList (internal/api/handlers/admin_queue.go) reads "from"
  // and "to" query params for a substring match on sender/recipient —
  // there is no free-text "q" param on this endpoint.
  if (filter.from) params.set("from", filter.from);
  if (filter.to) params.set("to", filter.to);
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<ListQueueMessagesResponse>(`/admin/queue/messages${qs ? "?" + qs : ""}`);
}

export function getQueueSummary(): Promise<QueueSummaryResponse> {
  return request<QueueSummaryResponse>("/admin/queue/summary");
}

export function getQueueDetail(id: number): Promise<QueueDetailResponse> {
  return request<QueueDetailResponse>(`/admin/queue/messages/${id}`);
}

export function retryQueueMessage(id: number): Promise<QueueActionResponse> {
  return request<QueueActionResponse>(`/admin/queue/messages/${id}/retry`, { method: "POST" });
}

export function bounceQueueMessage(id: number, reason?: string, confirmation?: string): Promise<QueueActionResponse> {
  return request<QueueActionResponse>(`/admin/queue/messages/${id}/bounce`, {
    method: "POST",
    body: JSON.stringify({ reason: reason || "" }),
    headers: confirmation ? { "X-Confirm": confirmation } : undefined,
  });
}

export function cancelQueueMessage(id: number, confirmation?: string): Promise<QueueActionResponse> {
  return request<QueueActionResponse>(`/admin/queue/messages/${id}/cancel`, {
    method: "POST",
    headers: confirmation ? { "X-Confirm": confirmation } : undefined,
  });
}

export function bulkQueueAction(ids: number[], action: BulkQueueAction, reason?: string): Promise<BulkQueueActionResponse> {
  return request<BulkQueueActionResponse>("/admin/queue/messages/bulk-action", {
    method: "POST",
    body: JSON.stringify({ ids, action, reason: reason || "" }),
  });
}

/**
 * Immutable delivery-attempt history (GET /admin/queue/history),
 * cursor-paginated: pass after_id = last row's id to page forward.
 */
export function getQueueHistory(filter: QueueHistoryFilter): Promise<QueueHistoryResponse> {
  const params = new URLSearchParams();
  if (filter.status) params.set("status", filter.status);
  if (filter.remote_host) params.set("remote_host", filter.remote_host);
  if (filter.after_id !== undefined && filter.after_id > 0) params.set("after_id", String(filter.after_id));
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  const qs = params.toString();
  return request<QueueHistoryResponse>(`/admin/queue/history${qs ? "?" + qs : ""}`);
}

/**
 * Redacted CSV export of the current live queue (GET /admin/queue/
 * export). The endpoint answers text/csv with Content-Disposition —
 * NOT a JSON envelope — so this client requests a raw Blob through the
 * shared CSRF/auth-aware transport (documented file-download
 * exception) and the caller triggers the browser download.
 */
export function exportQueueCsv(): Promise<Blob> {
  return request<Blob>("/admin/queue/export", { responseType: "blob" });
}
