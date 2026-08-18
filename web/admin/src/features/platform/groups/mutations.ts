import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  addPlatformGroupMember,
  createPlatformGroup,
  deletePlatformGroup,
  removePlatformGroupMember,
} from "./api";
import { groupKeys } from "./queries";
import type { CreatePlatformGroupRequest } from "./contract";

export function useCreatePlatformGroupMutation(tenantId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreatePlatformGroupRequest) => createPlatformGroup(tenantId, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: groupKeys.list(tenantId, {}) });
    },
  });
}

export function useDeletePlatformGroupMutation(tenantId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => deletePlatformGroup(tenantId, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: groupKeys.list(tenantId, {}) });
    },
  });
}

export function useAddPlatformGroupMemberMutation(tenantId: number, groupId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (email: string) => addPlatformGroupMember(tenantId, groupId, email),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: groupKeys.members(tenantId, groupId) });
      qc.invalidateQueries({ queryKey: groupKeys.list(tenantId, {}) });
    },
  });
}

export function useRemovePlatformGroupMemberMutation(tenantId: number, groupId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (memberId: string) => removePlatformGroupMember(tenantId, groupId, memberId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: groupKeys.members(tenantId, groupId) });
      qc.invalidateQueries({ queryKey: groupKeys.list(tenantId, {}) });
    },
  });
}
