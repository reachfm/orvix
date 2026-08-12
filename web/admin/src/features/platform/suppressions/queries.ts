import { useQuery } from "@tanstack/react-query";
import { getPlatformSuppression, getPlatformSuppressionHistory, listPlatformSuppressions } from "./api";
import type { SuppressionFilter } from "./contract";

export const suppressionKeys = {
  list: (tenantId: number | null, filter: SuppressionFilter) =>
    ["platform-suppressions", "list", tenantId ?? "none", filter.q ?? "", filter.reason ?? "", filter.source ?? "", filter.state ?? "", filter.domain ?? "", filter.limit ?? 50, filter.offset ?? 0] as const,
  detail: (tenantId: number | null, id: number) =>
    ["platform-suppressions", "detail", tenantId ?? "none", id] as const,
  history: (tenantId: number | null, id: number) =>
    ["platform-suppressions", "history", tenantId ?? "none", id] as const,
};

export function usePlatformSuppressions(tenantId: number | null, filter: SuppressionFilter) {
  return useQuery({
    queryKey: suppressionKeys.list(tenantId, filter),
    queryFn: () => listPlatformSuppressions(tenantId as number, filter),
    enabled: tenantId !== null,
    retry: false,
  });
}

export function usePlatformSuppression(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: suppressionKeys.detail(tenantId, id ?? 0),
    queryFn: () => getPlatformSuppression(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}

export function usePlatformSuppressionHistory(tenantId: number | null, id: number | null) {
  return useQuery({
    queryKey: suppressionKeys.history(tenantId, id ?? 0),
    queryFn: () => getPlatformSuppressionHistory(tenantId as number, id as number),
    enabled: tenantId !== null && id !== null,
    retry: false,
  });
}
