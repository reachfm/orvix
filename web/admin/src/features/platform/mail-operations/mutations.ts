import { useMutation, useQueryClient } from "@tanstack/react-query";
import { bounceQueueMessage, bulkQueueAction, cancelQueueMessage, retryQueueMessage } from "./api";
import { bounceQueueConfirmation, cancelQueueConfirmation, type BulkQueueAction } from "./contract";

// React Query's useMutation already serializes concurrent .mutate()
// calls against isPending — every caller (QueueTable's row buttons,
// BulkQueueActionPanel) disables its trigger while isPending is true,
// protecting retry/bounce/cancel against duplicate submission.
// Bounce/cancel always carry the typed X-Confirm header the backend
// requires (queueActionConfirm).

export function useRetryQueueMessageMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => retryQueueMessage(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["queue-messages"] });
      qc.invalidateQueries({ queryKey: ["queue-summary"] });
      qc.invalidateQueries({ queryKey: ["queue-detail"] });
    },
  });
}

export function useBounceQueueMessageMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, reason }: { id: number; reason?: string }) =>
      bounceQueueMessage(id, reason, bounceQueueConfirmation(id)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["queue-messages"] });
      qc.invalidateQueries({ queryKey: ["queue-summary"] });
      qc.invalidateQueries({ queryKey: ["queue-detail"] });
    },
  });
}

export function useCancelQueueMessageMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => cancelQueueMessage(id, cancelQueueConfirmation(id)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["queue-messages"] });
      qc.invalidateQueries({ queryKey: ["queue-summary"] });
      qc.invalidateQueries({ queryKey: ["queue-detail"] });
    },
  });
}

export function useBulkQueueActionMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ ids, action, reason }: { ids: number[]; action: BulkQueueAction; reason?: string }) =>
      bulkQueueAction(ids, action, reason),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["queue-messages"] });
      qc.invalidateQueries({ queryKey: ["queue-summary"] });
      qc.invalidateQueries({ queryKey: ["queue-detail"] });
    },
  });
}
