import { useQuery } from "@tanstack/react-query";
import { getAuditEntry, listAuditLogs } from "./api";

export const auditKeys = {
  list: (params: string) => ["audit", "list", params] as const,
  detail: (id: number) => ["audit", "detail", id] as const,
};

export function useAuditLogs(params: { page?: number; page_size?: number; action?: string; tenant_id?: number; result?: string }) {
  const key = JSON.stringify(params);
  return useQuery({ queryKey: auditKeys.list(key), queryFn: () => listAuditLogs(params) });
}

export function useAuditEntry(id: number) {
  return useQuery({ queryKey: auditKeys.detail(id), queryFn: () => getAuditEntry(id) });
}
