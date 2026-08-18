import { useState } from "react";
import { Download, History, Loader2, AlertCircle, ChevronLeft, ChevronRight } from "lucide-react";
import { useQueueHistoryQuery } from "../queries";
import { exportQueueCsv } from "../api";
import { QUEUE_HISTORY_STATUSES, queueHistoryStatusLabel } from "../contract";

const PAGE_LIMIT = 100;

/**
 * QueueHistoryPanel renders the immutable, cross-entry delivery-attempt
 * history (GET /admin/queue/history) with cursor pagination
 * (after_id = last row's id) and status/remote-host filters, plus the
 * redacted CSV export of the LIVE queue (GET /admin/queue/export).
 * The export is a file download through the shared client's blob
 * response mode; the history rows are the real delivery-attempt
 * evidence store, never fabricated.
 */
export default function QueueHistoryPanel() {
  const [statusFilter, setStatusFilter] = useState("");
  const [hostFilter, setHostFilter] = useState("");
  const [afterId, setAfterId] = useState<number>(0);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<unknown>(null);

  const q = useQueueHistoryQuery({
    status: statusFilter || undefined,
    remote_host: hostFilter || undefined,
    after_id: afterId || undefined,
    limit: PAGE_LIMIT,
  });

  const attempts = q.data?.attempts ?? [];
  const nextAfterId = q.data?.next_after_id ?? 0;

  const resetPaging = () => setAfterId(0);

  const runExport = () => {
    setExporting(true);
    setExportError(null);
    exportQueueCsv()
      .then((blob) => {
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = "queue-export.csv";
        document.body.appendChild(a);
        a.click();
        a.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 1000);
      })
      .catch((e) => setExportError(e))
      .finally(() => setExporting(false));
  };

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] flex items-center gap-1.5">
          <History size={15} /> Delivery history
        </h3>
        <button
          type="button"
          disabled={exporting}
          onClick={runExport}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)] disabled:opacity-50"
        >
          {exporting ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
          Export queue CSV
        </button>
      </div>

      {exportError !== null && (
        <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm flex items-center gap-2" role="alert">
          <AlertCircle size={16} className="text-[var(--danger)] shrink-0" />
          <span className="text-[var(--danger)]">
            Export failed: {exportError instanceof Error ? exportError.message : "the server rejected the request"}.
          </span>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3 text-sm">
        <label className="text-[var(--text-secondary)]">Status
          <select
            value={statusFilter}
            onChange={(e) => { setStatusFilter(e.target.value); resetPaging(); }}
            className="ml-2 px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
          >
            <option value="">All statuses</option>
            {QUEUE_HISTORY_STATUSES.map((s) => (
              <option key={s} value={s}>{queueHistoryStatusLabel(s)}</option>
            ))}
          </select>
        </label>
        <label className="text-[var(--text-secondary)]">Remote host
          <input
            value={hostFilter}
            onChange={(e) => { setHostFilter(e.target.value); resetPaging(); }}
            placeholder="mx.example.com"
            className="ml-2 px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
          />
        </label>
      </div>

      {q.isLoading ? (
        <div className="flex items-center justify-center h-32" role="status">
          <Loader2 size={20} className="text-[var(--accent)] animate-spin" />
        </div>
      ) : q.error ? (
        <div className="border border-[var(--danger)]/30 rounded-lg p-4 text-sm" role="alert">
          <p className="text-[var(--danger)] font-medium">Failed to load delivery history</p>
          <p className="text-xs text-[var(--text-secondary)] mt-0.5">{(q.error as Error).message}</p>
          <button
            type="button"
            onClick={() => q.refetch()}
            className="mt-2 px-3 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]"
          >
            Retry
          </button>
        </div>
      ) : attempts.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
          No delivery attempts match the current filters.
        </div>
      ) : (
        <>
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
            <div className="max-h-[28rem] overflow-auto">
              <table className="w-full text-sm">
                <thead className="sticky top-0 bg-[var(--bg-surface)]">
                  <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                    <th className="p-3">ID</th>
                    <th className="p-3">Queue entry</th>
                    <th className="p-3">Attempt</th>
                    <th className="p-3">Status</th>
                    <th className="p-3">Remote host</th>
                    <th className="p-3">Code</th>
                    <th className="p-3">TLS</th>
                    <th className="p-3">Attempted at</th>
                  </tr>
                </thead>
                <tbody>
                  {attempts.map((a) => (
                    <tr key={a.id} className="border-b border-[var(--bg-subtle)]">
                      <td className="p-3 text-[var(--text-muted)] font-mono text-xs">{a.id}</td>
                      <td className="p-3 text-[var(--text-secondary)] font-mono text-xs">{`#${a.queue_entry_id}`}</td>
                      <td className="p-3 text-[var(--text-secondary)]">{a.attempt_number}</td>
                      <td className="p-3 text-[var(--text-primary)]">{queueHistoryStatusLabel(a.status)}</td>
                      <td className="p-3 text-[var(--text-secondary)] font-mono text-xs">{a.remote_host || "—"}</td>
                      <td className="p-3 text-[var(--text-secondary)]">{a.status_code || "—"}</td>
                      <td className="p-3 text-[var(--text-secondary)]">{a.tls_used ? "yes" : "no"}</td>
                      <td className="p-3 text-[var(--text-muted)] whitespace-nowrap">{new Date(a.attempted_at).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
          <div className="flex items-center justify-between text-xs text-[var(--text-secondary)]">
            <span>{q.data?.count ?? attempts.length} attempt{attempts.length === 1 ? "" : "s"} shown</span>
            <div className="flex gap-2">
              <button
                type="button"
                disabled={afterId === 0}
                onClick={() => setAfterId(0)}
                className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded border border-[var(--border)] disabled:opacity-40"
              >
                <ChevronLeft size={12} /> First
              </button>
              <button
                type="button"
                disabled={attempts.length < PAGE_LIMIT || nextAfterId === 0}
                onClick={() => setAfterId(nextAfterId)}
                className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded border border-[var(--border)] disabled:opacity-40"
              >
                Next <ChevronRight size={12} />
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
