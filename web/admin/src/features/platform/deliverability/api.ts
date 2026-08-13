// HTTP transport only, through the shared CSRF/auth-aware client.
import { request } from "../../../api";
import type {
  DeliverabilityMetricsResponse,
  DeliverabilityEventFilter,
  ListDeliverabilityEventsResponse,
  SafeEvent,
} from "./contract";

/** RFC3339 for a Date, matching the backend's start/end contract. */
export function toRFC3339(d: Date): string {
  return d.toISOString();
}

export function listPlatformDeliverabilityMetrics(
  tenantId: number,
  start: string,
  end: string,
  dimension?: string,
  value?: string,
): Promise<DeliverabilityMetricsResponse> {
  const params = new URLSearchParams({ start, end });
  if (dimension) params.set("dimension", dimension);
  if (value) params.set("value", value);
  return request<DeliverabilityMetricsResponse>(`/platform/deliverability/${tenantId}/metrics?${params.toString()}`);
}

export function listPlatformDeliverabilityEvents(tenantId: number, filter: DeliverabilityEventFilter): Promise<ListDeliverabilityEventsResponse> {
  const params = new URLSearchParams();
  if (filter.domain) params.set("domain", filter.domain);
  if (filter.type) params.set("type", filter.type);
  if (filter.provider) params.set("provider", filter.provider);
  if (filter.start) params.set("start", filter.start);
  if (filter.end) params.set("end", filter.end);
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<ListDeliverabilityEventsResponse>(`/platform/deliverability/${tenantId}/events${qs ? "?" + qs : ""}`);
}

export function getPlatformDeliverabilityEvent(tenantId: number, id: number): Promise<SafeEvent> {
  return request<SafeEvent>(`/platform/deliverability/${tenantId}/events/${id}`);
}
