import { useQueueSummaryQuery } from "../queries";

export default function QueueSummaryCards() {
  const summaryQ = useQueueSummaryQuery();

  if (summaryQ.isLoading) return <p className="text-[var(--text-secondary)] mb-6" role="status">Loading summary…</p>;
  if (summaryQ.error) return <p className="text-[var(--danger)] mb-6" role="alert">Failed to load queue summary: {(summaryQ.error as Error).message}</p>;
  const m = summaryQ.data?.metrics;
  if (!m) return null;

  const failed = m.dead_letter + m.bounced;
  return (
    <div className="grid grid-cols-2 md:grid-cols-5 gap-4 mb-6">
      {([
        ["total", m.total],
        ["pending", m.pending],
        ["deferred", m.deferred],
        ["delivering", m.delivering],
        ["failed", failed],
      ] as const).map(([k, v]) => (
        <div key={k} className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <p className="text-xs text-[var(--text-secondary)] mb-1 capitalize">{k}</p>
          <p className={`text-2xl font-bold ${k === "failed" && v > 0 ? "text-[var(--danger)]" : "text-[var(--text-primary)]"}`}>{v}</p>
        </div>
      ))}
    </div>
  );
}
