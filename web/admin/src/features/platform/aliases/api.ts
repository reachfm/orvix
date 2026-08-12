// HTTP transport only, through the shared CSRF/auth-aware client.
import { request } from "../../../api";
import type {
  CreatePlatformAliasRequest,
  DeletePlatformAliasResponse,
  PlatformAlias,
  PlatformAliasFilter,
  PlatformAliasList,
} from "./contract";

export function listPlatformAliases(tenantId: number, filter: PlatformAliasFilter): Promise<PlatformAliasList> {
  const params = new URLSearchParams();
  if (filter.q) params.set("q", filter.q);
  if (filter.to) params.set("to", filter.to);
  if (filter.domain_id !== undefined) params.set("domain_id", String(filter.domain_id));
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<PlatformAliasList>(`/platform/aliases/${tenantId}${qs ? "?" + qs : ""}`);
}

export function getPlatformAlias(tenantId: number, id: number): Promise<PlatformAlias> {
  return request<PlatformAlias>(`/platform/aliases/${tenantId}/${id}`);
}

export function createPlatformAlias(tenantId: number, body: CreatePlatformAliasRequest): Promise<PlatformAlias> {
  return request<PlatformAlias>(`/platform/aliases/${tenantId}`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function deletePlatformAlias(tenantId: number, id: number): Promise<DeletePlatformAliasResponse> {
  return request<DeletePlatformAliasResponse>(`/platform/aliases/${tenantId}/${id}`, { method: "DELETE" });
}
