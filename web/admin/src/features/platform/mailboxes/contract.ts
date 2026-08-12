// Exact contracts for the Platform Super Admin mailbox routes
// (internal/api/router.go: /platform/mailboxes/:tenant_id,
// .../:tenant_id/:id, .../status, .../quota, .../reset-password,
// DELETE .../:tenant_id/:id).
//
// Wire shapes match the Go structs field-for-field:
//   - PlatformMailbox: internal/platform/mailcontrol/domain.go
//   - BulkMailboxResult: internal/platform/mailcontrol/domain.go
//
// The backend exposes exactly: list, detail, lifecycle status
// mutation, quota update, one-time password reset, typed-confirmation
// soft delete (X-Confirm: PURGE-MAILBOX-<id>), and bulk status. There
// is NO platform create/restore route — the UI must not fabricate one.

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
  created_at: string;
  updated_at: string;
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
