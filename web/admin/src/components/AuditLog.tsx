import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Search } from "lucide-react";
import { api } from "../api";

export default function AuditLog() {
  const { data: logs, isLoading, error } = useQuery({ queryKey: ["auditLogs"], queryFn: api.listAuditLogs });
  const [filter, setFilter] = useState("");

  if (isLoading) return <p className="text-[var(--text-secondary)]">Loading...</p>;
  if (error) return <p className="text-[var(--danger)]">Failed to load audit logs</p>;

  const items: any[] = Array.isArray(logs) ? logs : [];
  const filtered = filter
    ? items.filter((l: any) => l.action?.toLowerCase().includes(filter.toLowerCase()))
    : items;

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[var(--text-primary)]">Audit Log</h2>

      <div className="mb-4">
        <div className="relative max-w-xs">
          <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" />
          <input
            placeholder="Filter by action..."
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-full pl-9 pr-3 py-2 bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg text-sm text-[var(--text-primary)] placeholder-[var(--text-muted)] focus:outline-none focus:border-[var(--accent)]"
          />
        </div>
      </div>

      {filtered.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center">
          <p className="text-[var(--text-secondary)]">No audit entries found.</p>
        </div>
      ) : (
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
              {filtered.map((l: any) => (
                <tr key={l.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                  <td className="p-4 text-[var(--text-primary)] font-mono text-xs">{l.action}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{l.actor || "-"}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{l.target || "-"}</td>
                  <td className="p-4 text-center">
                    <span className={`px-2 py-1 text-xs rounded-full ${
                      l.result === "success" || l.result === "ok"
                        ? "bg-[var(--success)]/10 text-[var(--success)]"
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
      )}
    </div>
  );
}
