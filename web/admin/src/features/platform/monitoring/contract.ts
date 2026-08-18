// Exact contract for GET /api/v1/monitoring/health (platformMW-gated,
// internal/api/handlers/monitoring.go's GetMonitoringHealth), matching
// internal/monitoring/types.go's Health struct field-for-field.
//
// hostUptimeSeconds / hostUptimeAvailable are DISTINCT from
// uptimeSeconds: uptimeSeconds is the Orvix process/service uptime;
// hostUptimeSeconds is the real Linux OS host uptime (time since
// boot), only available on Linux — hostUptimeAvailable is false
// (never a fabricated 0) everywhere else. Never label uptimeSeconds
// as "Server Uptime" in the UI; use hostUptimeSeconds for that.
//
// network.primaryPublicIPv4 is discovered by the server enumerating
// its own OS network interfaces — never fetched from a third-party
// IP-lookup service. It is null (never a placeholder) when the host
// has no publicly-routable IPv4 address visible to the OS.

export interface ComponentHealth {
  status: "ok" | "warning" | "critical" | "unknown" | string;
  message: string;
}

export interface DiskUsage {
  label: string;
  totalBytes: number;
  usedBytes: number;
  freeBytes: number;
  usedPct: number;
}

export interface NetworkInfo {
  primaryPublicIPv4: string | null;
  addresses?: string[];
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

export interface MonitoringHealth {
  status: "ok" | "degraded" | "down" | string;
  uptimeSeconds: number;
  generatedAt: string;
  disk: DiskUsage[];
  db: ComponentHealth;
  queue: ComponentHealth;
  backup: ComponentHealth;
  api: ComponentHealth;
  capacity: Capacity;
  openAlerts: number;
  hostUptimeAvailable: boolean;
  hostUptimeSeconds: number;
  network: NetworkInfo;
}
