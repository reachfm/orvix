import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getPlatformDomain, getPlatformDomainDNS, listPlatformDomains } from "./api";
import type { PlatformDKIMResult, PlatformDNSRequirement, PlatformDomainFilter } from "./contract";

export const domainKeys = {
  list: (tenantId: number | null, filter: PlatformDomainFilter) =>
    ["platform-domains", "list", tenantId ?? "none", filter.q ?? "", filter.status ?? "", filter.limit ?? 25, filter.offset ?? 0] as const,
  detail: (tenantId: number | null, id: number) =>
    ["platform-domains", "detail", tenantId ?? "none", id] as const,
  /**
   * Live existing-domain DNS/DKIM snapshot: GET .../dns
   * (mailcontrol.PlatformDomainDNSResult). Tenant- and domain-scoped,
   * refetched on demand — this is the authoritative source for the DNS
   * Setup/DKIM tabs, superseding the one-time creation-response cache
   * below for any domain opened from the list.
   */
  dns: (tenantId: number | null, id: number) =>
    ["platform-domains", "dns", tenantId ?? "none", id] as const,
  /**
   * DNS/DKIM records also arrive in the one-time domain-creation
   * response (PlatformCreateDomainResult). This key holds that
   * one-time payload in the query cache, tenant- and domain-scoped,
   * purely so "View domain" right after Create can show the exact
   * values the backend just returned without waiting on a refetch.
   * The live `dns` query above is authoritative for anything beyond
   * that immediate post-create moment — a stale creation cache must
   * never override newer server state.
   */
  dnsCache: (tenantId: number | null, id: number) =>
    ["platform-domains", "dns-cache", tenantId ?? "none", id] as const,
};

export interface DomainDNSCacheEntry {
  dkim?: PlatformDKIMResult;
  dns_requirements?: PlatformDNSRequirement[];
  dns_next_step?: string;
}

/** Reads the one-time creation-response DNS/DKIM cache for a domain, if present. */
export function useDomainDNSCache(tenantId: number | null, id: number | null) {
  const qc = useQueryClient();
  if (tenantId === null || id === null) return null;
  return qc.getQueryData<DomainDNSCacheEntry>(domainKeys.dnsCache(tenantId, id)) ?? null;
}

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

/**
 * Live DNS/DKIM snapshot for an EXISTING domain — calls the real
 * backend GET .../dns route. Authoritative: never falls back to the
 * one-time creation cache when this has loaded.
 */
export function usePlatformDomainDNS(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: domainKeys.dns(tenantId, id ?? 0),
    queryFn: () => getPlatformDomainDNS(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}
