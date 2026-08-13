import { useClusterStatusQuery } from "../queries";
import { Loading, ErrorBox } from "./StateViews";

export default function ClusterPanel() {
  const q = useClusterStatusQuery();
  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const d = q.data;
  if (!d) return null;

  return (
    <div className="space-y-4">
      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 text-sm space-y-1">
        <p className="text-[var(--text-secondary)]">Deployment mode: <span className="text-[var(--text-primary)]">{d.deployment_mode}</span></p>
        <p className="text-[var(--text-secondary)]">Nodes: <span className="text-[var(--text-primary)]">{d.current_nodes} / {d.max_nodes}</span></p>
        <p className="text-[var(--text-secondary)]">Consensus: <span className="text-[var(--text-primary)]">{d.consensus}</span></p>
      </div>
      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Proxy</th><th className="text-left p-3 text-[var(--text-secondary)]">Configured</th><th className="text-left p-3 text-[var(--text-secondary)]">State</th></tr></thead>
          <tbody>{d.proxies.map((p) => (
            <tr key={p.name} className="border-b border-[var(--border)]">
              <td className="p-3 text-[var(--text-primary)]">{p.name}</td>
              <td className="p-3 text-[var(--text-secondary)]">{p.configured ? "yes" : "no"}</td>
              <td className="p-3 text-[var(--text-secondary)]">{p.runtime_state}</td>
            </tr>
          ))}</tbody>
        </table>
      </div>
      <p className="text-xs text-[var(--text-muted)]">{d.honest_note}</p>
    </div>
  );
}
