// Exact contracts for the Platform Super Admin group routes
// (internal/api/router.go: /platform/groups/:tenant_id,
// .../:tenant_id/:id, .../:tenant_id/:id/members, plus the
// Phase 1/2 mutation routes POST/DELETE .../:tenant_id[/:id],
// POST/DELETE .../:id/members[/:member_id]).
//
// Wire shapes match the Go structs field-for-field:
//   - PlatformGroup: internal/platform/mailcontrol/domain.go
//   - members: []string (ListPlatformGroupMembers returns
//     {"group_id": ..., "members": [...]}).
//   - DeletePlatformGroup requires the typed confirmation header
//     X-Confirm: DELETE-GROUP-<id> (mailcontrol.ConfirmGroupDelete).
//
// The backend exposes: list, detail, members, create, delete
// (soft-delete), add-member, remove-member — all tenant-scoped in SQL
// and audited.

export interface PlatformGroup {
  id: number;
  tenant_id: number;
  name: string;
  description?: string;
  member_count: number;
  created_at: string;
  updated_at: string;
}

export interface PlatformGroupList {
  groups: PlatformGroup[];
  total: number;
  limit: number;
  offset: number;
}

export interface PlatformGroupFilter {
  q?: string;
  limit?: number;
  offset?: number;
}

export interface GroupMembersResponse {
  group_id: number;
  members: string[];
}

export interface CreatePlatformGroupRequest {
  name: string;
  description?: string;
}

export interface CreatePlatformGroupResponse {
  id: number;
  tenant_id: number;
  name: string;
  description?: string;
  member_count: number;
  created_at: string;
  updated_at: string;
}

export interface GroupActionResponse {
  status: string;
  id?: number;
  group_id?: number;
  member_id?: number;
  email?: string;
}

/** Typed confirmation required by DELETE /platform/groups/:tenant_id/:id. */
export function confirmGroupDelete(id: number): string {
  return `DELETE-GROUP-${id}`;
}
