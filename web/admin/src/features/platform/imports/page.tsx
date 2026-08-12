import { useRef, useState } from "react";
import { useImports, useImportDetail, useImportReport, useValidateImport, useExecuteImport, useResumeImport, useCancelImport, useCompensateImport, useCreateImport } from "./queries";
import { CONFLICT_POLICIES, IMPORT_STATUSES, type ConflictPolicy, type ImportJob, type ValidationReport } from "./contract";

function typedConfirmation(action: string, id: number): string {
  return action.toUpperCase() + "-IMPORT-" + id;
}

function StatusBadge({ status }: { status: string }) {
  const tone = status.includes("fail") || status.includes("cancel") || status === "compensation_failed"
    ? "text-[var(--danger)] bg-[var(--danger)]/10"
    : status === "completed" || status === "compensated"
      ? "text-[var(--success)] bg-[var(--success)]/10"
      : status === "running" || status === "validating" || status === "compensating"
        ? "text-[var(--warning)] bg-[var(--warning)]/10"
        : "text-[var(--text-secondary)] bg-[var(--bg-subtle)]";
  return <span className={`px-2 py-0.5 rounded text-xs ${tone}`}>{status}</span>;
}

function ReportView({ report }: { report: ValidationReport }) {
  const rows = report.rows ?? [];
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-4 gap-2 text-sm">
        <div className="bg-[var(--bg-subtle)] rounded p-2"><span className="text-[var(--text-secondary)]">Valid</span><br /><b>{report.valid}</b></div>
        <div className="bg-[var(--bg-subtle)] rounded p-2"><span className="text-[var(--text-secondary)]">Invalid</span><br /><b>{report.invalid}</b></div>
        <div className="bg-[var(--bg-subtle)] rounded p-2"><span className="text-[var(--text-secondary)]">Conflict</span><br /><b>{report.conflict}</b></div>
        <div className="bg-[var(--bg-subtle)] rounded p-2"><span className="text-[var(--text-secondary)]">Updated</span><br /><b>{report.updated}</b></div>
        <div className="bg-[var(--bg-subtle)] rounded p-2"><span className="text-[var(--text-secondary)]">Unchanged</span><br /><b>{report.unchanged}</b></div>
        <div className="bg-[var(--bg-subtle)] rounded p-2"><span className="text-[var(--text-secondary)]">Deferred</span><br /><b>{report.deferred}</b></div>
        <div className="bg-[var(--bg-subtle)] rounded p-2"><span className="text-[var(--text-secondary)]">Total</span><br /><b>{report.total}</b></div>
      </div>
      {rows.length > 0 && (
        <div className="max-h-72 overflow-auto rounded border border-[var(--border)]">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-[var(--bg-surface)]">
              <tr className="text-left text-[var(--text-secondary)]">
                <th className="p-2">Line</th><th className="p-2">Entity</th><th className="p-2">Status</th><th className="p-2">Details</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.line} className="border-t border-[var(--bg-subtle)]">
                  <td className="p-2">{r.line}</td>
                  <td className="p-2">{r.entity}</td>
                  <td className="p-2"><StatusBadge status={r.status} /></td>
                  <td className="p-2 text-[var(--text-secondary)]">
                    {(r.errors ?? []).map((e, i) => <span key={i} className="block text-xs">{e.code}: {e.message}</span>)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function ImportDetail({ job, onClose }: { job: ImportJob; onClose: () => void }) {
  const { data: report, isLoading: reportLoading } = useImportReport(job.id);
  const validate = useValidateImport(job.id);
  const execute = useExecuteImport(job.id);
  const resume = useResumeImport(job.id);
  const cancel = useCancelImport(job.id);
  const compensate = useCompensateImport(job.id);
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  return (
    <div className="border border-[var(--border)] rounded-lg p-4 bg-[var(--bg-surface)] space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Import #{job.id} — {job.source_name}</h3>
        <button onClick={onClose} className="text-sm text-[var(--text-secondary)] hover:underline">Close</button>
      </div>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-2 text-sm">
        <div><span className="text-[var(--text-secondary)]">Status</span><br /><StatusBadge status={job.status} /></div>
        <div><span className="text-[var(--text-secondary)]">Policy</span><br /><span className="text-[var(--text-primary)]">{job.conflict_policy}</span></div>
        <div><span className="text-[var(--text-secondary)]">Rows</span><br /><span className="text-[var(--text-primary)]">{job.total_rows}</span></div>
        <div><span className="text-[var(--text-secondary)]">Progress</span><br /><span className="text-[var(--text-primary)]">{job.processed_rows}/{job.total_rows}</span></div>
        <div><span className="text-[var(--text-secondary)]">Succeeded</span><br /><span className="text-[var(--text-primary)]">{job.succeeded_rows}</span></div>
        <div><span className="text-[var(--text-secondary)]">Skipped</span><br /><span className="text-[var(--text-primary)]">{job.skipped_rows}</span></div>
        <div><span className="text-[var(--text-secondary)]">Failed</span><br /><span className="text-[var(--text-primary)]">{job.failed_rows}</span></div>
        <div><span className="text-[var(--text-secondary)]">Created</span><br /><span className="text-[var(--text-primary)]">{new Date(job.created_at).toLocaleString()}</span></div>
      </div>
      {job.last_error && <p className="text-sm text-[var(--danger)]">Last error: {job.last_error}</p>}

      {reportLoading ? <p className="text-sm text-[var(--text-muted)]">Loading report…</p> : report ? <ReportView report={report} /> : null}

      <div className="flex flex-wrap gap-2">
        <button
          onClick={() => {
            setFeedback(null);
            validate.mutate(undefined, {
              onSuccess: () => setFeedback({ kind: "ok", message: "Validation succeeded." }),
              onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Validation failed." }),
            });
          }}
          disabled={validate.isPending}
          className="px-3 py-1.5 rounded text-sm bg-[var(--bg-subtle)] text-[var(--text-primary)] disabled:opacity-50"
        >
          Validate
        </button>
        <button
          onClick={() => {
            setFeedback(null);
            execute.mutate(
              { idempotencyKey: crypto.randomUUID(), confirmation: typedConfirmation("EXECUTE", job.id) },
              {
                onSuccess: () => setFeedback({ kind: "ok", message: "Execution submitted." }),
                onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Execution failed." }),
              },
            );
          }}
          disabled={execute.isPending}
          className="px-3 py-1.5 rounded text-sm bg-[var(--accent)] text-white disabled:opacity-50"
        >
          Execute
        </button>
        <button
          onClick={() => {
            setFeedback(null);
            resume.mutate(crypto.randomUUID(), {
              onSuccess: () => setFeedback({ kind: "ok", message: "Resume submitted." }),
              onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Resume failed." }),
            });
          }}
          disabled={resume.isPending}
          className="px-3 py-1.5 rounded text-sm bg-[var(--bg-subtle)] text-[var(--text-primary)] disabled:opacity-50"
        >
          Resume
        </button>
        <button
          onClick={() => {
            setFeedback(null);
            cancel.mutate(undefined, {
              onSuccess: () => setFeedback({ kind: "ok", message: "Cancellation submitted." }),
              onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Cancel failed." }),
            });
          }}
          disabled={cancel.isPending}
          className="px-3 py-1.5 rounded text-sm bg-[var(--bg-subtle)] text-[var(--text-primary)] disabled:opacity-50"
        >
          Cancel
        </button>
        <button
          onClick={() => {
            setFeedback(null);
            compensate.mutate(
              { idempotencyKey: crypto.randomUUID(), confirmation: typedConfirmation("COMPENSATE", job.id) },
              {
                onSuccess: () => setFeedback({ kind: "ok", message: "Compensation submitted." }),
                onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Compensation failed." }),
              },
            );
          }}
          disabled={compensate.isPending}
          className="px-3 py-1.5 rounded text-sm bg-[var(--danger)]/10 text-[var(--danger)] disabled:opacity-50"
        >
          Compensate
        </button>
      </div>

      {feedback && <p className={`text-sm ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</p>}
    </div>
  );
}

export default function ImportsPage() {
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState("");
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [sourceName, setSourceName] = useState("import.csv");
  const [policy, setPolicy] = useState<ConflictPolicy>("fail");
  const [uploadResult, setUploadResult] = useState<{ kind: "ok" | "error"; message: string } | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const createImport = useCreateImport();
  const { data, isLoading } = useImports(page, 25, statusFilter || undefined);
  const { data: selected } = useImportDetail(selectedId ?? 0);

  const upload = () => {
    const file = fileRef.current?.files?.[0];
    if (!file) return;
    setUploadResult(null);
    createImport.mutate(
      { body: file, sourceName: sourceName.trim() || file.name, policy },
      {
        onSuccess: (r) => {
          setUploadResult({ kind: "ok", message: `Import ${r.id} staged (${r.status}).` });
          if (fileRef.current) fileRef.current.value = "";
        },
        onError: (e) => setUploadResult({ kind: "error", message: e instanceof Error ? e.message : "Upload failed." }),
      },
    );
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Imports</h2>
        <p className="text-sm text-[var(--text-secondary)]">Bulk provisioning via staged CSV/JSON sources.</p>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 space-y-3">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Stage a new import</h3>
        <div className="flex flex-wrap gap-2 items-end">
          <label className="block text-sm text-[var(--text-secondary)]">
            Source name
            <input value={sourceName} onChange={(e) => setSourceName(e.target.value)} className="mt-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          </label>
          <label className="block text-sm text-[var(--text-secondary)]">
            Conflict policy
            <select value={policy} onChange={(e) => setPolicy(e.target.value as ConflictPolicy)} className="mt-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm">
              {CONFLICT_POLICIES.map((p) => <option key={p} value={p}>{p}</option>)}
            </select>
          </label>
          <input ref={fileRef} type="file" accept=".csv,.json" className="text-sm" />
          <button onClick={upload} disabled={createImport.isPending} className="px-4 py-2 bg-[var(--accent)] text-white rounded text-sm disabled:opacity-50">
            {createImport.isPending ? "Staging…" : "Stage Import"}
          </button>
        </div>
        {uploadResult && <p className={`text-sm ${uploadResult.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{uploadResult.message}</p>}
      </div>

      <div className="flex items-center gap-2">
        <label className="text-sm text-[var(--text-secondary)]">Status</label>
        <select value={statusFilter} onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }} className="px-3 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-sm">
          <option value="">All</option>
          {IMPORT_STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
      </div>

      {isLoading ? (
        <p className="text-sm text-[var(--text-muted)]">Loading…</p>
      ) : !data || data.jobs.length === 0 ? (
        <p className="text-sm text-[var(--text-muted)]">No imports yet. Stage a source above to begin.</p>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">ID</th><th className="p-3">Name</th><th className="p-3">Type</th><th className="p-3">Status</th><th className="p-3">Rows</th><th className="p-3">Progress</th><th className="p-3">Created</th>
              </tr>
            </thead>
            <tbody>
              {data.jobs.map((j) => (
                <tr key={j.id} className="border-b border-[var(--bg-subtle)] cursor-pointer hover:bg-[var(--bg-elevated)]" onClick={() => setSelectedId(j.id)}>
                  <td className="p-3 text-[var(--text-primary)]">{j.id}</td>
                  <td className="p-3 text-[var(--text-primary)]">{j.source_name}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{j.source_type}</td>
                  <td className="p-3"><StatusBadge status={j.status} /></td>
                  <td className="p-3 text-[var(--text-secondary)]">{j.total_rows}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{j.processed_rows}/{j.total_rows}</td>
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

      {selected && <ImportDetail job={selected} onClose={() => setSelectedId(null)} />}
    </div>
  );
}
