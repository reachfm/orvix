// Exact contracts for the Platform Super Admin domain routes
// (internal/api/router.go: /platform/domains/:tenant_id,
// /platform/domains/:tenant_id/:id, .../status, .../mail-access-mode,
// .../dns, .../dkim/generate, .../dkim/rotate, .../deactivate).
//
// Wire shapes match the Go structs field-for-field:
//   - PlatformDomain, PlatformDomainList, PlatformDomainDNSResult,
//     PlatformDKIMResult: internal/platform/mailcontrol/domain.go
//   - deactivate request/response: internal/api/handlers/platform_domain_lifecycle.go
//
// The backend exposes: list, detail, creation (POST
// /platform/domains/:tenant_id — internal/api/handlers/platform_provisioning.go's
// CreatePlatformDomain), lifecycle status mutation (active | disabled |
// suspended — the writable set from internal/admin/domain/types.go
// ParseDomainStatus; "locked" and "deleted" are read-side values only),
// the DOMAIN-level mail-access-mode mutation (internal_only |
// internal_external — a distinct, pre-existing route from mailbox
// creation's access-mode choice), a read-only existing-domain DNS/DKIM
// snapshot (GET .../dns), version-guarded DKIM generate/rotate, and the
// canonical audited deactivate/soft-delete route. TLS/ACME state,
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
  /**
   * Real optimistic-concurrency counter from coremail_domains.version
   * (mailcontrol.PlatformDomain.Version). Backend truth only — never
   * fabricated, defaulted to 0, derived from timestamps, or incremented
   * client-side. Passed back as expected_version on guarded mutations
   * (dkim/generate, dkim/rotate, deactivate).
   */
  version: number;
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
 * UI must never render or expect one. This is the SAME Go struct
 * (mailcontrol.PlatformDKIMResult) used both by domain creation's
 * optional dkim field and by the generate/rotate mutation responses —
 * version is set only by the version-guarded generate/rotate route
 * (zero/absent on the creation-response usage).
 */
