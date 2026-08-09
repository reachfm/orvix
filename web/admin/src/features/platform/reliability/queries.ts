import { useQuery } from "@tanstack/react-query";
import * as api from "./api";

// Staggered via `enabled` chaining rather than fired simultaneously:
// avoids a burst of parallel requests the instant a tab mounts — each
// secondary panel is supplementary context, not needed for the
// primary table's first paint.

export function useBackupsQuery() {
  return useQuery({ queryKey: ["backups"], queryFn: api.listBackups, retry: false });
}
export function useBackupScheduleQuery(enabled: boolean) {
  return useQuery({ queryKey: ["backup-schedule"], queryFn: api.getBackupSchedule, enabled, retry: false });
}
export function useBackupMetricsQuery(enabled: boolean) {
  return useQuery({ queryKey: ["backup-metrics"], queryFn: api.getBackupMetrics, enabled, retry: false });
}
export function useBackupHealthQuery(enabled: boolean) {
  return useQuery({ queryKey: ["backup-health"], queryFn: api.getBackupHealth, enabled, retry: false });
}
export function useRestoreJobStatusQuery(jobId: string | null) {
  return useQuery({
    queryKey: ["restore-job", jobId],
    queryFn: () => api.getRestoreJobStatus(jobId as string),
    enabled: jobId !== null,
    // Poll while the job is in a non-terminal state.
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status && !["succeeded", "failed"].includes(status) ? 2000 : false;
    },
    retry: false,
  });
}

export function useUpdateStatusQuery() {
  return useQuery({ queryKey: ["update-status"], queryFn: api.getUpdateStatus, retry: false });
}
export function useUpdateCheckQuery(enabled: boolean) {
  return useQuery({ queryKey: ["update-check"], queryFn: api.checkUpdates, enabled, retry: false });
}
export function useUpdateHistoryQuery(enabled: boolean) {
  return useQuery({ queryKey: ["update-history"], queryFn: api.getUpdateHistory, enabled, retry: false });
}
export function useUpdatePreflightQuery(enabled: boolean) {
  return useQuery({ queryKey: ["update-preflight"], queryFn: api.getUpdatePreflight, enabled, retry: false });
}

export function useMonitoringAlertsQuery(enabled: boolean) {
  return useQuery({ queryKey: ["mon-alerts"], queryFn: api.getMonitoringAlerts, enabled, retry: false });
}
export function useMonitoringCapacityQuery(enabled: boolean) {
  return useQuery({ queryKey: ["mon-capacity"], queryFn: api.getMonitoringCapacity, enabled, retry: false });
}
export function useMonitoringSnapshotQuery(enabled: boolean) {
  return useQuery({ queryKey: ["mon-snapshot"], queryFn: api.getMonitoringSnapshot, enabled, retry: false });
}
export function useMonitoringProvidersQuery(enabled: boolean) {
  return useQuery({ queryKey: ["mon-providers"], queryFn: api.getMonitoringProviders, enabled, retry: false });
}
export function useAlertDeliveriesQuery(enabled: boolean) {
  return useQuery({ queryKey: ["mon-deliveries"], queryFn: api.listAlertDeliveries, enabled, retry: false });
}

export function useStorageVolumesQuery() {
  return useQuery({ queryKey: ["storage-volumes"], queryFn: api.listStorageVolumes, retry: false });
}

export function useClusterStatusQuery() {
  return useQuery({ queryKey: ["cluster-status"], queryFn: api.getClusterStatus, retry: false });
}
