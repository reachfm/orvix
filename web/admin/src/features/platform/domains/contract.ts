// Exact contracts for the Platform Super Admin domain routes
// (internal/api/router.go: /platform/domains/:tenant_id,
// /platform/domains/:tenant_id/:id, .../status, .../mail-access-mode).
//
// Wire shapes match the Go structs field-for-field:
//   - PlatformDomain: internal/platform/mailcontrol/domain.go
//   - PlatformDomainList: internal/platform/mailcontrol/domain.go
//
// The backend exposes: list, detail, creation (POST
// /platform/domains/:tenant_id — internal/api/handlers/platform_provisioning.go's
// CreatePlatformDomain), lifecycle status mutation (active | disabled |
// suspended — the writable set from internal/admin/domain/types.go
// ParseDomainStatus; "locked" and "deleted" are read-side values only)
// and the DOMAIN-level mail-access-mode mutation (internal_only |
// internal_external — a distinct, pre-existing route from mailbox
// creation's access-mode choice). DNS record detail beyond the
// one-time creation response, DKIM rotation, TLS/ACME state,
// update-after-create, and hard deletion are NOT part of this route
// family — the UI must not fabricate them.
//
// Domain creation deliberately has NO mail-access-mode field: that
// policy belongs to the mailbox (internal/platform/mailcontrol/domain.go's
// PlatformCreateDomainRequest has no such field either).

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

// ── Domain creation (POST /platform/domains/:tenant_id) ────────────
// Field-for-field match of mailcontrol.PlatformCreateDomainRequest.
// Deliberately has NO mail_access_mode — that choice belongs to the
// mailbox, never the domain, on the platform creation route.

export interface PlatformDomainLimits {
  max_mailboxes?: number;
  max_aliases?: number;
  default_mailbox_quota_mb?: number;
  max_mailbox_quota_mb?: number;
}

export interface PlatformDKIMOptions {
  generate: boolean;
  selector?: string;
}

export interface PlatformCreateDomainRequest {
  name: string;
  description?: string;
  status?: string;
  limits?: PlatformDomainLimits;
  dkim?: PlatformDKIMOptions;
}

/** Mirrors mailcontrol.PlatformEffectiveLimits exactly. */
export interface PlatformEffectiveLimits {
  max_mailboxes: number;
  max_mailboxes_unlimited: boolean;
  max_mailboxes_inherited: boolean;
  max_aliases: number;
  max_aliases_unlimited: boolean;
  max_aliases_inherited: boolean;
  default_mailbox_quota_mb: number;
  max_mailbox_quota_mb: number;
  max_mailbox_quota_mb_unlimited: boolean;
  max_mailbox_quota_mb_inherited: boolean;
  default_mailbox_quota_mb_inherited: boolean;
}

/**
 * PUBLIC DKIM result only — selector and the public DNS TXT record.
 * The backend NEVER returns a private key on this or any route; the
 * UI must never render or expect one.
 */
export interface PlatformDKIMResult {
  selector: string;
  public_dns_txt: string;
  dns_record_name: string;
}

export interface PlatformDNSRequirement {
  name: string;
  type: string;
  value: string;
  ttl: number;
  priority?: number;
  required: boolean;
  purpose?: string;
}

export interface PlatformPlanSummary {
  plan: string;
  max_domains: number;
  max_domains_unlimited: boolean;
  domains_used: number;
  remaining_domains: number | null;
  max_mailboxes: number;
  max_mailboxes_unlimited: boolean;
  mailboxes_used: number;
  remaining_mailboxes: number | null;
}

export interface PlatformCreateDomainResult {
  domain: PlatformDomain;
  effective_limits: PlatformEffectiveLimits;
  dkim?: PlatformDKIMResult;
  dns_requirements?: PlatformDNSRequirement[];
  dns_next_step: string;
  public_dns_changed: boolean;
  plan?: PlatformPlanSummary;
  idempotent: boolean;
}
