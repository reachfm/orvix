import { useMutation, useQueryClient } from "@tanstack/react-query";
import { bulkMailboxStatus } from "./api";
import type { BulkMailboxAction, BulkMailboxRequest } from "./contract";

export function useBulkMailboxStatusMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ ids, action, reason, domainId }: { ids: number[]; action: BulkMailboxAction; reason?: string; domainId?: number }) => {
      const body: BulkMailboxRequest = { ids, action, reason: reason ?? "" };
      return bulkMailboxStatus(tenantId as number, body);
    },
    onSuccess: () => {
      // The bulk response is a per-row result envelope, not updated
      // rows — invalidate list/detail/usage so the new states refetch.
      qc.invalidateQueries({ queryKey: ["platform-mailboxes"] });
      qc.invalidateQueries({ queryKey: ["platform-domains"] });
      qc.invalidateQueries({ queryKey: ["overview"] });
    },
  });
}
