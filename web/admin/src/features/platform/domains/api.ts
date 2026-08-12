import { request } from "../../../api";
import { headerForTenantContext } from "../tenant-context/queries";
import type { DomainListResponse } from "./contract";

/**
 * Lists the selected tenant's domains through the support-access-aware
 * GET /domains route. The X-Support-Tenant-ID header is required and
 * the backend validates the operator's active grant on every request.
 * Returns null when no tenant context is set — callers must render the
 * honest unavailable state rather than fabricate a tenant.
 */
export function listDomainsForTenant(tenantId: number | null | undefined): Promise<DomainListResponse | null> {
  if (!tenantId) return Promise.resolve(null);
  return request<DomainListResponse>("/domains", { headers: headerForTenantContext(tenantId) });
}
