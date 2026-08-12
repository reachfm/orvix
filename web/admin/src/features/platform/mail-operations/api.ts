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

export function bounceQueueMessage(id: number, reason?: string): Promise<QueueActionResponse> {
  return request<QueueActionResponse>(`/admin/queue/messages/${id}/bounce`, {
    method: "POST",
    body: JSON.stringify({ reason: reason || "" }),
  });
}

export function cancelQueueMessage(id: number): Promise<QueueActionResponse> {
  return request<QueueActionResponse>(`/admin/queue/messages/${id}/cancel`, { method: "POST" });
}

export function bulkQueueAction(ids: number[], action: BulkQueueAction, reason?: string): Promise<BulkQueueActionResponse> {
  return request<BulkQueueActionResponse>("/admin/queue/messages/bulk-action", {
    method: "POST",
    body: JSON.stringify({ ids, action, reason: reason || "" }),
  });
}
