import { useState } from "react";
import { X, Loader2, AlertCircle } from "lucide-react";
import { useBulkMailboxJob, useBulkMailboxJobRows } from "../queries";
import { useCancelBulkMailboxJob, useExecuteBulkMailboxJob, useRetryBulkMailboxJob } from "../mutations";
import { safeErrorInfo } from "../../errors";
import PaginationControls from "../../components/PaginationControls";
import { BULK_TERMINAL_STATUSES } from "../contract";

const ROWS_PAGE_SIZE = 25;

function jobStatusLabel(status: string): string {
  switch (status) {
    case "queued": return "Queued";
    case "validating": return "Validating";
    case "ready": return "Ready to execute";
    case "running": return "Running";
    case "completed": return "Completed";
    case "partially_failed": return "Partially failed";
    case "failed": return "Failed";
    case "cancelled": return "Cancelled";
    default: return status;
  }
}

export default function JobDetailDrawer({ tenantId, jobId, onClose }: { tenantId: number; jobId: number; onClose: () => void }) {
  const jobQ = useBulkMailboxJob(tenantId, jobId);
  const [rowsPage, setRowsPage] = useState(0);
  const rowsQ = useBulkMailboxJobRows(tenantId, jobId, rowsPage, ROWS_PAGE_SIZE);
  const executeMut = useExecuteBulkMailboxJob(tenantId);
  const cancelMut = useCancelBulkMailboxJob(tenantId);
  const retryMut = useRetryBulkMailboxJob(tenantId);
  const [executeKey] = useState(() => crypto.randomUUID());
  const [actionError, setActionError] = useState<unknown>(null);

  const job = jobQ.data?.job;
  const rows = rowsQ.data?.rows ?? [];
  const rowsTotal = rowsQ.data?.total ?? 0;
  const terminal = job ? BULK_TERMINAL_STATUSES.has(job.status) : false;

  return (
    <div className="fixed inset-0 z-40 flex justify-end" role="dialog" aria-label="Bulk mailbox import job detail">
      <div className="fixed inset-0 bg-black/50" onClick={onClose} />
      <div className="relative z-50 w-full max-w-2xl h-full bg-[var(--bg-surface)] border-l border-[var(--border)] overflow-y-auto p-6 space-y-5">
        <div className="flex items-start justify-between">
          <h3 className="text-base font-semibold text-[var(--text-primary)]">Bulk import job #{jobId}</h3>
          <button type="button" onClick={onClose} aria-label="Close" className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
            <X size={18} />
          </button>
        </div>

        {jobQ.isLoading ? (
          <div className="flex justify-center py-8"><Loader2 className="animate-spin text-[var(--accent)]" size={22} /></div>
        ) : jobQ.error ? (
          <p className="text-sm text-[var(--danger)]" role="alert">{safeErrorInfo(jobQ.error).detail}</p>
        ) : job ? (
          <>
            <div className="grid grid-cols-2 gap-3 text-sm">
              <Field label="Status" value={jobStatusLabel(job.status)} />
              <Field label="Strategy" value={job.strategy} />
              <Field label="Conflict policy" value={job.conflict_policy} />
              <Field label="Total rows" value={String(job.total_rows)} />
              <Field label="Valid rows" value={String(job.valid_rows)} />
              <Field label="Invalid rows" value={String(job.invalid_rows)} />
              <Field label="Created" value={String(job.created_count)} />
              <Field label="Failed" value={String(job.failed_count)} />
              <Field label="Skipped" value={String(job.skipped_count)} />
            </div>

            {actionError !== null && (
              <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm flex items-start gap-2" role="alert">
                <AlertCircle size={16} className="text-[var(--danger)] shrink-0 mt-0.5" />
                <div>
                  <p className="text-[var(--danger)] font-medium">{safeErrorInfo(actionError).title}</p>
                  <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(actionError).detail}</p>
                </div>
              </div>
            )}

            <div className="flex flex-wrap gap-2">
              {job.status === "ready" && (
                <button
                  type="button"
                  disabled={executeMut.isPending}
                  onClick={() => {
                    setActionError(null);
                    executeMut.mutate({ jobId, idempotencyKey: executeKey }, { onError: setActionError });
                  }}
                  className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                >
                  {executeMut.isPending && <Loader2 size={14} className="animate-spin" />}
                  Execute job
                </button>
              )}
              {!terminal && job.status !== "ready" && (
                <button
                  type="button"
                  disabled={cancelMut.isPending}
                  onClick={() => { setActionError(null); cancelMut.mutate(jobId, { onError: setActionError }); }}
                  className="px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-primary)] disabled:opacity-40"
                >
                  Cancel job
                </button>
              )}
              {(job.status === "partially_failed" || job.status === "failed") && job.failed_count > 0 && (
                <button
                  type="button"
                  disabled={retryMut.isPending}
                  onClick={() => { setActionError(null); retryMut.mutate(jobId, { onError: setActionError }); }}
                  className="px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-primary)] disabled:opacity-40"
                >
                  Retry failed rows
                </button>
              )}
              {!terminal && (
                <span className="text-xs text-[var(--text-muted)] self-center">Refreshing automatically while this job is in progress…</span>
              )}
            </div>

            <div>
              <h4 className="text-sm font-medium text-[var(--text-primary)] mb-2">Row report</h4>
              {rowsQ.isLoading ? (
                <Loader2 className="animate-spin text-[var(--accent)]" size={18} />
              ) : rows.length === 0 ? (
                <p className="text-sm text-[var(--text-secondary)]">No rows to show.</p>
              ) : (
                <>
                  <div className="overflow-x-auto border border-[var(--border)] rounded-lg">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                          <th className="p-2">Row</th>
                          <th className="p-2">Email</th>
                          <th className="p-2">Status</th>
                          <th className="p-2">Error</th>
                        </tr>
                      </thead>
                      <tbody>
                        {rows.map((r) => (
                          <tr key={r.id} className="border-b border-[var(--border)] last:border-0">
                            <td className="p-2 text-[var(--text-secondary)]">{r.row_number}</td>
                            {/* Rendered as plain text nodes only — never dangerouslySetInnerHTML — so
                                spreadsheet cell content can never execute as markup. */}
                            <td className="p-2 text-[var(--text-primary)]">{r.email}</td>
                            <td className="p-2 text-[var(--text-primary)]">{r.status}</td>
                            <td className="p-2 text-[var(--text-secondary)]">{r.error_detail || r.error_code || "—"}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                  <PaginationControls page={rowsPage} pageSize={ROWS_PAGE_SIZE} total={rowsTotal} onChange={setRowsPage} />
                </>
              )}
            </div>
          </>
        ) : null}
      </div>
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs text-[var(--text-secondary)]">{label}</p>
      <p className="text-[var(--text-primary)]">{value}</p>
    </div>
  );
}
