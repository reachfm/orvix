import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  cancelImport,
  compensateImport,
  createImport,
  executeImport,
  getImport,
  getImportReport,
  listImports,
  resumeImport,
  validateImport,
} from "./api";
import type { ConflictPolicy } from "./contract";

export const importKeys = {
  list: (page: number, status?: string) => ["platform-imports", "list", page, status ?? ""] as const,
  detail: (id: number) => ["platform-imports", "detail", id] as const,
  report: (id: number) => ["platform-imports", "report", id] as const,
};

export function useImports(page = 1, pageSize = 25, status?: string) {
  return useQuery({
    queryKey: importKeys.list(page, status),
    queryFn: () => listImports(page, pageSize, status),
  });
}

export function useImportDetail(id: number) {
  return useQuery({ queryKey: importKeys.detail(id), queryFn: () => getImport(id) });
}

export function useImportReport(id: number) {
  return useQuery({ queryKey: importKeys.report(id), queryFn: () => getImportReport(id) });
}

function useInvalidate(id: number) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: importKeys.detail(id) });
    qc.invalidateQueries({ queryKey: importKeys.report(id) });
    qc.invalidateQueries({ queryKey: ["platform-imports", "list"] });
  };
}

export function useCreateImport() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { body: Blob | ArrayBuffer | string; sourceName: string; policy: ConflictPolicy }) =>
      createImport(args.body, args.sourceName, args.policy),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["platform-imports", "list"] }),
  });
}

export function useValidateImport(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({ mutationFn: () => validateImport(id), onSuccess: invalidate });
}

export function useExecuteImport(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({
    mutationFn: (args: { idempotencyKey: string; confirmation: string }) =>
      executeImport(id, args.idempotencyKey, args.confirmation),
    onSuccess: invalidate,
  });
}

export function useResumeImport(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({
    mutationFn: (idempotencyKey: string) => resumeImport(id, idempotencyKey),
    onSuccess: invalidate,
  });
}

export function useCancelImport(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({ mutationFn: () => cancelImport(id), onSuccess: invalidate });
}

export function useCompensateImport(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({
    mutationFn: (args: { idempotencyKey: string; confirmation: string }) =>
      compensateImport(id, args.idempotencyKey, args.confirmation),
    onSuccess: invalidate,
  });
}
