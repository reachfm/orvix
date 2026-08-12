// Shared mailbox formatters — single source of truth for status
// labels, quota/usage formatting, and allowed transitions.
import { MAILBOX_STATUSES } from "./contract";

export function mailboxStatusLabel(status: string): string {
  switch (status) {
    case "active":
      return "Active";
    case "disabled":
      return "Disabled";
    case "suspended":
      return "Suspended";
    case "deleted":
      return "Deleted";
    default:
      return status;
  }
}

export function mailboxStatusTone(status: string): "success" | "warning" | "danger" | "neutral" {
  switch (status) {
    case "active":
      return "success";
    case "disabled":
      return "neutral";
    case "suspended":
      return "warning";
    case "deleted":
      return "danger";
    default:
      return "neutral";
  }
}

/**
 * The allowed status transitions per current status, mirroring
 * internal/admin/mailbox/service.go isValidStatusTransition. Deleted
 * mailboxes have no platform status route — restore is not a platform
 * capability, so it is never offered.
 */
export function allowedMailboxTransitions(current: string): string[] {
  switch (current) {
    case "active":
      return ["disabled", "suspended"];
    case "disabled":
      return ["active"];
    case "suspended":
      return ["active"];
    default:
      return [];
  }
}

export function formatBytes(bytes: number | undefined | null): string {
  if (bytes === undefined || bytes === null || bytes < 0) return "—";
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** i;
  return `${value.toFixed(value >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export function usagePercent(usedBytes: number | undefined | null, quotaMb: number | undefined | null): number | null {
  if (!usedBytes || !quotaMb || quotaMb <= 0) return null;
  return Math.min(100, Math.round((usedBytes / (quotaMb * 1024 * 1024)) * 100));
}

/** True when the status set includes the writable set for this mailbox. */
export function isMailboxStatusWritable(status: string): boolean {
  return MAILBOX_STATUSES.includes(status);
}
