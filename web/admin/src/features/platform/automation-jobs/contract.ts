// Exact contracts for the platform automation jobs endpoints
// (internal/api/handlers/automation_jobs.go + internal/platform/jobs/domain.go).
// Payload, lease owner/token, and lease internals are never serialized.

export type JobStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";
export type JobScope = "tenant" | "platform";

export interface Job {
  id: number;
  tenant_id?: number;
  scope: JobScope;
  actor: string;
  type: string;
  payload_version: number;
  status: JobStatus;
  progress: number;
  attempt_count: number;
  max_attempts: number;
  run_after: string;
  cancellation_requested_at?: string;
  created_at: string;
  started_at?: string;
}

export interface ListJobsResponse {
  jobs: Job[];
  total: number;
  page: number;
  page_size: number;
}

export interface JobDetailResponse {
  job: Job;
}

export interface SubmitJobRequest {
  type: string;
  payload: Record<string, unknown>;
  max_attempts?: number;
  run_after?: string;
}

export interface SubmitJobResponse {
  job: Job;
  idempotent_replay: boolean;
}

export const JOB_STATUSES: ReadonlyArray<JobStatus> = ["queued", "running", "succeeded", "failed", "cancelled"];
