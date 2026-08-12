import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cancelJob, getJob, listJobs, retryJob, submitJob } from "./api";
import type { SubmitJobRequest } from "./contract";

export const jobKeys = {
  list: (page: number, status?: string, type?: string) => ["platform-jobs", "list", page, status ?? "", type ?? ""] as const,
  detail: (id: number) => ["platform-jobs", "detail", id] as const,
};

export function useJobs(page = 1, pageSize = 25, status?: string, type?: string) {
  return useQuery({
    queryKey: jobKeys.list(page, status, type),
    queryFn: () => listJobs(page, pageSize, status, type),
  });
}

export function useJobDetail(id: number) {
  return useQuery({ queryKey: jobKeys.detail(id), queryFn: () => getJob(id) });
}

function useInvalidate(id?: number) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["platform-jobs", "list"] });
    if (id) qc.invalidateQueries({ queryKey: jobKeys.detail(id) });
  };
}

export function useSubmitJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { data: SubmitJobRequest; idempotencyKey: string }) => submitJob(args.data, args.idempotencyKey),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["platform-jobs", "list"] }),
  });
}

export function useCancelJob(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({ mutationFn: () => cancelJob(id), onSuccess: invalidate });
}

export function useRetryJob(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({ mutationFn: () => retryJob(id), onSuccess: invalidate });
}
