import { request } from "../../../api";
import type { AccessGrant, CreateGrantRequest, GrantDetailResponse, ListGrantsResponse } from "./contract";

export function listGrants(tenantId?: number): Promise<ListGrantsResponse> {
  const qs = tenantId ? `?tenant_id=${tenantId}` : "";
  return request<ListGrantsResponse>(`/platform/support/grants${qs}`);
}

export function getGrant(id: number): Promise<GrantDetailResponse> {
  return request<GrantDetailResponse>(`/platform/support/grants/${id}`);
}

export function createGrant(data: CreateGrantRequest): Promise<GrantDetailResponse> {
  return request<GrantDetailResponse>("/platform/support/grants", { method: "POST", body: JSON.stringify(data) });
}

export function activateGrant(id: number): Promise<GrantDetailResponse> {
  return request<GrantDetailResponse>(`/platform/support/grants/${id}/activate`, { method: "POST" });
}

export function revokeGrant(id: number, reason?: string): Promise<GrantDetailResponse> {
  return request<GrantDetailResponse>(`/platform/support/grants/${id}/revoke`, { method: "POST", body: JSON.stringify({ reason: reason ?? "" }) });
}
