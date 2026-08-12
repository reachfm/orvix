import { request } from "../../../api";
import type { Job, JobDetailResponse, ListJobsResponse, SubmitJobRequest, SubmitJobResponse } from "./contract";

export function listJobs(page = 1, pageSize = 25, status?: string, type?: string): Promise<ListJobsResponse> {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (status) params.set("status", status);
  if (type) params.set("type", type);
  return request<ListJobsResponse>(`/platform/automation/jobs?${params.toString()}`);
}

export function getJob(id: number): Promise<JobDetailResponse> {
  return request<JobDetailResponse>(`/platform/automation/jobs/${id}`);
}

export function submitJob(data: SubmitJobRequest, idempotencyKey: string): Promise<SubmitJobResponse> {
  return request<SubmitJobResponse>("/platform/automation/jobs", {
    method: "POST",
    body: JSON.stringify(data),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function cancelJob(id: number): Promise<JobDetailResponse> {
  return request<JobDetailResponse>(`/platform/automation/jobs/${id}/cancel`, { method: "POST" });
}

export function retryJob(id: number): Promise<JobDetailResponse> {
  return request<JobDetailResponse>(`/platform/automation/jobs/${id}/retry`, { method: "POST" });
}
