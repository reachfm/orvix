// Shared suppression formatters — labels, tone, and impact copy.
import type { SuppressionReason, SuppressionState } from "./contract";

export function suppressionReasonLabel(reason: SuppressionReason | string): string {
  switch (reason) {
    case "hard_bounce":
      return "Hard bounce";
    case "complaint":
      return "Complaint";
    case "manual":
      return "Manual";
    default:
      return reason;
  }
}

export function suppressionStateLabel(state: SuppressionState | string): string {
  switch (state) {
    case "active":
      return "Active";
    case "released":
      return "Released";
    case "expired":
      return "Expired";
    default:
      return state;
  }
}

export function suppressionStateTone(state: SuppressionState | string): "success" | "warning" | "danger" | "neutral" {
  switch (state) {
    case "active":
      return "danger";
    case "released":
      return "success";
    case "expired":
      return "neutral";
    default:
      return "neutral";
  }
}

/**
 * Operational impact copy. An ACTIVE suppression blocks outbound
 * delivery to the address until expiry; release/expiry restore
 * delivery. History is retained for released suppressions — release is
 * not destructive deletion.
 */
export function suppressionImpact(state: SuppressionState | string): string {
  switch (state) {
    case "active":
      return "This suppression is active: outbound delivery to this address is blocked until it expires or is released.";
    case "released":
      return "This suppression is released: outbound delivery is allowed again. The suppression history is retained.";
    case "expired":
      return "This suppression expired: outbound delivery is allowed again. The suppression history is retained.";
    default:
      return "";
  }
}

export function formatTimestamp(value: string | undefined | null): string {
  if (!value) return "—";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString();
}
