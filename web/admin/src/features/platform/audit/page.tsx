import { useState } from "react";
import { Download, Loader2, AlertCircle } from "lucide-react";
import { useAuditLogs } from "./queries";
import { downloadAuditExport, exportAuditLogs } from "./api";
import type { AuditExportFormat } from "./contract";

export default function AuditPage() {
  const [page, setPage] = useState(1);
  const [actionFilter, setActionFilter] = useState("");
  const [tenantFilter, setTenantFilter] = useState("");
  const [resultFilter, setResultFilter] = useState("");
  const [exporting, setExporting] = useState<AuditExportFormat | null>(null);
  const [exportError, setExportError] = useState<unknown>(null);
  const { data, isLoading } = useAuditLogs({
    page,
    page_size: 25,
    action: actionFilter || undefined,
    tenant_id: tenantFilter ? Number.parseInt(tenantFilter, 10) || undefined : undefined,
    result: resultFilter || undefined,
  });

  const runExport = (format: AuditExportFormat) => {
    setExporting(format);
    setExportError(null);
    exportAuditLogs({
      format,
      action: actionFilter || undefined,
      tenant_id: tenantFilter ? Number.parseInt(tenantFilter, 10) || undefined : undefined,
    })
      .then((blob) => downloadAuditExport(blob, format))
      .catch((e) => setExportError(e))
      .finally(() => setExporting(null));
  };

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-xl font-semibold text-[var(--text-primary)]">Audit Log</h2>
          <p className="text-sm text-[var(--text-secondary)]">Platform-wide audited activity, newest first.</p>
        </div>
        <div className="flex gap-2 shrink-0">
          <button
            type="button"
            disabled={exporting !== null}
            onClick={() => runExport("csv")}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)] disabled:opacity-50"
          >
            {exporting === "csv" ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
            Export CSV
          </button>
          <button
            type="button"
            disabled={exporting !== null}
            onClick={() => runExport("json")}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)] disabled:opacity-50"
          >
            {exporting === "json" ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
            Export JSON
          </button>
        </div>
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
        <label className="text-[var(--text-secondary)]">Action
          <input value={actionFilter} onChange={(e) => { setActionFilter(e.target.value); setPage(1); }} placeholder="e.g. organization.update" className="ml-2 px-3 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded" />
        </label>
        <label className="text-[var(--text-secondary)]">Tenant ID
          <input value={tenantFilter} onChange={(e) => { setTenantFilter(e.target.value); setPage(1); }} placeholder="All" type="number" min={0} className="ml-2 px-3 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded" />
        </label>
        <label className="text-[var(--text-secondary)]">Result
          <select value={resultFilter} onChange={(e) => { setResultFilter(e.target.value); setPage(1); }} className="ml-2 px-3 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded">
            <option value="">All</option>
            <option value="success">success</option>
            <option value="failure">failure</option>
            <option value="denied">denied</option>
          </select>
        </label>
      </div>

      {isLoading ? (
        <p className="text-sm text-[var(--text-muted)]">Loading…</p>
      ) : !data || data.entries.length === 0 ? (
        <p className="text-sm text-[var(--text-muted)]">No audit entries match the current filters.</p>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
          <div className="max-h-[36rem] overflow-auto">
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-[var(--bg-surface)]">
                <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                  <th className="p-3">Time</th><th className="p-3">Actor</th><th className="p-3">Tenant</th><th className="p-3">Action</th><th className="p-3">Target</th><th className="p-3">Result</th><th className="p-3">Request ID</th>
                </tr>
              </thead>
              <tbody>
                {data.entries.map((e) => (
                  <tr key={e.id} className="border-b border-[var(--bg-subtle)]">
                    <td className="p-3 text-[var(--text-secondary)] whitespace-nowrap">{new Date(e.timestamp).toLocaleString()}</td>
                    <td className="p-3 text-[var(--text-primary)]">{e.actor}{e.actor_role ? ` (${e.actor_role})` : ""}</td>
                    <td className="p-3 text-[var(--text-secondary)]">{e.tenant_id || "—"}</td>
                    <td className="p-3 text-[var(--text-primary)] font-mono text-xs">{e.action}</td>
                    <td className="p-3 text-[var(--text-secondary)]">{e.target || "—"}</td>
                    <td className="p-3 text-[var(--text-primary)]">{e.result}</td>
                    <td className="p-3 text-[var(--text-muted)] font-mono text-xs">{e.request_id || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {data && data.total > 25 && (
        <div className="flex items-center gap-2 text-sm">
          <button disabled={page <= 1} onClick={() => setPage(page - 1)} className="px-3 py-1.5 rounded bg-[var(--bg-surface)] border border-[var(--border)] disabled:opacity-50">Previous</button>
          <span className="text-[var(--text-secondary)]">Page {page} of {Math.max(1, Math.ceil(data.total / 25))}</span>
          <button disabled={page * 25 >= data.total} onClick={() => setPage(page + 1)} className="px-3 py-1.5 rounded bg-[var(--bg-surface)] border border-[var(--border)] disabled:opacity-50">Next</button>
        </div>
      )}
    </div>
  );
}
