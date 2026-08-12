import { useQuery } from "@tanstack/react-query";
import { getPlatformDomain, listPlatformDomains } from "./api";
import type { PlatformDomainFilter } from "./contract";

export const domainKeys = {
  list: (tenantId: number | null, filter: PlatformDomainFilter) =>
    ["platform-domains", "list", tenantId ?? "none", filter.q ?? "", filter.status ?? "", filter.limit ?? 25, filter.offset ?? 0] as const,
  detail: (tenantId: number | null, id: number) =>
    ["platform-domains", "detail", tenantId ?? "none", id] as const,
};

/**
 * Platform-wide domain inventory for one EXPLICIT tenant. The query
 * key is bound to the tenant id — switching scope evicts and refetches,
 * so cached rows from another tenant can never leak into this view.
 */
export function usePlatformDomains(tenantId: number | null, filter: PlatformDomainFilter) {
  return useQuery({
    queryKey: domainKeys.list(tenantId, filter),
    queryFn: () => listPlatformDomains(tenantId as number, filter),
    enabled: tenantId !== null,
    retry: false,
  });
}

export function usePlatformDomain(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: domainKeys.detail(tenantId, id ?? 0),
    queryFn: () => getPlatformDomain(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}
