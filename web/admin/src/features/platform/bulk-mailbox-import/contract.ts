// Wire contract for the durable CSV/XLSX bulk mailbox IMPORT/provisioning
// workflow — internal/platform/bulkprovision + internal/api/handlers/
// platform_bulk_mailboxes.go. This is a DIFFERENT capability from
// features/platform/bulk-mailboxes (POST /platform/mailboxes/:tenant_id/
// bulk/status, a bulk STATUS CHANGE on already-existing mailboxes) — do
// not merge the two modules.
//
// Routes (all platformMW-gated, explicit :tenant_id path param except the
// template download, which is not tenant-scoped):
//   GET  /platform/mailboxes/bulk/template?format=csv|xlsx
//   POST /platform/mailboxes/bulk/:tenant_id/stage        (multipart "file", Idempotency-Key)
//   POST /platform/mailboxes/bulk/:tenant_id/validate
//   POST /platform/mailboxes/bulk/:tenant_id/jobs         (Idempotency-Key)
//   POST /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/execute (Idempotency-Key)
//   GET  /platform/mailboxes/bulk/:tenant_id/jobs
//   GET  /platform/mailboxes/bulk/:tenant_id/jobs/:jobId
//   GET  /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/rows
//   POST /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/cancel
//   POST /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/retry

export type BulkUploadFormat = "csv" | "xlsx";

/** Opaque staging handle. Never a filesystem path — staging_id/source_hash are server-generated identifiers. */
export interface BulkStageResult {
  staging_id: string;
  source_hash: string;
  row_count: number;
  format: string;
}

export type BulkAccessMode = "inherit" | "internal_only" | "internal_external";

export type BulkRowStatus = "pending" | "valid" | "invalid" | "created" | "failed" | "skipped";

export type BulkRowErrorCode =
  | "invalid_email"
  | "duplicate_in_file"
  | "duplicate_in_database"
  | "domain_not_owned"
  | "domain_unavailable"
  | "quota_exceeded"
  | "access_mode_incompatible"
  | "missing_field"
  | "create_failed";

export interface BulkRow {
  id: number;
  job_id: number;
  row_number: number;
  external_ref?: string;
  email: string;
  name?: string;
  quota_mb?: number;
  access_mode?: BulkAccessMode;
  status: BulkRowStatus;
  error_code?: BulkRowErrorCode;
  error_detail?: string;
  mailbox_id?: number;
  created_at: string;
  updated_at: string;
}

export interface BulkValidationResult {
  total_rows: number;
  valid_rows: number;
  invalid_rows: number;
  rows: BulkRow[];
  capacity_remaining: number;
  source_hash: string;
  schema_version: number;
}

export type BulkStrategy = "atomic" | "partial";
export type BulkConflictPolicy = "fail" | "skip_existing";

export const BULK_STRATEGY_OPTIONS: ReadonlyArray<{ value: BulkStrategy; label: string; description: string }> = [
  { value: "partial", label: "Partial (continue on row failure)", description: "Rows that fail are skipped; every other valid row is still created." },
  { value: "atomic", label: "Atomic (all or nothing)", description: "If any row fails, no mailboxes from this job are created." },
];

export const BULK_CONFLICT_POLICY_OPTIONS: ReadonlyArray<{ value: BulkConflictPolicy; label: string; description: string }> = [
  { value: "fail", label: "Fail on existing mailbox", description: "A row whose email already exists is treated as a row failure." },
  { value: "skip_existing", label: "Skip existing mailbox", description: "A row whose email already exists is left completely untouched and marked skipped." },
];

export type BulkJobStatus = "queued" | "validating" | "ready" | "running" | "completed" | "partially_failed" | "failed" | "cancelled";

export const BULK_TERMINAL_STATUSES: ReadonlySet<BulkJobStatus> = new Set(["completed", "partially_failed", "failed", "cancelled"]);

export interface BulkJob {
  id: number;
  tenant_id: number;
  domain_id: number;
  status: BulkJobStatus;
  strategy: BulkStrategy;
  conflict_policy: BulkConflictPolicy;
  idempotency_key?: string;
  source_hash?: string;
  schema_version: number;
  total_rows: number;
  valid_rows: number;
  invalid_rows: number;
  created_count: number;
  failed_count: number;
  skipped_count: number;
  next_row_number: number;
  version: number;
  created_by: number;
  created_at: string;
  updated_at: string;
}

export interface BulkValidateRequest {
  staging_id: string;
  source_hash: string;
  format: string;
  domain_id: number;
}

export interface BulkCreateJobRequest {
  staging_id: string;
  source_hash: string;
  format: string;
  domain_id: number;
  strategy: BulkStrategy;
  conflict_policy: BulkConflictPolicy;
}

export interface BulkJobRowsPage {
  rows: BulkRow[];
  total: number;
  limit: number;
  offset: number;
}

export interface BulkJobsPage {
  jobs: BulkJob[];
  total: number;
  limit: number;
  offset: number;
}
