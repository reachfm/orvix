// Exact contracts for the Platform deliverability routes
// (internal/api/router.go: /platform/deliverability/:tenant_id/metrics,
// .../events, .../events/:id).
//
// Wire shapes match the Go structs field-for-field:
//   - TenantMetrics / TimeBucket: internal/platform/deliverability/service.go
//   - SafeEvent: internal/platform/deliverability/service.go
//   - WindowMetrics: internal/platform/deliverability/domain.go
//   - metrics response envelope: internal/api/handlers/platform_mail_control.go
//
// BreakdownRow has NO json tags in the backend, so it serializes as
// {"Key": ..., "Count": ...} — the frontend matches that exactly.

export interface BreakdownRow {
  Key: string;
  Count: number;
}

export interface TimeBucket {
  start: string;
  delivered: number;
  failed: number;
  other: number;
  total: number;
}

export interface TenantMetrics {
  tenant_id: number;
  window_start: string;
  window_end: string;
  volume: number;
  delivered: number;
  failed: number;
  deferred: number;
  bounced: number;
  policy_denied: number;
  suppressed: number;
  complaints: number;
  delivery_rate: number;
  bounce_rate: number;
  failure_rate: number;
  deferred_rate: number;
  by_category: BreakdownRow[];
  by_domain: BreakdownRow[];
  by_provider: BreakdownRow[];
  time_buckets: TimeBucket[];
  bucket_size: string;
}

export interface WindowMetrics {
  dimension: string;
  dimension_value: string;
  window_start: string;
  window_end: string;
  volume: number;
  delivered: number;
  temp_fail: number;
  perm_fail: number;
  bounced: number;
  complaints: number;
  avg_latency_ms: number;
  delivery_rate: number;
  bounce_rate: number;
  complaint_rate: number;
  temp_fail_rate: number;
  perm_fail_rate: number;
}

export interface DeliverabilityMetricsResponse {
  window: WindowMetrics;
  summary: TenantMetrics;
  volume: number;
  delivered: number;
  bounced: number;
  complaints: number;
  delivery_rate: number;
  bounce_rate: number;
  complaint_rate: number;
}

export type SignalType =
  | "delivered"
  | "temp_fail"
  | "perm_fail"
  | "bounce"
  | "complaint"
  | "spam_reject"
  | "policy_reject"
  | "throttled"
  | "tls_failure"
  | "auth_failure"
  | "suppressed";

export type SignalCategory =
  | "delivered"
  | "failed"
  | "deferred"
  | "bounced"
  | "policy_denied"
  | "suppressed"
  | "relay_failure"
  | "other";

export interface SafeEvent {
  id: number;
  tenant_id: number;
  dimension: string;
  dimension_value: string;
  type: SignalType;
  category: SignalCategory;
  latency_ms?: number;
  recorded_at: string;
}

export interface ListDeliverabilityEventsResponse {
  events: SafeEvent[];
  total: number;
  limit: number;
  offset: number;
}

export interface DeliverabilityEventFilter {
  domain?: string;
  type?: SignalType;
  provider?: string;
  start?: string;
  end?: string;
  limit?: number;
  offset?: number;
}
