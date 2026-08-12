import { request } from "../../../api";
import type { ChainOfCustodyEvent, LegalHold, ListCustodyResponse, ListHoldsResponse, Policy, PurgeExecuteRequest, PurgePlan } from "./contract";

export function createPolicy(data: Partial<Policy>): Promise<Policy> {
  return request<Policy>("/retention/policies", { method: "POST", body: JSON.stringify(data) });
}

export function getEffectivePolicy(scope: { tenant_id?: number; domain_id?: number; mailbox_id?: number; category?: string }): Promise<Policy> {
  const params = new URLSearchParams();
  if (scope.tenant_id) params.set("tenant_id", String(scope.tenant_id));
  if (scope.domain_id) params.set("domain_id", String(scope.domain_id));
  if (scope.mailbox_id) params.set("mailbox_id", String(scope.mailbox_id));
  if (scope.category) params.set("category", scope.category);
  const qs = params.toString();
  return request<Policy>(`/retention/policies/effective${qs ? `?${qs}` : ""}`);
}

export function listLegalHolds(scopeKind: string, scopeId: number): Promise<ListHoldsResponse> {
  return request<ListHoldsResponse>(`/retention/legal-holds?scope_kind=${scopeKind}&scope_id=${scopeId}`);
}

export function placeLegalHold(data: { scope_kind: string; scope_id: number; case_ref?: string; reason: string; ends_at?: string }): Promise<LegalHold> {
  return request<LegalHold>("/retention/legal-holds", { method: "POST", body: JSON.stringify(data) });
}

export function releaseLegalHold(id: number): Promise<{ status: string }> {
  return request<{ status: string }>(`/retention/legal-holds/${id}/release`, { method: "POST" });
}

export function planPurge(scopeKind: string, scopeId: number, olderThan: string): Promise<PurgePlan> {
  return request<PurgePlan>("/retention/purge/plan", { method: "POST", body: JSON.stringify({ scope_kind: scopeKind, scope_id: scopeId, older_than: olderThan }) });
}

export function executePurge(req: PurgeExecuteRequest): Promise<{ purged: number }> {
  return request<{ purged: number }>("/retention/purge/execute", {
    method: "POST",
    body: JSON.stringify({ scope_kind: req.scope_kind, scope_id: req.scope_id, older_than: req.older_than, confirmation: req.confirmation }),
    headers: { "Idempotency-Key": req.idempotency_key },
  });
}

export function listCustodyEvents(scopeKind: string, scopeId: number): Promise<ListCustodyResponse> {
  return request<ListCustodyResponse>(`/retention/custody?scope_kind=${scopeKind}&scope_id=${scopeId}`);
}
