// Exact contracts for the Platform Super Admin domain routes
// (internal/api/router.go: /platform/domains/:tenant_id,
// /platform/domains/:tenant_id/:id, .../status, .../mail-access-mode).
//
// Wire shapes match the Go structs field-for-field:
//   - PlatformDomain: internal/platform/mailcontrol/domain.go
//   - PlatformDomainList: internal/platform/mailcontrol/domain.go
//
// The backend exposes exactly: list, detail, lifecycle status mutation
// (active | disabled | suspended — the writable set from
// internal/admin/domain/types.go ParseDomainStatus; "locked" and
// "deleted" are read-side values only) and the mail-access-mode
// mutation (internal_only | internal_external). DNS record detail,
// DKIM rotation, TLS/ACME state, create/update, and hard deletion are
// NOT part of this route family — the UI must not fabricate them.

export interface PlatformDomain {
  id: number;
  tenant_id: number;
  name: string;
  status: string;
  plan: string;
  description?: string;
  mailbox_count: number;
  alias_count: number;
  dkim_enabled: boolean;
  dkim_selector?: string;
  dmarc_enabled: boolean;
  mail_access_mode: string;
  created_at: string;
  updated_at: string;
}

export interface PlatformDomainList {
  domains: PlatformDomain[];
  total: number;
  limit: number;
  offset: number;
}

export interface PlatformDomainFilter {
  q?: string;
  status?: string;
  limit?: number;
  offset?: number;
}

/** Writable domain statuses (ParseDomainStatus). */
export const DOMAIN_STATUSES: ReadonlyArray<string> = ["active", "disabled", "suspended"];

export type MailAccessMode = "internal_only" | "internal_external";

export const MAIL_ACCESS_MODES: ReadonlyArray<MailAccessMode> = ["internal_only", "internal_external"];

export interface SetPlatformDomainStatusRequest {
  status: string;
  reason?: string;
}

export interface SetPlatformDomainStatusResponse {
  status: "ok";
  id: number;
}

export interface SetPlatformDomainMailAccessModeRequest {
  mail_access_mode: MailAccessMode;
}

export interface SetPlatformDomainMailAccessModeResponse {
  status: "ok";
  id: number;
  mail_access_mode: MailAccessMode;
}
