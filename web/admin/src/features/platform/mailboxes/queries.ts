import { useQuery } from "@tanstack/react-query";
import { listMailboxesForTenant } from "./api";
import { useTenantContext } from "../tenant-context/queries";

export const mailboxKeys = {
  list: (tenantId: number | null) => ["platform-mailboxes", "list", tenantId ?? "none"] as const,
};

/**
 * Reads the selected tenant's mailboxes. The query key is bound to the
 * tenant id so switching tenant context invalidates and refetches —
 * cached data from a previous tenant can never leak into another.
 */
export function useMailboxesForTenant() {
  const { data: context } = useTenantContext();
  const tenantId = context?.tenantId ?? null;
  return useQuery({
    queryKey: mailboxKeys.list(tenantId),
    queryFn: () => listMailboxesForTenant(tenantId),
    enabled: tenantId !== null,
    retry: false,
  });
}
