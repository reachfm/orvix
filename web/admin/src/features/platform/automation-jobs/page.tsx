import { useState } from "react";
import { useJobs, useJobDetail, useCancelJob, useRetryJob } from "./queries";
import { JOB_STATUSES, type Job } from "./contract";

function JobStatusBadge({ status }: { status: string }) {
  const tone =
    status === "succeeded"
      ? "text-[var(--success)] bg-[var(--success)]/10"
      : status === "failed" || status === "cancelled"
        ? "text-[var(--danger)] bg-[var(--danger)]/10"
        : status === "running"
          ? "text-[var(--warning)] bg-[var(--warning)]/10"
          : "text-[var(--text-secondary)] bg-[var(--bg-subtle)]";
  return <span className={`px-2 py-0.5 rounded text-xs ${tone}`}>{status}</span>;
}

function JobDetail({ job, onClose }: { job: Job; onClose: () => void }) {
  const cancel = useCancelJob(job.id);
  const retry = useRetryJob(job.id);
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  const run = (m: typeof cancel, okMsg: string) => {
    setFeedback(null);
    m.mutate(undefined, {
      onSuccess: () => setFeedback({ kind: "ok", message: okMsg }),
      onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Action failed." }),
    });
  };

  return (
    <div className="border border-[var(--border)] rounded-lg p-4 bg-[var(--bg-surface)] space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Job #{job.id} — {job.type}</h3>
        <button onClick={onClose} className="text-sm text-[var(--text-secondary)] hover:underline">Close</button>
      </div>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-2 text-sm">
        <div><span className="text-[var(--text-secondary)]">Status</span><br /><JobStatusBadge status={job.status} /></div>
        <div><span className="text-[var(--text-secondary)]">Scope</span><br /><span className="text-[var(--text-primary)]">{job.scope}</span></div>
        <div><span className="text-[var(--text-secondary)]">Attempts</span><br /><span className="text-[var(--text-primary)]">{job.attempt_count}/{job.max_attempts}</span></div>
        <div><span className="text-[var(--text-secondary)]">Progress</span><br /><span className="text-[var(--text-primary)]">{job.progress}%</span></div>
        <div><span className="text-[var(--text-secondary)]">Actor</span><br /><span className="text-[var(--text-primary)]">{job.actor || "—"}</span></div>
        <div><span className="text-[var(--text-secondary)]">Created</span><br /><span className="text-[var(--text-primary)]">{new Date(job.created_at).toLocaleString()}</span></div>
      </div>
      {(job.status === "failed" || job.status === "cancelled") && (
        <div className="flex gap-2">
          <button onClick={() => run(retry, "Job retried.")} disabled={retry.isPending} className="px-3 py-1.5 rounded text-sm bg-[var(--bg-subtle)] text-[var(--text-primary)] disabled:opacity-50">Retry</button>
        </div>
      )}
      {job.status === "queued" && (
        <button onClick={() => run(cancel, "Cancellation requested.")} disabled={cancel.isPending} className="px-3 py-1.5 rounded text-sm bg-[var(--danger)]/10 text-[var(--danger)] disabled:opacity-50">Cancel</button>
      )}
      {feedback && <p className={`text-sm ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</p>}
    </div>
  );
}

export default function AutomationJobsPage() {
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState("");
  const [typeFilter, setTypeFilter] = useState("");
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const { data, isLoading } = useJobs(page, 25, statusFilter || undefined, typeFilter || undefined);
  const { data: selected } = useJobDetail(selectedId ?? 0);

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Automation Jobs</h2>
        <p className="text-sm text-[var(--text-secondary)]">Platform-scoped durable jobs with fenced leases.</p>
      </div>

      <div className="flex flex-wrap items-center gap-3 text-sm">
        <label className="text-[var(--text-secondary)]">Status
          <select value={statusFilter} onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }} className="ml-2 px-3 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded">
            <option value="">All</option>
            {JOB_STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </label>
        <label className="text-[var(--text-secondary)]">Type
          <input value={typeFilter} onChange={(e) => { setTypeFilter(e.target.value); setPage(1); }} placeholder="e.g. maintenance" className="ml-2 px-3 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded" />
        </label>
      </div>

      {isLoading ? (
        <p className="text-sm text-[var(--text-muted)]">Loading…</p>
      ) : !data || data.jobs.length === 0 ? (
        <p className="text-sm text-[var(--text-muted)]">No automation jobs found.</p>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">ID</th><th className="p-3">Type</th><th className="p-3">Scope</th><th className="p-3">Status</th><th className="p-3">Attempts</th><th className="p-3">Progress</th><th className="p-3">Created</th>
              </tr>
            </thead>
            <tbody>
              {data.jobs.map((j) => (
                <tr key={j.id} className="border-b border-[var(--bg-subtle)] cursor-pointer hover:bg-[var(--bg-elevated)]" onClick={() => setSelectedId(j.id)}>
                  <td className="p-3 text-[var(--text-primary)]">{j.id}</td>
                  <td className="p-3 text-[var(--text-primary)]">{j.type}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{j.scope}</td>
                  <td className="p-3"><JobStatusBadge status={j.status} /></td>
                  <td className="p-3 text-[var(--text-secondary)]">{j.attempt_count}/{j.max_attempts}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{j.progress}%</td>
                  <td className="p-3 text-[var(--text-secondary)]">{new Date(j.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {data && data.total > 25 && (
        <div className="flex items-center gap-2 text-sm">
          <button disabled={page <= 1} onClick={() => setPage(page - 1)} className="px-3 py-1.5 rounded bg-[var(--bg-surface)] border border-[var(--border)] disabled:opacity-50">Previous</button>
          <span className="text-[var(--text-secondary)]">Page {page} of {Math.max(1, Math.ceil(data.total / 25))}</span>
          <button disabled={page * 25 >= data.total} onClick={() => setPage(page + 1)} className="px-3 py-1.5 rounded bg-[var(--bg-surface)] border border-[var(--border)] disabled:opacity-50">Next</button>
        </div>
      )}

      {selected && <JobDetail job={selected.job} onClose={() => setSelectedId(null)} />}
    </div>
  );
}
