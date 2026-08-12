// Shared domain formatters — the single source of truth for how domain
// lifecycle and mail-access-mode values are labeled and explained.
import type { MailAccessMode } from "./contract";

export function domainStatusLabel(status: string): string {
  switch (status) {
    case "active":
      return "Active";
    case "disabled":
      return "Disabled";
    case "suspended":
      return "Suspended";
    case "locked":
      return "Locked";
    case "deleted":
      return "Deleted";
    default:
      return status;
  }
}

/** Semantic tone per lifecycle state — non-color-only status. */
export function domainStatusTone(status: string): "success" | "warning" | "danger" | "neutral" {
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

export function mailAccessModeLabel(mode: MailAccessMode | string | undefined): string {
  switch (mode) {
    case "internal_only":
      return "Internal only";
    case "internal_external":
      return "Internal + external";
    default:
      return mode || "Not set";
  }
}

/**
 * Plain-language effect of each canonical mode on inbound/outbound
 * mail, matching the backend's canonical semantics. Local-to-local
 * delivery is ALWAYS permitted in both modes.
 */
export function mailAccessModeDescription(mode: MailAccessMode | string | undefined): string {
  switch (mode) {
    case "internal_only":
      return "Mail may only flow between addresses on this domain (local-to-local). Outbound to external recipients is restricted and inbound external mail is not accepted. Local-to-local delivery remains permitted.";
    case "internal_external":
      return "Mail flows between this domain's addresses and external recipients: outbound external delivery and inbound external acceptance are both enabled. Local-to-local delivery remains permitted.";
    default:
      return "The domain has no effective mail-access policy configured.";
  }
}

export function formatTimestamp(value: string | undefined | null): string {
  if (!value) return "—";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString();
}
