// Exact contracts for the platform-owned queue-admin endpoints
// (internal/api/router.go: /admin/queue/summary, /admin/queue/messages,
// /admin/queue/messages/:id, /admin/queue/messages/:id/{retry,bounce,
// cancel} — all platformMW-gated), matching the Go structs field-for-
// field:
//   - QueueMessage: internal/api/handlers/admin_queue.go
//   - QueueMetrics: internal/coremail/queue/types.go
// The prior MailOperations.tsx used from/to/attempts/id:string — none
// of which exist on the real wire shape (from_address/to_address/
// attempt_count/id:number) — every queue row silently rendered
// undefined for those fields. Fixed here.

export interface QueueMessage {
  id: number;
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

// The stable, sanitized contract every queue-admin endpoint returns
// when CoreMail is intentionally disabled (coreMailUnavailableResponse
// in admin_queue.go) — 503 with this exact body, never leaked SQL/
// table/DSN text.
export interface CoreMailDisabledBody {
  error: string;
  code: "COREMAIL_DISABLED";
}
