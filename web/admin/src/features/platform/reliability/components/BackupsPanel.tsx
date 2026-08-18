import { useState } from "react";
import { Download, ShieldCheck, RefreshCw, Trash2, Save, Loader2 } from "lucide-react";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { downloadBackupUrl } from "../api";
import {
  useBackupsQuery, useBackupScheduleQuery, useBackupMetricsQuery, useBackupHealthQuery,
  useRestoreJobStatusQuery,
} from "../queries";
import {
  useCreateBackupMutation, useRunBackupNowMutation, useValidateBackupMutation,
  useRestoreBackupMutation, useDeleteBackupMutation, useRunBackupRetentionMutation,
  useSetBackupScheduleMutation,
} from "../mutations";
import { Loading, ErrorBox, Empty } from "./StateViews";

const FREQUENCIES: ReadonlyArray<string> = ["hourly", "daily", "weekly"];

export default function BackupsPanel() {
  const [confirm, setConfirm] = useState<{ id: string; kind: "restore" | "delete" | "retention" } | null>(null);
  const [restoreJobId, setRestoreJobId] = useState<string | null>(null);
  // Schedule edit draft: starts null (no edits pending) and mirrors the
  // loaded schedule when the operator opens the editor.
  const [scheduleDraft, setScheduleDraft] = useState<{ enabled: boolean; frequency: string; retentionCount: string } | null>(null);
  const [scheduleError, setScheduleError] = useState<unknown>(null);

  const listQ = useBackupsQuery();
  const scheduleQ = useBackupScheduleQuery(listQ.isSuccess || listQ.isError);
  const metricsQ = useBackupMetricsQuery(listQ.isSuccess || listQ.isError);
  const healthQ = useBackupHealthQuery(scheduleQ.isSuccess || scheduleQ.isError);
  const restoreJobQ = useRestoreJobStatusQuery(restoreJobId);

  const createMut = useCreateBackupMutation();
  const nowMut = useRunBackupNowMutation();
  const validateMut = useValidateBackupMutation();
  const restoreMut = useRestoreBackupMutation();
  const deleteMut = useDeleteBackupMutation();
  const retentionMut = useRunBackupRetentionMutation();
  const scheduleMut = useSetBackupScheduleMutation();

  const backups = listQ.data ?? [];

  const openScheduleEditor = () => {
    if (!scheduleQ.data) return;
    setScheduleError(null);
    setScheduleDraft({
      enabled: scheduleQ.data.enabled,
      frequency: scheduleQ.data.frequency,
      retentionCount: String(scheduleQ.data.retentionCount),
    });
  };

  const saveSchedule = () => {
    if (!scheduleDraft) return;
    const retention = Number.parseInt(scheduleDraft.retentionCount, 10);
    if (!Number.isFinite(retention) || retention < 1) {
      setScheduleError(new Error("Retention count must be a positive integer."));
      return;
    }
    setScheduleError(null);
    scheduleMut.mutate(
      { enabled: scheduleDraft.enabled, frequency: scheduleDraft.frequency, retentionCount: retention },
      {
        onSuccess: () => setScheduleDraft(null),
        onError: (e) => setScheduleError(e),
      },
    );
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div className="flex gap-4 text-xs text-[var(--text-secondary)]">
          {healthQ.data && <span>Health: <span className="text-[var(--text-primary)]">{healthQ.data.status}</span></span>}
          {metricsQ.data && <span>Total: <span className="text-[var(--text-primary)]">{metricsQ.data.totalBackups}</span></span>}
        </div>
        <div className="flex gap-2">
          <button disabled={createMut.isPending} onClick={() => createMut.mutate()} className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-50">Create backup</button>
          <button disabled={nowMut.isPending} onClick={() => nowMut.mutate()} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded disabled:opacity-50">Run now</button>
          <button onClick={() => setConfirm({ id: "retention", kind: "retention" })} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded">Run retention</button>
        </div>
      </div>

      {scheduleQ.data && !scheduleDraft && (
        <div className="flex items-center gap-4 text-xs text-[var(--text-secondary)] bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-3 mb-4">
          <span>
            Schedule: <span className="text-[var(--text-primary)]">{scheduleQ.data.enabled ? scheduleQ.data.frequency : "disabled"}</span>
          </span>
          <span>
            Retention: <span className="text-[var(--text-primary)]">{scheduleQ.data.retentionCount} backups</span>
          </span>
          <button
            type="button"
            onClick={openScheduleEditor}
            className="ml-auto px-2.5 py-1.5 rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]"
          >
            Edit schedule
          </button>
        </div>
      )}

      {scheduleDraft && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 mb-4 space-y-3" role="group" aria-label="Backup schedule editor">
          <h3 className="text-sm font-semibold text-[var(--text-primary)]">Edit backup schedule</h3>
          {scheduleError !== null && (
            <p className="text-xs text-[var(--danger)]" role="alert">
              {scheduleError instanceof Error ? scheduleError.message : "Schedule update failed."}
            </p>
          )}
          {scheduleMut.isSuccess && <p className="text-xs text-[var(--success)]" role="status">Schedule saved.</p>}
          <label className="flex items-center gap-2 text-sm text-[var(--text-primary)]">
            <input
              type="checkbox"
              checked={scheduleDraft.enabled}
              onChange={(e) => setScheduleDraft({ ...scheduleDraft, enabled: e.target.checked })}
            />
            Scheduled backups enabled
          </label>
          <label className="block text-sm">
            <span className="text-[var(--text-secondary)]">Frequency</span>
            <select
              value={scheduleDraft.frequency}
              onChange={(e) => setScheduleDraft({ ...scheduleDraft, frequency: e.target.value })}
              className="mt-1 px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            >
              {FREQUENCIES.map((f) => (
                <option key={f} value={f}>{f}</option>
              ))}
            </select>
          </label>
          <label className="block text-sm">
            <span className="text-[var(--text-secondary)]">Retention count (backups kept)</span>
            <input
              type="number"
              min={1}
              value={scheduleDraft.retentionCount}
              onChange={(e) => setScheduleDraft({ ...scheduleDraft, retentionCount: e.target.value })}
              className="mt-1 px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
          </label>
          <div className="flex gap-2">
            <button
              type="button"
              disabled={scheduleMut.isPending}
              onClick={saveSchedule}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-50"
            >
              {scheduleMut.isPending ? <Loader2 size={12} className="animate-spin" /> : <Save size={12} />}
              Save schedule
            </button>
            <button
              type="button"
              disabled={scheduleMut.isPending}
              onClick={() => setScheduleDraft(null)}
              className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {restoreJobId && restoreJobQ.data && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 mb-4 text-sm">
          <p className="text-[var(--text-secondary)]">Restore job <span className="font-mono text-[var(--text-primary)]">{restoreJobQ.data.job_id}</span>: <span className="text-[var(--text-primary)]">{restoreJobQ.data.status}</span></p>
          {restoreJobQ.data.message && <p className="text-[var(--text-muted)] text-xs mt-1">{restoreJobQ.data.message}</p>}
          {restoreJobQ.data.rolled_back && <p className="text-[var(--warning)] text-xs mt-1">Rolled back to the pre-restore safety backup ({restoreJobQ.data.safety_backup_id}).</p>}
          {restoreJobQ.data.error && <p className="text-[var(--danger)] text-xs mt-1">{restoreJobQ.data.error}</p>}
        </div>
      )}

      {listQ.isLoading ? <Loading /> : listQ.error ? <ErrorBox error={listQ.error} /> : backups.length === 0 ? <Empty text="No backups found" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Name</th><th className="text-left p-3 text-[var(--text-secondary)]">Status</th><th className="text-right p-3 text-[var(--text-secondary)]">Actions</th></tr></thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                  <td className="p-3 text-[var(--text-primary)]">{b.name || b.id}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{b.status}</td>
                  <td className="p-3 text-right space-x-1">
                    <a href={downloadBackupUrl(b.id)} title="Download" className="inline-block p-1.5 text-[var(--text-secondary)] hover:text-[var(--accent)]"><Download size={14} /></a>
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
          if (confirm.kind === "restore") {
            restoreMut.mutate(confirm.id, {
              onSuccess: (res) => { setRestoreJobId(res.job_id); setConfirm(null); },
            });
          } else if (confirm.kind === "delete") {
            deleteMut.mutate(confirm.id, { onSuccess: () => setConfirm(null) });
          } else {
            retentionMut.mutate(undefined, { onSuccess: () => setConfirm(null) });
          }
        }}
      />
    </div>
  );
}
