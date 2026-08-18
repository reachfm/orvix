import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Search, AlertCircle } from "lucide-react";
import { api } from "../api";

const PAGE_SIZE = 25;

export default function AuditLog() {
  const [filter, setFilter] = useState("");
  const [resultFilter, setResultFilter] = useState("");
  const [page, setPage] = useState(1);

  const { data, isLoading, error } = useQuery({
    queryKey: ["auditLogs", page, filter, resultFilter],
    queryFn: () =>
      api.listAuditLogsEnvelope({
        page,
        page_size: PAGE_SIZE,
        action: filter || undefined,
        result: resultFilter || undefined,
      }),
  });

  if (isLoading) return <p className="text-[var(--text-secondary)]">Loading...</p>;
  if (error) return (
    <div className="flex items-center gap-3 rounded-lg border border-[var(--danger)]/30 bg-[var(--bg-surface)] p-6 text-sm text-[var(--danger)]" role="alert">
      <AlertCircle size={20} /> Failed to load audit logs. Reload the page to retry.
    </div>
  );

  const items: any[] = data?.entries ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[var(--text-primary)]">Audit Log</h2>

      <div className="mb-4 flex flex-wrap gap-3">
        <div className="relative max-w-xs">
          <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" />
          <input
            placeholder="Filter by action..."
            value={filter}
            onChange={(e) => { setFilter(e.target.value); setPage(1); }}
            className="w-full pl-9 pr-3 py-2 bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg text-sm text-[var(--text-primary)] placeholder-[var(--text-muted)] focus:outline-none focus:border-[var(--accent)]"
          />
        </div>
        <select
          value={resultFilter}
          onChange={(e) => { setResultFilter(e.target.value); setPage(1); }}
          aria-label="Filter by result"
          className="px-3 py-2 bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg text-sm text-[var(--text-primary)]"
        >
          <option value="">All results</option>
          <option value="success">success</option>
          <option value="failure">failure</option>
          <option value="denied">denied</option>
        </select>
      </div>

      {items.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center">
          <p className="text-[var(--text-secondary)]">No audit entries found.</p>
        </div>
      ) : (
        <>
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-[var(--border)]">
                  <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Action</th>
                  <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Actor</th>
                  <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Target</th>
                  <th className="text-center p-4 text-[var(--text-secondary)] font-medium">Result</th>
                  <th className="text-right p-4 text-[var(--text-secondary)] font-medium">Time</th>
                </tr>
              </thead>
              <tbody>
                {items.map((l: any) => (
                  <tr key={l.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                    <td className="p-4 text-[var(--text-primary)] font-mono text-xs">{l.action}</td>
                    <td className="p-4 text-[var(--text-secondary)]">{l.actor || "-"}</td>
                    <td className="p-4 text-[var(--text-secondary)]">{l.target || "-"}</td>
                    <td className="p-4 text-center">
                      <span className={`px-2 py-1 text-xs rounded-full ${
                        l.result === "success" || l.result === "ok"
                          ? "bg-[var(--success)]/10 text-[var(--success)]"
                          : l.result === "denied"
                            ? "bg-[var(--warning)]/10 text-[var(--warning)]"
                            : "bg-[var(--danger)]/10 text-[var(--danger)]"
                      }`}>
                        {l.result || "unknown"}
                      </span>
                    </td>
                    <td className="p-4 text-right text-[var(--text-muted)] text-xs">
                      {l.timestamp ? new Date(l.timestamp).toLocaleString() : "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mt-3 flex items-center justify-between text-sm text-[var(--text-secondary)]">
            <span>{total} entr{total === 1 ? "y" : "ies"}</span>
            <div className="flex items-center gap-2">
              <button
                disabled={page <= 1}
                onClick={() => setPage((p) => p - 1)}
                className="px-3 py-1.5 rounded bg-[var(--bg-surface)] border border-[var(--border)] disabled:opacity-50"
              >
                Previous
              </button>
              <span>Page {page} of {totalPages}</span>
              <button
                disabled={page >= totalPages}
                onClick={() => setPage((p) => p + 1)}
                className="px-3 py-1.5 rounded bg-[var(--bg-surface)] border border-[var(--border)] disabled:opacity-50"
              >
                Next
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
