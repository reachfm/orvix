// Exact contracts for the Platform Super Admin group routes
// (internal/api/router.go: /platform/groups/:tenant_id,
// .../:tenant_id/:id, .../:tenant_id/:id/members).
//
// Wire shapes match the Go structs field-for-field:
//   - PlatformGroup: internal/platform/mailcontrol/domain.go
//   - members: []string (ListPlatformGroupMembers returns
//     {"group_id": ..., "members": [...]}).
//
// The backend exposes exactly: list, detail, and members. There are NO
// platform group mutation routes (create/update/delete/add-member/
// remove-member) — the UI is inventory + membership read-only and must
// not fabricate mutation controls.

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