export interface PlatformDKIMResult {
  selector: string;
  public_dns_txt: string;
  dns_record_name: string;
  version?: number;
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

// ── Existing-domain DNS/DKIM snapshot (GET .../dns) ─────────────────
// Field-for-field match of mailcontrol.PlatformDomainDNSResult. A
// read-only snapshot for an EXISTING domain — distinct from the
// one-time creation response above. dkim_configured=false means the
// dkim_* fields are all absent (an honest not-configured state, never
// a placeholder). No private key material is ever part of this or any
// contract in this feature.
export interface PlatformDomainDNSResult {
  tenant_id: number;
  domain_id: number;
  domain: string;
  version: number;
  status: string;
  dkim_configured: boolean;
  dkim_selector?: string;
  dkim_dns_record_name?: string;
  dkim_public_dns_txt?: string;
  dns_requirements?: PlatformDNSRequirement[];
  dns_next_step?: string;
}

// ── DKIM generate/rotate (POST .../dkim/generate, .../dkim/rotate) ──
// Field-for-field match of the platformDKIMMutation request body
// (internal/api/handlers/platform_mail_control.go) and PlatformDKIMResult
// response. confirm_rotation is REQUIRED (and must equal exactly
// "rotate-dkim-key") on rotate only; generate must omit/leave it blank.
export interface PlatformDKIMMutationRequest {
  /** Empty string lets the backend default to its standard selector ("orvix"). */
  selector?: string;
  /** Rotate only: must be exactly "rotate-dkim-key". Omitted for generate. */
  confirm_rotation?: string;
  expected_version: number;
}

/**
 * Response to both generate and rotate: the same PlatformDKIMResult
 * Go struct as the creation response, with version always populated.
 * NEVER a private key — the backend does not return one on this or
 * any route, and this contract must never grow a private_key field.
 */
export type PlatformDKIMMutationResult = Required<Pick<PlatformDKIMResult, "selector" | "public_dns_txt" | "dns_record_name" | "version">>;

// ── Deactivate / soft-delete (POST .../deactivate) ──────────────────
// Field-for-field match of internal/api/handlers/platform_domain_lifecycle.go's
// DeactivatePlatformDomain request/response. This is the canonical,
// audited platform domain deactivation — NOT a hard delete: it sets
// status=disabled and deactivated_at but never touches deleted_at and
// never purges DKIM config or history. confirm MUST equal exactly
// `DEACTIVATE-DOMAIN-<id>` (see deactivateDomainConfirmation below).
export interface DeactivatePlatformDomainRequest {
  confirm: string;
  reason: string;
  expected_version: number;
}

export interface DeactivatePlatformDomainResponse {
  id: number;
  tenant_id: number;
  status: string;
  version: number;
  request_id: string;
}

// ── Delete (POST .../delete) — PERMANENT, distinct from deactivate ──
// Field-for-field match of DeletePlatformDomain's request/response.
// This is the canonical deleted_at-tombstone delete: the domain
// disappears from active inventory and its active DKIM config is
// purged (DKIM/audit history survives). Requires the domain to already
// be canonically deactivated — a live domain gets a 409
// DOMAIN_NOT_DEACTIVATED. confirm MUST equal exactly
// `DELETE-DOMAIN-<id>` (see deleteDomainConfirmation below).
export interface DeletePlatformDomainRequest {
  confirm: string;
  reason: string;
  expected_version: number;
}

export interface DeletePlatformDomainResponse {
  id: number;
  tenant_id: number;
  deleted: true;
  version: number;
  request_id: string;
}

/**
 * Structured blocker counts the backend returns on 409
 * DOMAIN_DELETE_BLOCKED — exactly what must be cleaned up before the
 * domain can be permanently deleted.
 */
export interface DomainDeleteBlockers {
  mailboxes: number;
  aliases: number;
  queued_messages: number;
}

/** The exact typed-confirmation phrase the backend requires for delete. */
export function deleteDomainConfirmation(domainId: number): string {
  return `DELETE-DOMAIN-${domainId}`;
}

/** The exact typed-confirmation phrase the backend requires for deactivate. */
export function deactivateDomainConfirmation(domainId: number): string {
  return `DEACTIVATE-DOMAIN-${domainId}`;
}

// ── Live public-DNS verification (POST .../dns/verify) ──────────────
// Field-for-field match of mailcontrol.PlatformDNSVerifyRecord /
// PlatformDNSVerifyResult. READ-ONLY: performs external DNS lookups
// only — never mutates public DNS, never generates/rotates DKIM,
// never modifies the domain. The expected fields (name/type/value/
// priority/purpose/required) are the SAME values dns_requirements
// carries; this is not a second, independent expected-record model.
//
// status is the backend's own vocabulary (verified | missing |
// mismatch | conflict | multiple_spf | not_checked | unsupported |
// error | not_found) — the UI maps "verified" to the operator-facing
// label "Matched" rather than the backend inventing a parallel enum.
export type PlatformDNSVerifyStatus =
  | "verified"
  | "missing"
  | "mismatch"
  | "conflict"
  | "multiple_spf"
  | "not_checked"
  | "unsupported"
  | "error"
  | "not_found";

export interface PlatformDNSVerifyRecord {
  name: string;
  type: string;
  value: string;
  ttl: number;
  priority?: number;
  required: boolean;
  purpose?: string;
  status: PlatformDNSVerifyStatus;
  verified: boolean;
  reason?: string;
  /** Actual live value(s) found in public DNS. Absent on error/missing — render an honest "not found" state, never an empty value. */
  observed?: string;
}

export interface PlatformDNSVerifyResult {
  tenant_id: number;
  domain_id: number;
  domain: string;
  checked_at: string;
  records: PlatformDNSVerifyRecord[];
  total_count: number;
  matched_count: number;
  issue_count: number;
  all_verified: boolean;
  warnings?: string[];
}
