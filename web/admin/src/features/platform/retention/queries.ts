import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createPolicy, executePurge, getEffectivePolicy, listCustodyEvents, listLegalHolds, placeLegalHold, planPurge, releaseLegalHold } from "./api";
import type { PurgeExecuteRequest } from "./contract";

export const retentionKeys = {
  effectivePolicy: (scope: string) => ["retention", "policy", scope] as const,
  holds: (kind: string, id: number) => ["retention", "holds", kind, id] as const,
  custody: (kind: string, id: number) => ["retention", "custody", kind, id] as const,
  purgePlan: (kind: string, id: number) => ["retention", "purge-plan", kind, id] as const,
};

export function useEffectivePolicy(scope: { tenant_id?: number; domain_id?: number; mailbox_id?: number; category?: string }) {
  const key = JSON.stringify(scope);
  return useQuery({ queryKey: retentionKeys.effectivePolicy(key), queryFn: () => getEffectivePolicy(scope), retry: false });
}

export function useLegalHolds(scopeKind: string, scopeId: number) {
  return useQuery({ queryKey: retentionKeys.holds(scopeKind, scopeId), queryFn: () => listLegalHolds(scopeKind, scopeId) });
}

export function useCustodyEvents(scopeKind: string, scopeId: number) {
  return useQuery({ queryKey: retentionKeys.custody(scopeKind, scopeId), queryFn: () => listCustodyEvents(scopeKind, scopeId) });
}

function useInvalidate(kind: string, id: number) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["retention"] });
  };
}

export function useCreatePolicy() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: (data: Partial<import("./contract").Policy>) => createPolicy(data), onSuccess: () => qc.invalidateQueries({ queryKey: ["retention"] }) });
}

export function usePlaceLegalHold(kind: string, id: number) {
  const invalidate = useInvalidate(kind, id);
  return useMutation({
    mutationFn: (data: { scope_kind: string; scope_id: number; case_ref?: string; reason: string; ends_at?: string }) => placeLegalHold(data),
    onSuccess: invalidate,
  });
}

export function useReleaseLegalHold(kind: string, id: number) {
  const invalidate = useInvalidate(kind, id);
  return useMutation({ mutationFn: (holdId: number) => releaseLegalHold(holdId), onSuccess: invalidate });
}

export function usePlanPurge(kind: string, id: number) {
  const invalidate = useInvalidate(kind, id);
  return useMutation({ mutationFn: (olderThan: string) => planPurge(kind, id, olderThan), onSuccess: invalidate });
}

export function useExecutePurge(kind: string, id: number) {
  const invalidate = useInvalidate(kind, id);
  return useMutation({ mutationFn: (req: PurgeExecuteRequest) => executePurge(req), onSuccess: invalidate });
}
