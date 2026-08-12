import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useGrants } from "../support-access/queries";
import { TENANT_CONTEXT_QUERY_KEY, type TenantContextState } from "./contract";

/**
 * Tenant-context state is a client-side *selection* only. It is never
 * sent to the backend as authentication, and it never replaces the
 * operator's real identity. Every mail-control read that uses it must
 * ALSO carry the X-Support-Tenant-ID header, which the backend
 * validates against the operator's active grant per request.
 *
 * The selected tenant is derived from the real platform organization
 * list (GET /platform/organizations) so the operator picks a real
 * tenant, never a fabricated id.
 */
export function useTenantContext() {
  const qc = useQueryClient();
  return useQuery({
    queryKey: TENANT_CONTEXT_QUERY_KEY,
    queryFn: () => ({ tenantId: null, tenantName: undefined, grant: undefined }) as TenantContextState,
    staleTime: Infinity,
  });
}

export function useSetTenantContext() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (state: TenantContextState) => Promise.resolve(state),
    onSuccess: (state) => {
      // Setting a new tenant context MUST invalidate every tenant-
      // scoped mail query so cached data from a previous tenant can
      // never leak into the newly selected tenant's view.
      qc.setQueryData(TENANT_CONTEXT_QUERY_KEY, state);
      qc.removeQueries({ queryKey: ["platform-domains"] });
      qc.removeQueries({ queryKey: ["platform-mailboxes"] });
      qc.removeQueries({ queryKey: ["support-grants", "list"] });
    },
  });
}

export function useClearTenantContext() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => Promise.resolve(undefined),
    onSuccess: () => {
      qc.setQueryData(TENANT_CONTEXT_QUERY_KEY, { tenantId: null, grant: undefined });
      qc.removeQueries({ queryKey: ["platform-domains"] });
      qc.removeQueries({ queryKey: ["platform-mailboxes"] });
    },
  });
}

/**
 * Resolves the active grant for the selected tenant from the real
 * support-grants inventory. Returns undefined when no grant exists or
 * the grant is not active — callers must render the unavailable state.
 */
export function useActiveGrantForSelectedTenant() {
  const { data: context } = useTenantContext();
  const { data: grants } = useGrants();
  const tenantId = context?.tenantId ?? null;
  if (!tenantId || !grants) return undefined;
  const g = grants.grants.find((x) => x.target_tenant_id === tenantId && (x.status === "active" || x.status === "approved"));
  return g ? { id: g.id, scope: g.permission_scope, status: g.status } : undefined;
}

export function headerForTenantContext(tenantId: number | null | undefined): Record<string, string> {
  return tenantId ? { "X-Support-Tenant-ID": String(tenantId) } : {};
}
