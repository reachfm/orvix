import { useQuery } from "@tanstack/react-query";
import { getMonitoringHealth } from "./api";

// Shared across the Platform shell's top-bar alert badge and the
// Overview dashboard's infra/health panels — react-query dedupes
// concurrent callers under this one queryKey, so mounting both at
// once issues a single network request, not two. A 60s refetch
// interval keeps the alert badge reasonably fresh without polling
// aggressively.
export function useMonitoringHealthQuery() {
  return useQuery({
    queryKey: ["platform-monitoring-health"],
    queryFn: getMonitoringHealth,
    retry: false,
    refetchInterval: 60_000,
  });
}
