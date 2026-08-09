// HTTP transport only, through the shared CSRF/auth-aware client.
import { request } from "../../../api";
import type {
  Backup, BackupSchedule, BackupMetrics, BackupHealth,
  RestoreJobSubmitResponse, RestoreJobResult,
  UpdateStatus, UpdateHistoryRow, PreflightResult, UpdateCheckResult, ChangelogEntry,
  Alert, MonitoringSnapshot, Capacity, MonitoringProvidersResponse, ListAlertDeliveriesResponse,
  ListStorageVolumesResponse, ClusterStatus,
} from "./contract";

// Backups
export const listBackups = () => request<Backup[]>("/admin/backups");
export const createBackup = () => request<Backup>("/admin/backups", { method: "POST" });
export const runBackupNow = () => request<Backup>("/admin/backups/now", { method: "POST" });
export const getBackupSchedule = () => request<BackupSchedule>("/admin/backups/schedule");
export const setBackupSchedule = (cfg: Partial<BackupSchedule>) =>
  request<BackupSchedule>("/admin/backups/schedule", { method: "POST", body: JSON.stringify(cfg) });
export const getBackupMetrics = () => request<BackupMetrics>("/admin/backups/metrics");
export const getBackupHealth = () => request<BackupHealth>("/admin/backups/health");
export const runBackupRetention = () => request<{ deleted?: number }>("/admin/backups/retention", { method: "POST" });
export const validateBackup = (id: string) => request<{ status: string }>(`/admin/backups/${id}/validate`, { method: "POST" });
export const downloadBackupUrl = (id: string) => `/api/v1/admin/backups/${id}/download`;
// DeleteBackup (internal/api/handlers/backups.go) requires the exact
// literal confirmation string "delete-orvix-backup" in the JSON body —
// not the backup's own id/name. The previous implementation sent no
// body at all, so every delete attempt was rejected with 400.
export const deleteBackup = (id: string) =>
  request<void>(`/admin/backups/${id}`, { method: "DELETE", body: JSON.stringify({ confirm: "delete-orvix-backup" }) });

// Restore jobs
// PostRestoreBackup requires the exact literal confirmation string
// "restore-orvix-backup" — same class of defect as delete above, also
// previously sent no body.
export const restoreBackup = (id: string) =>
  request<RestoreJobSubmitResponse>(`/admin/backups/${id}/restore`, { method: "POST", body: JSON.stringify({ confirm: "restore-orvix-backup" }) });
export const getRestoreJobStatus = (jobId: string) => request<RestoreJobResult>(`/admin/backups/restore-jobs/${jobId}`);

// GetChangelog defaults the "module" query param to "orvix-core"
// server-side when omitted — matched explicitly here rather than
// relying on that default, so the frontend contract is self-evident.
export const getChangelog = (module: string = "orvix-core") =>
  request<ChangelogEntry[]>(`/updates/changelog?module=${encodeURIComponent(module)}`);

// Updates
// GET /update/check (GetUpdateCheck) and POST /update/check
// (PostUpdateCheck) both call CheckManifest and return the same
// UpdateCheckResult shape — GET for the query, POST for the "Check
// now" mutation trigger. The legacy GET /updates/check (CheckUpdates)
// returns an unrelated flat {status,module,version} shape and is not
// used here.
export const checkUpdates = () => request<UpdateCheckResult>("/update/check");
export const getUpdateStatus = () => request<UpdateStatus>("/update/status");
// GetUpdateHistory (internal/api/handlers/update.go) wraps its rows in
// an envelope — {"history": [...]}, not a bare array — unlike
// GetUpdateStatus/GetUpdateCheck/GetUpdatePreflight on the same
// resource. Unwrap it here so every caller sees a plain row array.
export const getUpdateHistory = () => request<{ history: UpdateHistoryRow[] }>("/update/history").then((r) => r.history);
export const getUpdatePreflight = () => request<PreflightResult>("/update/preflight");
export const postUpdateCheck = () => request<UpdateCheckResult>("/update/check", { method: "POST" });
export const runUpdate = () => request<UpdateHistoryRow>("/update/run", { method: "POST" });

// Monitoring
export const getMonitoringAlerts = () => request<{ alerts: Alert[] }>("/monitoring/alerts");
export const getMonitoringCapacity = () => request<Capacity>("/monitoring/capacity");
export const getMonitoringSnapshot = () => request<MonitoringSnapshot>("/monitoring/snapshot");
export const getMonitoringProviders = () => request<MonitoringProvidersResponse>("/monitoring/alert-providers");
export const listAlertDeliveries = () => request<ListAlertDeliveriesResponse>("/monitoring/alert-deliveries");
export const resolveMonitoringAlert = (id: number) =>
  request<{ status: string; id: number }>(`/monitoring/alerts/${id}/resolve`, { method: "POST" });

// Storage
export const listStorageVolumes = () => request<ListStorageVolumesResponse>("/admin/storage/volumes");

// Cluster
export const getClusterStatus = () => request<ClusterStatus>("/admin/cluster/status");
