import { request } from "../../../api";
import { headerForTenantContext } from "../tenant-context/queries";
import type { MailboxListResponse } from "./contract";

/**
 * Lists the selected tenant's mailboxes through the support-access-
 * aware GET /mailboxes route. The X-Support-Tenant-ID header is
 * required and the backend validates the operator's active grant on
 * every request. Returns null when no tenant context is set — callers
 * must render the honest unavailable state rather than fabricate a
 * tenant.
 */
export function listMailboxesForTenant(tenantId: number | null | undefined): Promise<MailboxListResponse | null> {
  if (!tenantId) return Promise.resolve(null);
  return request<MailboxListResponse>("/mailboxes", { headers: headerForTenantContext(tenantId) });
}
