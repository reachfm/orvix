import { useQuery } from "@tanstack/react-query";
import { getPlatformRelay, listPlatformRelays } from "./api";
import type { PlatformRelayFilter } from "./contract";

export const relayKeys = {
  list: (filter: PlatformRelayFilter) =>
    ["platform-relays", "list", filter.scope ?? "", filter.q ?? "", filter.tenant_id ?? 0, filter.domain_id ?? 0, filter.active ?? "", filter.limit ?? 50, filter.offset ?? 0] as const,
  detail: (id: number) => ["platform-relays", "detail", id] as const,
};

export function usePlatformRelays(filter: PlatformRelayFilter) {
  return useQuery({
    queryKey: relayKeys.list(filter),
    queryFn: () => listPlatformRelays(filter),
    retry: false,
  });
}

export function usePlatformRelay(id: number | null) {
  return useQuery({
    queryKey: relayKeys.detail(id ?? 0),
    queryFn: () => getPlatformRelay(id as number),
    enabled: id !== null,
    retry: false,
  });
}
