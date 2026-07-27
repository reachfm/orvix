import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle, CheckCircle2, Clock, Mail, RefreshCw, Search,
  Trash2, XCircle, Zap,
} from "lucide-react";
import { useState } from "react";
import { api } from "../api";

const STATUS_STYLES: Record<string, { color: string; bg: string; label: string }> = {
  queued: { color: "var(--accent-blue)", bg: "rgba(91,145,255,0.12)", label: "Queued" },
  deferred: { color: "var(--accent-yellow)", bg: "rgba(251,191,36,0.12)", label: "Deferred" },
  active: { color: "var(--accent)", bg: "rgba(0,201,167,0.12)", label: "Active" },
  failed: { color: "var(--status-danger)", bg: "rgba(251,113,133,0.12)", label: "Failed" },
  bounced: { color: "var(--status-danger)", bg: "rgba(251,113,133,0.12)", label: "Bounced" },
};

export default function MailQueuePage() {
  const qc = useQueryClient();
  const [statusFilter, setStatusFilter] = useState("all");
  const [search, setSearch] = useState("");
  const [confirmFlush, setConfirmFlush] = useState(false);

  const { data, isLoading, refetch } = useQuery({
    queryKey: ["smtpQueue", statusFilter],
    queryFn: () => api.listSmtpQueue({ status: statusFilter === "all" ? undefined : statusFilter, limit: 100 }),
    refetchInterval: 15_000,
  });

  const retryMut = useMutation({
    mutationFn: (id: string) => api.retrySmtpQueueItem(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["smtpQueue"] }),
  });
  const deleteMut = useMutation({
    mutationFn: (id: string) => api.deleteSmtpQueueItem(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["smtpQueue"] }),
  });
  const flushMut = useMutation({
    mutationFn: () => api.flushSmtpQueue(),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["smtpQueue"] }); setConfirmFlush(false); },
  });

  const items: any[] = (data as any)?.items || (data as any)?.messages || [];
  const total: number = (data as any)?.total ?? items.length;
  const deferred = items.filter((m) => m.status === "deferred").length;
  const failed = items.filter((m) => m.status === "failed").length;

  const filtered = items.filter((m) => {
    if (!search) return true;
    const s = search.toLowerCase();
    return (m.recipient || m.to || "").toLowerCase().includes(s) ||
      (m.sender || m.from || "").toLowerCase().includes(s) ||
      (m.subject || "").toLowerCase().includes(s);
  });

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)]">Mail Services</p>
          <h1 className="text-2xl font-bold text-[var(--text-primary)]">SMTP Queue</h1>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">
            Monitor and manage outbound mail queue — retry, delete, or flush messages
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => refetch()}
            className="flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2 text-sm text-[var(--text-secondary)] transition-colors hover:border-[var(--accent)]/40 hover:text-[var(--text-primary)]"
          >
            <RefreshCw size={14} />
            Refresh
          </button>
          {!confirmFlush ? (
            <button
              onClick={() => setConfirmFlush(true)}
              className="flex items-center gap-2 rounded-lg border border-[var(--status-danger)]/30 bg-[var(--status-danger)]/10 px-3 py-2 text-sm text-[var(--status-danger)] transition-colors hover:bg-[var(--status-danger)]/20"
            >
              <Trash2 size={14} />
              Flush queue
            </button>
          ) : (
            <div className="flex items-center gap-2 rounded-lg border border-[var(--status-danger)] bg-[var(--status-danger)]/10 px-3 py-2">
              <span className="text-sm text-[var(--status-danger)]">Confirm flush?</span>
              <button onClick={() => flushMut.mutate()} disabled={flushMut.isPending} className="text-xs font-semibold text-[var(--status-danger)] underline">
                {flushMut.isPending ? "Flushing…" : "Yes, flush"}
              </button>
              <button onClick={() => setConfirmFlush(false)} className="text-xs text-[var(--text-muted)] underline">Cancel</button>
            </div>
          )}
        </div>
      </div>

      {/* Stats Row */}
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        {[
          { label: "Total queued", value: total, icon: Mail, color: "var(--accent-blue)" },
          { label: "Deferred", value: deferred, icon: Clock, color: "var(--accent-yellow)" },
          { label: "Failed", value: failed, icon: XCircle, color: "var(--status-danger)" },
          { label: "Active", value: items.filter((m) => m.status === "active").length, icon: Zap, color: "var(--accent)" },
        ].map((s) => {
          const Icon = s.icon;
          return (
            <div key={s.label} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-xs text-[var(--text-secondary)]">{s.label}</span>
                <Icon size={16} style={{ color: s.color }} />
              </div>
              <p className="text-2xl font-bold text-[var(--text-primary)]">{s.value}</p>
            </div>
          );
        })}
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-48">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search recipient, sender, subject…"
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] py-2 pl-9 pr-3 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none"
          />
        </div>
        <div className="flex items-center gap-1 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-1">
          {["all", "queued", "deferred", "active", "failed"].map((s) => (
            <button
              key={s}
              onClick={() => setStatusFilter(s)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-all ${
                statusFilter === s
                  ? "bg-[var(--accent)] text-[var(--bg-base)]"
                  : "text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              }`}
            >
              {s.charAt(0).toUpperCase() + s.slice(1)}
            </button>
          ))}
        </div>
      </div>

      {/* Queue Table */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] bg-[var(--bg-base)]">
                {["Recipient", "Sender", "Subject", "Status", "Attempts", "Next retry", "Actions"].map((h) => (
                  <th key={h} className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-[0.06em] text-[var(--text-muted)]">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]">
              {isLoading ? (
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i}>
                    {Array.from({ length: 7 }).map((__, j) => (
                      <td key={j} className="px-4 py-3">
                        <div className="h-4 animate-pulse rounded bg-[var(--border)]" />
                      </td>
                    ))}
                  </tr>
                ))
              ) : filtered.length === 0 ? (
                <tr>
                  <td colSpan={7} className="px-4 py-12 text-center">
                    <CheckCircle2 size={32} className="mx-auto mb-3 text-[var(--status-success)]" />
                    <p className="text-sm font-medium text-[var(--text-primary)]">Queue is empty</p>
                    <p className="mt-1 text-xs text-[var(--text-muted)]">No messages matching your filter</p>
                  </td>
                </tr>
              ) : (
                filtered.map((m, i) => {
                  const st = STATUS_STYLES[m.status] || STATUS_STYLES.queued;
                  return (
                    <tr key={m.id || i} className="group transition-colors hover:bg-[var(--bg-base)]">
                      <td className="px-4 py-3 font-medium text-[var(--text-primary)]">{m.recipient || m.to || "—"}</td>
                      <td className="px-4 py-3 text-[var(--text-secondary)]">{m.sender || m.from || "—"}</td>
                      <td className="px-4 py-3 max-w-[180px] truncate text-[var(--text-secondary)]">{m.subject || "—"}</td>
                      <td className="px-4 py-3">
                        <span className="rounded-full px-2.5 py-0.5 text-xs font-semibold" style={{ color: st.color, backgroundColor: st.bg }}>
                          {st.label}
                        </span>
                      </td>
                      <td className="px-4 py-3 text-[var(--text-muted)]">{m.attempts ?? 0}</td>
                      <td className="px-4 py-3 text-xs text-[var(--text-muted)]">
                        {m.next_retry ? new Date(m.next_retry).toLocaleString() : "—"}
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
                          <button
                            onClick={() => retryMut.mutate(m.id)}
                            disabled={retryMut.isPending}
                            title="Retry now"
                            className="rounded p-1.5 text-[var(--accent)] hover:bg-[var(--accent)]/10 transition-colors"
                          >
                            <RefreshCw size={13} />
                          </button>
                          <button
                            onClick={() => deleteMut.mutate(m.id)}
                            disabled={deleteMut.isPending}
                            title="Delete"
                            className="rounded p-1.5 text-[var(--status-danger)] hover:bg-[var(--status-danger)]/10 transition-colors"
                          >
                            <Trash2 size={13} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
        {filtered.length > 0 && (
          <div className="border-t border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-xs text-[var(--text-muted)]">
            Showing {filtered.length} of {total} messages · Auto-refreshes every 15 seconds
            {failed > 0 && (
              <span className="ml-3 text-[var(--status-danger)]">
                <AlertTriangle size={12} className="inline mr-1" />
                {failed} failed messages need attention
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
