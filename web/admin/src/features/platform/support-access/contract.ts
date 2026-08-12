// Exact contracts for the platform support-access endpoints
// (internal/api/handlers/incidents_support.go + internal/supportaccess/domain.go).

export type GrantStatus = "requested" | "approved" | "active" | "revoked" | "expired";

export const SUPPORT_SCOPES: ReadonlyArray<string> = [
  "read_only",
  "mailbox_view",
  "domain_view",
  "full_tenant_view",
];

export interface AccessGrant {
  id: number;
  ticket_ref: string;
  reason: string;
  target_tenant_id: number;
  target_tenant_name?: string;
  granted_by_id: number;
  permission_scope: string;
  status: GrantStatus;
  activated_at?: string;
  expires_at: string;
  revoked_at?: string;
  revoke_reason?: string;
  emergency_break_glass: boolean;
  created_at: string;
  updated_at: string;
}

export interface ListGrantsResponse {
  grants: AccessGrant[];
}

export interface GrantDetailResponse {
  grant: AccessGrant;
}

export interface CreateGrantRequest {
  ticket_ref: string;
  reason: string;
  target_tenant_id: number;
  permission_scope: string;
  expires_at: string;
  emergency_break_glass?: boolean;
}
