import { request } from "../../../api";
import type {
  ConflictPolicy,
  CreateImportResponse,
  ImportJob,
  ListImportsResponse,
  ValidationReport,
} from "./contract";

export function listImports(page = 1, pageSize = 25, status?: string): Promise<ListImportsResponse> {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (status) params.set("status", status);
  return request<ListImportsResponse>(`/platform/imports?${params.toString()}`);
}

export function getImport(id: number): Promise<ImportJob> {
  return request<ImportJob>(`/platform/imports/${id}`);
}

export function getImportReport(id: number): Promise<ValidationReport> {
  return request<ValidationReport>(`/platform/imports/${id}/report`);
}

export function createImport(body: Blob | ArrayBuffer | string, sourceName: string, conflictPolicy: ConflictPolicy): Promise<CreateImportResponse> {
  return request<CreateImportResponse>("/platform/imports", {
    method: "POST",
    body: body as BodyInit,
    headers: { "Content-Type": "application/octet-stream", "X-Import-Name": sourceName },
    skipCSRF: true,
  });
}

export function validateImport(id: number): Promise<ValidationReport> {
  return request<ValidationReport>(`/platform/imports/${id}/validate`, { method: "POST" });
}

export function executeImport(id: number, idempotencyKey: string, confirmation: string): Promise<ImportJob> {
  return request<ImportJob>(`/platform/imports/${id}/execute`, {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey, "X-Import-Confirm": confirmation },
  });
}

export function resumeImport(id: number, idempotencyKey: string): Promise<ImportJob> {
  return request<ImportJob>(`/platform/imports/${id}/resume`, {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function cancelImport(id: number): Promise<ImportJob> {
  return request<ImportJob>(`/platform/imports/${id}/cancel`, { method: "POST" });
}

export function compensateImport(id: number, idempotencyKey: string, confirmation: string): Promise<ImportJob> {
  return request<ImportJob>(`/platform/imports/${id}/compensate`, {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey, "X-Import-Confirm": confirmation },
  });
}
