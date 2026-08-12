import { useQuery } from "@tanstack/react-query";
import { getPlatformMailbox, listPlatformMailboxes } from "./api";
import type { PlatformMailboxFilter } from "./contract";

export const mailboxKeys = {
  list: (tenantId: number | null, filter: PlatformMailboxFilter) =>
    ["platform-mailboxes", "list", tenantId ?? "none", filter.q ?? "", filter.status ?? "", filter.domain_id ?? 0, filter.limit ?? 25, filter.offset ?? 0] as const,
  detail: (tenantId: number | null, id: number) =>
    ["platform-mailboxes", "detail", tenantId ?? "none", id] as const,
};

/**
 * Platform-wide mailbox inventory for one EXPLICIT tenant. The query
 * key is bound to the tenant id — cached rows from another tenant can
 * never leak into this view.
 */
export function usePlatformMailboxes(tenantId: number | null, filter: PlatformMailboxFilter) {
  return useQuery({
    queryKey: mailboxKeys.list(tenantId, filter),
    queryFn: () => listPlatformMailboxes(tenantId as number, filter),
    enabled: tenantId !== null,
    retry: false,
  });
}

export function usePlatformMailbox(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: mailboxKeys.detail(tenantId, id ?? 0),
    queryFn: () => getPlatformMailbox(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}
