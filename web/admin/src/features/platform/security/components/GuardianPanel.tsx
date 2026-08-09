import { useGuardianLogsQuery } from "../queries";
import { Loading, ErrorBox, Empty } from "./StateViews";

export default function GuardianPanel() {
  const q = useGuardianLogsQuery();
  const rows = q.data ?? [];
  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  if (rows.length === 0) return <Empty text="No guardian analysis events" />;

  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
      <table className="w-full text-sm">
        <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Message</th><th className="text-left p-3 text-[var(--text-secondary)]">Verdict</th><th className="text-left p-3 text-[var(--text-secondary)]">Score</th><th className="text-left p-3 text-[var(--text-secondary)]">Action</th></tr></thead>
        <tbody>{rows.map((r) => (
          <tr key={r.id} className="border-b border-[var(--border)]">
            <td className="p-3 text-[var(--text-primary)] font-mono text-xs">{r.message_id}</td>
            <td className="p-3 text-[var(--text-secondary)]">{r.verdict}</td>
            <td className="p-3 text-[var(--text-secondary)]">{r.threat_score.toFixed(2)}</td>
            <td className="p-3 text-[var(--text-secondary)]">{r.action}</td>
          </tr>
        ))}</tbody>
      </table>
    </div>
  );
}
