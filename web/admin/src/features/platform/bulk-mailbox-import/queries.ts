import { useQuery } from "@tanstack/react-query";
import { getBulkMailboxJob, getBulkMailboxJobRows, listBulkMailboxJobs } from "./api";
import { BULK_TERMINAL_STATUSES } from "./contract";

export const bulkImportKeys = {
  jobs: (tenantId: number | null, page: number, pageSize: number) =>
    ["platform-bulk-mailbox-import", "jobs", tenantId ?? "none", page, pageSize] as const,
  job: (tenantId: number | null, jobId: number | null) =>
    ["platform-bulk-mailbox-import", "job", tenantId ?? "none", jobId ?? 0] as const,
  rows: (tenantId: number | null, jobId: number | null, page: number, pageSize: number) =>
    ["platform-bulk-mailbox-import", "rows", tenantId ?? "none", jobId ?? 0, page, pageSize] as const,
};

export function useBulkMailboxJobs(tenantId: number | null, page: number, pageSize: number) {
  return useQuery({
    queryKey: bulkImportKeys.jobs(tenantId, page, pageSize),
    queryFn: () => listBulkMailboxJobs(tenantId as number, pageSize, page * pageSize),
    enabled: tenantId !== null,
    retry: false,
  });
}

/**
 * Polls a job only while it is non-terminal, at a bounded interval —
 * stops automatically once the job reaches a terminal status (or the
 * component unmounts, per React Query's own lifecycle).
 */
export function useBulkMailboxJob(tenantId: number | null, jobId: number | null) {
  return useQuery({
    queryKey: bulkImportKeys.job(tenantId, jobId),
    queryFn: () => getBulkMailboxJob(tenantId as number, jobId as number),
    enabled: tenantId !== null && jobId !== null,
    retry: false,
    refetchInterval: (query) => {
      const status = query.state.data?.job.status;
      if (!status || BULK_TERMINAL_STATUSES.has(status)) return false;
      return 3000;
    },
  });
}

export function useBulkMailboxJobRows(tenantId: number | null, jobId: number | null, page: number, pageSize: number) {
  return useQuery({
    queryKey: bulkImportKeys.rows(tenantId, jobId, page, pageSize),
    queryFn: () => getBulkMailboxJobRows(tenantId as number, jobId as number, pageSize, page * pageSize),
    enabled: tenantId !== null && jobId !== null,
    retry: false,
  });
}
