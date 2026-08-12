// HTTP transport only, through the shared CSRF/auth-aware client.
import { request } from "../../../api";
import type {
  AddSuppressionRequest,
  DeleteSuppressionResponse,
  ListSuppressionsResponse,
  ReactivateSuppressionRequest,
  ReactivateSuppressionResponse,
  ReleaseSuppressionRequest,
  ReleaseSuppressionResponse,
  Suppression,
  SuppressionFilter,
  SuppressionHistoryResponse,
} from "./contract";

export function listPlatformSuppressions(tenantId: number, filter: SuppressionFilter): Promise<ListSuppressionsResponse> {
  const params = new URLSearchParams();
  if (filter.domain) params.set("domain", filter.domain);
  if (filter.q) params.set("q", filter.q);
  if (filter.reason) params.set("reason", filter.reason);
  if (filter.source) params.set("source", filter.source);
  if (filter.state) params.set("state", filter.state);
  if (filter.created_from) params.set("created_from", filter.created_from);
  if (filter.created_to) params.set("created_to", filter.created_to);
  if (filter.expiry_from) params.set("expiry_from", filter.expiry_from);
  if (filter.expiry_to) params.set("expiry_to", filter.expiry_to);
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<ListSuppressionsResponse>(`/platform/suppressions/${tenantId}${qs ? "?" + qs : ""}`);
}

export function getPlatformSuppression(tenantId: number, id: number): Promise<Suppression> {
  return request<Suppression>(`/platform/suppressions/${tenantId}/${id}`);
}

export function getPlatformSuppressionHistory(tenantId: number, id: number): Promise<SuppressionHistoryResponse> {
  return request<SuppressionHistoryResponse>(`/platform/suppressions/${tenantId}/${id}/history`);
}

export function addPlatformSuppression(tenantId: number, body: AddSuppressionRequest): Promise<Suppression> {
  return request<Suppression>(`/platform/suppressions/${tenantId}`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function releasePlatformSuppression(tenantId: number, id: number, body: ReleaseSuppressionRequest): Promise<ReleaseSuppressionResponse> {
  return request<ReleaseSuppressionResponse>(`/platform/suppressions/${tenantId}/${id}/release`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function reactivatePlatformSuppression(tenantId: number, id: number, body: ReactivateSuppressionRequest): Promise<ReactivateSuppressionResponse> {
  return request<ReactivateSuppressionResponse>(`/platform/suppressions/${tenantId}/${id}/reactivate`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

/** Release with typed confirmation — history is retained by the backend. */
export function deletePlatformSuppression(tenantId: number, id: number, confirmation: string): Promise<DeleteSuppressionResponse> {
  return request<DeleteSuppressionResponse>(`/platform/suppressions/${tenantId}/${id}`, {
    method: "DELETE",
    headers: { "X-Confirm": confirmation },
  });
}
