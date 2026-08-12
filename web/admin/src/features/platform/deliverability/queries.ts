import { useQuery } from "@tanstack/react-query";
import { getPlatformDeliverabilityEvent, listPlatformDeliverabilityEvents, listPlatformDeliverabilityMetrics } from "./api";
import type { DeliverabilityEventFilter } from "./contract";

export const deliverabilityKeys = {
  metrics: (tenantId: number | null, start: string, end: string, dimension?: string, value?: string) =>
    ["platform-deliverability", "metrics", tenantId ?? "none", start, end, dimension ?? "", value ?? ""] as const,
  events: (tenantId: number | null, filter: DeliverabilityEventFilter) =>
    ["platform-deliverability", "events", tenantId ?? "none", filter.domain ?? "", filter.type ?? "", filter.provider ?? "", filter.start ?? "", filter.end ?? "", filter.limit ?? 100, filter.offset ?? 0] as const,
  event: (tenantId: number | null, id: number) =>
    ["platform-deliverability", "event", tenantId ?? "none", id] as const,
};

export function useDeliverabilityMetrics(
  tenantId: number | null,
  start: string,
  end: string,
  dimension?: string,
  value?: string,
) {
  return useQuery({
    queryKey: deliverabilityKeys.metrics(tenantId, start, end, dimension, value),
    queryFn: () => listPlatformDeliverabilityMetrics(tenantId as number, start, end, dimension, value),
    enabled: tenantId !== null,
    retry: false,
  });
}

export function useDeliverabilityEvents(tenantId: number | null, filter: DeliverabilityEventFilter) {
  return useQuery({
    queryKey: deliverabilityKeys.events(tenantId, filter),
    queryFn: () => listPlatformDeliverabilityEvents(tenantId as number, filter),
    enabled: tenantId !== null,
    retry: false,
  });
}

export function useDeliverabilityEvent(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: deliverabilityKeys.event(tenantId, id ?? 0),
    queryFn: () => getPlatformDeliverabilityEvent(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}
