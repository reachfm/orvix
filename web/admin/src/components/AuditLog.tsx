import { useState, useEffect, useRef, useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ClipboardList, Download, Activity } from "lucide-react";
import { api } from "../api";
import PageHeader from "./ui/PageHeader";
import FilterBar from "./ui/FilterBar";
import DataTable from "./ui/DataTable";
import Pagination from "./ui/Pagination";
import Drawer from "./ui/Drawer";
import Badge from "./ui/Badge";
import Button from "./ui/Button";
import EmptyState from "./ui/EmptyState";
import ErrorBanner from "./ui/ErrorBanner";
import type { AuditLog } from "../types/audit";

function formatAction(action: string): string {
  return action.split(".").map((p) => p.charAt(0).toUpperCase() + p.slice(1).replace(/_/g, " ")).join(" → ");
}

function relativeTime(ts: string): string {
  const diff = Date.now() - new Date(ts).getTime();
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  return `${day}d ago`;
}

const RESULT_BADGE: Record<string, "teal" | "danger" | "warning"> = { success: "teal", failure: "danger", denied: "warning" };

export default function AuditLog() {
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [actionFilter, setActionFilter] = useState("");
  const [actorFilter, setActorFilter] = useState("");
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [debouncedAction, setDebouncedAction] = useState("");
  const [debouncedActor, setDebouncedActor] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [liveEnabled, setLiveEnabled] = useState(false);
  const liveRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  // Debounce both filters together
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => { setDebouncedAction(actionFilter); setDebouncedActor(actorFilter); setPage(1); }, 300);
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current); };
  }, [actionFilter, actorFilter]);

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ["admin-audit", page, debouncedAction, debouncedActor, fromDate, toDate],
    queryFn: () => api.listAdminAuditLogs({ page, limit: 50, action: debouncedAction || undefined, actor: debouncedActor || undefined, from: fromDate || undefined, to: toDate || undefined }),
  });

  const logs: AuditLog[] = data?.logs || (Array.isArray(data) ? data : []);
  const total = data?.total || 0;

  // Live refresh
  useEffect(() => {
    if (liveEnabled) { liveRef.current = setInterval(() => queryClient.invalidateQueries({ queryKey: ["admin-audit"] }), 30000); }
    return () => { if (liveRef.current) clearInterval(liveRef.current); };
  }, [liveEnabled, queryClient]);

  const exportCsv = useCallback(() => {
    const headers = ["ID", "Timestamp", "Action", "Actor", "Target", "Result"];
    const rows = logs.map((l) => [l.id, l.timestamp, l.action, l.actor, l.target, l.result]);
    const csv = [headers.join(","), ...rows.map((r) => r.map((v) => `"${String(v).replace(/"/g, '""')}"`).join(","))].join("\n");
    const blob = new Blob([csv], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a"); a.href = url; a.download = `audit-log-${new Date().toISOString().slice(0, 10)}.csv`; a.click();
    URL.revokeObjectURL(url);
  }, [logs]);

  const [selectedLog, setSelectedLog] = useState<AuditLog | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);

  const openDetail = (row: AuditLog) => { setSelectedLog(row); setDrawerOpen(true); };

  const cols = [
    { key: "timestamp", label: "Time", render: (row: AuditLog) => <span title={new Date(row.timestamp).toLocaleString()} className="text-xs text-[var(--text-muted)]">{relativeTime(row.timestamp)}</span> },
    { key: "actor", label: "Actor", render: (row: AuditLog) => <span className="text-sm">{row.actor}</span> },
    { key: "role", label: "Role", render: (row: AuditLog) => row.role ? <Badge variant={row.role === "superadmin" || row.role === "platform_super_admin" ? "purple" : row.role === "admin" ? "blue" : "neutral"}>{row.role}</Badge> : null },
    { key: "action", label: "Action", render: (row: AuditLog) => <span className="font-mono text-xs text-[var(--accent-blue)]" title={row.action}>{formatAction(row.action)}</span> },
    { key: "target", label: "Target" },
    { key: "result", label: "Result", render: (row: AuditLog) => <Badge variant={RESULT_BADGE[row.result] || "neutral"}>{row.result}</Badge> },
    { key: "actions", label: "", width: "60px", render: () => <Button variant="ghost" size="sm">View</Button> },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Audit Log" subtitle="Platform-wide operator and system audit trail"
        actions={
          <div className="flex gap-2">
            <Button variant={liveEnabled ? "primary" : "ghost"} size="sm" onClick={() => setLiveEnabled((v) => !v)} iconLeft={<Activity size={14} />}>
              {liveEnabled ? "Live" : "Auto-refresh"}
            </Button>
            <Button variant="secondary" size="sm" onClick={exportCsv} iconLeft={<Download size={14} />}>Export CSV</Button>
          </div>
        }
      />

      {isError && <ErrorBanner message={(error as any)?.message || "Failed to load audit logs"} onRetry={() => queryClient.invalidateQueries({ queryKey: ["admin-audit"] })} />}

      <FilterBar
        search={{ value: actionFilter, onChange: setActionFilter, placeholder: "Filter by action..." }}
        onClear={() => { setActionFilter(""); setActorFilter(""); setFromDate(""); setToDate(""); }}
      >
        <input value={actorFilter} onChange={(e) => setActorFilter(e.target.value)} placeholder="Actor..." className="orvix-input w-40" />
        <input type="date" value={fromDate} onChange={(e) => { setFromDate(e.target.value); setPage(1); }} className="orvix-input w-36" title="From date" />
        <input type="date" value={toDate} onChange={(e) => { setToDate(e.target.value); setPage(1); }} className="orvix-input w-36" title="To date" />
      </FilterBar>

      <DataTable columns={cols} rows={logs} loading={isLoading} onRowClick={openDetail}
        emptyState={<EmptyState icon={ClipboardList} title="No audit entries found" />}
      />

      <Pagination pagination={{ page, pageSize: 50, total }} onPageChange={setPage} />

      <Drawer open={drawerOpen} onClose={() => setDrawerOpen(false)} title="Audit Entry">
        {selectedLog && (
          <div className="space-y-4 text-sm">
            <div className="grid grid-cols-2 gap-3">
              <div className="col-span-2"><span className="text-[var(--text-muted)]">ID:</span><span className="ml-1 text-[var(--text-primary)]">#{selectedLog.id}</span></div>
              <div className="col-span-2"><span className="text-[var(--text-muted)]">Timestamp:</span><span className="ml-1 text-[var(--text-primary)]">{new Date(selectedLog.timestamp).toLocaleString()}</span></div>
              <div><span className="text-[var(--text-muted)]">Action:</span><span className="ml-1 font-mono text-xs text-[var(--accent-blue)]">{selectedLog.action}</span></div>
              <div><span className="text-[var(--text-muted)]">Actor:</span><span className="ml-1 text-[var(--text-primary)]">{selectedLog.actor}</span></div>
              <div><span className="text-[var(--text-muted)]">Role:</span><span className="ml-1">{selectedLog.role ? <Badge variant="neutral">{selectedLog.role}</Badge> : "—"}</span></div>
              <div><span className="text-[var(--text-muted)]">Result:</span><span className="ml-1"><Badge variant={RESULT_BADGE[selectedLog.result] || "neutral"}>{selectedLog.result}</Badge></span></div>
              <div className="col-span-2"><span className="text-[var(--text-muted)]">Target:</span><span className="ml-1 text-[var(--text-primary)]">{selectedLog.target || "—"}</span></div>
              {selectedLog.ip_address && <div className="col-span-2"><span className="text-[var(--text-muted)]">IP:</span><span className="ml-1 font-mono text-xs text-[var(--text-secondary)]">{selectedLog.ip_address}</span></div>}
            </div>
          </div>
        )}
      </Drawer>
    </div>
  );
}
