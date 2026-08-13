import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { activateGrant, createGrant, getGrant, listGrants, revokeGrant } from "./api";
import type { CreateGrantRequest } from "./contract";

export const grantKeys = {
  list: ["platform-support-grants", "list"] as const,
  detail: (id: number) => ["platform-support-grants", "detail", id] as const,
};

export function useGrants() {
  return useQuery({ queryKey: grantKeys.list, queryFn: () => listGrants() });
}

export function useGrantDetail(id: number) {
  return useQuery({ queryKey: grantKeys.detail(id), queryFn: () => getGrant(id) });
}

function useInvalidate(id?: number) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["platform-support-grants", "list"] });
    if (id) qc.invalidateQueries({ queryKey: grantKeys.detail(id) });
  };
}

export function useCreateGrant() {
  const invalidate = useInvalidate();
  return useMutation({ mutationFn: (data: CreateGrantRequest) => createGrant(data), onSuccess: invalidate });
}

export function useActivateGrant(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({ mutationFn: () => activateGrant(id), onSuccess: invalidate });
}

export function useRevokeGrant(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({ mutationFn: (reason?: string) => revokeGrant(id, reason), onSuccess: invalidate });
}
