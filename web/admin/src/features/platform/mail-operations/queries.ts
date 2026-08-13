import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../../../api";
import { getQueueDetail, getQueueSummary, listQueueMessages } from "./api";
import type { QueueMessageFilter } from "./contract";

export function isCoreMailDisabled(err: unknown): boolean {
  return err instanceof ApiError && err.code === "COREMAIL_DISABLED";
}

export function useQueueSummaryQuery() {
  return useQuery({ queryKey: ["queue-summary"], queryFn: getQueueSummary, retry: false });
}

export function useQueueMessagesQuery(filter: QueueMessageFilter) {
  return useQuery({
    queryKey: ["queue-messages", filter.status, filter.from, filter.to, filter.limit, filter.offset],
    queryFn: () => listQueueMessages(filter),
    retry: false,
  });
}

export function useQueueDetailQuery(id: number | null) {
  return useQuery({
    queryKey: ["queue-message", id],
    queryFn: () => getQueueDetail(id as number),
    enabled: id !== null,
    retry: false,
  });
}
