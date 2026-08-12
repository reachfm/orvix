// Tenant-context model for Platform Super Admin mail-control reads.
//
// A Platform Super Admin has no owning tenant. The backend allows
// platform-wide reads of tenant mail data ONLY through an active
// support-access grant: requests to GET /domains and GET /mailboxes
// must carry the X-Support-Tenant-ID header naming the grant's target
// tenant, and the support middleware validates the operator's grant
// and scope on every request.
//
// This feature models that honestly:
//   - selectTenantId: the tenant the operator is currently inspecting
//   - activeGrant: the operator's active grant for that tenant (from
//     GET /platform/support/grants), which the backend validates per
//     request; the UI never fabricates or stores authentication
//   - no grant => the mail-control pages render an honest unavailable
//     state that links to the Support Access page

export interface TenantContextState {
  /** Tenant currently inspected (never used as auth context). */
  tenantId: number | null;
  /** Tenant display name when available from the organizations list. */
  tenantName?: string;
  /** Active grant id + scope for the selected tenant, when one exists. */
  grant?: { id: number; scope: string; status: string };
}

export const TENANT_CONTEXT_QUERY_KEY = ["tenant-context"] as const;
