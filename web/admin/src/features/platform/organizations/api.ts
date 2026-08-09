// HTTP transport only — no business logic, no React, no query/mutation
// state (that lives in queries.ts/mutations.ts). Every call goes through
// the shared CSRF/auth-aware client (src/api.ts's request), never fetch()
// directly.
import { request } from "../../../api";
import type {
  ListOrganizationsResponse,
  OrganizationDetail,
  SetOrganizationActiveRequest,
  SetOrganizationActiveResponse,
} from "./contract";

export function listOrganizations(search?: string, limit?: number, offset?: number): Promise<ListOrganizationsResponse> {
  const params = new URLSearchParams();
  if (search) params.set("search", search);
  if (limit !== undefined) params.set("limit", String(limit));
  if (offset !== undefined) params.set("offset", String(offset));
  const qs = params.toString();
  return request<ListOrganizationsResponse>(`/platform/organizations${qs ? "?" + qs : ""}`);
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
