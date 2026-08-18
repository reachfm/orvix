// HTTP transport only — no business logic, no React, no query/mutation
// state (that lives in queries.ts/mutations.ts). Every call goes through
// the shared CSRF/auth-aware client (src/api.ts's request), never fetch()
// directly.
import { request } from "../../../api";
import type {
  CreatePlatformOrganizationRequest,
  CreatePlatformOrganizationResponse,
  ListOrganizationsResponse,
  OrganizationDetail,
  ScheduleOrganizationDeletionRequest,
  ScheduleOrganizationDeletionResponse,
  SetOrganizationActiveRequest,
  SetOrganizationActiveResponse,
  UpdateOrganizationRequest,
  UpdateOrganizationResponse,
} from "./contract";

export function listOrganizations(search?: string, limit?: number, offset?: number): Promise<ListOrganizationsResponse> {
  const params = new URLSearchParams();
  if (search) params.set("search", search);
  if (limit !== undefined) params.set("limit", String(limit));
  if (offset !== undefined) params.set("offset", String(offset));
  const qs = params.toString();
  return request<ListOrganizationsResponse>(`/platform/organizations${qs ? "?" + qs : ""}`);
}

// POST /platform/organizations — the caller MUST supply a stable
// idempotencyKey per submission attempt (reuse it when retrying the
// identical request; generate a fresh one for a new request). The live
// response includes the one-time owner invitation token; an idempotent
// replay returns the stored body WITHOUT the token.
export function createPlatformOrganization(
  body: CreatePlatformOrganizationRequest,
  idempotencyKey: string,
): Promise<CreatePlatformOrganizationResponse> {
  return request<CreatePlatformOrganizationResponse>("/platform/organizations", {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function getOrganizationDetail(id: number): Promise<OrganizationDetail> {
  return request<OrganizationDetail>(`/platform/organizations/${id}/detail`);
}

export function setOrganizationActive(id: number, body: SetOrganizationActiveRequest): Promise<SetOrganizationActiveResponse> {
  return request<SetOrganizationActiveResponse>(`/platform/organizations/${id}/active`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateOrganization(id: number, body: UpdateOrganizationRequest): Promise<UpdateOrganizationResponse> {
  return request<UpdateOrganizationResponse>(`/platform/organizations/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function scheduleOrganizationDeletion(
  id: number,
  body: ScheduleOrganizationDeletionRequest,
): Promise<ScheduleOrganizationDeletionResponse> {
  return request<ScheduleOrganizationDeletionResponse>(`/platform/organizations/${id}/deletion`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}
