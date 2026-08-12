import { useState } from "react";
import { useReadiness, useDrills, useOperations, useRecordDrill, useCoordinatedBackup } from "./queries";
import { DRILL_OUTCOMES, type DrillOutcome } from "./contract";

export default function DRPage() {
  const { data: readiness } = useReadiness();
  const { data: drills } = useDrills();
  const { data: operations } = useOperations(25, 0);
  const record = useRecordDrill();
  const backup = useCoordinatedBackup();
  const [backupId, setBackupId] = useState("");
  const [outcome, setOutcome] = useState<DrillOutcome>("success");
  const [durationMs, setDurationMs] = useState("1000");
  const [failureReason, setFailureReason] = useState("");
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  const submitDrill = () => {
    if (backupId.trim() === "") { setFeedback({ kind: "error", message: "A backup ID is required." }); return; }
    setFeedback(null);
    record.mutate(
      { backup_id: backupId.trim(), outcome, duration_ms: Number.parseInt(durationMs, 10) || 0, failure_reason: failureReason.trim() || undefined },
      {
        onSuccess: () => { setBackupId(""); setFeedback({ kind: "ok", message: "Drill recorded." }); },
        onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Record failed." }),
      },
    );
  };

  const runBackup = () => {
    setFeedback(null);
    backup.mutate(crypto.randomUUID(), {
      onSuccess: (r) => setFeedback({ kind: "ok", message: `Coordinated backup started: ${r.ref_id}` }),
      onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Backup failed." }),
    });
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Disaster Recovery</h2>
        <p className="text-sm text-[var(--text-secondary)]">Fenced backup/restore coordination, drills, and readiness.</p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
          <p className="text-xs text-[var(--text-secondary)]">Last verified backup</p>
          <p className="text-sm text-[var(--text-primary)]">{readiness?.last_verified_backup_at ? new Date(readiness.last_verified_backup_at).toLocaleString() : "Never"}</p>
        </div>
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
          <p className="text-xs text-[var(--text-secondary)]">RPO gap</p>
          <p className="text-sm text-[var(--text-primary)]">{readiness?.rpo_gap ?? "—"}</p>
        </div>
        <div className={`bg-[var(--bg-surface)] border rounded-lg p-4 ${readiness?.missing_backup_alert ? "border-[var(--danger)]" : "border-[var(--border)]"}`}>
          <p className="text-xs text-[var(--text-secondary)]">Missing backup alert</p>
          <p className={`text-sm font-semibold ${readiness?.missing_backup_alert ? "text-[var(--danger)]" : "text-[var(--success)]"}`}>
            {readiness?.missing_backup_alert ? "ALERT" : "OK"}
          </p>
        </div>
      </div>

      <div className="flex flex-wrap gap-2">
        <button onClick={runBackup} disabled={backup.isPending} className="px-4 py-2 bg-[var(--accent)] text-white rounded text-sm disabled:opacity-50">
          {backup.isPending ? "Starting…" : "Coordinated Backup"}
        </button>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 space-y-3">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Record a drill outcome</h3>
        <div className="flex flex-wrap gap-2">
          <input value={backupId} onChange={(e) => setBackupId(e.target.value)} placeholder="Backup ID" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <select value={outcome} onChange={(e) => setOutcome(e.target.value as DrillOutcome)} className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm">
            {DRILL_OUTCOMES.map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
          <input value={durationMs} onChange={(e) => setDurationMs(e.target.value)} placeholder="Duration ms" type="number" min={0} className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <input value={failureReason} onChange={(e) => setFailureReason(e.target.value)} placeholder="Failure reason" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <button onClick={submitDrill} disabled={record.isPending} className="px-4 py-2 bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded text-sm disabled:opacity-50">Record Drill</button>
        </div>
      </div>

      {feedback && <p className={`text-sm ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</p>}

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] p-4 border-b border-[var(--border)]">Drill history</h3>
        {!drills || drills.drills.length === 0 ? (
          <p className="p-4 text-sm text-[var(--text-muted)]">No drills recorded.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">Backup</th><th className="p-3">Outcome</th><th className="p-3">Duration</th><th className="p-3">Started</th>
              </tr>
            </thead>
            <tbody>
              {drills.drills.map((d) => (
                <tr key={d.id} className="border-b border-[var(--bg-subtle)]">
                  <td className="p-3 text-[var(--text-primary)]">{d.backup_id}</td>
                  <td className="p-3 text-[var(--text-primary)]">{d.outcome}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{d.duration_ms} ms</td>
                  <td className="p-3 text-[var(--text-secondary)]">{new Date(d.started_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] p-4 border-b border-[var(--border)]">Operation history</h3>
        {!operations || operations.operations.length === 0 ? (
          <p className="p-4 text-sm text-[var(--text-muted)]">No DR operations recorded.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">Type</th><th className="p-3">Ref</th><th className="p-3">Status</th><th className="p-3">Created</th>
              </tr>
            </thead>
            <tbody>
              {operations.operations.map((op) => (
                <tr key={op.id} className="border-b border-[var(--bg-subtle)]">
                  <td className="p-3 text-[var(--text-primary)]">{op.type}</td>
                  <td className="p-3 text-[var(--text-primary)]">{op.ref_id}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{op.status}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{new Date(op.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
