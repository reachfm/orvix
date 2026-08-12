// Exact contracts for the platform bulk mailbox workflow
// (POST /api/v1/platform/mailboxes/:tenant_id/bulk/status —
// BulkPlatformMailboxStatus in internal/api/handlers/
// platform_mail_control.go).
//
// Semantics (from internal/platform/mailcontrol/service.go
// BulkMailboxStatus): suspend/reactivate are applied as a single
// tenant-scoped bulk update and report the affected count; delete
// soft-deletes each row through the production lifecycle service and
// reports per-row failures. The response is the authoritative
// partial-vs-atomic statement — the UI repeats it exactly.
import type {
  BulkMailboxAction,
  BulkMailboxRequest,
  BulkMailboxResult,
  PlatformMailbox,
} from "../mailboxes/contract";

export type { BulkMailboxAction, BulkMailboxRequest, BulkMailboxResult, PlatformMailbox };

/** Backend-enforced ceiling for one bulk call (service validation). */
export const BULK_MAILBOX_MAX = 500;

export interface BulkMailboxImpact {
  ids: number[];
  action: BulkMailboxAction;
  tenantId: number;
  domainName?: string;
  affectedStatus: string;
  irreversible: boolean;
}

export function impactSummary(impact: BulkMailboxImpact): string {
  const scope = impact.domainName ? `domain ${impact.domainName}` : `tenant ${impact.tenantId}`;
  const verb = impact.action === "suspend" ? "suspended" : impact.action === "reactivate" ? "reactivated" : "soft-deleted";
  return `${impact.ids.length} mailbox(es) in ${scope} will be ${verb} (status → ${impact.affectedStatus}).${
    impact.irreversible ? " Deletion is irreversible without a restore path." : ""
  }`;
}
