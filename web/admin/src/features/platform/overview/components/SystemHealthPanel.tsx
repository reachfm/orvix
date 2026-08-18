import { AlertCircle, CheckCircle2, ChevronRight, HelpCircle, Loader2, XCircle } from "lucide-react";
import { useMonitoringHealthQuery } from "../../monitoring/queries";
import type { ComponentHealth } from "../../monitoring/contract";

type Status = "ok" | "warning" | "critical" | "unknown";

function normalizeStatus(s: string): Status {
  if (s === "ok") return "ok";
  if (s === "warning" || s === "degraded") return "warning";
  if (s === "critical" || s === "down") return "critical";
  return "unknown";
}

const STATUS_STYLE: Record<Status, { label: string; fg: string; bg: string; Icon: typeof CheckCircle2 }> = {
  ok: { label: "Healthy", fg: "var(--success)", bg: "var(--accent-emerald-soft)", Icon: CheckCircle2 },
  warning: { label: "Warning", fg: "var(--warning)", bg: "var(--accent-amber-soft)", Icon: AlertCircle },
  critical: { label: "Critical", fg: "var(--danger)", bg: "var(--accent-orange-soft)", Icon: XCircle },
  unknown: { label: "Unknown", fg: "var(--text-muted)", bg: "var(--bg-subtle)", Icon: HelpCircle },
};

function StatusPill({ status }: { status: string }) {
  const s = normalizeStatus(status);
  const { label, fg, bg, Icon } = STATUS_STYLE[s];
  return (
    <span
      className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium"
      style={{ color: fg, backgroundColor: bg }}
    >
      <Icon size={12} aria-hidden="true" />
      {label}
    </span>
  );
}

// SystemHealthPanel is a compact real-data status summary — it does
// NOT duplicate the full Health page's disk/runtime detail, only the
// component-level status pills plus a link to the real Health
// destination for anyone who wants the full picture.
export default function SystemHealthPanel({ onNavigate }: { onNavigate: () => void }) {
  const healthQ = useMonitoringHealthQuery();

  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-2xl p-6">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">System Health</h3>
        <button
          onClick={onNavigate}
          className="inline-flex items-center gap-0.5 text-xs font-medium text-[var(--accent)] hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--accent)] rounded"
        >
          View Health <ChevronRight size={13} />
        </button>
      </div>

      {healthQ.isLoading && (
        <div className="flex items-center gap-2 text-[var(--text-muted)] text-sm py-4" role="status">
          <Loader2 size={15} className="animate-spin" /> Loading system health…
        </div>
      )}
      {healthQ.error && (
        <div className="flex items-center gap-2 text-[var(--danger)] text-sm py-2" role="alert">
          <AlertCircle size={15} /> Failed to load system health: {(healthQ.error as Error).message}
        </div>
      )}
      {healthQ.data && (
        <div className="space-y-3">
          <div className="flex items-center justify-between pb-3 border-b border-[var(--border)]">
            <span className="text-sm text-[var(--text-secondary)]">Overall status</span>
            <StatusPill status={healthQ.data.status} />
          </div>
          <ComponentRow label="API" health={healthQ.data.api} />
          <ComponentRow label="Database" health={healthQ.data.db} />
          <ComponentRow label="Queue" health={healthQ.data.queue} />
          <ComponentRow label="Backup" health={healthQ.data.backup} />
        </div>
      )}
    </div>
  );
}

function ComponentRow({ label, health }: { label: string; health: ComponentHealth }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-sm text-[var(--text-secondary)]">{label}</span>
      <StatusPill status={health?.status ?? "unknown"} />
    </div>
  );
}
