import { useMutation, useQueryClient } from "@tanstack/react-query";
import { deletePlatformMailbox, resetPlatformMailboxPassword, setPlatformMailboxQuota, setPlatformMailboxStatus } from "./api";

export function mailboxInvalidationKeys() {
  return [
    ["platform-mailboxes"],
    ["platform-domains"],
    ["overview"],
    ["platform-audit"],
    ["platform-usage"],
  ] as const;
}

export function useSetMailboxStatusMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, status, reason }: { id: number; status: string; reason?: string }) =>
      setPlatformMailboxStatus(tenantId as number, id, { status, reason: reason ?? "" }),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useSetMailboxQuotaMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, quotaMb }: { id: number; quotaMb: number }) =>
      setPlatformMailboxQuota(tenantId as number, id, { quota_mb: quotaMb }),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useResetMailboxPasswordMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => resetPlatformMailboxPassword(tenantId as number, id),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useDeleteMailboxMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, confirmation }: { id: number; confirmation: string }) =>
      deletePlatformMailbox(tenantId as number, id, confirmation),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}
