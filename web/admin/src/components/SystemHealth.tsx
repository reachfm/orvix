import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { AlertCircle, AlertTriangle, CheckCircle2, Clock, Database, HardDrive, HeartPulse, Inbox, Loader2, Server, XCircle } from "lucide-react";

interface ComponentHealth { status: string; message: string }
interface DiskUsage { label: string; totalBytes: number; usedBytes: number; freeBytes: number; usedPct: number }
interface HealthData {
  status: string;
  uptimeSeconds: number;
  disk: DiskUsage[];
  db: ComponentHealth;
  queue: ComponentHealth;
  backup: ComponentHealth;
  api: ComponentHealth;
  openAlerts: number;
}

const formatGB = (bytes: number) => bytes > 0 ? `${(bytes / 1073741824).toFixed(1)} GB` : "Unavailable";
const formatUptime = (seconds: number) => Number.isFinite(seconds) && seconds >= 0 ? `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m` : "Unavailable";

export default function SystemHealth() {
  const [data, setData] = useState<HealthData | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    fetch("/api/v1/monitoring/health").then((res) => {
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      return res.json();
    }).then(setData).catch((err: Error) => setError(err.message));
  }, []);
  if (!data && !error) return <div className="flex h-64 items-center justify-center"><Loader2 size={24} className="animate-spin text-[var(--accent)]" /></div>;
  if (error) return <div className="flex items-center gap-3 rounded-lg border border-[var(--danger)]/30 bg-[var(--bg-surface)] p-6 text-sm text-[var(--danger)]"><AlertCircle size={20} />Failed to load health data: {error}</div>;
  if (!data) return null;

  const disk = data.disk?.[0];
  const indicators = [
    { label: "System", status: data.status, icon: Server },
    { label: "Database", status: data.db?.status ?? "unknown", icon: Database },
    { label: "Queue", status: data.queue?.status ?? "unknown", icon: Inbox },
    { label: "Disk", status: disk ? (disk.usedPct >= 95 ? "critical" : disk.usedPct >= 85 ? "warning" : "ok") : "unknown", icon: HardDrive },
  ];
  return <div>
    <h2 className="mb-6 flex items-center gap-2 text-2xl font-semibold text-[var(--text-primary)]"><HeartPulse size={24} className="text-[var(--accent)]" />System Health</h2>
    <div className="mb-6 grid grid-cols-2 gap-4 lg:grid-cols-4">{indicators.map(({ label, status, icon: Icon }) => <div key={label} className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-4"><div className="mb-2 flex items-center gap-2"><Icon size={16} className="text-[var(--accent)]" /><span className="text-xs text-[var(--text-secondary)]">{label}</span></div><StatusBadge status={status} /></div>)}</div>
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
      <section className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-6"><h3 className="mb-4 text-sm font-medium text-[var(--text-primary)]">Runtime</h3><Info label="Uptime" value={formatUptime(data.uptimeSeconds)} icon={<Clock size={12} />} /><Info label="Open alerts" value={String(data.openAlerts)} /><Info label="API" value={data.api?.message || data.api?.status || "unknown"} /><Info label="Backup" value={data.backup?.message || data.backup?.status || "unknown"} /></section>
      <section className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-6"><h3 className="mb-4 text-sm font-medium text-[var(--text-primary)]">Disk usage</h3>{disk ? <><div className="mb-2 flex justify-between text-xs text-[var(--text-secondary)]"><span>{disk.label}: {formatGB(disk.usedBytes)} / {formatGB(disk.totalBytes)}</span><span>{disk.usedPct}%</span></div><div className="h-2 overflow-hidden rounded bg-[var(--bg-subtle)]"><div className="h-full bg-[var(--accent)]" style={{ width: `${Math.min(disk.usedPct, 100)}%` }} /></div></> : <p className="text-sm text-[var(--warning)]">Disk metrics unavailable</p>}<div className="mt-6"><Info label="Database" value={data.db?.message || data.db?.status || "unknown"} /><Info label="Queue" value={data.queue?.message || data.queue?.status || "unknown"} /></div></section>
    </div>
  </div>;
}

function StatusBadge({ status }: { status: string }) {
  if (status === "ok") return <span className="inline-flex items-center gap-1 text-xs text-[var(--success)]"><CheckCircle2 size={14} />ok</span>;
  if (status === "warning" || status === "degraded" || status === "unknown") return <span className="inline-flex items-center gap-1 text-xs text-[var(--warning)]"><AlertTriangle size={14} />{status}</span>;
  return <span className="inline-flex items-center gap-1 text-xs text-[var(--danger)]"><XCircle size={14} />{status}</span>;
}

function Info({ label, value, icon }: { label: string; value: string; icon?: ReactNode }) {
  return <div className="mb-3 flex items-center justify-between"><span className="text-sm text-[var(--text-secondary)]">{label}</span><span className="flex items-center gap-1 text-sm text-[var(--text-primary)]">{icon}{value}</span></div>;
}
