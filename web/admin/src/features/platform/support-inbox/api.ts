// HTTP transport only — no business logic, no React. Every call goes
// through the shared CSRF/auth-aware client (src/api.ts's request),
// never fetch() directly.
import { request } from "../../../api";
import type {
  GetTicketDetailResponse,
  ListTicketsParams,
  ListTicketsResponse,
  SupportTicket,
} from "./contract";

export function listTickets(params: ListTicketsParams = {}): Promise<ListTicketsResponse> {
  const qs = new URLSearchParams();
  if (params.limit !== undefined) qs.set("limit", String(params.limit));
  if (params.offset !== undefined) qs.set("offset", String(params.offset));
  if (params.status) qs.set("status", params.status);
  if (params.category) qs.set("category", params.category);
  if (params.tenant_id !== undefined) qs.set("tenant_id", String(params.tenant_id));
  if (params.search) qs.set("search", params.search);
  const tail = qs.toString();
  return request<ListTicketsResponse>(`/platform/support/tickets${tail ? `?${tail}` : ""}`);
}

export function getTicket(ref: string): Promise<GetTicketDetailResponse> {
  return request<GetTicketDetailResponse>(
    `/platform/support/tickets/${encodeURIComponent(ref)}`,
  );
}

export function replyOnTicket(ref: string, body: string): Promise<{ message: any }> {
  return request<{ message: any }>(
    `/platform/support/tickets/${encodeURIComponent(ref)}/reply`,
    { method: "POST", body: JSON.stringify({ body }) },
  );
}

export function setTicketStatus(ref: string, status: string): Promise<{ ticket: SupportTicket }> {
  return request<{ ticket: SupportTicket }>(
    `/platform/support/tickets/${encodeURIComponent(ref)}/status`,
    { method: "POST", body: JSON.stringify({ status }) },
  );
}
