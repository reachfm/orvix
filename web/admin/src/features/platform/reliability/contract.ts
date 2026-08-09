// Exact contracts for the platform-owned Reliability endpoints, read
// from the real Go structs (not guessed) before writing this file:
//   Backups:    internal/backup/types.go, internal/api/handlers/backups.go
//   Restore:    internal/restorecoord/coordinator.go, restore_jobs.go
//   Updates:    internal/updater/types.go
//   Monitoring: internal/monitoring/types.go, delivery.go
//   Storage:    internal/api/handlers/enterprise_parity.go's VolumeStat
//   Cluster:    internal/api/handlers/enterprise_admin_v3.go's
//               AdminClusteringStatus (a fully static single-node-
//               deployment status — Orvix has no clustering
//               implementation yet, see honest_note)

// --- Backups ---

export interface Backup {
  id: string;
  name: string;
  status: string;
  size_bytes: number;
  sha256?: string;
  created_at: string;
  completed_at?: string;
}

export interface BackupSchedule {
  enabled: boolean;
  frequency: string;
  retentionCount: number;
  lastRunAt?: string | null;
  nextRunAt?: string | null;
  updatedAt: string;
}

export interface BackupMetrics {
  totalBackups: number;
  totalSizeBytes: number;
  newestBackupAt?: string;
  oldestBackupAt?: string;
  lastSuccessfulAt?: string;
  nextScheduledAt?: string;
}

export interface BackupHealth {
  schedulerEnabled: boolean;
  retentionEnabled: boolean;
  directoryExists: boolean;
  writable: boolean;
  availableDiskBytes: number;
  lastBackupAgeHours: number;
  lastBackupAgeWarning: boolean;
  lastBackupAgeCritical: boolean;
  status: string;
  reason?: string;
}

// --- Restore jobs ---

export type RestoreJobStatus = "pending" | "activating" | "restarting" | "verifying" | "rolling_back" | "succeeded" | "failed";

export interface RestoreJobSubmitResponse {
  job_id: string;
  status: RestoreJobStatus;
  poll_url: string;
  message: string;
}

export interface RestoreJobResult {
  job_id: string;
  backup_id: string;
  status: RestoreJobStatus;
  message?: string;
  safety_backup_id?: string;
  rolled_back: boolean;
  error?: string;
  rollback_error?: string;
  created_at: string;
  updated_at: string;
}

// --- Updates ---

export interface UpdateStatus {
  currentVersion: string;
  currentSha: string;
  buildTime: string;
  availableVersion: string;
  availableSha: string;
  channel: string;
  updateAvailable: boolean;
  releaseNotes: string;
  updateError?: string;
  checkedAt: string;
  jobStatus: string;
  jobStartedAt?: string | null;
  jobCompletedAt?: string | null;
  jobActor?: string;
}

export interface UpdateHistoryRow {
  id: number;
  startedAt: string;
  completedAt?: string | null;
  durationSeconds: number;
  previousSha: string;
  newSha: string;
  fromVersion: string;
  toVersion: string;
  status: string;
  severity: string;
  actor: string;
  notes?: string;
}

export interface PreflightCheck {
  name: string;
  status: "pass" | "warning" | "fail";
  detail: string;
}

export interface PreflightResult {
  pass: boolean;
  checks: PreflightCheck[];
  message: string;
}

export interface UpdateCheckResult {
  current_version: string;
  current_sha: string;
  latest_version: string;
  latest_sha: string;
  update_available: boolean;
  channel: string;
  release_notes: string[];
  message?: string;
}

// --- Monitoring ---

export interface Alert {
  id: number;
  category: string;
  severity: "info" | "warning" | "critical";
  title: string;
  message: string;
  source: string;
  active: boolean;
  createdAt: string;
  resolvedAt?: string | null;
}

export interface DiskUsage {
  label: string;
  totalBytes: number;
  usedBytes: number;
  freeBytes: number;
  usedPct: number;
}

export interface ComponentHealth {
  status: string;
  message: string;
}

export interface Capacity {
  domainCount: number;
  mailboxCount: number;
  messageCount: number;
  attachmentCount: number;
  queueCount: number;
  queueDeadLetter: number;
  storageBytes: number;
  databaseSize: number;
  backupCount: number;
  backupBytes: number;
}

export interface CertExpiryStatus {
  status: string;
  expiringWithin7: number;
  expiringWithin30: number;
}

export interface MonitoringSnapshot {
  generatedAt: string;
  serviceStatus: string;
  uptimeSeconds: number;
  disk: DiskUsage[];
  dbHealth: ComponentHealth;
  queueHealth: ComponentHealth;
  backupHealth: ComponentHealth;
  apiHealth: ComponentHealth;
  certExpiry: CertExpiryStatus;
  dnsReadiness: ComponentHealth;
  capacity: Capacity;
  openAlerts: number;
  memoryUsedBytes: number;
  memoryTotalBytes: number;
}

export interface ProviderStatus {
  name: string;
  enabled: boolean;
  target?: string;
  hasSecret: boolean;
  detail?: string;
}

export interface DeliveryRecord {
  id: number;
  alertTitle: string;
  alertSeverity: string;
  alertCategory: string;
  provider: string;
  status: string;
  detail: string;
  createdAt: string;
}

export interface MonitoringProvidersResponse {
  providers: ProviderStatus[];
  deliveries: DeliveryRecord[];
}

export interface ListAlertDeliveriesResponse {
  deliveries: DeliveryRecord[];
  limit: number;
  honest_note?: string;
}

// --- Storage ---

export interface VolumeStat {
  mounted: string;
  role: string;
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
  used_pct: number;
  available: boolean;
  detail?: string;
}

export interface ListStorageVolumesResponse {
  volumes: VolumeStat[];
  honest_note: string;
}

// --- Cluster ---

export interface ProxySlot {
  name: string;
  kind: string;
  configured: boolean;
  runtime_state: string;
  detail: string;
}

export interface ClusterStatus {
  deployment_mode: string;
  current_nodes: number;
  max_nodes: number;
  consensus: string;
  peer_nodes: unknown[];
  proxies: ProxySlot[];
  honest_note: string;
}
