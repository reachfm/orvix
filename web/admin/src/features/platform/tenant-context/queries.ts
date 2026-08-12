import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { listOrganizations } from "../organizations/api";
import { TENANT_SCOPE_QUERY_KEY, type TenantScopeState } from "./contract";

/**
 * Reads the operator's explicit tenant-scope selection. This is a pure
 * client-side filter over the platform routes — the selected tenant id
 * is passed in the route path (/platform/domains/:tenant_id), never as
 * an authentication header.
 */
export function useTenantScope() {
  return useQuery({
    queryKey: TENANT_SCOPE_QUERY_KEY,
    queryFn: () => ({ tenantId: null }) as TenantScopeState,
    staleTime: Infinity,
  });
}

export function useSetTenantScope() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (state: TenantScopeState) => Promise.resolve(state),
    onSuccess: (state) => {
      // Binding the new scope MUST evict every tenant-scoped mail
      // query so cached rows from a previous tenant can never leak
      // into the newly selected tenant's view.
      qc.setQueryData(TENANT_SCOPE_QUERY_KEY, state);
      qc.removeQueries({ queryKey: ["platform-domains"] });
      qc.removeQueries({ queryKey: ["platform-mailboxes"] });
      qc.removeQueries({ queryKey: ["platform-aliases"] });
      qc.removeQueries({ queryKey: ["platform-groups"] });
      qc.removeQueries({ queryKey: ["platform-suppressions"] });
      qc.removeQueries({ queryKey: ["platform-deliverability"] });
      qc.removeQueries({ queryKey: ["platform-bulk-mailboxes"] });
    },
  });
}

export function useClearTenantScope() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => Promise.resolve(undefined),
    onSuccess: () => {
      qc.setQueryData(TENANT_SCOPE_QUERY_KEY, { tenantId: null });
      qc.removeQueries({ queryKey: ["platform-domains"] });
      qc.removeQueries({ queryKey: ["platform-mailboxes"] });
      qc.removeQueries({ queryKey: ["platform-aliases"] });
      qc.removeQueries({ queryKey: ["platform-groups"] });
      qc.removeQueries({ queryKey: ["platform-suppressions"] });
      qc.removeQueries({ queryKey: ["platform-deliverability"] });
      qc.removeQueries({ queryKey: ["platform-bulk-mailboxes"] });
    },
  });
}

/**
 * Real tenant options for the explicit scope selector, sourced from
 * GET /platform/organizations so the operator always picks a real
 * tenant id — never a fabricated one. Mail-control pages do not ship
 * their own tenant list; they reuse this organization-derived source.
 */
export function useTenantOptions() {
  return useQuery({
    queryKey: ["platform-organizations", "", 200, 0],
    queryFn: () => listOrganizations(undefined, 200, 0),
    retry: false,
    staleTime: 60_000,
  });
}
