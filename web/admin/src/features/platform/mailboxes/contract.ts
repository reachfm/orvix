// Exact contracts for the Platform Super Admin mailbox routes
// (internal/api/router.go: /platform/mailboxes/:tenant_id,
// .../:tenant_id/:id, .../status, .../quota, .../reset-password,
// DELETE .../:tenant_id/:id).
//
// Wire shapes match the Go structs field-for-field:
//   - PlatformMailbox: internal/platform/mailcontrol/domain.go
//   - BulkMailboxResult: internal/platform/mailcontrol/domain.go
//
// The backend exposes: list, detail, creation (POST
// /platform/mailboxes/:tenant_id — internal/api/handlers/platform_provisioning.go's
// CreatePlatformMailbox; the target domain is resolved from the
// email's domain part, not a separate field), lifecycle status
// mutation, quota update, one-time password reset, typed-confirmation
// soft delete (X-Confirm: PURGE-MAILBOX-<id>), guarded mail-access-mode
// mutation (POST .../:id/access-mode, version-guarded — see
// SetPlatformMailboxAccessModeRequest), and bulk status. There is no
// platform restore route — the UI must not fabricate one.

export interface PlatformMailbox {
  id: number;
  tenant_id: number;
  domain_id: number;
  domain: string;
  email: string;
  name: string;
  status: string;
  is_admin: boolean;
  quota_mb: number;
  used_bytes: number;
  /**
   * MailAccessMode is the CONFIGURED per-mailbox policy;
   * EffectiveMailAccessMode is the RESOLVED policy after "inherit"
   * falls back to the domain. Never merge or rename these — they
   * answer different questions (what was set vs. what the delivery
   * path actually enforces).
   */
  mail_access_mode: string;
  effective_mail_access_mode: string;
  /**
   * Real optimistic-concurrency counter (internal/platform/mailcontrol
   * fix: previously missing from this projection). Read it here and
   * send it back unmodified as expected_version on the next
   * access-mode mutation — never fabricate or default it.
   */
  version: number;
  created_at: string;
  updated_at: string;
}

export type MailAccessMode = "internal_only" | "internal_external";

/**
 * The only two choices a Platform Super Admin may pick — for BOTH
 * mailbox creation and the access-mode mutation. "inherit" is
 * deliberately excluded: the whole point of this explicit platform
 * action is a definite per-mailbox decision, never a deferred one.
 */
export const MAILBOX_ACCESS_MODE_OPTIONS: ReadonlyArray<{ value: MailAccessMode; label: string; description: string }> = [
  {
    value: "internal_only",
    label: "Internal only",
    description: "Can communicate only with permitted internal/local recipients.",
  },
  {
    value: "internal_external",
    label: "Internal and external",
    description: "Can communicate with internal and external recipients subject to platform policy.",
  },
];

// ── Mailbox creation (POST /platform/mailboxes/:tenant_id) ─────────
// Field-for-field match of mailcontrol.PlatformCreateMailboxRequest.

export interface PlatformCreateMailboxRequest {
  email: string;
  name?: string;
  password: string;
  quota_mb?: number;
  send_limit_per_hour?: number;
  force_password_change?: boolean;
  mail_access_mode: MailAccessMode;
}

/**
 * The backend NEVER returns the password or any hash on this or any
 * other route — the UI must never expect one here.
 */
export interface PlatformCreateMailboxResult {
  mailbox: PlatformMailbox;
}

// ── Guarded access-mode mutation ────────────────────────────────────

export interface PlatformSetMailboxAccessModeRequest {
  mail_access_mode: MailAccessMode;
  expected_version: number;
}

export interface PlatformMailboxAccessModeResult {
  id: number;
  mail_access_mode: MailAccessMode;
  effective_mail_access_mode: MailAccessMode;
  version: number;
}

export interface PlatformMailboxList {
  mailboxes: PlatformMailbox[];
  total: number;
  limit: number;
  offset: number;
}

export interface PlatformMailboxFilter {
  q?: string;
  status?: string;
  domain_id?: number;
  limit?: number;
  offset?: number;
}

