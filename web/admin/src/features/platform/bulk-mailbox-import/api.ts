import { request } from "../../../api";
import type {
  BulkCreateJobRequest,
  BulkJob,
  BulkJobRowsPage,
  BulkJobsPage,
  BulkStageResult,
  BulkUploadFormat,
  BulkValidateRequest,
  BulkValidationResult,
} from "./contract";

/** GET /platform/mailboxes/bulk/template?format=csv|xlsx — NOT tenant-scoped. */
export function downloadBulkMailboxTemplate(format: BulkUploadFormat): Promise<Blob> {
  return request<Blob>(`/platform/mailboxes/bulk/template?format=${format}`, {
    method: "GET",
    responseType: "blob",
  });
}

/** POST /platform/mailboxes/bulk/:tenant_id/stage — multipart field "file". */
export function stageBulkMailboxUpload(tenantId: number, file: File, idempotencyKey: string): Promise<BulkStageResult> {
  const formData = new FormData();
  formData.append("file", file);
  return request<BulkStageResult>(`/platform/mailboxes/bulk/${tenantId}/stage`, {
    method: "POST",
    body: formData,
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function validateBulkMailboxUpload(tenantId: number, body: BulkValidateRequest): Promise<BulkValidationResult> {
  return request<BulkValidationResult>(`/platform/mailboxes/bulk/${tenantId}/validate`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function createBulkMailboxJob(tenantId: number, body: BulkCreateJobRequest, idempotencyKey: string): Promise<{ job: BulkJob }> {
  return request<{ job: BulkJob }>(`/platform/mailboxes/bulk/${tenantId}/jobs`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function executeBulkMailboxJob(tenantId: number, jobId: number, idempotencyKey: string): Promise<{ automation_job: unknown; import_job_id: number }> {
  return request(`/platform/mailboxes/bulk/${tenantId}/jobs/${jobId}/execute`, {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function listBulkMailboxJobs(tenantId: number, limit: number, offset: number): Promise<BulkJobsPage> {
  return request<BulkJobsPage>(`/platform/mailboxes/bulk/${tenantId}/jobs?limit=${limit}&offset=${offset}`);
}

export function getBulkMailboxJob(tenantId: number, jobId: number): Promise<{ job: BulkJob }> {
  return request<{ job: BulkJob }>(`/platform/mailboxes/bulk/${tenantId}/jobs/${jobId}`);
}

export function getBulkMailboxJobRows(tenantId: number, jobId: number, limit: number, offset: number): Promise<BulkJobRowsPage> {
  return request<BulkJobRowsPage>(`/platform/mailboxes/bulk/${tenantId}/jobs/${jobId}/rows?limit=${limit}&offset=${offset}`);
}

export function cancelBulkMailboxJob(tenantId: number, jobId: number): Promise<{ job: BulkJob }> {
  return request<{ job: BulkJob }>(`/platform/mailboxes/bulk/${tenantId}/jobs/${jobId}/cancel`, { method: "POST" });
}

export function retryBulkMailboxJob(tenantId: number, jobId: number): Promise<{ job: BulkJob; rows: unknown[] }> {
  return request(`/platform/mailboxes/bulk/${tenantId}/jobs/${jobId}/retry`, { method: "POST" });
}
