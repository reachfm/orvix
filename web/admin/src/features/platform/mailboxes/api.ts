// HTTP transport only, through the shared CSRF/auth-aware client.
// Every call targets an explicit tenant id in the route path — no
// support-context header, no grant, no impersonation.
import { request } from "../../../api";
import type {
  DeletePlatformMailboxResponse,
  EndMailboxSupportViewResponse,
  PlatformCreateMailboxRequest,
  PlatformCreateMailboxResult,
  PlatformMailbox,
  PlatformMailboxAccessModeResult,
  PlatformMailboxFilter,
  PlatformMailboxList,
  PlatformSetMailboxAccessModeRequest,
  ResetPlatformMailboxPasswordResponse,
  SetPlatformMailboxQuotaRequest,
  SetPlatformMailboxQuotaResponse,
  SetPlatformMailboxStatusRequest,
  SetPlatformMailboxStatusResponse,
  StartMailboxSupportViewRequest,
  StartMailboxSupportViewResponse,
  SupportViewFoldersResponse,
  SupportViewMessageDetailResponse,
  SupportViewMessagesResponse,
} from "./contract";

export function listPlatformMailboxes(tenantId: number, filter: PlatformMailboxFilter): Promise<PlatformMailboxList> {
  const params = new URLSearchParams();
  if (filter.q) params.set("q", filter.q);
  if (filter.status) params.set("status", filter.status);
  if (filter.domain_id !== undefined) params.set("domain_id", String(filter.domain_id));
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<PlatformMailboxList>(`/platform/mailboxes/${tenantId}${qs ? "?" + qs : ""}`);
}

export function getPlatformMailbox(tenantId: number, id: number): Promise<PlatformMailbox> {
  return request<PlatformMailbox>(`/platform/mailboxes/${tenantId}/${id}`);
}

/**
 * Sensitive mutation: the backend sends Cache-Control: no-store on
 * this response (it never carries the password, but callers must not
 * assume otherwise or cache it regardless).
 */
export function createPlatformMailbox(
  tenantId: number,
  body: PlatformCreateMailboxRequest,
  idempotencyKey: string,
): Promise<PlatformCreateMailboxResult> {
  return request<PlatformCreateMailboxResult>(`/platform/mailboxes/${tenantId}`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function setPlatformMailboxAccessMode(
  tenantId: number,
  id: number,
  body: PlatformSetMailboxAccessModeRequest,
  idempotencyKey: string,
): Promise<PlatformMailboxAccessModeResult> {
  return request<PlatformMailboxAccessModeResult>(`/platform/mailboxes/${tenantId}/${id}/access-mode`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function setPlatformMailboxStatus(
  tenantId: number,
  id: number,
  body: SetPlatformMailboxStatusRequest,
): Promise<SetPlatformMailboxStatusResponse> {
  return request<SetPlatformMailboxStatusResponse>(`/platform/mailboxes/${tenantId}/${id}/status`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function setPlatformMailboxQuota(
  tenantId: number,
  id: number,
  body: SetPlatformMailboxQuotaRequest,
): Promise<SetPlatformMailboxQuotaResponse> {
  return request<SetPlatformMailboxQuotaResponse>(`/platform/mailboxes/${tenantId}/${id}/quota`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function resetPlatformMailboxPassword(tenantId: number, id: number): Promise<ResetPlatformMailboxPasswordResponse> {
  return request<ResetPlatformMailboxPasswordResponse>(`/platform/mailboxes/${tenantId}/${id}/reset-password`, {
    method: "POST",
  });
}

/**
 * Soft-delete a mailbox. The backend requires the typed confirmation
 * header; the password/generated credential is never involved here.
 */
export function deletePlatformMailbox(tenantId: number, id: number, confirmation: string): Promise<DeletePlatformMailboxResponse> {
  return request<DeletePlatformMailboxResponse>(`/platform/mailboxes/${tenantId}/${id}`, {
    method: "DELETE",
    headers: { "X-Confirm": confirmation },
  });
}

// ── Audited, read-only support view ─────────────────────────────────
// The operator's own platform session is never touched by any of
// these calls — no impersonation, no customer JWT, no password read.

export function startMailboxSupportView(
  tenantId: number,
  id: number,
  body: StartMailboxSupportViewRequest,
): Promise<StartMailboxSupportViewResponse> {
  return request<StartMailboxSupportViewResponse>(`/platform/mailboxes/${tenantId}/${id}/support-view`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function listSupportViewFolders(tenantId: number, id: number, sessionId: string): Promise<SupportViewFoldersResponse> {
  return request<SupportViewFoldersResponse>(`/platform/mailboxes/${tenantId}/${id}/support-view/${sessionId}/folders`);
}

export function listSupportViewMessages(
  tenantId: number,
  id: number,
  sessionId: string,
  opts: { folderId?: number; q?: string } = {},
): Promise<SupportViewMessagesResponse> {
  const params = new URLSearchParams();
  if (opts.folderId !== undefined) params.set("folder_id", String(opts.folderId));
  if (opts.q) params.set("q", opts.q);
  const qs = params.toString();
  return request<SupportViewMessagesResponse>(`/platform/mailboxes/${tenantId}/${id}/support-view/${sessionId}/messages${qs ? "?" + qs : ""}`);
}

export function getSupportViewMessage(
  tenantId: number,
  id: number,
  sessionId: string,
  messageId: number,
): Promise<SupportViewMessageDetailResponse> {
  return request<SupportViewMessageDetailResponse>(`/platform/mailboxes/${tenantId}/${id}/support-view/${sessionId}/messages/${messageId}`);
}

export function supportViewAttachmentUrl(tenantId: number, id: number, sessionId: string, messageId: number, attachmentId: number): string {
  return `/api/v1/platform/mailboxes/${tenantId}/${id}/support-view/${sessionId}/messages/${messageId}/attachments/${attachmentId}`;
}

export function endMailboxSupportView(tenantId: number, id: number, sessionId: string): Promise<EndMailboxSupportViewResponse> {
  return request<EndMailboxSupportViewResponse>(`/platform/mailboxes/${tenantId}/${id}/support-view/${sessionId}/end`, {
    method: "POST",
  });
}
