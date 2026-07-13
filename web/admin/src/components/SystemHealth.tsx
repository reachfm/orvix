import { useState, useEffect } from "react";
import {
  HeartPulse,
  CheckCircle2,
  AlertTriangle,
  XCircle,
  Server,
  Database,
  HardDrive,
  Clock,
  Loader2,
  AlertCircle,
  Inbox,
} from "lucide-react";

interface HealthData {
  status: string;
  uptime: string;
  diskUsage: number;
  diskTotal: number;
  dbStatus: string;
  queueStatus: string;
  version: string;
}

function formatUptime(uptime: string): string {
  if (!uptime) return "—";
  const match = uptime.match(/(\d+)h(\d+)m(\d+)s/);
  if (match) return `${match[1]}h ${match[2]}m ${match[3]}s`;
  return uptime;
}

function formatGB(bytes: number): string {
  if (!bytes || bytes <= 0) return "—";
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

function formatPercent(usage: number, total: number): number {
  if (!total || total <= 0) return 0;
  return Math.round((usage / total) * 100);
}

export default function SystemHealth() {
  const [data, setData] = useState<HealthData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/api/v1/monitoring/health")
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then((json) => {
        setData(json);
        setLoading(false);
      })
      .catch((err) => {
        setError(err.message);
        setLoading(false);
      });
  }, []);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 size={24} className="text-[#4F7CFF] animate-spin" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="bg-[#13161C] border border-[#F87171]/30 rounded-xl p-6 flex items-center gap-3">
        <AlertCircle size={20} className="text-[#F87171]" />
        <span className="text-[#F87171] text-sm">Failed to load health data: {error}</span>
      </div>
    );
  }

  if (!data) return null;

  const isHealthy = data.status === "ok" || data.status === "healthy";
  const diskPct = formatPercent(data.diskUsage, data.diskTotal);

  const indicators = [
    {
      label: "System",
      status: isHealthy ? "ok" : "degraded",
      icon: Server,
    },
    {
      label: "Database",
      status: data.dbStatus === "connected" ? "ok" : "error",
      icon: Database,
    },
    {
      label: "Queue",
      status: data.queueStatus === "ok" || data.queueStatus === "healthy" ? "ok" : "warning",
      icon: Inbox,
    },
    {
      label: "Disk",
      status: diskPct > 90 ? "error" : diskPct > 75 ? "warning" : "ok",
      icon: HardDrive,
    },
  ];

  const statusIcon = (status: string) => {
    if (status === "ok") return <CheckCircle2 size={14} className="text-[#34D399]" />;
    if (status === "warning" || status === "degraded")
      return <AlertTriangle size={14} className="text-[#FBBF24]" />;
    return <XCircle size={14} className="text-[#F87171]" />;
  };

  const statusColor = (status: string) => {
    if (status === "ok") return "bg-[#34D399]/10 text-[#34D399]";
    if (status === "warning" || status === "degraded")
      return "bg-[#FBBF24]/10 text-[#FBBF24]";
    return "bg-[#F87171]/10 text-[#F87171]";
  };

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0] flex items-center gap-2">
        <HeartPulse size={24} className="text-[#4F7CFF]" />
        System Health
      </h2>

      <div className="grid grid-cols-4 gap-4 mb-6">
        {indicators.map((ind) => {
          const Icon = ind.icon;
          return (
            <div
              key={ind.label}
              className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4"
            >
              <div className="flex items-center gap-2 mb-2">
                <Icon size={16} className="text-[#4F7CFF]" />
                <span className="text-xs text-[#8B92A8]">{ind.label}</span>
              </div>
              <div className="flex items-center gap-1.5">
                {statusIcon(ind.status)}
                <span
                  className={`text-xs font-medium px-2 py-0.5 rounded-full ${statusColor(ind.status)}`}
                >
                  {ind.status}
                </span>
              </div>
            </div>
          );
        })}
      </div>

      <div className="grid grid-cols-2 gap-6">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">System Info</h3>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-sm text-[#8B92A8]">Version</span>
              <span className="text-sm text-[#E8EAF0]">{data.version || "—"}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm text-[#8B92A8]">Uptime</span>
              <span className="text-sm text-[#E8EAF0] flex items-center gap-1">
                <Clock size={12} className="text-[#555D73]" />
                {formatUptime(data.uptime)}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm text-[#8B92A8]">Overall Status</span>
              <span
                className={`px-2 py-1 text-xs rounded-full ${
                  isHealthy
                    ? "bg-[#34D399]/10 text-[#34D399]"
                    : "bg-[#FBBF24]/10 text-[#FBBF24]"
                }`}
              >
                {data.status || "unknown"}
              </span>
            </div>
          </div>
        </div>

        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">Disk Usage</h3>
          <div>
            <div className="flex justify-between text-xs mb-2">
              <span className="text-[#8B92A8]">
                {formatGB(data.diskUsage)} / {formatGB(data.diskTotal)}
              </span>
              <span
                className={
                  diskPct > 90
                    ? "text-[#F87171]"
                    : diskPct > 75
                      ? "text-[#FBBF24]"
                      : "text-[#34D399]"
                }
              >
                {diskPct}%
              </span>
            </div>
            <div className="h-2 bg-[#222736] rounded-full overflow-hidden">
              <div
                className={`h-full rounded-full transition-all ${
                  diskPct > 90
                    ? "bg-[#F87171]"
                    : diskPct > 75
                      ? "bg-[#FBBF24]"
                      : "bg-[#34D399]"
                }`}
                style={{ width: `${Math.min(diskPct, 100)}%` }}
              />
            </div>
          </div>

          <div className="mt-6 space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-sm text-[#8B92A8]">DB Status</span>
              <span
                className={`text-xs px-2 py-1 rounded-full ${
                  data.dbStatus === "connected"
                    ? "bg-[#34D399]/10 text-[#34D399]"
                    : "bg-[#F87171]/10 text-[#F87171]"
                }`}
              >
                {data.dbStatus || "—"}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm text-[#8B92A8]">Queue Status</span>
              <span
                className={`text-xs px-2 py-1 rounded-full ${
                  data.queueStatus === "ok" || data.queueStatus === "healthy"
                    ? "bg-[#34D399]/10 text-[#34D399]"
                    : "bg-[#FBBF24]/10 text-[#FBBF24]"
                }`}
              >
                {data.queueStatus || "—"}
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
