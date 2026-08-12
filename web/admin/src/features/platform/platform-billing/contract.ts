// Exact request/response contracts for the platform-owned billing
// endpoints (internal/api/handlers/platform_billing_admin.go and
// internal/platform/billing/domain.go), matching the Go structs
// field-for-field.

export type AdjustmentType = "credit" | "debit";

export interface Adjustment {
  id: number;
  tenant_id: number;
  type: AdjustmentType;
  amount_cents: number;
  currency: string;
  reason: string;
  actor_id: number;
  idempotency_key?: string;
  created_at: string;
}

export interface Balance {
  tenant_id: number;
  currency: string;
  balance_cents: number;
  version: number;
  updated_at: string;
}

export interface ReconciliationReport {
  tenant_id: number;
  currency: string;
  stored_balance_cents: number;
  recomputed_balance_cents: number;
  total_credits_cents: number;
  total_debits_cents: number;
  discrepancy_cents: number;
  discrepant: boolean;
  generated_at: string;
}

export interface ListAdjustmentsResponse {
  adjustments: Adjustment[];
}

export interface CreateAdjustmentRequest {
  type: AdjustmentType;
  amount_cents: number;
  currency: string;
  reason: string;
}

export const ADJUSTMENT_TYPES: ReadonlyArray<AdjustmentType> = ["credit", "debit"];
