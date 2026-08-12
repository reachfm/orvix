// Exact contracts for the platform import endpoints
// (internal/api/handlers/import_handlers.go + importer domain types).

export type ImportStatus =
  | "uploaded"
  | "validating"
  | "validated"
  | "running"
  | "completed"
  | "validation_failed"
  | "paused"
  | "failed"
  | "cancelled"
  | "compensating"
  | "compensated"
  | "compensation_failed";

export type ConflictPolicy = "fail" | "skip" | "update_safe_fields";

export interface ImportJob {
  id: number;
  tenant_id: number;
  scope: string;
  actor: string;
  source_type: "csv" | "json";
  conflict_policy: ConflictPolicy;
  schema_version: number;
  status: ImportStatus;
  source_hash: string;
  source_name: string;
  total_rows: number;
  processed_rows: number;
  succeeded_rows: number;
  skipped_rows: number;
  failed_rows: number;
  current_checkpoint: number;
  checkpoint_entity: string;
  checkpoint_row: number;
  last_error?: string;
  job_id?: number;
  created_at: string;
  updated_at: string;
  version: number;
}

export interface ListImportsResponse {
  jobs: ImportJob[];
  total: number;
  page: number;
  page_size: number;
}

export interface CreateImportResponse {
  id: number;
  status: ImportStatus;
  source_type: "csv" | "json";
  source_hash: string;
}

export interface RowValidationError {
  field?: string;
  code: string;
  message: string;
}

export interface ImportRow {
  line: number;
  entity: string;
  row_key: string;
  data?: Record<string, unknown>;
  errors?: RowValidationError[];
  status: string;
  before?: Record<string, unknown>;
  after?: Record<string, unknown>;
}

export interface ValidationReport {
  import_id: number;
  source_hash: string;
  schema_version: number;
  valid: number;
  invalid: number;
  conflict: number;
  updated: number;
  unchanged: number;
  deferred: number;
  total: number;
  rows?: ImportRow[];
  generated_at: string;
}

export const CONFLICT_POLICIES: ReadonlyArray<ConflictPolicy> = ["fail", "skip", "update_safe_fields"];

export const IMPORT_STATUSES: ReadonlyArray<ImportStatus> = [
  "uploaded", "validating", "validated", "running", "completed",
  "validation_failed", "paused", "failed", "cancelled",
  "compensating", "compensated", "compensation_failed",
];
