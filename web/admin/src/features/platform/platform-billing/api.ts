import { request } from "../../../api";
import type {
  Adjustment,
  Balance,
  CreateAdjustmentRequest,
  ListAdjustmentsResponse,
  ReconciliationReport,
} from "./contract";

export function getBalance(tenantId: number): Promise<Balance> {
  return request<Balance>(`/platform/billing/tenants/${tenantId}/balance`);
}

export function getAdjustments(tenantId: number, limit?: number): Promise<ListAdjustmentsResponse> {
  const qs = limit ? `?limit=${limit}` : "";
  return request<ListAdjustmentsResponse>(`/platform/billing/tenants/${tenantId}/adjustments${qs}`);
}

export function createAdjustment(tenantId: number, data: CreateAdjustmentRequest, idempotencyKey: string): Promise<Adjustment> {
  return request<Adjustment>(`/platform/billing/tenants/${tenantId}/adjustments`, {
    method: "POST",
    body: JSON.stringify(data),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function getReconciliation(tenantId: number): Promise<ReconciliationReport> {
  return request<ReconciliationReport>(`/platform/billing/tenants/${tenantId}/reconciliation`);
}
