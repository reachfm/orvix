// HTTP transport only, through the shared CSRF/auth-aware client.
// Every call targets an explicit tenant id in the route path — no
// support-context header, no grant, no impersonation.
import { request } from "../../../api";
import type {
  PlatformDomain,
  PlatformDomainFilter,
  PlatformDomainList,
  SetPlatformDomainMailAccessModeRequest,
  SetPlatformDomainMailAccessModeResponse,
  SetPlatformDomainStatusRequest,
  SetPlatformDomainStatusResponse,
} from "./contract";

export function listPlatformDomains(tenantId: number, filter: PlatformDomainFilter): Promise<PlatformDomainList> {
  const params = new URLSearchParams();
  if (filter.q) params.set("q", filter.q);
  if (filter.status) params.set("status", filter.status);
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<PlatformDomainList>(`/platform/domains/${tenantId}${qs ? "?" + qs : ""}`);
}

export function getPlatformDomain(tenantId: number, id: number): Promise<PlatformDomain> {
  return request<PlatformDomain>(`/platform/domains/${tenantId}/${id}`);
}

export function setPlatformDomainStatus(
  tenantId: number,
  id: number,
  body: SetPlatformDomainStatusRequest,
): Promise<SetPlatformDomainStatusResponse> {
  return request<SetPlatformDomainStatusResponse>(`/platform/domains/${tenantId}/${id}/status`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function setPlatformDomainMailAccessMode(
  tenantId: number,
  id: number,
  body: SetPlatformDomainMailAccessModeRequest,
): Promise<SetPlatformDomainMailAccessModeResponse> {
  return request<SetPlatformDomainMailAccessModeResponse>(`/platform/domains/${tenantId}/${id}/mail-access-mode`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}
