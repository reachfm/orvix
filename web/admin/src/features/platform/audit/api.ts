import { request } from "../../../api";
import type { AuditEntry, AuditExportFormat, ListAuditResponse } from "./contract";

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

/**
 * Streams the filtered audit log as a downloadable file. The endpoint
 * answers text/csv (with Content-Disposition) or application/json —
 * NOT a JSON envelope — so this client requests a raw Blob through the
 * shared CSRF/auth-aware transport and the caller triggers a download.
 * The blob path is the documented exception where the shared client's
 * raw-response mode is required (file download).
 */
export function exportAuditLogs(params: { action?: string; tenant_id?: number; since?: string; until?: string; format: AuditExportFormat }): Promise<Blob> {
  const p = new URLSearchParams();
  if (params.action) p.set("action", params.action);
  if (params.tenant_id) p.set("tenant_id", String(params.tenant_id));
  if (params.since) p.set("since", params.since);
  if (params.until) p.set("until", params.until);
  p.set("format", params.format);
  const qs = p.toString();
  return request<Blob>(`/audit/logs/export${qs ? `?${qs}` : ""}`, { responseType: "blob" });
}

/** Triggers a browser download of the streamed export blob. */
export function downloadAuditExport(blob: Blob, format: AuditExportFormat): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = format === "csv" ? "audit.csv" : "audit.json";
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
