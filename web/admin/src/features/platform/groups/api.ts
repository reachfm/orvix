// HTTP transport only, through the shared CSRF/auth-aware client.
import { request } from "../../../api";
import type { GroupMembersResponse, PlatformGroup, PlatformGroupFilter, PlatformGroupList } from "./contract";

export function listPlatformGroups(tenantId: number, filter: PlatformGroupFilter): Promise<PlatformGroupList> {
  const params = new URLSearchParams();
  if (filter.q) params.set("q", filter.q);
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<PlatformGroupList>(`/platform/groups/${tenantId}${qs ? "?" + qs : ""}`);
}

export function getPlatformGroup(tenantId: number, id: number): Promise<PlatformGroup> {
  return request<PlatformGroup>(`/platform/groups/${tenantId}/${id}`);
}

export function listPlatformGroupMembers(tenantId: number, id: number): Promise<GroupMembersResponse> {
  return request<GroupMembersResponse>(`/platform/groups/${tenantId}/${id}/members`);
}
