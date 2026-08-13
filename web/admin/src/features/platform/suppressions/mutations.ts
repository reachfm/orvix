import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import {
  addPlatformSuppression,
  deletePlatformSuppression,
  reactivatePlatformSuppression,
  releasePlatformSuppression,
} from "./api";
import type { AddSuppressionRequest, ReactivateSuppressionRequest } from "./contract";

export function suppressionInvalidationKeys() {
  return [
    ["platform-suppressions"],
    ["deliverability"],
    ["mail-operations"],
    ["platform-audit"],
  ] as const;
}

export function invalidateSuppressions(qc: QueryClient) {
  for (const key of suppressionInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
}

export function useAddSuppressionMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: AddSuppressionRequest) => addPlatformSuppression(tenantId as number, body),
    onSuccess: () => invalidateSuppressions(qc),
  });
}

export function useReleaseSuppressionMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, reason }: { id: number; reason: string }) =>
      releasePlatformSuppression(tenantId as number, id, { reason }),
    onSuccess: () => invalidateSuppressions(qc),
  });
}

export function useReactivateSuppressionMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: ReactivateSuppressionRequest }) =>
      reactivatePlatformSuppression(tenantId as number, id, body),
    onSuccess: () => invalidateSuppressions(qc),
  });
}

export function useDeleteSuppressionMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, confirmation }: { id: number; confirmation: string }) =>
      deletePlatformSuppression(tenantId as number, id, confirmation),
    onSuccess: () => invalidateSuppressions(qc),
  });
}
