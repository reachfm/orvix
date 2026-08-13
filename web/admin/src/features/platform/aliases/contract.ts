// Exact contracts for the Platform Super Admin alias routes
// (internal/api/router.go: /platform/aliases/:tenant_id,
// .../:tenant_id/:id, POST .../:tenant_id, DELETE .../:tenant_id/:id).
//
// Wire shapes match the Go structs field-for-field:
//   - PlatformAlias: internal/platform/mailcontrol/domain.go
//
// The backend exposes exactly: list, detail, create (domain_id,
// from_addr, to_addr), and delete. There is no update/enable/disable/
// restore route — the UI must not fabricate one. The backend rejects
// cross-tenant destinations and duplicate aliases with typed errors.

export interface PlatformAlias {
  id: number;
  tenant_id: number;
  domain_id: number;
  from_addr: string;
  to_addr: string;
  active: boolean;
  created_at: string;
  updated_at: string;
}

export interface PlatformAliasList {
  aliases: PlatformAlias[];
  total: number;
  limit: number;
  offset: number;
}

export interface PlatformAliasFilter {
  q?: string;
  to?: string;
  domain_id?: number;
  limit?: number;
  offset?: number;
}

export interface CreatePlatformAliasRequest {
  domain_id: number;
  from_addr: string;
  to_addr: string;
}

export interface DeletePlatformAliasResponse {
  status: "ok";
  id: number;
}
