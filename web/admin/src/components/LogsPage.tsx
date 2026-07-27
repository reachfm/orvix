import { useState, useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ScrollText, Activity, Download, ChevronDown, ChevronRight } from "lucide-react";
import { api } from "../api";

const LEVEL_COLORS: Record<string, string> = {
  error: "bg-[var(--status-danger)]/15 text-[var(--status-danger)] border-[var(--status-danger)]/30 shadow-[0_0_6px_rgba(251,113,133,0.3)]",
  warn: "bg-[var(--accent-yellow)]/15 text-[var(--accent-yellow)] border-[var(--accent-yellow)]/30",
  info: "bg-[var(--accent-blue)]/15 text-[var(--accent-blue)] border-[var(--accent-blue)]/30",
  debug: "bg-[var(--text-muted)]/15 text-[var(--text-muted)] border-[var(--text-muted)]/30",
};

export default function LogsPage() {
  const queryClient = useQueryClient();
  const [service, setService] = useState("all");
  const [level, setLevel] = useState("all");
  const [search, setSearch] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [expanded, setExpanded] = useState<number | null>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  useEffect(() => {
    if (autoRefresh) { intervalRef.current = setInterval(() => queryClient.invalidateQueries({ queryKey: ["system-logs"] }), 5000); }
    return () => { if (intervalRef.current) clearInterval(intervalRef.current); };
  }, [autoRefresh, queryClient]);

  const { data: logs, isLoading } = useQuery({
    queryKey: ["system-logs", service, level, search],
    queryFn: () => api.getSystemLogs({ service: service !== "all" ? service : undefined, level: level !== "all" ? level : undefined, search, limit: 200 }) ?? Promise.resolve([]),
    refetchInterval: autoRefresh ? 5000 : false,
  });

  const logList: any[] = Array.isArray(logs) ? logs : [];
  const services = ["all","smtp","imap","antispam","api","auth","delivery"];
  const levels = ["all","error","warn","info","debug"];

  const handleDownload = () => {
    const text = logList.map((l: any) => `[${l.timestamp}] [${l.service}] [${l.level}] ${l.message}`).join("\n");
    const blob = new Blob([text], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a"); a.href = url; a.download = `logs-${new Date().toISOString().slice(0, 10)}.txt`; a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">System Diagnostics</p>
          <h1 className="text-2xl font-bold text-[var(--text-primary)]">Logs</h1>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">Platform system log viewer · {logList.length} entries</p>
        </div>
        <div className="flex gap-2">
          <button onClick={() => setAutoRefresh((v) => !v)} className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs border ${autoRefresh ? "border-[var(--accent)] bg-[var(--accent)]/10 text-[var(--accent)]" : "border-[var(--border)] text-[var(--text-secondary)]"}`}><Activity size={13} />{autoRefresh ? "Live" : "Auto-refresh"}</button>
          <button onClick={handleDownload} className="inline-flex items-center gap-1.5 border border-[var(--border)] text-[var(--text-secondary)] rounded-lg px-3 py-1.5 text-xs"><Download size={13} /> Download</button>
        </div>
      </div>

      <div className="flex flex-wrap gap-2 bg-[var(--bg-surface)] rounded-xl border border-[var(--border)] p-3">
        <select value={service} onChange={(e) => setService(e.target.value)} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-1.5 text-sm text-[var(--text-primary)]"><option value="all">All Services</option>{services.filter(s => s !== "all").map(s => <option key={s} value={s}>{s}</option>)}</select>
        <select value={level} onChange={(e) => setLevel(e.target.value)} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-1.5 text-sm text-[var(--text-primary)]"><option value="all">All Levels</option>{levels.filter(l => l !== "all").map(l => <option key={l} value={l}>{l}</option>)}</select>
        <input placeholder="Search logs..." value={search} onChange={(e) => setSearch(e.target.value)} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-1.5 text-sm text-[var(--text-primary)] flex-1 min-w-[150px]" />
        <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-1.5 text-sm text-[var(--text-primary)] w-36" />
        <input type="date" value={to} onChange={(e) => setTo(e.target.value)} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-1.5 text-sm text-[var(--text-primary)] w-36" />
      </div>

      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead><tr className="border-b border-[var(--border)]">{["Time","Service","Level","Message",""].map((h) => <th key={h} className="text-left px-4 py-3 text-[var(--text-muted)] text-xs uppercase tracking-[0.06em]">{h}</th>)}</tr></thead>
            <tbody>
              {isLoading ? Array.from({ length: 8 }).map((_, i) => <tr key={i} className="border-b border-[var(--border)]"><td colSpan={5}><div className="h-8 bg-[var(--border)] rounded animate-pulse m-2" /></td></tr>) : logList.length === 0 ? (
                <tr><td colSpan={5} className="px-4 py-12 text-center"><ScrollText size={36} className="mx-auto text-[var(--text-muted)] opacity-40 mb-3" /><p className="text-[var(--text-muted)]">No log entries found</p></td></tr>
              ) : logList.map((l: any, i: number) => (<tr key={i} className={`border-b border-[var(--border)] hover:bg-[var(--bg-base)] ${l.level === "error" ? "bg-[var(--status-danger)]/5" : ""}`}><td className="px-4 py-2.5 text-xs text-[var(--text-muted)] font-mono whitespace-nowrap">{l.timestamp?.slice(11, 19) || l.timestamp}</td><td className="px-4 py-2.5"><span className="text-[11px] px-2 py-0.5 rounded-full bg-[var(--accent)]/10 text-[var(--accent)]">{l.service}</span></td><td className="px-4 py-2.5"><span className={`text-[11px] px-2 py-0.5 rounded-full border ${LEVEL_COLORS[l.level] || LEVEL_COLORS.info}`}>{l.level}</span></td><td className="px-4 py-2.5 text-[var(--text-primary)] text-xs max-w-[400px] truncate">{l.message}</td><td className="px-4 py-2.5"><button onClick={() => setExpanded(expanded === i ? null : i)} className="text-[var(--text-muted)] hover:text-[var(--text-primary)]">{expanded === i ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</button></td></tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
