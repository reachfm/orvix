import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  cancelBulkMailboxJob,
  createBulkMailboxJob,
  executeBulkMailboxJob,
  retryBulkMailboxJob,
  stageBulkMailboxUpload,
  validateBulkMailboxUpload,
} from "./api";
import type { BulkCreateJobRequest, BulkValidateRequest } from "./contract";
import { bulkImportKeys } from "./queries";

export function useStageBulkMailboxUpload(tenantId: number | null) {
  return useMutation({
    mutationFn: (args: { file: File; idempotencyKey: string }) =>
      stageBulkMailboxUpload(tenantId as number, args.file, args.idempotencyKey),
  });
}

export function useValidateBulkMailboxUpload(tenantId: number | null) {
  return useMutation({
    mutationFn: (body: BulkValidateRequest) => validateBulkMailboxUpload(tenantId as number, body),
  });
}

export function useCreateBulkMailboxJob(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { body: BulkCreateJobRequest; idempotencyKey: string }) =>
      createBulkMailboxJob(tenantId as number, args.body, args.idempotencyKey),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["platform-bulk-mailbox-import", "jobs"] }),
  });
}

export function useExecuteBulkMailboxJob(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { jobId: number; idempotencyKey: string }) =>
      executeBulkMailboxJob(tenantId as number, args.jobId, args.idempotencyKey),
    onSuccess: (_res, vars) => {
      qc.invalidateQueries({ queryKey: bulkImportKeys.job(tenantId, vars.jobId) });
      qc.invalidateQueries({ queryKey: ["platform-bulk-mailbox-import", "jobs"] });
    },
  });
}

export function useCancelBulkMailboxJob(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (jobId: number) => cancelBulkMailboxJob(tenantId as number, jobId),
    onSuccess: (_res, jobId) => {
      qc.invalidateQueries({ queryKey: bulkImportKeys.job(tenantId, jobId) });
      qc.invalidateQueries({ queryKey: ["platform-bulk-mailbox-import", "jobs"] });
    },
  });
}

export function useRetryBulkMailboxJob(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (jobId: number) => retryBulkMailboxJob(tenantId as number, jobId),
    onSuccess: (_res, jobId) => {
      qc.invalidateQueries({ queryKey: bulkImportKeys.job(tenantId, jobId) });
      qc.invalidateQueries({ queryKey: ["platform-bulk-mailbox-import", "rows"] });
      qc.invalidateQueries({ queryKey: ["platform-bulk-mailbox-import", "jobs"] });
    },
  });
}
