// Exact contracts for the platform-owned queue-admin endpoints
// (internal/api/router.go: /admin/queue/summary, /admin/queue/messages,
// /admin/queue/messages/:id, /admin/queue/messages/:id/{retry,bounce,
// cancel}, /admin/queue/messages/bulk-action — all platformMW-gated),
// matching the Go structs field-for-field:
//   - QueueMessage: internal/api/handlers/admin_queue.go (PR #65 adds
//     tenant_id, domain_id, retryable and failure_category projections;
//     attempts now come from the real coremail_delivery_attempts table)
//   - QueueMetrics: internal/coremail/queue/types.go

export interface QueueMessage {
  id: number;
  tenant_id: number;
  domain_id: number;
  from_address: string;
  to_address: string;
  recipient_domain: string;
  status: string;
  priority: number;
  attempt_count: number;
  max_attempts: number;
  next_attempt_at?: string | null;
  last_attempt_at?: string | null;
  last_error?: string;
  last_status_code: number;
  delivery_mode?: string;
  remote_host?: string;
  created_at: string;
  /** Derived from the real queue state machine: only pending/deferred/bounced/dead_letter may be retried. */
  retryable: boolean;
  /** Canonical failure category: suppressed | policy_denied | bounce | other. */
  failure_category?: string;
}

export interface ListQueueMessagesResponse {
  messages: QueueMessage[];
  total: number;
  limit: number;
  offset: number;
}

export interface QueueMessageFilter {
  status?: string;
  from?: string;
  to?: string;
  limit?: number;
  offset?: number;
}

export interface QueueMetrics {
  pending: number;
  leased: number;
  delivering: number;
  deferred: number;
  delivered: number;
  bounced: number;
  dead_letter: number;
  cancelled: number;
  total: number;
  avg_attempts: number;
  oldest_pending?: string | null;
}

export interface QueueSummaryResponse {
  metrics: QueueMetrics;
}

export interface DeliveryAttempt {
  attempt: number;
  at: string;
  result: string;
  error: string;
  remote_host: string;
  status_code: number;
}

export interface QueueDetailResponse {
  message: QueueMessage;
  attempts: DeliveryAttempt[];
}

export interface QueueActionResponse {
  status: string;
  id: number;
}

export type BulkQueueAction = "retry" | "cancel" | "bounce";

export interface BulkQueueActionResult {
  id: number;
  success: boolean;
  error?: string;
  code?: string;
}

export interface BulkQueueActionResponse {
  action: BulkQueueAction;
  total: number;
  succeeded: number;
  results: BulkQueueActionResult[];
}

/** Terminal statuses: delivered/cancelled must never expose invalid actions. */
export const TERMINAL_QUEUE_STATUSES: ReadonlySet<string> = new Set(["delivered", "cancelled"]);

export function canRetryMessage(m: QueueMessage): boolean {
  return m.retryable;
}

export function canBounceMessage(m: QueueMessage): boolean {
  return !TERMINAL_QUEUE_STATUSES.has(m.status);
}

export function canCancelMessage(m: QueueMessage): boolean {
  return !TERMINAL_QUEUE_STATUSES.has(m.status);
}

/** Typed confirmation phrases (queueActionConfirm in admin_queue.go). */
export function bounceQueueConfirmation(id: number): string {
  return `BOUNCE-QUEUE-${id}`;
}

export function cancelQueueConfirmation(id: number): string {
  return `CANCEL-QUEUE-${id}`;
}

/** Safe failure-category label (never a raw SMTP error). */
export function failureCategoryLabel(category: string | undefined): string {
  switch (category) {
    case "suppressed":
      return "Suppressed";
    case "policy_denied":
      return "Policy denied";
    case "bounce":
      return "Bounce";
    case "other":
      return "Other";
    default:
      return "";
  }
}

// The stable, sanitized contract every queue-admin endpoint returns
// when CoreMail is intentionally disabled (coreMailUnavailableResponse
// in admin_queue.go) — 503 with this exact body, never leaked SQL/
// table/DSN text.
export interface CoreMailDisabledBody {
  error: string;
  code: "COREMAIL_DISABLED";
}

// ── Queue history (GET /admin/queue/history) ────────────────────────
// AdminQueueHistory: immutable, cross-entry delivery-attempt history,
// cursor-paginated via after_id (the last row's ID becomes the next
// call's after_id). Field names match delivery.DeliveryAttempt
// (internal/coremail/delivery/history.go).
export interface QueueHistoryAttempt {
  id: number;
  queue_entry_id: number;
  attempt_number: number;
  status: string; // success | deferred | bounced | dead_letter
  remote_host: string;
  remote_ip: string;
  status_code: number;
  status_msg: string;
  enhanced_code: string;
  duration_ms: number;
  tls_used: boolean;
  worker_id: string;
  attempted_at: string;
}

export interface QueueHistoryResponse {
  attempts: QueueHistoryAttempt[];
  next_after_id: number;
  count: number;
}

export interface QueueHistoryFilter {
  status?: string;
  remote_host?: string;
  after_id?: number;
  limit?: number;
}

/** History status options the backend accepts (delivery attempt statuses). */
export const QUEUE_HISTORY_STATUSES: ReadonlyArray<string> = ["success", "deferred", "bounced", "dead_letter"];

export function queueHistoryStatusLabel(status: string): string {
  switch (status) {
    case "success":
      return "Success";
    case "deferred":
      return "Deferred";
    case "bounced":
      return "Bounced";
    case "dead_letter":
      return "Dead letter";
    default:
      return status;
  }
}
