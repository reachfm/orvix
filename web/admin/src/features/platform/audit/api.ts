import { request } from "../../../api";
import type { AuditEntry, AuditExport, ListAuditResponse } from "./contract";

export function listAuditLogs(params: { page?: number; page_size?: number; action?: string; actor?: string; tenant_id?: number; result?: string }): Promise<ListAuditResponse> {
  const p = new URLSearchParams();
  if (params.page) p.set("page", String(params.page));
  if (params.page_size) p.set("page_size", String(params.page_size));
  if (params.action) p.set("action", params.action);
  if (params.actor) p.set("actor", params.actor);
  if (params.tenant_id) p.set("tenant_id", String(params.tenant_id));
  if (params.result) p.set("result", params.result);
  const qs = p.toString();
  return request<ListAuditResponse>(`/audit/logs${qs ? `?${qs}` : ""}`);
}

export function getAuditEntry(id: number): Promise<AuditEntry> {
  return request<AuditEntry>(`/audit/logs/${id}`);
}

export function exportAuditLogs(params: { action?: string; tenant_id?: number; since?: string; until?: string; format?: "json" | "csv" }): Promise<AuditExport> {
  const p = new URLSearchParams();
  if (params.action) p.set("action", params.action);
  if (params.tenant_id) p.set("tenant_id", String(params.tenant_id));
  if (params.since) p.set("since", params.since);
  if (params.until) p.set("until", params.until);
  if (params.format) p.set("format", params.format);
  const qs = p.toString();
  return request<AuditExport>(`/audit/logs/export${qs ? `?${qs}` : ""}`);
}
