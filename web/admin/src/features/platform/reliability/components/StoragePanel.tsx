import { useStorageVolumesQuery } from "../queries";
import { Loading, ErrorBox, Empty } from "./StateViews";

export default function StoragePanel() {
  const q = useStorageVolumesQuery();
  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const volumes = q.data?.volumes ?? [];
  if (volumes.length === 0) return <Empty text="No storage volumes reported" />;

  return (
    <div>
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {volumes.map((v) => (
          <div key={v.mounted + v.role} className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
            <p className="text-sm font-medium text-[var(--text-primary)] mb-1 capitalize">{v.role}</p>
            {v.available ? (
              <p className="text-xs text-[var(--text-secondary)]">{(v.used_bytes / 1e9).toFixed(1)} GB used ({v.used_pct.toFixed(0)}%)</p>
            ) : (
              <p className="text-xs text-[var(--text-muted)]">{v.detail || "unavailable"}</p>
            )}
          </div>
        ))}
      </div>
      {q.data?.honest_note && <p className="text-xs text-[var(--text-muted)] mt-4">{q.data.honest_note}</p>}
    </div>
  );
}
