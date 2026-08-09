import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { HardDrive, RefreshCw, Loader2, AlertCircle, Download, ShieldCheck, Trash2, Server, Boxes, Network } from "lucide-react";
import { api } from "../api";
import ConfirmDialog from "./ConfirmDialog";

type Tab = "backups" | "updates" | "monitoring" | "storage" | "cluster";

function Loading() {
  return <div className="flex items-center justify-center h-32"><Loader2 size={20} className="text-[var(--accent)] animate-spin" /></div>;
}
function ErrorBox({ error }: { error: unknown }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-4 flex items-center gap-3">
      <AlertCircle size={18} className="text-[var(--danger)]" />
      <span className="text-[var(--danger)] text-sm">{(error as Error)?.message || "Failed to load"}</span>
    </div>
  );
}
function Empty({ text }: { text: string }) {
  return <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6 text-center text-[var(--text-secondary)] text-sm">{text}</div>;
}

function BackupsTab() {
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState<{ id: string; kind: "restore" | "delete" | "retention" } | null>(null);

  // Staggered rather than fired simultaneously: avoids hammering the
  // backend with a burst of parallel requests the instant this tab
  // mounts — the secondary panels are supplementary context, not needed
  // for the primary table's first paint.
  const listQ = useQuery<any>({ queryKey: ["backups"], queryFn: api.listBackups });
  const scheduleQ = useQuery<any>({ queryKey: ["backup-schedule"], queryFn: api.getBackupSchedule, enabled: listQ.isSuccess || listQ.isError });
  const metricsQ = useQuery<any>({ queryKey: ["backup-metrics"], queryFn: api.getBackupMetrics, enabled: listQ.isSuccess || listQ.isError });
  const healthQ = useQuery<any>({ queryKey: ["backup-health"], queryFn: api.getBackupHealth, enabled: scheduleQ.isSuccess || scheduleQ.isError });

  const invalidate = () => { qc.invalidateQueries({ queryKey: ["backups"] }); qc.invalidateQueries({ queryKey: ["backup-metrics"] }); };
  const createMut = useMutation({ mutationFn: () => api.createBackup(), onSuccess: invalidate });
  const nowMut = useMutation({ mutationFn: () => api.runBackupNow(), onSuccess: invalidate });
  const validateMut = useMutation({ mutationFn: (id: string) => api.validateBackup(id), onSuccess: invalidate });
  const restoreMut = useMutation({ mutationFn: (id: string) => api.restoreBackup(id), onSuccess: () => { invalidate(); setConfirm(null); } });
  const deleteMut = useMutation({ mutationFn: (id: string) => api.deleteBackup(id), onSuccess: () => { invalidate(); setConfirm(null); } });
  const retentionMut = useMutation({ mutationFn: () => api.runBackupRetention(), onSuccess: () => { invalidate(); setConfirm(null); } });

  const backups: any[] = Array.isArray(listQ.data) ? listQ.data : listQ.data?.backups ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div className="flex gap-4 text-xs text-[var(--text-secondary)]">
          {healthQ.data && <span>Health: <span className="text-[var(--text-primary)]">{String(healthQ.data.status ?? healthQ.data.ok ?? "unknown")}</span></span>}
          {metricsQ.data && <span>Total: <span className="text-[var(--text-primary)]">{metricsQ.data.total_backups ?? metricsQ.data.total ?? "—"}</span></span>}
          {scheduleQ.data && <span>Schedule: <span className="text-[var(--text-primary)]">{scheduleQ.data.cron ?? scheduleQ.data.schedule ?? "unset"}</span></span>}
        </div>
        <div className="flex gap-2">
          <button disabled={createMut.isPending} onClick={() => createMut.mutate()} className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-50">Create backup</button>
          <button disabled={nowMut.isPending} onClick={() => nowMut.mutate()} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded disabled:opacity-50">Run now</button>
          <button onClick={() => setConfirm({ id: "retention", kind: "retention" })} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded">Run retention</button>
        </div>
      </div>

      {listQ.isLoading ? <Loading /> : listQ.error ? <ErrorBox error={listQ.error} /> : backups.length === 0 ? <Empty text="No backups found" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Name</th><th className="text-left p-3 text-[var(--text-secondary)]">Status</th><th className="text-right p-3 text-[var(--text-secondary)]">Actions</th></tr></thead>
            <tbody>
              {backups.map((b: any) => (
                <tr key={b.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                  <td className="p-3 text-[var(--text-primary)]">{b.name || b.id}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{b.status}</td>
                  <td className="p-3 text-right space-x-1">
                    <a href={api.downloadBackupUrl(b.id)} title="Download" className="inline-block p-1.5 text-[var(--text-secondary)] hover:text-[var(--accent)]"><Download size={14} /></a>
                    <button title="Validate" disabled={validateMut.isPending} onClick={() => validateMut.mutate(b.id)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--success)]"><ShieldCheck size={14} /></button>
                    <button title="Restore" onClick={() => setConfirm({ id: b.id, kind: "restore" })} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--warning)]"><RefreshCw size={14} /></button>
                    <button title="Delete" onClick={() => setConfirm({ id: b.id, kind: "delete" })} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]"><Trash2 size={14} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <ConfirmDialog
        open={!!confirm}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={confirm?.kind === "restore" ? "Restore backup" : confirm?.kind === "delete" ? "Delete backup" : "Run retention"}
        description={
          confirm?.kind === "restore" ? "This restarts the service and restores this backup. On failure the service rolls back automatically." :
          confirm?.kind === "delete" ? "This permanently deletes the backup." :
          "This permanently deletes backups outside the retention window."
        }
        requireTypedName={confirm?.kind === "retention" ? "run-retention" : confirm?.id}
        danger
        pending={restoreMut.isPending || deleteMut.isPending || retentionMut.isPending}
        onConfirm={() => {
          if (!confirm) return;
          if (confirm.kind === "restore") restoreMut.mutate(confirm.id);
          else if (confirm.kind === "delete") deleteMut.mutate(confirm.id);
          else retentionMut.mutate();
        }}
      />
    </div>
  );
}

