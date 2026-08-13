import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createAdjustment, getAdjustments, getBalance, getReconciliation } from "./api";
import type { CreateAdjustmentRequest } from "./contract";

/**
 * B-3: Platform Billing is bound to an EXPLICITLY selected tenant.
 *
 * The page previously received a hardcoded `tenantId={1}`, so it displayed —
 * and could mutate — the first customer's billing data no matter which
 * customer the operator meant. Every key below therefore carries the tenant
 * id, and every hook is disabled until a tenant is actually selected, so no
 * request is ever issued without an explicit target.
 *
 * `tenantId: number | null` is the contract: null means "nothing selected
 * yet", which must never be coerced to 1, to the first organization, or to a
 * previously cached tenant.
 */
export const billingKeys = {
  // Scoped root used to evict EVERY billing entry for a tenant on switch.
  all: ["platform-billing"] as const,
  balance: (tenantId: number) => ["platform-billing", "balance", tenantId] as const,
  adjustments: (tenantId: number) => ["platform-billing", "adjustments", tenantId] as const,
  reconciliation: (tenantId: number) => ["platform-billing", "reconciliation", tenantId] as const,
};

export function useBalance(tenantId: number | null) {
  return useQuery({
    queryKey: billingKeys.balance(tenantId ?? 0),
    queryFn: () => getBalance(tenantId as number),
    // No tenant selected → no request. This is what stops the page from
    // silently loading some default customer's ledger.
    enabled: tenantId !== null,
  });
}

export function useAdjustments(tenantId: number | null, limit?: number) {
  return useQuery({
    queryKey: billingKeys.adjustments(tenantId ?? 0),
    queryFn: () => getAdjustments(tenantId as number, limit),
    enabled: tenantId !== null,
  });
}

export function useReconciliation(tenantId: number | null) {
  return useQuery({
    queryKey: billingKeys.reconciliation(tenantId ?? 0),
    queryFn: () => getReconciliation(tenantId as number),
    enabled: tenantId !== null,
  });
}

export function useCreateAdjustment(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { data: CreateAdjustmentRequest; idempotencyKey: string }) => {
      // A mutation without an explicit target must never be attempted. The
      // backend rejects it too, but failing here keeps a mis-wired caller
      // from issuing a request whose target it could not name.
      if (tenantId === null) {
        return Promise.reject(new Error("Select a tenant before applying an adjustment."));
      }
      return createAdjustment(tenantId, args.data, args.idempotencyKey);
    },
    onSuccess: () => {
      if (tenantId === null) return;
      qc.invalidateQueries({ queryKey: billingKeys.balance(tenantId) });
      qc.invalidateQueries({ queryKey: billingKeys.adjustments(tenantId) });
      qc.invalidateQueries({ queryKey: billingKeys.reconciliation(tenantId) });
    },
  });
}
