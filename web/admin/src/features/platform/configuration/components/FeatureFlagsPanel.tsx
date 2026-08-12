import { useFeatureFlagsQuery } from "../queries";
import { useUpdateFeatureFlagMutation } from "../mutations";
import { Loading, ErrorBox, Empty } from "./StateViews";

export default function FeatureFlagsPanel() {
  const q = useFeatureFlagsQuery();
  const toggleMut = useUpdateFeatureFlagMutation();
  const rows = q.data ?? [];

  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  if (rows.length === 0) return <Empty text="No feature flags configured" />;

  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
      <table className="w-full text-sm">
        <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Flag</th><th className="text-left p-3 text-[var(--text-secondary)]">Tier</th><th className="text-right p-3 text-[var(--text-secondary)]">Enabled</th></tr></thead>
        <tbody>{rows.map((f) => (
          <tr key={f.id} className="border-b border-[var(--border)]">
            <td className="p-3 text-[var(--text-primary)]">{f.name}</td>
            <td className="p-3 text-[var(--text-secondary)]">{f.tier_required}</td>
            <td className="p-3 text-right">
              <button
                disabled={toggleMut.isPending}
                onClick={() => toggleMut.mutate({ id: f.id, enabled: !f.enabled })}
                className={`px-2 py-1 text-xs rounded disabled:opacity-50 ${f.enabled ? "bg-[var(--success)]/10 text-[var(--success)]" : "bg-[var(--border)] text-[var(--text-secondary)]"}`}
              >
                {f.enabled ? "On" : "Off"}
              </button>
            </td>
          </tr>
        ))}</tbody>
      </table>
    </div>
  );
}
