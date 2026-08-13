import { useState } from "react";
import { Play } from "lucide-react";
import { useHealHistoryQuery } from "../queries";
import { useRunHealCheckMutation } from "../mutations";
import { Loading, ErrorBox, Empty } from "./StateViews";

export default function SelfHealPanel() {
  const q = useHealHistoryQuery();
  const [name, setName] = useState("database");
  const runMut = useRunHealCheckMutation();
  const rows = q.data ?? [];

  return (
    <div>
      <div className="flex items-center gap-2 mb-4">
        <select value={name} onChange={(e) => setName(e.target.value)} className="px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
          <option value="database">database</option>
          <option value="disk">disk</option>
        </select>
        <button disabled={runMut.isPending} onClick={() => runMut.mutate(name)} className="flex items-center gap-1 px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-50"><Play size={12} /> Run check</button>
      </div>
      {q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No self-heal history" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><tbody>{rows.map((r) => (
            <tr key={r.id} className="border-b border-[var(--border)]">
              <td className="p-3 text-[var(--text-primary)]">{r.check_name}</td>
              <td className="p-3 text-[var(--text-secondary)]">{r.success ? "success" : "failed"}: {r.issue}</td>
              <td className="p-3 text-[var(--text-muted)]">{r.created_at}</td>
            </tr>
          ))}</tbody></table>
        </div>
      )}
    </div>
  );
}
