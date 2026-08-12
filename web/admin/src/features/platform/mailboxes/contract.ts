// Exact contracts for the platform mailboxes read endpoints.
// GET /mailboxes is support-access-aware: a Platform Super Admin must
// present X-Support-Tenant-ID plus an active grant (verified by the
// support middleware on every request). The handler (ListMailboxes in
// internal/api/handlers/handlers.go) returns the tenant's mailboxes
// when a support context is active.

export interface PlatformMailbox {
  id: number;
  email: string;
  domain: string;
  status: string;
  is_admin: boolean;
  created_at: string;
}

export type MailboxListResponse = PlatformMailbox[];

export const MAILBOX_STATUSES: ReadonlyArray<string> = ["active", "suspended", "disabled", "deleted"];
