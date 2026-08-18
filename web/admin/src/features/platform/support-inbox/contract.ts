// Support Inbox typed contract (FINAL-ENTERPRISE-COMPLETION).
//
// The Platform Super Admin Support Inbox is the canonical read-write
// view of every tenant support ticket. The shapes below match the
// backend's support.Repository return values, with `description`
// representing the original ticket body (DB column: message) and
// `messages` representing the reply thread.

export type SupportTicketStatus =
  | "open"
  | "in_progress"
  | "waiting_for_customer"
  | "customer_replied"
  | "resolved"
  | "closed";

export type SupportTicketPriority = "low" | "normal" | "high" | "urgent";

export interface SupportTicket {
  id: number;
  created_at: string;
  updated_at: string;
  reference_id: string;
  tenant_id: number;
  user_id: number;
  user_email: string;
  category: string;
  subject: string;
  description: string;
  status: SupportTicketStatus;
  priority: SupportTicketPriority;
  assigned_to_id?: number | null;
  last_reply_at?: string | null;
  last_reply_by?: "tenant" | "platform" | "" | null;
  closed_at?: string | null;
  resolved_at?: string | null;
  delivery_status: "pending" | "sent" | "failed" | "disabled";
  delivery_error?: string;
}

export interface SupportTicketMessage {
  id: number;
  created_at: string;
  updated_at: string;
  ticket_id: number;
  author_user_id: number;
  author_email: string;
  author_kind: "tenant" | "platform";
  body: string;
}

export interface ListTicketsResponse {
  entries: SupportTicket[];
  total: number;
  limit: number;
  offset: number;
}

export interface GetTicketDetailResponse {
  ticket: SupportTicket;
  messages: SupportTicketMessage[];
}

export interface ListTicketsParams {
  limit?: number;
  offset?: number;
  status?: SupportTicketStatus | "";
  category?: string;
  tenant_id?: number;
  search?: string;
}

export const SUPPORT_TICKET_STATUSES: SupportTicketStatus[] = [
  "open",
  "in_progress",
  "waiting_for_customer",
  "customer_replied",
  "resolved",
  "closed",
];

export const SUPPORT_TICKET_CATEGORIES: { value: string; label: string }[] = [
  { value: "general", label: "General" },
  { value: "billing", label: "Billing" },
  { value: "technical", label: "Technical" },
  { value: "security", label: "Security" },
];

export function statusTone(status: SupportTicketStatus): {
  bg: string;
  fg: string;
} {
  switch (status) {
    case "open":
      return { bg: "bg-[var(--accent)]/10", fg: "text-[var(--accent)]" };
    case "in_progress":
      return { bg: "bg-[var(--warning)]/10", fg: "text-[var(--warning)]" };
    case "waiting_for_customer":
      return { bg: "bg-[var(--bg-subtle)]", fg: "text-[var(--text-secondary)]" };
    case "customer_replied":
      return { bg: "bg-[var(--bg-subtle)]", fg: "text-[var(--text-secondary)]" };
    case "resolved":
      return { bg: "bg-[var(--success)]/10", fg: "text-[var(--success)]" };
    case "closed":
      return { bg: "bg-[var(--bg-subtle)]", fg: "text-[var(--text-muted)]" };
  }
}
