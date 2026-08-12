import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createAdjustment, getAdjustments, getBalance, getReconciliation } from "./api";
import type { CreateAdjustmentRequest } from "./contract";

export const billingKeys = {
  balance: (tenantId: number) => ["platform-billing", "balance", tenantId] as const,
  adjustments: (tenantId: number) => ["platform-billing", "adjustments", tenantId] as const,
  reconciliation: (tenantId: number) => ["platform-billing", "reconciliation", tenantId] as const,
};

export function useBalance(tenantId: number) {
  return useQuery({ queryKey: billingKeys.balance(tenantId), queryFn: () => getBalance(tenantId) });
}

export function useAdjustments(tenantId: number, limit?: number) {
  return useQuery({
    queryKey: billingKeys.adjustments(tenantId),
    queryFn: () => getAdjustments(tenantId, limit),
  });
}

export function useReconciliation(tenantId: number) {
  return useQuery({
    queryKey: billingKeys.reconciliation(tenantId),
    queryFn: () => getReconciliation(tenantId),
  });
}

export function useCreateAdjustment(tenantId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { data: CreateAdjustmentRequest; idempotencyKey: string }) =>
      createAdjustment(tenantId, args.data, args.idempotencyKey),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: billingKeys.balance(tenantId) });
      qc.invalidateQueries({ queryKey: billingKeys.adjustments(tenantId) });
      qc.invalidateQueries({ queryKey: billingKeys.reconciliation(tenantId) });
    },
  });
}
