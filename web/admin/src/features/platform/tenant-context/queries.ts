import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { listOrganizations } from "../organizations/api";
import { TENANT_SCOPE_QUERY_KEY, type TenantScopeState } from "./contract";

// Harmless UX preference only — never credentials or secrets. Stores
// the operator's last-used platform tenant id/name so returning to a
// mail-control page (or opening a Create dialog) doesn't start from a
// blank slate every time. Every mutation still sends this id
// explicitly in its own request; nothing is ever inferred silently
// from this value for a WRITE — it only pre-fills selectors.
const LAST_TENANT_STORAGE_KEY = "orvix.platform.lastTenantScope";

function readLastTenantScope(): TenantScopeState {
  try {
    const raw = window.localStorage.getItem(LAST_TENANT_STORAGE_KEY);
    if (!raw) return { tenantId: null };
    const parsed = JSON.parse(raw) as Partial<TenantScopeState>;
    if (typeof parsed.tenantId !== "number") return { tenantId: null };
    return { tenantId: parsed.tenantId, tenantName: typeof parsed.tenantName === "string" ? parsed.tenantName : undefined };
  } catch {
    return { tenantId: null };
  }
}

function writeLastTenantScope(state: TenantScopeState): void {
  try {
    if (state.tenantId === null) {
      window.localStorage.removeItem(LAST_TENANT_STORAGE_KEY);
    } else {
      window.localStorage.setItem(LAST_TENANT_STORAGE_KEY, JSON.stringify(state));
    }
  } catch {
    // Storage unavailable (private mode, quota) — the preference is
    // purely a convenience, never required for correctness.
  }
}

/**
 * Reads the operator's explicit tenant-scope selection. This is a pure
 * client-side filter over the platform routes — the selected tenant id
 * is passed in the route path (/platform/domains/:tenant_id), never as
 * an authentication header. Initializes from the last-used tenant
 * preference (localStorage, id/name only) so the page isn't blank on
 * every visit; still always explicit and always overridable.
 */
export function useTenantScope() {
  return useQuery({
    queryKey: TENANT_SCOPE_QUERY_KEY,
    queryFn: () => readLastTenantScope(),
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
      writeLastTenantScope(state);
      qc.removeQueries({ queryKey: ["platform-domains"] });
      qc.removeQueries({ queryKey: ["platform-mailboxes"] });
      qc.removeQueries({ queryKey: ["platform-aliases"] });
      qc.removeQueries({ queryKey: ["platform-groups"] });
      qc.removeQueries({ queryKey: ["platform-suppressions"] });
      qc.removeQueries({ queryKey: ["platform-deliverability"] });
      qc.removeQueries({ queryKey: ["platform-bulk-mailboxes"] });
      // B-3: billing is tenant-scoped too. Without this, switching
      // tenants would leave the previous customer's balance,
      // adjustments and reconciliation on screen.
      qc.removeQueries({ queryKey: ["platform-billing"] });
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
      // B-3: billing is tenant-scoped too. Without this, switching
      // tenants would leave the previous customer's balance,
      // adjustments and reconciliation on screen.
      qc.removeQueries({ queryKey: ["platform-billing"] });
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
