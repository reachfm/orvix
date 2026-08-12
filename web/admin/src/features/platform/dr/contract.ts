// Exact contracts for the platform DR endpoints
// (internal/platform/dr/domain.go + internal/api/handlers/dr_admin.go).

export type DrillOutcome = "success" | "failure" | "partial";

export interface Drill {
  id: number;
  backup_id: string;
  outcome: DrillOutcome;
  duration_ms: number;
  failure_reason?: string;
  actor_id: number;
  started_at: string;
}

export interface Operation {
  id: number;
  type: "backup" | "restore";
  ref_id: string;
  status: string;
  idempotency_key?: string;
  actor_id: number;
  created_at: string;
}

export interface Readiness {
  last_verified_backup_at?: string;
  rpo_gap?: string;
  last_successful_drill_at?: string;
  last_drill_duration_ms?: number;
  missing_backup_alert: boolean;
}

export interface ListDrillsResponse {
  drills: Drill[];
}

export interface ListOperationsResponse {
  operations: Operation[];
  total: number;
  limit: number;
  offset: number;
}

export interface RecordDrillRequest {
  backup_id: string;
  outcome: DrillOutcome;
  duration_ms: number;
  failure_reason?: string;
}

export const DRILL_OUTCOMES: ReadonlyArray<DrillOutcome> = ["success", "failure", "partial"];
