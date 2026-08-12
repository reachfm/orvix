import { request } from "../../../api";
import type { Drill, ListDrillsResponse, ListOperationsResponse, Readiness, RecordDrillRequest } from "./contract";

export function getReadiness(): Promise<Readiness> {
  return request<Readiness>("/dr/readiness");
}

export function listDrills(): Promise<ListDrillsResponse> {
  return request<ListDrillsResponse>("/dr/drills");
}

export function listOperations(limit?: number, offset?: number): Promise<ListOperationsResponse> {
  const params = new URLSearchParams();
  if (limit) params.set("limit", String(limit));
  if (offset) params.set("offset", String(offset));
  const qs = params.toString();
  return request<ListOperationsResponse>(`/dr/operations${qs ? `?${qs}` : ""}`);
}

export function recordDrill(data: RecordDrillRequest): Promise<Drill> {
  return request<Drill>("/dr/drills", { method: "POST", body: JSON.stringify(data) });
}

export function coordinatedBackup(idempotencyKey: string): Promise<{ ref_id: string }> {
  return request<{ ref_id: string }>("/dr/backup", { method: "POST", headers: { "Idempotency-Key": idempotencyKey } });
}
