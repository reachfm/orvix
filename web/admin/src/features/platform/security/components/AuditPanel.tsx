import { useAuditLogsQuery } from "../queries";
import { Loading, ErrorBox, Empty } from "./StateViews";

export default function AuditPanel() {
  const q = useAuditLogsQuery();
  const rows = q.data ?? [];
  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  if (rows.length === 0) return <Empty text="No audit events" />;

  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
      <table className="w-full text-sm">
        <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Actor</th><th className="text-left p-3 text-[var(--text-secondary)]">Action</th><th className="text-left p-3 text-[var(--text-secondary)]">Target</th><th className="text-left p-3 text-[var(--text-secondary)]">Result</th><th className="text-left p-3 text-[var(--text-secondary)]">Time</th></tr></thead>
        <tbody>{rows.map((r) => (
          <tr key={r.id} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{r.actor}</td><td className="p-3 text-[var(--text-secondary)]">{r.action}</td><td className="p-3 text-[var(--text-secondary)]">{r.target}</td><td className="p-3 text-[var(--text-secondary)]">{r.result}</td><td className="p-3 text-[var(--text-muted)]">{r.timestamp}</td></tr>
        ))}</tbody>
      </table>
    </div>
  );
}
