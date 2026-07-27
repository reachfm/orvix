import { useState, useEffect, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Server, Activity, RefreshCw, RotateCw, AlertTriangle, CheckCircle2, XCircle, ShieldAlert } from "lucide-react";
import { api } from "../api";

const PORTS = [
  { port: 25, service: "SMTP", proto: "tcp" },
  { port: 587, service: "Submission", proto: "tcp" },
  { port: 465, service: "SMTPS", proto: "tcp" },
  { port: 143, service: "IMAP", proto: "tcp" },
  { port: 993, service: "IMAPS", proto: "tcp" },
  { port: 110, service: "POP3", proto: "tcp" },
  { port: 995, service: "POP3S", proto: "tcp" },
  { port: 4190, service: "ManageSieve", proto: "tcp" },
  { port: 80, service: "HTTP", proto: "tcp" },
  { port: 443, service: "HTTPS", proto: "tcp" },
];

interface PortStatus {
  port: number;
  service: string;
  status: "open" | "filtered" | "closed";
  latency_ms?: number;
}

const statusBadge = (s: string) => {
  if (s === "open") return { cls: "bg-[var(--status-success)]/15 text-[var(--status-success)] border-[var(--status-success)]/30", icon: CheckCircle2, glow: "shadow-[0_0_8px_rgba(52,211,153,0.3)]" };
  if (s === "filtered") return { cls: "bg-[var(--accent-yellow)]/15 text-[var(--accent-yellow)] border-[var(--accent-yellow)]/30", icon: ShieldAlert, glow: "shadow-[0_0_8px_rgba(251,191,36,0.3)]" };
  return { cls: "bg-[var(--status-danger)]/15 text-[var(--status-danger)] border-[var(--status-danger)]/30", icon: XCircle, glow: "shadow-[0_0_8px_rgba(251,113,133,0.3)]" };
};

function SkeletonCard() {
  return <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 animate-pulse"><div className="h-4 bg-[var(--border)] rounded w-1/3 mb-3" /><div className="h-3 bg-[var(--border)] rounded w-1/2 mb-2" /><div className="h-3 bg-[var(--border)] rounded w-1/4" /></div>;
}

export default function PortsServicesPage() {
  const queryClient = useQueryClient();
  const [restarting, setRestarting] = useState<string | null>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  const { data: portData, isLoading: portsLoading } = useQuery({
    queryKey: ["port-status"], queryFn: () => api.getPortStatus() ?? Promise.resolve([]), refetchInterval: 10000,
  });
  const { data: healthData, isLoading: healthLoading } = useQuery({
    queryKey: ["service-health"], queryFn: () => api.getServiceHealth() ?? Promise.resolve({}), refetchInterval: 10000,
  });

  useEffect(() => {
    intervalRef.current = setInterval(() => { queryClient.invalidateQueries({ queryKey: ["port-status"] }); queryClient.invalidateQueries({ queryKey: ["service-health"] }); }, 10000);
    return () => { if (intervalRef.current) clearInterval(intervalRef.current); };
  }, [queryClient]);

  const restartMutation = useMutation({
    mutationFn: (name: string) => api.restartService(name) ?? Promise.resolve(),
    onSuccess: () => { setRestarting(null); queryClient.invalidateQueries({ queryKey: ["service-health"] }); },
  });

  const ports: PortStatus[] = Array.isArray(portData) ? portData : PORTS.map((p) => ({ ...p, status: "closed" as const }));
  const health: any = healthData || {};

  return (
    <div className="space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">Ops Command</p>
        <h1 className="text-2xl font-bold text-[var(--text-primary)]">Ports & Services</h1>
        <p className="mt-1 text-sm text-[var(--text-secondary)]">Live connectivity surveillance with auto-refresh</p>
      </div>

      <div className="flex items-center gap-3 text-xs text-[var(--text-muted)]">
        <Activity size={14} className="text-[var(--accent)]" />
        <span>Auto-refreshing every 10s</span>
        <span>·</span>
        <span>Last check: {new Date().toLocaleTimeString()}</span>
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
        {portsLoading ? Array.from({ length: 10 }).map((_, i) => <SkeletonCard key={i} />) : ports.map((p) => {
          const badge = statusBadge(p.status);
          const Icon = badge.icon;
          return (
            <div key={p.port} className={`rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 ${p.status === "open" ? badge.glow : ""}`}>
              <div className="flex items-center justify-between mb-2">
                <span className="text-sm font-medium text-[var(--text-primary)]">{p.port}</span>
                <Icon size={16} className={badge.cls.split(" ")[1]} />
              </div>
              <p className="text-xs text-[var(--text-secondary)]">{p.service}</p>
              <span className={`mt-2 inline-block text-[10px] px-2 py-0.5 rounded-full border ${badge.cls}`}>{p.status}</span>
              {p.latency_ms !== undefined && <p className="text-[11px] text-[var(--text-muted)] mt-1">{p.latency_ms}ms</p>}
            </div>
          );
        })}
      </div>

      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-4">Service Health</h3>
        {healthLoading ? <SkeletonCard /> : (
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
            {["smtp", "imap", "antispam", "delivery"].map((svc) => {
              const status = (health as any)?.[svc]?.status ?? "unknown";
              const uptime = (health as any)?.[svc]?.uptime ?? 0;
              return (
                <div key={svc} className="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-3">
                  <div className="flex items-center justify-between mb-2">
                    <span className="text-xs font-medium uppercase text-[var(--text-primary)]">{svc}</span>
                    <span className={`w-2 h-2 rounded-full ${status === "ok" || status === "active" ? "bg-[var(--status-success)] shadow-[0_0_6px_rgba(52,211,153,0.5)]" : status === "degraded" ? "bg-[var(--accent-yellow)]" : "bg-[var(--status-danger)]"}`} />
                  </div>
                  <p className="text-xs text-[var(--text-muted)]">{status}</p>
                  <p className="text-xs text-[var(--text-muted)] mt-1">Uptime: {uptime}%</p>
                  <button onClick={() => { setRestarting(svc); restartMutation.mutate(svc); }} disabled={restarting === svc} className="mt-2 text-[11px] text-[var(--accent)] hover:underline flex items-center gap-1">
                    <RotateCw size={12} className={restarting === svc ? "animate-spin" : ""} /> Restart
                  </button>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
