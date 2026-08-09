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
