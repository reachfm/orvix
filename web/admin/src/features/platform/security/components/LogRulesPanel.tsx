import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { useLogRulesQuery } from "../queries";
import { useCreateLogRuleMutation, useDeleteLogRuleMutation } from "../mutations";
import { Loading, ErrorBox, Empty } from "./StateViews";

export default function LogRulesPanel() {
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [newName, setNewName] = useState("");
  const [newPattern, setNewPattern] = useState("");
  const q = useLogRulesQuery();
  const createMut = useCreateLogRuleMutation();
  const deleteMut = useDeleteLogRuleMutation();
  const rows = q.data?.rules ?? [];

  return (
    <div>
      <div className="flex gap-2 mb-4">
        <input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="Rule name…" className="px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)] w-40" />
        <input value={newPattern} onChange={(e) => setNewPattern(e.target.value)} placeholder="Match pattern…" className="px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)] flex-1" />
        <button
          disabled={!newName || createMut.isPending}
          onClick={() => createMut.mutate({ name: newName, match_pattern: newPattern }, { onSuccess: () => { setNewName(""); setNewPattern(""); } })}
          className="flex items-center gap-1 px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-50"
        >
          <Plus size={12} /> Add rule
        </button>
      </div>
      {q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No log rules configured" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><tbody>{rows.map((r) => (
            <tr key={r.id} className="border-b border-[var(--border)]">
              <td className="p-3 text-[var(--text-primary)]">{r.name}</td>
              <td className="p-3 text-[var(--text-secondary)]">{r.match_pattern}</td>
              <td className="p-3 text-right"><button onClick={() => setConfirmDelete(r.id)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]"><Trash2 size={14} /></button></td>
            </tr>
          ))}</tbody></table>
        </div>
      )}
      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(o) => !o && setConfirmDelete(null)}
        title="Delete log rule"
        description="This permanently removes the log rule."
        requireTypedName={confirmDelete !== null ? String(confirmDelete) : ""}
        danger
        pending={deleteMut.isPending}
        onConfirm={() => confirmDelete !== null && deleteMut.mutate(confirmDelete, { onSuccess: () => setConfirmDelete(null) })}
      />
    </div>
  );
}
