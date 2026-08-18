import { AlertCircle, Clock, HardDrive, Network as NetworkIcon, Server } from "lucide-react";
import { useMonitoringHealthQuery } from "../../monitoring/queries";
import { formatBytes, formatDuration } from "../../monitoring/format";
import KpiCard, { KpiCardSkeleton } from "./KpiCard";

// aggregateDisk sums totalBytes/usedBytes/freeBytes across every
// reported mount. This is semantically correct here because
// monitoring.DiskUsage entries are DISTINCT filesystems/directories
// (backup dir, mailstore, database, ...) rather than overlapping
// views of the same disk — summing gives a real combined-capacity
// figure rather than double-counting the same bytes. The per-mount
// breakdown is not silently dropped: StorageDonut (rendered
// alongside these cards) lists every individual mount.
function aggregateDisk(disks: { totalBytes: number; usedBytes: number; freeBytes: number }[]) {
  return disks.reduce(
    (acc, d) => ({
      totalBytes: acc.totalBytes + d.totalBytes,
      usedBytes: acc.usedBytes + d.usedBytes,
      freeBytes: acc.freeBytes + d.freeBytes,
    }),
    { totalBytes: 0, usedBytes: 0, freeBytes: 0 }
  );
}

// InfraKPICards is the first dashboard row: real host infrastructure
// data only, sourced from GET /api/v1/monitoring/health. Every value
// here is either a real backend field or an explicit "Unavailable" —
// never a fabricated number. See PlatformShell's mission report for
// the hostUptimeSeconds vs uptimeSeconds distinction (this card uses
// ONLY hostUptimeSeconds, the real OS boot-time uptime).
export default function InfraKPICards() {
  const healthQ = useMonitoringHealthQuery();

  if (healthQ.isLoading) {
    return (
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {Array.from({ length: 4 }).map((_, i) => <KpiCardSkeleton key={i} />)}
      </div>
    );
  }
  if (healthQ.error) {
    return (
      <div className="flex items-center gap-2 text-[var(--danger)] text-sm bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-2xl p-4" role="alert">
        <AlertCircle size={16} /> Failed to load infrastructure metrics: {(healthQ.error as Error).message}
      </div>
    );
  }
  const h = healthQ.data;
  if (!h) return null;

  const disk = aggregateDisk(h.disk || []);
  const usedPct = disk.totalBytes > 0 ? Math.round((disk.usedBytes / disk.totalBytes) * 100) : null;

  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
      <KpiCard
        accent="emerald"
        icon={<Clock size={19} />}
        label="Server Uptime"
        value={h.hostUptimeAvailable ? formatDuration(h.hostUptimeSeconds) : "Unavailable"}
        sub={h.hostUptimeAvailable ? "Host uptime since boot" : "Not supported on this OS"}
      />
      <KpiCard
        accent="blue"
        icon={<HardDrive size={19} />}
        label="Total Storage"
        value={disk.totalBytes > 0 ? formatBytes(disk.totalBytes) : "Unavailable"}
        sub={h.disk?.length ? `${h.disk.length} mount${h.disk.length === 1 ? "" : "s"}` : undefined}
      />
      <KpiCard
        accent="violet"
        icon={<Server size={19} />}
        label="Used Storage"
        value={disk.usedBytes > 0 || disk.totalBytes > 0 ? formatBytes(disk.usedBytes) : "Unavailable"}
        sub={usedPct !== null ? `${usedPct}% used` : undefined}
      />
      <KpiCard
        accent="amber"
        icon={<NetworkIcon size={19} />}
        label="Public IP"
        value={h.network?.primaryPublicIPv4 ?? "Unavailable"}
        sub={
          h.network?.addresses && h.network.addresses.length > 1
            ? `+${h.network.addresses.length - 1} more address${h.network.addresses.length - 1 === 1 ? "" : "es"}`
            : "Primary IPv4"
        }
      />
    </div>
  );
}
