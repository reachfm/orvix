import { useChangelogQuery } from "../queries";
import { Loading, ErrorBox, Empty } from "./StateViews";

export default function ChangelogPanel() {
  const q = useChangelogQuery(true);
  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const entries = q.data ?? [];
  if (entries.length === 0) return <Empty text="No changelog entries recorded for this module" />;

  return (
    <div className="space-y-2">
      {entries.map((e) => (
        <div key={e.ID} className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-3">
          <div className="flex justify-between text-xs text-[var(--text-secondary)] mb-1">
            <span className="font-mono text-[var(--text-primary)]">{e.Version}</span>
            <span>{e.ReleasedAt}</span>
          </div>
          <p className="text-sm text-[var(--text-primary)] whitespace-pre-wrap">{e.Changes}</p>
        </div>
      ))}
    </div>
  );
}
