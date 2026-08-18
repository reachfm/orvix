// Exact request/response contracts for the platform-owned organization
// endpoints (internal/api/router.go lines ~1420-1424), matching the Go
// structs field-for-field:
//   - OrganizationSummary: internal/admin/platform/service.go
//   - OrganizationDetail:  internal/admin/organization/types.go (embeds
//     Organization; served by GetOrganizationDetail via orgAdminSvc, the
//     ONE typed detail source this feature uses — the other detail path,
//     GET /platform/organizations/:id, returns an untyped
//     map[string]interface{} from a different service and is not used
//     here to avoid generic/untyped rendering).

export interface OrganizationSummary {
  id: number;
  name: string;
  slug: string;
  domain: string;
  plan: string;
  active: boolean;
  mailbox_count: number;
  domain_count: number;
  created_at: string;
}

export interface ListOrganizationsResponse {
  organizations: OrganizationSummary[];
  total: number;
}

export interface OrganizationDetail {
  id: number;
  name: string;
  slug: string;
  domain: string;
  plan: string;
  max_domains: number;
  max_mailboxes: number;
  logo_url?: string;
  primary_color: string;
  active: boolean;
  created_at: string;
  updated_at: string;
  domain_count: number;
  mailbox_count: number;
  admin_count: number;
  quota_used_bytes: number;
  status_label: string;
}

export interface SetOrganizationActiveRequest {
  active: boolean;
  reason?: string;
}

// SetOrganizationActive's real handler (organization_admin.go) returns
// only {"status":"ok"} — the caller must invalidate and refetch the
// detail/list queries to see the new active state, never assume the
// mutation response itself carries updated organization fields.
export interface SetOrganizationActiveResponse {
  status: string;
}

// PATCH /platform/organizations/:id (UpdateOrganization,
// organization_admin.go) — a real, registered, previously-unwired
// route. Matches organization.UpdateOrganizationRequest's pointer
// fields exactly: every field is optional, and only fields present in
// the JSON body are applied (a Go nil pointer means "leave unchanged",
// not "clear to zero value"). The frontend must therefore only include
// keys the operator actually edited, never the full form state.
export interface UpdateOrganizationRequest {
  name?: string;
  domain?: string;
  plan?: string;
  max_domains?: number;
  max_mailboxes?: number;
  logo_url?: string;
  primary_color?: string;
}

// The handler returns {"organization": Organization} — the base
// struct (id/name/slug/domain/plan/max_domains/max_mailboxes/
// logo_url/primary_color/active/created_at/updated_at), NOT the
// Detail-augmented shape GetOrganizationDetail returns (no
// domain_count/mailbox_count/admin_count/quota_used_bytes/
// status_label) — the caller must invalidate the detail query to see
// those recomputed, not read them off this response.
export interface UpdateOrganizationResponse {
  organization: {
    id: number;
    name: string;
    slug: string;
    domain: string;
    plan: string;
    max_domains: number;
    max_mailboxes: number;
    logo_url?: string;
    primary_color: string;
    active: boolean;
    created_at: string;
    updated_at: string;
  };
}

// Phase 3 (Frontend): POST /platform/organizations
// (CreatePlatformOrganization, internal/api/handlers/platform_admin.go,
// routed at internal/api/router.go). The live response carries the
// ONE-TIME owner invitation token — shown exactly once with a copy
// action, never persisted by the UI, never re-fetched. The org starts
// pending_activation (tenants.active=0) and becomes active ONLY when
// the owner redeems the token at POST /auth/invitations/accept
// (InvitationAcceptPage). Idempotency-Key is REQUIRED; a replay
// returns the stored body WITHOUT the token.
export interface CreatePlatformOrganizationRequest {
  name: string;
  slug?: string;
  domain?: string;
  owner_email: string;
  plan_id?: string;
  max_domains?: number;
  max_mailboxes?: number;
}

export interface PlatformInvitation {
  id: number;
  organization_id: number;
  inviter_id: number;
  email: string;
  role: string;
  status: string;
  expires_at: string;
  accepted_at?: string | null;
  revoked_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface CreatePlatformOrganizationResponse {
  organization: {
    id: number;
    name: string;
    slug: string;
    domain: string;
    plan: string;
    active: boolean;
    created_at: string;
    updated_at: string;
  };
  invitation: PlatformInvitation;
  invite_token: string;
  warning: string;
}

// Canonical activation lifecycle states reported by
// GET /platform/organizations/:id/detail (status_label).
export const ORG_LIFECYCLE_LABELS: Readonly<Record<string, string>> = {
  pending_activation: "Pending activation",
  active: "Active",
  disabled: "Disabled",
};

export function orgLifecycleLabel(statusLabel: string | undefined): string {
  if (!statusLabel) return "";
  return ORG_LIFECYCLE_LABELS[statusLabel] ?? statusLabel;
}

// Phase G: POST /platform/organizations/:id/deletion
// (PlatformScheduleOrganizationDeletion, organization_admin.go). Requires a
// typed confirmation matching the org's exact domain and a reason; blocked
// by a dependency check (active domains/mailboxes) that comes back as a 409
// with a `blockers` list, and is idempotent (a repeat call while a deletion
// is already scheduled returns status "deletion_already_scheduled" rather
// than erroring).
export interface ScheduleOrganizationDeletionRequest {
  confirm_domain: string;
  reason: string;
}

export interface ScheduleOrganizationDeletionResponse {
  status: "deletion_scheduled" | "deletion_already_scheduled";
}