function UpdatesTab() {
  const [confirmRun, setConfirmRun] = useState(false);
  const qc = useQueryClient();
  // Staggered — see the comment on BackupsTab's queries above.
  const statusQ = useQuery<any>({ queryKey: ["update-status"], queryFn: api.getUpdateStatus });
  const checkQ = useQuery<any>({ queryKey: ["update-check"], queryFn: api.checkUpdates, enabled: statusQ.isSuccess || statusQ.isError });
  const historyQ = useQuery<any>({ queryKey: ["update-history"], queryFn: api.getUpdateHistory, enabled: checkQ.isSuccess || checkQ.isError });
  const preflightQ = useQuery<any>({ queryKey: ["update-preflight"], queryFn: api.getUpdatePreflight, enabled: historyQ.isSuccess || historyQ.isError });
  const checkNowMut = useMutation({ mutationFn: () => api.postUpdateCheck(), onSuccess: () => qc.invalidateQueries({ queryKey: ["update-check"] }) });
  const runMut = useMutation({ mutationFn: () => api.runUpdate(), onSuccess: () => { qc.invalidateQueries({ queryKey: ["update-status"] }); setConfirmRun(false); } });

  const history: any[] = Array.isArray(historyQ.data) ? historyQ.data : historyQ.data?.history ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div className="text-sm text-[var(--text-secondary)]">
          Status: <span className="text-[var(--text-primary)]">{statusQ.data?.status ?? (statusQ.isLoading ? "…" : "unknown")}</span>
          {checkQ.data?.available && <span className="ml-3 text-[var(--warning)]">Update available: {checkQ.data.latest_version}</span>}
        </div>
        <div className="flex gap-2">
          <button disabled={checkNowMut.isPending} onClick={() => checkNowMut.mutate()} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded disabled:opacity-50">Check now</button>
          <button onClick={() => setConfirmRun(true)} className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded">Run update</button>
        </div>
      </div>
      {preflightQ.data && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 mb-4 text-sm text-[var(--text-secondary)]">
          Preflight: <span className="text-[var(--text-primary)]">{preflightQ.data.ready === false ? "not ready" : "ready"}</span>
        </div>
      )}
      {historyQ.isLoading ? <Loading /> : historyQ.error ? <ErrorBox error={historyQ.error} /> : history.length === 0 ? <Empty text="No update history" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Version</th><th className="text-left p-3 text-[var(--text-secondary)]">Result</th><th className="text-left p-3 text-[var(--text-secondary)]">When</th></tr></thead>
            <tbody>{history.map((h: any, i: number) => (
              <tr key={i} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{h.version}</td><td className="p-3 text-[var(--text-secondary)]">{h.result || h.status}</td><td className="p-3 text-[var(--text-secondary)]">{h.applied_at || h.timestamp}</td></tr>
            ))}</tbody>
          </table>
        </div>
      )}
      <ConfirmDialog
        open={confirmRun}
        onOpenChange={setConfirmRun}
        title="Run update"
        description="This applies the pending update and may restart services."
        requireTypedName="run-update"
        danger
        pending={runMut.isPending}
        onConfirm={() => runMut.mutate()}
      />
    </div>
  );
}

