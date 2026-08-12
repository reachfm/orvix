// Exact contracts for the platform domains read endpoints.
// GET /domains is support-access-aware: a Platform Super Admin must
// present X-Support-Tenant-ID plus an active grant (verified by the
// support middleware on every request). The handler (ListDomains in
// internal/api/handlers/handlers.go) returns the tenant's domains when
// a support context is active.

export interface PlatformDomain {
  id: number;
  domain: string;
  plan: string;
  status: string;
  mailbox_count: number;
}

export type DomainListResponse = PlatformDomain[];

export const DOMAIN_STATUSES: ReadonlyArray<string> = ["active", "suspended", "locked", "deleted"];
