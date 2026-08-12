// Explicit platform tenant scope for the Platform Super Admin mail
// control pages.
//
// A Platform Super Admin has no owning tenant. Every /api/v1/platform/*
// mail-control route requires an EXPLICIT target tenant_id in the path;
// the backend never derives, infers, or defaults one. The scope state
// below is a pure client-side selection that binds query keys — it is
// NEVER sent as an auth header (no X-Support-Tenant-ID, no grant), and
// it never impersonates a tenant. The operator's platform identity is
// the only authentication.
//
// Support Access remains a separate, unrelated feature (see
// features/platform/support-access) and is not consulted here.

export const TENANT_SCOPE_QUERY_KEY = ["platform-tenant-scope"] as const;

export interface TenantScopeState {
  tenantId: number | null;
  tenantName?: string;
}

export const EMPTY_TENANT_SCOPE: TenantScopeState = { tenantId: null };
