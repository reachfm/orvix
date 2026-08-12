import { useQuery } from "@tanstack/react-query";
import { listDomainsForTenant } from "./api";
import { useTenantContext } from "../tenant-context/queries";

export const domainKeys = {
  list: (tenantId: number | null) => ["platform-domains", "list", tenantId ?? "none"] as const,
};

/**
 * Reads the selected tenant's domains. The query key is bound to the
 * tenant id so switching tenant context invalidates and refetches —
 * cached data from a previous tenant can never leak into another.
 */
export function useDomainsForTenant() {
  const { data: context } = useTenantContext();
  const tenantId = context?.tenantId ?? null;
  return useQuery({
    queryKey: domainKeys.list(tenantId),
    queryFn: () => listDomainsForTenant(tenantId),
    enabled: tenantId !== null,
    retry: false,
  });
}
