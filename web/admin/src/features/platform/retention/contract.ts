// Exact contracts for the platform retention endpoints
// (internal/api/handlers/retention_admin.go + internal/platform/retention/domain.go).

export type PolicyLevel = "platform" | "tenant" | "domain" | "mailbox";

export const POLICY_LEVELS: ReadonlyArray<PolicyLevel> = ["platform", "tenant", "domain", "mailbox"];

export interface Policy {
  id: number;
  level: PolicyLevel;
  tenant_id?: number;
  domain_id?: number;
  mailbox_id?: number;
  category?: string;
  retention_days: number;
  recovery_days: number;
  archive_eligible: boolean;
  created_at: string;
  updated_at: string;
}

export interface LegalHold {
  id: number;
  scope_kind: string;
  scope_id: number;
  case_ref: string;
  reason: string;
  actor_id: number;
  started_at: string;
  ends_at?: string;
  released: boolean;
  created_at: string;
}

export interface ListHoldsResponse {
  holds: LegalHold[];
}

export interface PurgePlan {
  scope_kind: string;
  scope_id: number;
  eligible_count: number;
  held_count: number;
  generated_at: string;
}

export interface ChainOfCustodyEvent {
  id: number;
  operation: string;
  scope_kind: string;
  scope_id: number;
  actor_id: number;
  content_hash?: string;
  record_count: number;
  created_at: string;
}

export interface ListCustodyResponse {
  events: ChainOfCustodyEvent[];
}

export const PURGE_CONFIRMATION_PHRASE = "PURGE-ELIGIBLE-DATA";

export interface PurgeExecuteRequest {
  scope_kind: string;
  scope_id: number;
  older_than: string;
  confirmation: string;
  idempotency_key: string;
}