function MonitoringTab() {
  const [sub, setSub] = useState<"alerts" | "capacity" | "snapshot" | "providers" | "deliveries">("alerts");
  const qc = useQueryClient();
  const alertsQ = useQuery<any>({ queryKey: ["mon-alerts"], queryFn: api.getMonitoringAlerts, enabled: sub === "alerts" });
  const capacityQ = useQuery<any>({ queryKey: ["mon-capacity"], queryFn: api.getMonitoringCapacity, enabled: sub === "capacity" });
  const snapshotQ = useQuery<any>({ queryKey: ["mon-snapshot"], queryFn: api.getMonitoringSnapshot, enabled: sub === "snapshot" });
  const providersQ = useQuery<any>({ queryKey: ["mon-providers"], queryFn: api.getMonitoringProviders, enabled: sub === "providers" });
  const deliveriesQ = useQuery<any[]>({ queryKey: ["mon-deliveries"], queryFn: api.listAlertDeliveries, enabled: sub === "deliveries" });
  const resolveMut = useMutation({ mutationFn: (id: string) => api.resolveMonitoringAlert(id), onSuccess: () => qc.invalidateQueries({ queryKey: ["mon-alerts"] }) });

  const alerts: any[] = Array.isArray(alertsQ.data) ? alertsQ.data : alertsQ.data?.alerts ?? [];

  return (
    <div>
      <div className="flex gap-1 mb-4">
        {(["alerts", "capacity", "snapshot", "providers", "deliveries"] as const).map((s) => (
          <button key={s} onClick={() => setSub(s)} className={`px-3 py-1.5 text-xs rounded capitalize ${sub === s ? "bg-[var(--bg-subtle)] text-[var(--text-primary)]" : "text-[var(--text-secondary)]"}`}>{s}</button>
        ))}
        <a href="/api/v1/metrics" target="_blank" rel="noreferrer" className="ml-auto px-3 py-1.5 text-xs text-[var(--accent)] hover:underline">Open raw metrics</a>
      </div>
      {sub === "alerts" && (alertsQ.isLoading ? <Loading /> : alertsQ.error ? <ErrorBox error={alertsQ.error} /> : alerts.length === 0 ? <Empty text="No active alerts" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Alert</th><th className="text-left p-3 text-[var(--text-secondary)]">Severity</th><th className="text-right p-3 text-[var(--text-secondary)]">Action</th></tr></thead>
          <tbody>{alerts.map((a: any) => (
            <tr key={a.id} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{a.title || a.name}</td><td className="p-3 text-[var(--text-secondary)]">{a.severity}</td>
              <td className="p-3 text-right"><button disabled={resolveMut.isPending} onClick={() => resolveMut.mutate(a.id)} className="text-xs text-[var(--accent)] hover:underline">Resolve</button></td></tr>
          ))}</tbody></table>
        </div>
      ))}
      {sub === "capacity" && (capacityQ.isLoading ? <Loading /> : capacityQ.error ? <ErrorBox error={capacityQ.error} /> : <pre className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 text-xs text-[var(--text-secondary)] overflow-auto">{JSON.stringify(capacityQ.data, null, 2)}</pre>)}
      {sub === "snapshot" && (snapshotQ.isLoading ? <Loading /> : snapshotQ.error ? <ErrorBox error={snapshotQ.error} /> : <pre className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 text-xs text-[var(--text-secondary)] overflow-auto">{JSON.stringify(snapshotQ.data, null, 2)}</pre>)}
      {sub === "providers" && (providersQ.isLoading ? <Loading /> : providersQ.error ? <ErrorBox error={providersQ.error} /> : <pre className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 text-xs text-[var(--text-secondary)] overflow-auto">{JSON.stringify(providersQ.data, null, 2)}</pre>)}
      {sub === "deliveries" && (deliveriesQ.isLoading ? <Loading /> : deliveriesQ.error ? <ErrorBox error={deliveriesQ.error} /> : (!deliveriesQ.data || deliveriesQ.data.length === 0) ? <Empty text="No alert deliveries" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden"><table className="w-full text-sm"><tbody>{deliveriesQ.data.map((d: any, i: number) => (
          <tr key={i} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{d.provider}</td><td className="p-3 text-[var(--text-secondary)]">{d.status}</td></tr>
        ))}</tbody></table></div>
      ))}
    </div>
  );
}

function StorageTab() {
  const q = useQuery<any[]>({ queryKey: ["storage-volumes"], queryFn: api.listStorageVolumes });
  const volumes = q.data ?? [];
  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : volumes.length === 0 ? <Empty text="No storage volumes reported" /> : (
    <div className="grid grid-cols-3 gap-4">
      {volumes.map((v: any, i: number) => (
        <div key={i} className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <p className="text-sm font-medium text-[var(--text-primary)] mb-1">{v.path || v.name}</p>
          <p className="text-xs text-[var(--text-secondary)]">{v.used_bytes ? `${(v.used_bytes / 1e9).toFixed(1)} GB used` : "usage unavailable"}</p>
        </div>
      ))}
    </div>
  );
}

function ClusterTab() {
  const q = useQuery<any>({ queryKey: ["cluster-status"], queryFn: api.getClusterStatus });
  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : (
    <pre className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 text-xs text-[var(--text-secondary)] overflow-auto">{JSON.stringify(q.data, null, 2)}</pre>
  );
}

export default function Reliability() {
  const [tab, setTab] = useState<Tab>("backups");
  const tabs: { id: Tab; label: string; icon: typeof HardDrive }[] = [
    { id: "backups", label: "Backups", icon: HardDrive },
    { id: "updates", label: "Updates", icon: RefreshCw },
    { id: "monitoring", label: "Monitoring", icon: AlertCircle },
    { id: "storage", label: "Storage", icon: Boxes },
    { id: "cluster", label: "Cluster", icon: Network },
  ];
  return (
    <div>
      <h2 className="text-2xl font-semibold mb-4 text-[var(--text-primary)] flex items-center gap-2"><Server size={22} className="text-[var(--accent)]" /> Reliability</h2>
      <div className="flex gap-1 mb-6 border-b border-[var(--border)]">
        {tabs.map((t) => {
          const Icon = t.icon;
          return (
            <button key={t.id} onClick={() => setTab(t.id)} className={`flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 ${tab === t.id ? "border-[var(--accent)] text-[var(--text-primary)]" : "border-transparent text-[var(--text-secondary)]"}`}>
              <Icon size={14} /> {t.label}
            </button>
          );
        })}
      </div>
      {tab === "backups" && <BackupsTab />}
      {tab === "updates" && <UpdatesTab />}
      {tab === "monitoring" && <MonitoringTab />}
      {tab === "storage" && <StorageTab />}
      {tab === "cluster" && <ClusterTab />}
    </div>
  );
}
