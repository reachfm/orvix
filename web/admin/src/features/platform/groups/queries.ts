import { useQuery } from "@tanstack/react-query";
import { getPlatformGroup, listPlatformGroupMembers, listPlatformGroups } from "./api";
import type { PlatformGroupFilter } from "./contract";

export const groupKeys = {
  list: (tenantId: number | null, filter: PlatformGroupFilter) =>
    ["platform-groups", "list", tenantId ?? "none", filter.q ?? "", filter.limit ?? 25, filter.offset ?? 0] as const,
  detail: (tenantId: number | null, id: number) =>
    ["platform-groups", "detail", tenantId ?? "none", id] as const,
  members: (tenantId: number | null, id: number) =>
    ["platform-groups", "members", tenantId ?? "none", id] as const,
};

export function usePlatformGroups(tenantId: number | null, filter: PlatformGroupFilter) {
  return useQuery({
    queryKey: groupKeys.list(tenantId, filter),
    queryFn: () => listPlatformGroups(tenantId as number, filter),
    enabled: tenantId !== null,
    retry: false,
  });
}

export function usePlatformGroup(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: groupKeys.detail(tenantId, id ?? 0),
    queryFn: () => getPlatformGroup(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}

export function usePlatformGroupMembers(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: groupKeys.members(tenantId, id ?? 0),
    queryFn: () => listPlatformGroupMembers(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}
