// HTTP transport only, through the shared CSRF/auth-aware client.
import { request } from "../../../api";
import type {
  CreatePlatformGroupRequest,
  CreatePlatformGroupResponse,
  GroupActionResponse,
  GroupMembersResponse,
  PlatformGroup,
  PlatformGroupFilter,
  PlatformGroupList,
} from "./contract";
import { confirmGroupDelete } from "./contract";

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

export function createPlatformGroup(tenantId: number, body: CreatePlatformGroupRequest): Promise<CreatePlatformGroupResponse> {
  return request<CreatePlatformGroupResponse>(`/platform/groups/${tenantId}`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

/**
 * Soft-deletes a tenant group. Destructive: requires the typed
 * X-Confirm: DELETE-GROUP-<id> header the handler checks before any
 * write (mailcontrol.ConfirmGroupDelete); audited server-side.
 */
export function deletePlatformGroup(tenantId: number, id: number): Promise<GroupActionResponse> {
  return request<GroupActionResponse>(`/platform/groups/${tenantId}/${id}`, {
    method: "DELETE",
    headers: { "X-Confirm": confirmGroupDelete(id) },
  });
}

export function addPlatformGroupMember(tenantId: number, id: number, email: string): Promise<GroupActionResponse> {
  return request<GroupActionResponse>(`/platform/groups/${tenantId}/${id}/members`, {
    method: "POST",
    body: JSON.stringify({ email }),
  });
}

export function removePlatformGroupMember(tenantId: number, id: number, memberId: string): Promise<GroupActionResponse> {
  return request<GroupActionResponse>(`/platform/groups/${tenantId}/${id}/members/${encodeURIComponent(memberId)}`, {
    method: "DELETE",
  });
}
