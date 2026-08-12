// Shared deliverability formatters — labels and real-rate helpers.
// Percentages are ONLY ever computed from returned numerators and
// denominators; no reputation/inbox-placement/spam scores are invented.
import type { SignalCategory, SignalType } from "./contract";

export function signalTypeLabel(type: SignalType | string): string {
  switch (type) {
    case "delivered":
      return "Delivered";
    case "temp_fail":
      return "Temporary failure";
    case "perm_fail":
      return "Permanent failure";
    case "bounce":
      return "Bounce";
    case "complaint":
      return "Complaint";
    case "spam_reject":
      return "Spam rejection";
    case "policy_reject":
      return "Policy rejection";
    case "throttled":
      return "Throttled";
    case "tls_failure":
      return "TLS failure";
    case "auth_failure":
      return "Auth failure";
    case "suppressed":
      return "Suppressed";
    default:
      return type;
  }
}

export function categoryLabel(category: SignalCategory | string): string {
  switch (category) {
    case "delivered":
      return "Delivered";
    case "failed":
      return "Failed";
    case "deferred":
      return "Deferred";
    case "bounced":
      return "Bounced";
    case "policy_denied":
      return "Policy denied";
    case "suppressed":
      return "Suppressed";
    case "relay_failure":
      return "Relay failure";
    case "other":
      return "Other";
    default:
      return category;
  }
}

/** Real percentage from a returned numerator/denominator, or null when not meaningful. */
export function realPercent(numerator: number, denominator: number): number | null {
  if (!denominator || denominator <= 0) return null;
  return Math.round((numerator / denominator) * 1000) / 10;
}

export function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  return `${value}%`;
}

export function formatNumber(value: number | undefined | null): string {
  if (value === undefined || value === null) return "—";
  return value.toLocaleString();
}

export function formatTimestamp(value: string | undefined | null): string {
  if (!value) return "—";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString();
}