/**
 * Writable mailbox statuses (internal/admin/mailbox/types.go). The
 * valid transition matrix is: active → disabled|suspended|deleted;
 * disabled → active|deleted; suspended → active|deleted; deleted is
 * only ever reached via soft-delete and has no platform restore route.
 */
export const MAILBOX_STATUSES: ReadonlyArray<string> = ["active", "disabled", "suspended", "deleted"];

export interface SetPlatformMailboxStatusRequest {
  status: string;
  reason?: string;
}

export interface SetPlatformMailboxStatusResponse {
  status: "ok";
  id: number;
}

export interface SetPlatformMailboxQuotaRequest {
  quota_mb: number;
}

export interface SetPlatformMailboxQuotaResponse {
  status: "ok";
  id: number;
  quota_mb: number;
}

/**
 * One-time credential response. `generated_password` is returned
 * EXACTLY ONCE by the backend; the UI shows it once with a copy
 * control and irreversible dismissal, and must never persist it.
 */
export interface ResetPlatformMailboxPasswordResponse {
  status: "ok";
  id: number;
  generated_password: string;
  show_once: boolean;
}

export interface DeletePlatformMailboxResponse {
  status: "ok";
  id: number;
}

/** Typed confirmation phrase for mailbox soft-delete (handler-enforced). */
export function mailboxPurgeConfirmation(id: number): string {
  return `PURGE-MAILBOX-${id}`;
}

export interface BulkMailboxFailure {
  id: number;
  error: string;
}

export interface BulkMailboxResult {
  total: number;
  succeeded: number;
  failed?: BulkMailboxFailure[];
}

export type BulkMailboxAction = "suspend" | "reactivate" | "delete";

export interface BulkMailboxRequest {
  ids: number[];
  action: BulkMailboxAction;
  reason?: string;
}

// ── Audited, read-only support view (POST .../support-view) ────────
// Field-for-field match of internal/api/handlers/platform_mailbox_support_view.go.
// This is NOT impersonation: the operator stays authenticated as
// themselves. The mailbox password is never read; the response never
// contains a password, hash, or reusable bearer token — only an
// opaque session id scoped to (operator, tenant, mailbox, mailbox_view).
// confirm MUST equal exactly `ACCESS-MAILBOX-<id>`.

export interface StartMailboxSupportViewRequest {
  ticket_ref: string;
  reason: string;
  duration_minutes?: number;
  confirm: string;
}

export interface StartMailboxSupportViewResponse {
  session_id: string;
  tenant_id: number;
  mailbox_id: number;
  email: string;
  mode: "read_only";
  expires_at: string;
}

export function accessMailboxConfirmation(mailboxId: number): string {
  return `ACCESS-MAILBOX-${mailboxId}`;
}

export const SUPPORT_VIEW_DEFAULT_DURATION_MINUTES = 30;
export const SUPPORT_VIEW_MAX_DURATION_MINUTES = 60;

// ── Support-view read-only folder/message/attachment reads ─────────
// Field-for-field match of the coremail storage package's Folder,
// Message, and Attachment structs, as projected by the support-view
// handlers. These are READ-ONLY views — there is no write route in
// this family, by design, and the UI must never fabricate one.

export interface SupportViewFolder {
  id: number;
  mailbox_id: number;
  parent_id?: number | null;
  name: string;
  path: string;
  folder_type: string;
  message_count: number;
  unread_count: number;
  total_size: number;
}

export interface SupportViewMessageSummary {
  id: number;
  mailbox_id: number;
  folder_id: number;
  subject: string;
  from_address: string;
  to_addresses: string;
  received_date: string;
  size_bytes: number;
  seen: boolean;
}

export interface SupportViewFoldersResponse {
  folders: SupportViewFolder[];
}

export interface SupportViewMessagesResponse {
  messages: SupportViewMessageSummary[];
  total: number;
}

export interface SupportViewAttachmentSummary {
  id: number;
  message_id: number;
  filename: string;
  content_type: string;
  size_bytes: number;
}

export interface SupportViewMessageDetailResponse {
  message: SupportViewMessageSummary;
  raw_rfc822: string;
  attachments: SupportViewAttachmentSummary[];
}

export interface EndMailboxSupportViewResponse {
  session_id: string;
  ended: true;
}
