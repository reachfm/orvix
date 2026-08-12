import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createPlatformAlias, deletePlatformAlias } from "./api";
import type { CreatePlatformAliasRequest } from "./contract";

export function aliasInvalidationKeys() {
  return [
    ["platform-aliases"],
    ["platform-domains"],
    ["platform-audit"],
  ] as const;
}

export function useCreatePlatformAliasMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreatePlatformAliasRequest) => createPlatformAlias(tenantId as number, body),
    onSuccess: () => {
      for (const key of aliasInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useDeletePlatformAliasMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => deletePlatformAlias(tenantId as number, id),
    onSuccess: () => {
      for (const key of aliasInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}
