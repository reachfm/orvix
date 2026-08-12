import { useQuery } from "@tanstack/react-query";
import { getPlatformAlias, listPlatformAliases } from "./api";
import type { PlatformAliasFilter } from "./contract";

export const aliasKeys = {
  list: (tenantId: number | null, filter: PlatformAliasFilter) =>
    ["platform-aliases", "list", tenantId ?? "none", filter.q ?? "", filter.to ?? "", filter.domain_id ?? 0, filter.limit ?? 25, filter.offset ?? 0] as const,
  detail: (tenantId: number | null, id: number) =>
    ["platform-aliases", "detail", tenantId ?? "none", id] as const,
};

export function usePlatformAliases(tenantId: number | null, filter: PlatformAliasFilter) {
  return useQuery({
    queryKey: aliasKeys.list(tenantId, filter),
    queryFn: () => listPlatformAliases(tenantId as number, filter),
    enabled: tenantId !== null,
    retry: false,
  });
}

export function usePlatformAlias(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: aliasKeys.detail(tenantId, id ?? 0),
    queryFn: () => getPlatformAlias(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}
