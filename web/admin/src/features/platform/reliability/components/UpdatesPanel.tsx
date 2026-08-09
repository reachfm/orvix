import { useState } from "react";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { useUpdateStatusQuery, useUpdateCheckQuery, useUpdateHistoryQuery, useUpdatePreflightQuery } from "../queries";
import { useCheckForUpdateMutation, useRunUpdateMutation } from "../mutations";
import { Loading, ErrorBox, Empty } from "./StateViews";

export default function UpdatesPanel() {
  const [confirmRun, setConfirmRun] = useState(false);
  const statusQ = useUpdateStatusQuery();
  const checkQ = useUpdateCheckQuery(statusQ.isSuccess || statusQ.isError);
  const historyQ = useUpdateHistoryQuery(checkQ.isSuccess || checkQ.isError);
  const preflightQ = useUpdatePreflightQuery(historyQ.isSuccess || historyQ.isError);
  const checkNowMut = useCheckForUpdateMutation();
  const runMut = useRunUpdateMutation();

  const history = historyQ.data ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div className="text-sm text-[var(--text-secondary)]">
          Status: <span className="text-[var(--text-primary)]">{statusQ.data?.jobStatus ?? (statusQ.isLoading ? "…" : "unknown")}</span>
          {checkQ.data?.update_available && <span className="ml-3 text-[var(--warning)]">Update available: {checkQ.data.latest_version}</span>}
        </div>
        <div className="flex gap-2">
          <button disabled={checkNowMut.isPending} onClick={() => checkNowMut.mutate()} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded disabled:opacity-50">Check now</button>
          <button onClick={() => setConfirmRun(true)} className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded">Run update</button>
        </div>
      </div>
      {preflightQ.data && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 mb-4 text-sm">
          <p className="text-[var(--text-secondary)] mb-2">Preflight: <span className={preflightQ.data.pass ? "text-[var(--success)]" : "text-[var(--danger)]"}>{preflightQ.data.pass ? "ready" : "not ready"}</span></p>
          <ul className="space-y-1">
            {preflightQ.data.checks.map((c) => (
              <li key={c.name} className="text-xs flex justify-between">
                <span className="text-[var(--text-secondary)]">{c.name}</span>
                <span className={c.status === "pass" ? "text-[var(--success)]" : c.status === "warning" ? "text-[var(--warning)]" : "text-[var(--danger)]"}>{c.status}{c.detail ? ` — ${c.detail}` : ""}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {historyQ.isLoading ? <Loading /> : historyQ.error ? <ErrorBox error={historyQ.error} /> : history.length === 0 ? <Empty text="No update history" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Version</th><th className="text-left p-3 text-[var(--text-secondary)]">Result</th><th className="text-left p-3 text-[var(--text-secondary)]">When</th></tr></thead>
            <tbody>{history.map((h) => (
              <tr key={h.id} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{h.toVersion}</td><td className="p-3 text-[var(--text-secondary)]">{h.status}</td><td className="p-3 text-[var(--text-secondary)]">{h.startedAt}</td></tr>
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
        onConfirm={() => runMut.mutate(undefined, { onSuccess: () => setConfirmRun(false) })}
      />
    </div>
  );
}
