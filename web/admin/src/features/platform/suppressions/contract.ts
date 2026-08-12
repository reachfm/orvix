// Exact contracts for the Platform suppression routes
// (internal/api/router.go: /platform/suppressions/:tenant_id*).
//
// Wire shapes match the Go structs field-for-field:
//   - Suppression / SuppressionEvent: internal/platform/deliverability/domain.go
//   - list envelope: internal/api/handlers/platform_mail_control.go
//
// Lifecycle semantics: a suppression is ACTIVE, RELEASED (operator
// action, history retained) or EXPIRED (scheduler reconciliation).
// DELETE /:id is release-with-confirmation — history is preserved, so
// the UI must not describe it as destructive deletion.

export type SuppressionReason = "hard_bounce" | "complaint" | "manual";
export type SuppressionState = "active" | "released" | "expired";

export interface Suppression {
  id: number;
  tenant_id: number;
  /** normalized lowercase address */
  address: string;
  reason: SuppressionReason;
  source: string;
  actor_id?: number;
  notes?: string;
  expires_at?: string | null;
  state: SuppressionState;
  released_at?: string | null;
  released_by?: number;
  released_reason?: string;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface SuppressionEvent {
  id: number;
  suppression_id: number;
  tenant_id: number;
  event: string;
  actor_id?: number;
  reason?: string;
  at: string;
}

export interface ListSuppressionsResponse {
  suppressions: Suppression[];
  total: number;
  limit: number;
  offset: number;
}

export interface SuppressionFilter {
  domain?: string;
  q?: string;
  reason?: SuppressionReason;
  source?: string;
  state?: SuppressionState;
  created_from?: string;
  created_to?: string;
  expiry_from?: string;
  expiry_to?: string;
  limit?: number;
  offset?: number;
}

export interface AddSuppressionRequest {
  address: string;
  reason: SuppressionReason;
  source?: string;
  notes?: string;
  expires_at?: string | null;
}

export interface ReleaseSuppressionRequest {
  reason: string;
}

export interface ReleaseSuppressionResponse {
  status: "ok";
  id: number;
  state: "released";
}

export interface ReactivateSuppressionRequest {
  reason: SuppressionReason;
  source?: string;
  notes?: string;
  expires_at?: string | null;
}

export interface ReactivateSuppressionResponse {
  status: "ok";
  id: number;
  state: "active";
}

export interface SuppressionHistoryResponse {
  suppression_id: number;
  events: SuppressionEvent[];
}

export interface DeleteSuppressionResponse {
  status: "ok";
  id: number;
}

/** Typed confirmation for release-with-confirmation (history retained). */
export function suppressionReleaseConfirmation(id: number): string {
  return `RELEASE-SUPPRESSION-${id}`;
}

export const SUPPRESSION_REASONS: ReadonlyArray<SuppressionReason> = ["hard_bounce", "complaint", "manual"];
export const SUPPRESSION_STATES: ReadonlyArray<SuppressionState> = ["active", "released", "expired"];
